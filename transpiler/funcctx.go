package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"strconv"
	"strings"
)

type loopInfo struct {
	label      string // Go label, if any
	breakLbl   string // Lua label after the statement ("" until used)
	contLbl    string // Lua label for continue ("" for switch/select)
	isLoop     bool
	usedBreak  bool
	usedCont   bool
	rangeFunc  *rangeFuncInfo // non-nil when this is a range-over-func loop
	breakIsLua bool           // plain Lua break works (innermost Lua loop, no intervening switch)
}

type rangeFuncInfo struct {
	ctl     string // control variable name
	res     string // result table name
	signals map[string]bool
}

type funcCtx struct {
	e      *pkgEmitter
	parent *funcCtx
	sig    *types.Signature

	results    []string // Lua names of result variables (when named or deferred)
	namedRes   bool
	hasDefer   bool
	deferVar   string
	loops      []*loopInfo
	pre        []string
	inRangeFn  int // depth of range-over-func bodies in this function
	selfFields map[*types.Var]bool
	fallVar    string
	views      map[*types.Var]*strView
	paramSubst map[*types.Var]string // inline template placeholders

	// captureRefs maps the variables a converted function literal captures to their slots in
	// its table (see closures.go).
	captureRefs map[*types.Var]string
	// params are the function's Lua parameter names.
	params []string
	// topLevel is set for functions that can move code to module level: declared functions
	// and literals that are hoisted or converted, which reach nothing through upvalues.
	topLevel bool
	// hoistedDefer is set when the body of a function with defers is a module-level function
	// whose returns carry the results (see body).
	hoistedDefer bool
}

func (e *pkgEmitter) newFuncCtx(parent *funcCtx, sig *types.Signature) *funcCtx {
	return &funcCtx{e: e, parent: parent, sig: sig}
}

func (fc *funcCtx) flushPre(indent int) string {
	var b strings.Builder
	for _, s := range fc.pre {
		b.WriteString(strings.Repeat("\t", indent))
		b.WriteString(s)
		b.WriteByte('\n')
	}
	fc.pre = nil
	return b.String()
}

func (fc *funcCtx) info() *types.Info { return fc.e.info }

func (fc *funcCtx) typeOf(e ast.Expr) types.Type { return fc.e.info.TypeOf(e) }

// ---------------------------------------------------------------------------
// Type descriptors

func (fc *funcCtx) typeExpr(t types.Type) string {
	if fc.e.hoistable(t) {
		if _, ok := types.Unalias(t).(*types.Basic); !ok {
			if n, ok := types.Unalias(t).(*types.Named); !ok || n.TypeArgs().Len() > 0 {
				return fc.e.typeRef(fc, t)
			}
		}
	}
	return fc.typeExprRaw(t)
}

func (fc *funcCtx) typeExprRaw(t types.Type) string {
	switch t := types.Unalias(t).(type) {
	case *types.Basic:
		return "rt." + basicKind(t)
	case *types.TypeParam:
		return typeParamName(t)
	case *types.Named:
		obj := t.Obj()
		if obj.Pkg() == nil {
			if obj.Name() == "error" {
				return "rt.error"
			}
			if obj.Name() == "comparable" {
				return "rt.any"
			}
			return "rt." + obj.Name()
		}
		var base string
		if obj.Parent() != nil && obj.Parent() != obj.Pkg().Scope() {
			base = fc.e.localTypeName(obj)
		} else {
			base = fc.e.importAlias(obj.Pkg()) + "." + luaIdent(obj.Name())
		}
		if t.TypeArgs().Len() == 0 {
			return base
		}
		var args []string
		for i := 0; i < t.TypeArgs().Len(); i++ {
			args = append(args, fc.typeExpr(t.TypeArgs().At(i)))
		}
		return fmt.Sprintf("%s(%s)", base, strings.Join(args, ", "))
	case *types.Pointer:
		return fmt.Sprintf("rt.ptrTo(%s)", fc.typeExpr(t.Elem()))
	case *types.Slice:
		return fmt.Sprintf("rt.sliceOf(%s)", fc.typeExpr(t.Elem()))
	case *types.Array:
		return fmt.Sprintf("rt.arrayOf(%s, %d)", fc.typeExpr(t.Elem()), t.Len())
	case *types.Map:
		return fmt.Sprintf("rt.mapOf(%s, %s)", fc.typeExpr(t.Key()), fc.typeExpr(t.Elem()))
	case *types.Chan:
		return fmt.Sprintf("rt.chanOf(%s, %d)", fc.typeExpr(t.Elem()), t.Dir())
	case *types.Signature:
		var ps, rs []string
		for i := 0; i < t.Params().Len(); i++ {
			ps = append(ps, fc.typeExpr(t.Params().At(i).Type()))
		}
		for i := 0; i < t.Results().Len(); i++ {
			rs = append(rs, fc.typeExpr(t.Results().At(i).Type()))
		}
		return fmt.Sprintf("rt.funcOf({%s}, {%s}, %v)", strings.Join(ps, ", "), strings.Join(rs, ", "), t.Variadic())
	case *types.Struct:
		return fmt.Sprintf("rt.structOf(%s)", fc.structFieldList(t))
	case *types.Interface:
		var ms []string
		for i := 0; i < t.NumMethods(); i++ {
			ms = append(ms, luaString(t.Method(i).Name()))
		}
		if t.IsComparable() && t.NumMethods() == 0 && !t.IsMethodSet() {
			return "rt.any"
		}
		return fmt.Sprintf("rt.ifaceOf({%s})", strings.Join(ms, ", "))
	}
	return "rt.invalid"
}

// localTypeName names a function-local type. It only has to be unique in its function, and the
// source line (unlike a token.Pos) does not depend on the order packages were loaded in.
func (e *pkgEmitter) localTypeName(obj *types.TypeName) string {
	return fmt.Sprintf("__lt_%s_%d", luaIdent(obj.Name()), e.pos(obj.Pos()).Line)
}

// structFieldList renders a struct's fields as {key, type, name, flags, tag} where
// flags is 0 for plain fields, 1 for embedded fields stored under their key, and
// 2 for embedded structs flattened into the containing table (key is unused).
func (fc *funcCtx) structFieldList(s *types.Struct) string {
	l := fc.e.c.layout(s)
	var parts []string
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		info := l.fields[i]
		if info.skip {
			continue
		}
		flags := 0
		if info.flat && f.Embedded() {
			flags = 2
		} else if f.Embedded() {
			flags = 1
		}
		tag := ""
		if t := s.Tag(i); t != "" {
			tag = ", " + luaString(t)
		}
		// A flattened named field keeps flags 0 and is marked inline in a sixth element.
		if info.flat && !f.Embedded() {
			if tag == "" {
				tag = ", nil"
			}
			tag += ", true"
		}
		// Atomic integer fields hold the plain value.
		ft := f.Type()
		if vt := atomicValueType(ft); vt != nil {
			ft = vt
		}
		parts = append(parts, fmt.Sprintf("{%s, %s, %s, %d%s}", info.luaKey(), fc.typeExpr(ft), luaString(f.Name()), flags, tag))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// funcRef returns the Lua expression referencing a declared function or method.
func (fc *funcCtx) funcRef(fn *types.Func) string {
	if fn.Origin() != nil {
		fn = fn.Origin()
	}
	alias := fc.e.importAlias(fn.Pkg())
	return alias + "." + funcKeyOf(fn)
}

// recvTypeArgs returns descriptor expressions for the type arguments of a
// (possibly pointer to) generic named receiver type.
func (fc *funcCtx) recvTypeArgs(t types.Type) []string {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	n, ok := t.(*types.Named)
	if !ok {
		return nil
	}
	var res []string
	if n.TypeArgs().Len() > 0 {
		for i := 0; i < n.TypeArgs().Len(); i++ {
			res = append(res, fc.typeExpr(n.TypeArgs().At(i)))
		}
	} else if n.TypeParams().Len() > 0 {
		for i := 0; i < n.TypeParams().Len(); i++ {
			res = append(res, typeParamName(n.TypeParams().At(i)))
		}
	}
	return res
}

// ---------------------------------------------------------------------------
// Zero values

func (fc *funcCtx) zeroValue(t types.Type) string {
	if isTypeParam(t) {
		return fmt.Sprintf("%s.zero()", fc.typeExpr(t))
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return "false"
		case u.Info()&types.IsString != 0:
			return "''"
		case u.Info()&types.IsFloat != 0:
			return "0.0"
		case u.Info()&types.IsComplex != 0:
			return "rt.complex(0.0, 0.0)"
		case u.Info()&types.IsInteger != 0:
			return "0"
		case u.Kind() == types.UnsafePointer:
			return "nil"
		}
		return "nil"
	case *types.Struct, *types.Array:
		return fmt.Sprintf("%s.zero()", fc.typeExpr(t))
	}
	return "nil"
}

// simpleZero is the inline zero for map reads, or "" if the zero is not a simple literal.
func (fc *funcCtx) simpleZero(t types.Type) string {
	if isTypeParam(t) {
		return ""
	}
	switch t.Underlying().(type) {
	case *types.Basic:
		z := fc.zeroValue(t)
		if z == "nil" || strings.Contains(z, "(") {
			return ""
		}
		return z
	}
	return ""
}

// ---------------------------------------------------------------------------
// Parameters

func (fc *funcCtx) paramNames(fl *ast.FieldList) []string {
	if fl == nil {
		return nil
	}
	var names []string
	for _, f := range fl.List {
		if len(f.Names) == 0 {
			names = append(names, fc.e.newTmp("p"))
			continue
		}
		for _, n := range f.Names {
			if n.Name == "_" {
				names = append(names, fc.e.newTmp("p"))
			} else {
				names = append(names, fc.localName(n))
			}
		}
	}
	return names
}

func (fc *funcCtx) localName(id *ast.Ident) string {
	obj := fc.info().ObjectOf(id)
	if obj == nil {
		return luaIdent(id.Name)
	}
	return fc.varName(obj)
}

func (fc *funcCtx) varName(obj types.Object) string {
	return luaIdent(obj.Name())
}

// prologue boxes address-taken params and copies aggregate params that are mutated.
func (fc *funcCtx) prologue(w *writer, recv *ast.FieldList, ft *ast.FuncType) {
	var fields []*ast.Field
	if recv != nil {
		fields = append(fields, recv.List...)
	}
	if ft.Params != nil {
		fields = append(fields, ft.Params.List...)
	}
	for _, f := range fields {
		for _, n := range f.Names {
			v, ok := fc.info().Defs[n].(*types.Var)
			if !ok || n.Name == "_" {
				continue
			}
			name := fc.varName(v)
			if fc.e.c.boxed[v] {
				w.line("%s = {v = %s}", name, name)
			} else if isAggregate(v.Type()) && fc.e.c.mutatedVars[v] {
				w.line("%s = %s", name, fc.cloneExpr(name, v.Type()))
			} else if isTypeParam(v.Type()) && fc.e.c.mutatedVars[v] {
				tp := fc.typeExpr(v.Type())
				w.line("if %s.aggr then %s = %s.clone(%s) end", tp, name, tp, name)
			}
		}
	}
	// Named results start at their zero values.
	if ft.Results != nil {
		named := false
		for _, f := range ft.Results.List {
			if len(f.Names) > 0 {
				named = true
			}
		}
		if named {
			fc.namedRes = true
			for _, f := range ft.Results.List {
				for _, n := range f.Names {
					v := fc.info().Defs[n].(*types.Var)
					var name string
					if n.Name == "_" {
						name = fc.e.newTmp("r")
					} else {
						name = fc.varName(v)
					}
					fc.results = append(fc.results, name)
					if fc.e.c.boxed[v] {
						w.line("local %s = {v = %s}", name, fc.zeroValue(v.Type()))
					} else {
						w.line("local %s = %s", name, fc.zeroValue(v.Type()))
					}
				}
			}
		}
	}
}

func (fc *funcCtx) cloneExpr(expr string, t types.Type) string {
	return fmt.Sprintf("%s.clone(%s)", prefix(fc.typeExpr(t)), expr)
}

func (fc *funcCtx) cloneIfNeeded(expr string, t types.Type) string {
	if isTypeParam(t) {
		// Only aggregates need copying; for other type arguments, skip the identity call. A
		// clone is always a table, so the and/or form is exact, and expr is evaluated once.
		tp := fc.typeExpr(t)
		return fmt.Sprintf("(%s.aggr and %s.clone(%s) or %s)", tp, tp, expr, prefixOrParen(expr))
	}
	if fc.e.c.needsClone(t) {
		return fc.cloneExpr(expr, t)
	}
	return expr
}

// body emits a function body, wrapping it in a protected call when it defers.
func (fc *funcCtx) body(w *writer, b *ast.BlockStmt) {
	fc.findStringViews(b)
	fc.hasDefer = containsDefer(b)
	w.indent++
	if fc.hasDefer && fc.canHoistDeferBody() {
		// The body becomes a module-level function of the defer list and the parameters, so
		// no closure is made per call (see the closures.go comment), and returns its results.
		fc.hoistedDefer = true
		fc.deferVar = fc.e.newTmp("df")
		args := append([]string{fc.deferVar}, fc.params...)
		bw := &writer{}
		bw.line("function(%s)", strings.Join(args, ", "))
		bw.indent++
		fc.stmts(bw, b.List)
		bw.indent--
		bw.raw("end")
		fc.e.hoisted = append(fc.e.hoisted, reindent(bw.b.String()))
		bodyIndex := len(fc.e.hoisted)
		zero := fc.zeroResults()
		w.line("local %s = {}", fc.deferVar)
		w.line("return rt.finish(%s, %s, xpcall(FN[%d], rt.tb, %s))", fc.deferVar, zero, bodyIndex, strings.Join(args, ", "))
	} else if fc.hasDefer {
		if !fc.namedRes && fc.sig != nil && fc.sig.Results().Len() > 0 {
			for i := 0; i < fc.sig.Results().Len(); i++ {
				name := fc.e.newTmp("r")
				fc.results = append(fc.results, name)
				w.line("local %s", name)
			}
		}
		fc.deferVar = fc.e.newTmp("df")
		w.line("local %s = {}", fc.deferVar)
		w.line("rt.rundefers(%s, xpcall(function()", fc.deferVar)
		w.indent++
		fc.stmts(w, b.List)
		w.indent--
		w.line("end, rt.tb))")
		if len(fc.results) > 0 {
			w.line("return %s", strings.Join(fc.resultRefs(), ", "))
		}
	} else {
		fc.stmts(w, b.List)
	}
	w.indent--
}

// canHoistDeferBody reports whether the body of a function with defers can run as a
// module-level function, reaching its variables only through its parameters. Named results
// are excluded, since deferred calls may change them after a return.
func (fc *funcCtx) canHoistDeferBody() bool {
	if !fc.topLevel || fc.namedRes || fc.sig == nil {
		return false
	}
	for i := 0; i < fc.sig.Results().Len(); i++ {
		if isTypeParam(fc.sig.Results().At(i).Type()) {
			return false
		}
	}
	return true
}

// zeroResults is a module-level function returning the zero values of the results, which a
// function returns after recovering from a panic, or nil when they are all nil.
func (fc *funcCtx) zeroResults() string {
	var zeros []string
	allNil := true
	for i := 0; i < fc.sig.Results().Len(); i++ {
		z := fc.zeroValue(fc.sig.Results().At(i).Type())
		zeros = append(zeros, z)
		if z != "nil" {
			allNil = false
		}
	}
	if allNil {
		return "nil"
	}
	fc.e.hoisted = append(fc.e.hoisted, fmt.Sprintf("function() return %s end", strings.Join(zeros, ", ")))
	return fmt.Sprintf("FN[%d]", len(fc.e.hoisted))
}

func (fc *funcCtx) resultRefs() []string {
	var refs []string
	for i, r := range fc.results {
		if fc.namedRes && fc.sig != nil {
			v := fc.sig.Results().At(i)
			if fc.e.c.boxed[v] {
				refs = append(refs, r+".v")
				continue
			}
		}
		refs = append(refs, r)
	}
	return refs
}

func containsDefer(b *ast.BlockStmt) bool {
	found := false
	ast.Inspect(b, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt:
			found = true
			return false
		}
		return true
	})
	return found
}

// ---------------------------------------------------------------------------
// Constants

func (fc *funcCtx) constant(val constant.Value, t types.Type) string {
	var b *types.Basic
	if t != nil {
		b, _ = t.Underlying().(*types.Basic)
	}
	switch val.Kind() {
	case constant.Bool:
		if constant.BoolVal(val) {
			return "true"
		}
		return "false"
	case constant.String:
		return luaString(constant.StringVal(val))
	case constant.Int, constant.Float:
		isFloat := val.Kind() == constant.Float
		if b != nil {
			if b.Info()&types.IsFloat != 0 {
				isFloat = true
			} else if b.Info()&types.IsInteger != 0 {
				isFloat = false
			}
		}
		if isTypeParam(t) {
			if val.Kind() == constant.Int {
				return fmt.Sprintf("%s.const(%s)", fc.typeExpr(t), genericConst(val))
			}
			return fmt.Sprintf("%s.const(%s)", fc.typeExpr(t), fc.constant(val, nil))
		}
		if !isFloat {
			return intConstant(constant.ToInt(val), t)
		}
		f, _ := constant.Float64Val(constant.ToFloat(val))
		return floatLit(f)
	case constant.Complex:
		re, _ := constant.Float64Val(constant.Real(val))
		im, _ := constant.Float64Val(constant.Imag(val))
		return fmt.Sprintf("rt.complex(%s, %s)", floatLit(re), floatLit(im))
	}
	return "nil"
}

func floatLit(f float64) string {
	switch {
	case f != f:
		return "(0.0/0.0)"
	case f > 1.7976931348623157e308:
		return "math.huge"
	case f < -1.7976931348623157e308:
		return "(-math.huge)"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEn") {
		s += ".0"
	}
	if strings.HasPrefix(s, "-") {
		return "(" + s + ")"
	}
	return s
}
