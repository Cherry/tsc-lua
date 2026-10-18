package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type Compiler struct {
	pkgs       []*packages.Package
	byPath     map[string]*packages.Package
	nativesDir string
	verbose    bool
	mainPkg    string
	warnings   []string

	layouts map[*types.Struct]*structLayout
	shapes  map[*types.Struct]*structShape
	// Struct modes (layout.go): the first slot - 1 of each indexed one, and each struct's mode.
	slotOffsets map[string]int
	structModes map[*types.Struct]string
	// Modules of the packages being compiled, whose structs may be indexed.
	ownModules map[string]bool

	// Non-aggregate variables whose address is taken; they are stored in a {v=...} box.
	boxed map[*types.Var]bool
	// Variables assigned other than by their declaration.
	assignedVars map[*types.Var]bool
	// Function literals emitted as callable tables, with the variables they capture (closures.go).
	closureCaps map[*ast.FuncLit][]*types.Var
	// Range-over-func loops whose bodies are emitted as callable tables, with their captures.
	rangeCaps map[*ast.RangeStmt][]*types.Var
	// Variables referenced from a nested function literal.
	captured map[*types.Var]bool
	// Aggregate (struct/array) variables whose storage is mutated in place.
	mutatedVars map[*types.Var]bool
	// Aggregate variables or fields whose address is taken (assignments must copy in place).
	addrTakenAgg map[*types.Var]bool
	// Aggregate element types of slices/arrays whose element address is taken.
	addrTakenElem map[string]bool
	// Aggregate types (keyed by typeKey) that are mutated anywhere in the program.
	mutableTypes map[string]bool
	// Unnamed aggregate types seen in composite literals, for mutability propagation.
	seenAgg       map[string]bool
	extraAggTypes []types.Type

	inlines map[*types.Func]*inlineTemplate

	// Slice fields and locals whose header escapes, so self-updates must make a new header.
	sliceEscapes map[*types.Var]bool

	embedOut    string
	embedCopied map[string]bool
}

func newCompiler(pkgs []*packages.Package, nativesDir string) *Compiler {
	c := &Compiler{
		pkgs:          pkgs,
		byPath:        map[string]*packages.Package{},
		nativesDir:    nativesDir,
		layouts:       map[*types.Struct]*structLayout{},
		slotOffsets:   map[string]int{},
		shapes:        map[*types.Struct]*structShape{},
		structModes:   map[*types.Struct]string{},
		ownModules:    map[string]bool{},
		boxed:         map[*types.Var]bool{},
		captured:      map[*types.Var]bool{},
		assignedVars:  map[*types.Var]bool{},
		closureCaps:   map[*ast.FuncLit][]*types.Var{},
		rangeCaps:     map[*ast.RangeStmt][]*types.Var{},
		mutatedVars:   map[*types.Var]bool{},
		addrTakenAgg:  map[*types.Var]bool{},
		addrTakenElem: map[string]bool{},
		mutableTypes:  map[string]bool{},
	}
	for _, p := range pkgs {
		c.byPath[p.PkgPath] = p
	}
	return c
}

func (c *Compiler) warnf(pos token.Position, format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf("%s: %s", pos, fmt.Sprintf(format, args...)))
}

func (c *Compiler) nativePath(pkgPath, suffix string) string {
	return filepath.Join(c.nativesDir, filepath.FromSlash(strings.ReplaceAll(luaModuleName(pkgPath), ".", "/"))+suffix+".lua")
}

func (c *Compiler) isFullNative(pkgPath string) bool {
	if pkgPath == "unsafe" {
		return true
	}
	_, err := os.Stat(c.nativePath(pkgPath, ""))
	return err == nil
}

func (c *Compiler) hasPatch(pkgPath string) bool {
	_, err := os.Stat(c.nativePath(pkgPath, "_patch"))
	return err == nil
}

// patchOverrides returns the functions a patch declares it replaces ("-- overrides:" header,
// as Lua keys such as Name or Type__Method); declared is false when the patch has no header.
func (c *Compiler) patchOverrides(pkgPath string) (names map[string]bool, declared bool) {
	data, err := os.ReadFile(c.nativePath(pkgPath, "_patch"))
	if err != nil {
		return nil, false
	}
	names = map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "-- overrides:") {
			declared = true
			for _, name := range strings.Fields(strings.TrimPrefix(line, "-- overrides:")) {
				names[name] = true
			}
		}
	}
	return names, declared
}

// reachable returns the packages needed at runtime: imports are followed through
// transpiled packages, while native packages contribute only the dependencies
// listed in their "-- deps:" header line.
func (c *Compiler) reachable(roots []*packages.Package) []*packages.Package {
	seen := map[string]bool{}
	var order []*packages.Package
	var visit func(path string)
	visit = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		p := c.byPath[path]
		if p == nil {
			return
		}
		var deps []string
		if c.isFullNative(path) {
			deps = c.nativeDeps(path)
		} else {
			for imp := range p.Imports {
				deps = append(deps, imp)
			}
			deps = append(deps, c.nativeDeps(path)...)
		}
		sort.Strings(deps)
		for _, d := range deps {
			visit(d)
		}
		order = append(order, p)
	}
	for _, r := range roots {
		visit(r.PkgPath)
	}
	return order
}

func (c *Compiler) nativeDeps(pkgPath string) []string {
	var deps []string
	for _, suffix := range []string{"", "_patch"} {
		data, err := os.ReadFile(c.nativePath(pkgPath, suffix))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "-- deps:") {
				deps = append(deps, strings.Fields(strings.TrimPrefix(line, "-- deps:"))...)
			}
		}
	}
	return deps
}

// skipPackage reports packages that are never needed at runtime by the Lua build.
func (c *Compiler) skipPackage(pkgPath string) bool {
	return false
}

// ---------------------------------------------------------------------------
// Type classification helpers.

func isAggregate(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Struct, *types.Array:
		return true
	}
	return false
}

func isTypeParam(t types.Type) bool {
	_, ok := types.Unalias(t).(*types.TypeParam)
	return ok
}

func isInterface(t types.Type) bool {
	if isTypeParam(t) {
		return false
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}

// typeKey identifies an aggregate type for mutability tracking; instantiations share their origin.
func typeKey(t types.Type) string {
	t = types.Unalias(t)
	if n, ok := t.(*types.Named); ok {
		n = n.Origin()
		if n.Obj().Pkg() != nil {
			return n.Obj().Pkg().Path() + "." + n.Obj().Name() + fmt.Sprintf("@%d", n.Obj().Pos())
		}
		return n.Obj().Name()
	}
	return types.TypeString(t, nil)
}

// needsClone reports whether values of t must be copied when stored to a new location.
func (c *Compiler) needsClone(t types.Type) bool {
	if !isAggregate(t) {
		return false
	}
	return c.mutableTypes[typeKey(t)]
}

// ---------------------------------------------------------------------------
// Whole program analysis.

func (c *Compiler) analyze() {
	// Layouts first: the passes below look them up.
	c.analyzeLayouts()
	// Pass 1: address-taken, captured, mutated variables and mutable types.
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		a := &analyzer{c: c, info: p.TypesInfo}
		for _, f := range p.Syntax {
			a.file(f)
		}
	}
	c.analyzeClosures()
	c.analyzeInPlaceSlices()
	// Mutability propagates to aggregates that contain mutable aggregates by value.
	changed := true
	for changed {
		changed = false
		for _, p := range c.pkgs {
			scope := p.Types.Scope()
			for _, name := range scope.Names() {
				tn, ok := scope.Lookup(name).(*types.TypeName)
				if !ok {
					continue
				}
				if c.propagateMutable(tn.Type()) {
					changed = true
				}
			}
		}
		for _, k := range c.extraAggTypes {
			if c.propagateMutable(k) {
				changed = true
			}
		}
	}
}

func (c *Compiler) propagateMutable(t types.Type) bool {
	if !isAggregate(t) || c.mutableTypes[typeKey(t)] {
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			ft := u.Field(i).Type()
			if isTypeParam(ft) || (isAggregate(ft) && c.mutableTypes[typeKey(ft)]) {
				c.mutableTypes[typeKey(t)] = true
				return true
			}
		}
	case *types.Array:
		if isTypeParam(u.Elem()) || (isAggregate(u.Elem()) && c.mutableTypes[typeKey(u.Elem())]) {
			c.mutableTypes[typeKey(t)] = true
			return true
		}
	}
	return false
}

type analyzer struct {
	c     *Compiler
	info  *types.Info
	funcs []*ast.FuncLit
}

func (a *analyzer) file(f *ast.File) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			a.noteNamedResults(n.Type)
		case *ast.FuncLit:
			a.funcLit(n)
			a.noteNamedResults(n.Type)
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				a.addrOf(n.X)
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				a.store(lhs, n.Tok == token.DEFINE)
				a.noteAssigned(lhs, n.Tok == token.DEFINE)
			}
		case *ast.IncDecStmt:
			a.store(n.X, false)
			a.noteAssigned(n.X, false)
		case *ast.RangeStmt:
			if n.Tok == token.ASSIGN {
				if n.Key != nil {
					a.store(n.Key, false)
					a.noteAssigned(n.Key, false)
				}
				if n.Value != nil {
					a.store(n.Value, false)
					a.noteAssigned(n.Value, false)
				}
			}
		case *ast.CallExpr:
			a.call(n)
		case *ast.CompositeLit:
			if t := a.info.TypeOf(n); t != nil && isAggregate(t) {
				a.c.noteAggType(t)
			}
		}
		return true
	})
}

func (c *Compiler) noteAggType(t types.Type) {
	if _, ok := types.Unalias(t).(*types.Named); ok {
		return
	}
	k := typeKey(t)
	if c.seenAgg == nil {
		c.seenAgg = map[string]bool{}
	}
	if !c.seenAgg[k] {
		c.seenAgg[k] = true
		c.extraAggTypes = append(c.extraAggTypes, t)
	}
}

func (a *analyzer) funcLit(fl *ast.FuncLit) {
	// Any identifier used inside fl that refers to a variable declared outside it is captured.
	ast.Inspect(fl.Body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := a.info.Uses[id].(*types.Var)
		if !ok || v.IsField() || v.Pkg() == nil || v.Parent() == v.Pkg().Scope() {
			return true
		}
		if v.Pos() < fl.Pos() || v.Pos() > fl.End() {
			a.c.captured[v] = true
		}
		return true
	})
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// rootVar walks field selections and array indexing (no pointer indirection) to the
// variable whose storage an expression designates.
func (a *analyzer) rootVar(e ast.Expr) *types.Var {
	switch e := unparen(e).(type) {
	case *ast.Ident:
		v, _ := a.info.ObjectOf(e).(*types.Var)
		return v
	case *ast.SelectorExpr:
		sel := a.info.Selections[e]
		if sel == nil || sel.Kind() != types.FieldVal || sel.Indirect() {
			return nil
		}
		if _, isPtr := sel.Recv().Underlying().(*types.Pointer); isPtr {
			return nil
		}
		return a.rootVar(e.X)
	case *ast.IndexExpr:
		if t := a.info.TypeOf(e.X); t != nil {
			if _, ok := t.Underlying().(*types.Array); ok {
				return a.rootVar(e.X)
			}
		}
	}
	return nil
}

// markMutation records that the storage designated by e (an aggregate or part of one) is modified.
func (a *analyzer) markMutation(e ast.Expr) {
	e = unparen(e)
	switch x := e.(type) {
	case *ast.SelectorExpr:
		sel := a.info.Selections[x]
		if sel != nil && sel.Kind() == types.FieldVal {
			a.markOwnerMutable(x, sel)
			// A flattened field is overwritten in place, so values of its type can no longer be shared.
			if v, ok := sel.Obj().(*types.Var); ok && isAggregate(v.Type()) {
				path := sel.Index()
				if st := derefStruct(fieldPathType(sel.Recv(), path[:len(path)-1])); st != nil && a.c.layout(st).fields[path[len(path)-1]].flat {
					a.c.mutableTypes[typeKey(v.Type())] = true
				}
			}
			if !sel.Indirect() {
				if v := a.rootVar(x.X); v != nil {
					a.c.mutatedVars[v] = true
				}
			}
			a.markMutation(x.X)
		}
	case *ast.IndexExpr:
		t := a.info.TypeOf(x.X)
		if t == nil {
			return
		}
		switch u := t.Underlying().(type) {
		case *types.Array:
			a.c.mutableTypes[typeKey(t)] = true
			if v := a.rootVar(x.X); v != nil {
				a.c.mutatedVars[v] = true
			}
			a.markMutation(x.X)
		case *types.Pointer:
			if isAggregate(u.Elem()) {
				a.c.mutableTypes[typeKey(u.Elem())] = true
			}
		}
	case *ast.StarExpr:
		if t := a.info.TypeOf(x); t != nil && isAggregate(t) {
			a.c.mutableTypes[typeKey(t)] = true
		}
	case *ast.Ident:
		if v, ok := a.info.ObjectOf(x).(*types.Var); ok && isAggregate(v.Type()) {
			a.c.mutatedVars[v] = true
		}
	}
}

// markOwnerMutable marks every struct type along a field selection path as mutable.
func (a *analyzer) markOwnerMutable(x *ast.SelectorExpr, sel *types.Selection) {
	t := sel.Recv()
	for _, idx := range sel.Index() {
		if p, ok := t.Underlying().(*types.Pointer); ok {
			t = p.Elem()
		}
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return
		}
		a.c.mutableTypes[typeKey(t)] = true
		t = st.Field(idx).Type()
	}
}

func (a *analyzer) store(lhs ast.Expr, define bool) {
	lhs = unparen(lhs)
	switch x := lhs.(type) {
	case *ast.Ident:
		if define {
			return
		}
		v, ok := a.info.ObjectOf(x).(*types.Var)
		if ok && isAggregate(v.Type()) && a.c.addrTakenAgg[v] {
			a.c.mutableTypes[typeKey(v.Type())] = true
		}
	case *ast.SelectorExpr, *ast.IndexExpr:
		a.markMutation(x)
	case *ast.StarExpr:
		a.markMutation(x)
	}
}

func (a *analyzer) addrOf(e ast.Expr) {
	e = unparen(e)
	switch x := e.(type) {
	case *ast.Ident:
		v, ok := a.info.ObjectOf(x).(*types.Var)
		if !ok {
			return
		}
		if isAggregate(v.Type()) {
			a.c.addrTakenAgg[v] = true
			a.c.mutatedVars[v] = true
			a.c.mutableTypes[typeKey(v.Type())] = true
		} else {
			a.c.boxed[v] = true
		}
	case *ast.SelectorExpr:
		sel := a.info.Selections[x]
		if sel != nil && sel.Kind() == types.FieldVal {
			if v, ok := sel.Obj().(*types.Var); ok && isAggregate(v.Type()) {
				a.c.addrTakenAgg[v] = true
				a.c.mutableTypes[typeKey(v.Type())] = true
			}
			a.markMutation(x)
		} else if sel == nil {
			// Qualified identifier of a package-level variable.
			if v, ok := a.info.ObjectOf(x.Sel).(*types.Var); ok && !isAggregate(v.Type()) {
				a.c.boxed[v] = true
			}
		}
	case *ast.IndexExpr:
		if t := a.info.TypeOf(x); t != nil && isAggregate(t) {
			a.c.addrTakenElem[typeKey(t)] = true
			a.c.mutableTypes[typeKey(t)] = true
		}
		a.markMutation(x)
	case *ast.CompositeLit:
		// &T{...} allocates; no existing storage is aliased.
	}
}

func (a *analyzer) call(call *ast.CallExpr) {
	// Calling a pointer method on an addressable value implicitly takes its address.
	se, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return
	}
	sel := a.info.Selections[se]
	if sel == nil || sel.Kind() != types.MethodVal {
		return
	}
	fn, ok := sel.Obj().(*types.Func)
	if !ok {
		return
	}
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return
	}
	if _, ptrRecv := recv.Type().Underlying().(*types.Pointer); !ptrRecv {
		return
	}
	xt := a.info.TypeOf(se.X)
	if xt == nil {
		return
	}
	if _, isPtr := xt.Underlying().(*types.Pointer); isPtr {
		return
	}
	if isInterface(xt) {
		return
	}
	// Walk the embedded path: if it goes through a pointer field, the value is not addressed.
	t := xt
	for _, idx := range sel.Index()[:len(sel.Index())-1] {
		st, ok := t.Underlying().(*types.Struct)
		if !ok {
			return
		}
		t = st.Field(idx).Type()
		if _, isPtr := t.Underlying().(*types.Pointer); isPtr {
			return
		}
	}
	if isAggregate(t) {
		a.c.mutableTypes[typeKey(t)] = true
	}
	a.addrOf(se.X)
}
