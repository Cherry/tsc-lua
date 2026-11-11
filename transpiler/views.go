package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

// View parameters: a string parameter that some caller passes a substring to, and that the
// function only measures, indexes, slices or hands to another such reader, takes the substring
// as its base string plus (offset, length) in two extra trailing parameters. Callers that pass
// a whole string leave those out, and the function then covers the whole string.

// Capped fields: string fields the program cuts to a prefix of themselves over and over (the
// JSDoc parser truncates the source text at the end of every comment it parses). Such a field
// keeps the whole string in its slot and the shorter length under a second key (nil when not
// cut), so the cut costs nothing; reading the field as a plain value makes the cut real.
var cappedFieldNames = map[string]bool{
	"github.com/microsoft/TypeScript/tsc/internal/scanner.Scanner.text":     true,
	"github.com/microsoft/TypeScript/tsc/internal/parser.Parser.sourceText": true,
	"golua.test/strview.source.text":                                        true,
}

// rangeCallNames are the functions rangeCall reads in place, from their first argument.
var rangeCallNames = map[string]bool{
	"unicode/utf8.DecodeRuneInString":     true,
	"unicode/utf8.DecodeLastRuneInString": true,
	"strings.HasPrefix":                   true,
	"strings.IndexByte":                   true,
	"strings.Index":                       true,
	"strings.IndexAny":                    true,
	"strings.LastIndex":                   true,
}

// capKey is the key holding a capped field's length.
func capKey(v *types.Var) string { return fieldKey(v.Name()) + "__n" }

// staticCallee returns the declared function or method a call invokes directly, or nil for
// calls through interfaces, function values, conversions and builtins.
func staticCallee(info *types.Info, call *ast.CallExpr) *types.Func {
	var fn *types.Func
	switch f := unparen(call.Fun).(type) {
	case *ast.Ident:
		fn, _ = info.Uses[f].(*types.Func)
	case *ast.SelectorExpr:
		if sel := info.Selections[f]; sel != nil {
			if sel.Kind() != types.MethodVal {
				return nil
			}
			fn, _ = sel.Obj().(*types.Func)
		} else {
			fn, _ = info.Uses[f.Sel].(*types.Func)
		}
	}
	if fn == nil {
		return nil
	}
	sig := fn.Type().(*types.Signature)
	if recv := sig.Recv(); recv != nil && (isInterface(recv.Type()) || isTypeParam(recv.Type())) {
		return nil
	}
	return fn
}

// rangeCallName returns the qualified name of a call's callee when rangeCall handles it.
func rangeCallName(info *types.Info, call *ast.CallExpr) string {
	se, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	fn, ok := info.Uses[se.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return ""
	}
	if name := fn.Pkg().Path() + "." + fn.Name(); rangeCallNames[name] {
		return name
	}
	return ""
}

// cappedFieldVar returns the capped field a selector expression reads, or nil.
func (c *Compiler) cappedFieldVar(info *types.Info, e ast.Expr) *types.Var {
	se, ok := unparen(e).(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	sel := info.Selections[se]
	if sel == nil || sel.Kind() != types.FieldVal {
		return nil
	}
	if v, ok := sel.Obj().(*types.Var); ok && c.cappedFields[v.Origin()] {
		return v.Origin()
	}
	return nil
}

// isSubstring reports string slice expressions, which a view can stand for.
func isSubstring(info *types.Info, e ast.Expr) bool {
	se, ok := unparen(e).(*ast.SliceExpr)
	return ok && !se.Slice3 && isString(info.TypeOf(se.X))
}

type viewParam struct {
	fn    *types.Func
	index int
}

// analyzeStringViews finds the capped fields and decides which parameters are view parameters.
func (c *Compiler) analyzeStringViews() {
	found := map[string]bool{}
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			tn, ok := scope.Lookup(name).(*types.TypeName)
			if !ok {
				continue
			}
			st, ok := tn.Type().Underlying().(*types.Struct)
			if !ok {
				continue
			}
			for i := 0; i < st.NumFields(); i++ {
				f := st.Field(i)
				if key := p.PkgPath + "." + name + "." + f.Name(); cappedFieldNames[key] && isString(f.Type()) {
					c.cappedFields[f] = true
					found[key] = true
				}
			}
		}
	}
	for key := range cappedFieldNames {
		pkgPath := key[:strings.LastIndex(key[:strings.LastIndex(key, ".")], ".")]
		if p := c.byPath[pkgPath]; p != nil && !c.isFullNative(pkgPath) && !found[key] {
			fatalf("capped field %s does not exist (transpiler/views.go)", key)
		}
	}

	// candidates are the parameters with only view-friendly uses; deps are the parameters of
	// other functions they are passed on to, which must be views as well.
	candidates := map[*types.Var]viewParam{}
	deps := map[*types.Var][]*types.Var{}
	wanted := map[*types.Var]bool{}
	param := func(fn *types.Func, i int) *types.Var {
		sig := fn.Type().(*types.Signature)
		if sig.Variadic() || i >= sig.Params().Len() {
			return nil
		}
		return sig.Params().At(i)
	}
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		info := p.TypesInfo
		// A patch that replaces a function does not know about the extra parameters, and one
		// that does not list what it replaces may replace anything.
		overrides, declared := c.patchOverrides(p.PkgPath)
		if c.hasPatch(p.PkgPath) && !declared {
			continue
		}
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil || fd.Name.Name == "_" {
					continue
				}
				fn := info.Defs[fd.Name].(*types.Func)
				sig := fn.Type().(*types.Signature)
				if sig.Variadic() || sig.TypeParams().Len() > 0 || sig.RecvTypeParams().Len() > 0 || overrides[funcKeyOf(fn)] {
					continue
				}
				for i := 0; i < sig.Params().Len(); i++ {
					v := sig.Params().At(i)
					if v.Name() != "" && v.Name() != "_" && isString(v.Type()) && !c.boxed[v] && !c.captured[v] && !c.assignedVars[v] {
						candidates[v] = viewParam{fn, i}
					}
				}
			}
		}
	}
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		info := p.TypesInfo
		for _, file := range p.Syntax {
			var stack []ast.Node
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				switch n := n.(type) {
				case *ast.CallExpr:
					// A substring (or capped field) argument makes the parameter worth a view, unless
					// rangeCall already reads that call's argument in place.
					if fn := staticCallee(info, n); fn != nil && rangeCallName(info, n) == "" {
						for i, arg := range n.Args {
							if isSubstring(info, arg) || c.cappedFieldVar(info, arg) != nil {
								if v := param(fn, i); v != nil {
									wanted[v] = true
								}
							}
						}
					}
				case *ast.Ident:
					v, ok := info.Uses[n].(*types.Var)
					if !ok {
						return true
					}
					if _, ok := candidates[v]; !ok {
						return true
					}
					if !c.viewFriendlyUse(info, stack, v, deps, param) {
						delete(candidates, v)
					}
				}
				return true
			})
		}
	}
	for changed := true; changed; {
		changed = false
		for v := range candidates {
			for _, target := range deps[v] {
				if _, ok := candidates[target]; !ok {
					delete(candidates, v)
					changed = true
					break
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for v := range candidates {
			if !wanted[v] {
				continue
			}
			for _, target := range deps[v] {
				if !wanted[target] {
					wanted[target] = true
					changed = true
				}
			}
		}
	}
	for v, vp := range candidates {
		if wanted[v] {
			c.viewParams[vp.fn] = append(c.viewParams[vp.fn], vp.index)
		}
	}
	for _, indexes := range c.viewParams {
		sort.Ints(indexes)
	}
}

// viewFriendlyUse reports whether the identifier on top of stack uses the parameter v in a way
// a view serves without becoming a string. Passing it on to another function records that
// function's parameter in deps: the use only holds if that one is a view parameter too.
func (c *Compiler) viewFriendlyUse(info *types.Info, stack []ast.Node, v *types.Var, deps map[*types.Var][]*types.Var, param func(*types.Func, int) *types.Var) bool {
	// The use is the node above any parentheses around the identifier.
	child := stack[len(stack)-1]
	i := len(stack) - 2
	for i >= 0 {
		pe, ok := stack[i].(*ast.ParenExpr)
		if !ok {
			break
		}
		child = pe
		i--
	}
	if i < 0 {
		return false
	}
	switch parent := stack[i].(type) {
	case *ast.IndexExpr:
		return parent.X == child
	case *ast.SliceExpr:
		return parent.X == child && !parent.Slice3
	case *ast.CallExpr:
		if id, ok := unparen(parent.Fun).(*ast.Ident); ok {
			if b, ok := info.Uses[id].(*types.Builtin); ok {
				return b.Name() == "len"
			}
		}
		if rangeCallName(info, parent) != "" {
			return len(parent.Args) > 0 && parent.Args[0] == child
		}
		if fn := staticCallee(info, parent); fn != nil {
			for j, arg := range parent.Args {
				if arg == child {
					if target := param(fn, j); target != nil {
						deps[v] = append(deps[v], target)
						return true
					}
				}
			}
		}
	case *ast.AssignStmt:
		// Storing into a capped field keeps the view when it is a prefix.
		return parent.Tok == token.ASSIGN && len(parent.Lhs) == 1 && len(parent.Rhs) == 1 && parent.Rhs[0] == child &&
			c.cappedFieldVar(info, parent.Lhs[0]) != nil
	}
	return false
}

// cappedRef is where a capped field lives: tab is the table expression, slot the whole string's
// storage, and key the storage of its length when cut.
type cappedRef struct {
	tab, slot, slotKey, key, keyName string
}

// cappedField resolves a selector of a capped field; tab replaces the table expression when set.
func (fc *funcCtx) cappedField(e ast.Expr, tab string) (cappedRef, bool) {
	v := fc.e.c.cappedFieldVar(fc.info(), e)
	if v == nil {
		return cappedRef{}, false
	}
	se := unparen(e).(*ast.SelectorExpr)
	path := fc.info().Selections[se].Index()
	owner := path[:len(path)-1]
	if tab == "" {
		tab = fc.fieldPath(fc.expr(se.X), fc.typeOf(se.X), owner)
	}
	st := derefStruct(fieldPathType(fc.typeOf(se.X), owner))
	info := fc.e.c.layout(st).fields[path[len(path)-1]]
	return cappedRef{tab: tab, slot: info.access(tab), slotKey: info.luaKey(), key: prefix(tab) + "." + capKey(v), keyName: capKey(v)}, true
}

// read renders the field as a plain string.
func (r cappedRef) read() string {
	if isSimple(r.tab) {
		return fmt.Sprintf("(%s and rt.capstr(%s, %s, %s) or %s)", r.key, r.tab, r.slotKey, luaString(r.keyName), r.slot)
	}
	return fmt.Sprintf("rt.capread(%s, %s, %s)", r.tab, r.slotKey, luaString(r.keyName))
}

// view is the field as a string view, when its table can be named more than once.
func (r cappedRef) view() *strView {
	if !isSimple(r.tab) {
		return nil
	}
	return &strView{s: r.slot, o: "0", n: fmt.Sprintf("(%s or #%s)", r.key, r.slot)}
}

// cappedStore emits `field = rhs` for a capped field when rhs is a prefix of a string or a
// view, which the field can hold without a copy. Other stores take the general path, which
// clears the length (see store).
func (fc *funcCtx) cappedStore(w *writer, lhs, rhs ast.Expr) bool {
	if fc.e.c.cappedFieldVar(fc.info(), lhs) == nil {
		return false
	}
	var base, length string
	var view *strView
	if se, ok := unparen(rhs).(*ast.SliceExpr); ok && !se.Slice3 && se.High != nil && isString(fc.typeOf(se.X)) {
		b, start, end := fc.stringRange(se)
		if start != "0" {
			return false
		}
		base, length = b, end
	} else if view = fc.viewOf(rhs); view == nil {
		return false
	}
	ref, _ := fc.cappedField(lhs, "")
	fc.flushInto(w)
	if !isSimple(ref.tab) {
		tmp := fc.e.newTmp("o")
		w.line("local %s = %s", tmp, ref.tab)
		ref, _ = fc.cappedField(lhs, tmp)
	}
	switch {
	case view != nil:
		emitLine(w, fmt.Sprintf("%s, %s = rt.capset(%s, %s, %s)", ref.slot, ref.key, view.s, view.o, view.n))
	case base == ref.slot:
		emitLine(w, fmt.Sprintf("%s = %s", ref.key, length))
	default:
		emitLine(w, fmt.Sprintf("%s, %s = %s, %s", ref.slot, ref.key, base, length))
	}
	return true
}

// viewArgs rewrites the arguments of a call to a function with view parameters: a substring
// argument becomes its base string, with its offset and length appended after the arguments.
func (fc *funcCtx) viewArgs(call *ast.CallExpr, args []string) ([]string, bool) {
	fn := staticCallee(fc.info(), call)
	if fn == nil || len(fc.e.c.viewParams[fn]) == 0 || len(args) != len(call.Args) {
		return nil, false
	}
	out := append([]string{}, args...)
	var extra []string
	last := 0
	for _, i := range fc.e.c.viewParams[fn] {
		arg := call.Args[i]
		if view := fc.viewOf(arg); view != nil {
			out[i] = view.s
			extra = append(extra, view.o, view.n)
			last = len(extra)
			continue
		}
		if isSubstring(fc.info(), arg) {
			base, start, end := fc.stringRange(arg)
			// The offset and length move after the other arguments, so they must not have effects.
			if !hasCall(base) && !hasCall(start) && !hasCall(end) {
				if end == "" {
					end = "#" + prefixOrParen(base)
				}
				length := end
				if start != "0" {
					length = fmt.Sprintf("%s - %s", prefixOrParen(end), prefixOrParen(start))
				}
				out[i] = base
				extra = append(extra, start, length)
				last = len(extra)
				continue
			}
		}
		extra = append(extra, "nil", "nil")
	}
	if last == 0 {
		return nil, false
	}
	return append(out, extra[:last]...), true
}

// viewParamNames declares the view parameters of fn in fc and returns their extra Lua
// parameters and the statements that default them to the whole string.
func (fc *funcCtx) viewParamNames(fn *types.Func) (extra []string, defaults []string) {
	sig := fn.Type().(*types.Signature)
	for _, i := range fc.e.c.viewParams[fn] {
		v := sig.Params().At(i)
		name := fc.varName(v)
		view := &strView{s: name, o: name + "__o", n: name + "__n"}
		if fc.views == nil {
			fc.views = map[*types.Var]*strView{}
		}
		fc.views[v] = view
		extra = append(extra, view.o, view.n)
		defaults = append(defaults, fmt.Sprintf("if %s == nil then %s, %s = 0, #%s end", view.o, view.o, view.n, name))
	}
	return extra, defaults
}
