package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"
)

// isPrefixExp reports whether s can be used directly before ".x", "[x]", ":m()" or "(args)".
func isPrefixExp(s string) bool {
	i := 0
	n := len(s)
	ident := func() bool {
		start := i
		for i < n && (s[i] == '_' || s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z' || (i > start && s[i] >= '0' && s[i] <= '9')) {
			i++
		}
		return i > start
	}
	// skipBalanced skips a bracketed group starting at s[i], honoring string literals.
	skipBalanced := func() bool {
		depth := 0
		for i < n {
			c := s[i]
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
				if depth == 0 {
					i++
					return true
				}
			case '\'', '"':
				q := c
				i++
				for i < n && s[i] != q {
					if s[i] == '\\' {
						i++
					}
					i++
				}
			}
			i++
		}
		return false
	}
	if i < n && s[i] == '(' {
		if !skipBalanced() {
			return false
		}
	} else if !ident() {
		return false
	}
	for i < n {
		switch s[i] {
		case '.':
			i++
			if !ident() {
				return false
			}
		case ':':
			i++
			if !ident() || i >= n || s[i] != '(' {
				return false
			}
			if !skipBalanced() {
				return false
			}
		case '[', '(':
			if !skipBalanced() {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func prefix(s string) string {
	// nil, true and false parse as names but cannot be indexed or called without parentheses.
	if isPrefixExp(s) && s != "nil" && s != "true" && s != "false" {
		return s
	}
	return "(" + s + ")"
}

// wrappedInParens reports code that is one parenthesized group, such as "(a or b)" (but not
// "(f)(x)").
func wrappedInParens(code string) bool {
	if !strings.HasPrefix(code, "(") {
		return false
	}
	depth := 0
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(code)-1
			}
		case '\'', '"':
			// Skip string literals, which may contain parentheses.
			q := code[i]
			for i++; i < len(code) && code[i] != q; i++ {
				if code[i] == '\\' {
					i++
				}
			}
		}
	}
	return false
}

// isSimple reports whether an expression can be evaluated multiple times without
// side effects or significant cost (identifiers and field chains).
func isSimple(s string) bool {
	if !isPrefixExp(s) {
		return false
	}
	return !strings.ContainsAny(s, "([:")
}

// ---------------------------------------------------------------------------

// platformConst reports whether obj is runtime.GOOS or runtime.GOARCH: packages load as wasip1/wasm,
// but the Lua runtime reports the real host, so these must be read at run time.
func platformConst(obj types.Object) bool {
	c, ok := obj.(*types.Const)
	return ok && c.Pkg() != nil && c.Pkg().Path() == "runtime" && (c.Name() == "GOOS" || c.Name() == "GOARCH")
}

// folded returns e's constant value unless e depends on a platform constant.
func (fc *funcCtx) folded(e ast.Expr) (constant.Value, types.Type, bool) {
	tv, ok := fc.info().Types[e]
	if !ok || tv.Value == nil {
		return nil, nil, false
	}
	platform := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && platformConst(fc.info().Uses[id]) {
			platform = true
		}
		return !platform
	})
	return tv.Value, tv.Type, !platform
}

func (fc *funcCtx) expr(e ast.Expr) string {
	if val, t, ok := fc.folded(e); ok {
		return fc.constant(val, t)
	}
	switch e := e.(type) {
	case *ast.ParenExpr:
		return fc.expr(e.X)
	case *ast.Ident:
		return fc.ident(e)
	case *ast.SelectorExpr:
		return fc.selector(e)
	case *ast.IndexExpr:
		return fc.index(e)
	case *ast.IndexListExpr:
		return fc.genericValue(e.X, e)
	case *ast.SliceExpr:
		return fc.sliceExpr(e)
	case *ast.StarExpr:
		return fc.deref(e)
	case *ast.UnaryExpr:
		return fc.unary(e)
	case *ast.BinaryExpr:
		return fc.binary(e)
	case *ast.CallExpr:
		return fc.call(e)
	case *ast.CompositeLit:
		return fc.compositeLit(e, fc.typeOf(e))
	case *ast.FuncLit:
		return fc.funcLit(e)
	case *ast.TypeAssertExpr:
		return fc.typeAssert(e, false)
	case *ast.BasicLit:
		// Synthetic literal (e.g. the 1 in x++), not type checked.
		return e.Value
	}
	fc.e.c.warnf(fc.e.pos(e.Pos()), "unsupported expression %T", e)
	return "rt.unsupported()"
}

// convertExpr evaluates e and converts it to the target type (interface boxing).
func (fc *funcCtx) convertExpr(e ast.Expr, target types.Type) string {
	from := fc.typeOf(e)
	if tv, ok := fc.info().Types[e]; ok && tv.IsNil() {
		return "nil"
	}
	if val, _, ok := fc.folded(e); ok && target != nil && !isInterface(target) {
		return fc.constant(val, target)
	}
	return fc.convertValue(fc.expr(e), from, target)
}

func (fc *funcCtx) convertValue(code string, from, to types.Type) string {
	if to == nil || from == nil {
		return code
	}
	if b, ok := from.(*types.Basic); ok && b.Kind() == types.UntypedNil {
		return "nil"
	}
	if isInterface(to) && !isInterface(from) {
		return fc.box(code, from)
	}
	if isTypeParam(to) && !isTypeParam(from) && isInterface(from) {
		return code
	}
	return code
}

// box converts a concrete value to its interface representation.
func (fc *funcCtx) box(code string, t types.Type) string {
	if isTypeParam(t) {
		return fmt.Sprintf("%s.box(%s)", fc.typeExpr(t), code)
	}
	if b, ok := types.Unalias(t).(*types.Basic); ok {
		switch b.Kind() {
		case types.Bool, types.String, types.Int, types.UntypedBool, types.UntypedString, types.UntypedInt:
			return code
		case types.Float64, types.UntypedFloat:
			// int and float64 are both Lua numbers, so a raw number in an interface is an int.
			return fmt.Sprintf("box(rt.float64, %s)", code)
		case types.UntypedNil:
			return "nil"
		case types.UntypedRune:
			return fmt.Sprintf("box(rt.int32, %s)", code)
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Pointer:
		if isStructType(u.Elem()) {
			return fmt.Sprintf("rt.boxp(%s, %s)", fc.typeExpr(t), code)
		}
	case *types.Struct:
		return fmt.Sprintf("rt.boxv(%s, %s)", fc.typeExpr(t), code)
	case *types.Array:
		return fmt.Sprintf("%s.box(%s)", prefix(fc.typeExpr(t)), code)
	}
	return fmt.Sprintf("box(%s, %s)", fc.typeExpr(t), code)
}

// isFresh reports whether an aggregate-valued expression produces a new value
// that nothing else references (so storing it needs no copy).
func (fc *funcCtx) isFresh(e ast.Expr) bool {
	switch e := unparen(e).(type) {
	case *ast.CompositeLit:
		return true
	case *ast.CallExpr:
		if tv, ok := fc.info().Types[e.Fun]; ok && tv.IsType() {
			return len(e.Args) == 1 && fc.isFresh(e.Args[0])
		}
		if id, ok := unparen(e.Fun).(*ast.Ident); ok {
			if _, ok := fc.info().Uses[id].(*types.Builtin); ok {
				return true
			}
		}
		return false
	case *ast.BasicLit:
		return true
	}
	return false
}

// valueForStore evaluates e for storing into a location of type t (conversion + copy).
func (fc *funcCtx) valueForStore(e ast.Expr, t types.Type) string {
	code := fc.convertExpr(e, t)
	if t != nil && !isInterface(t) && !fc.isFresh(e) {
		if isTypeParam(t) {
			code = fc.cloneIfNeeded(code, t)
		} else if fc.e.c.needsClone(t) {
			code = fc.cloneExpr(code, t)
		}
	}
	return code
}

// ---------------------------------------------------------------------------
// Identifiers and selectors

func (fc *funcCtx) ident(id *ast.Ident) string {
	obj := fc.info().Uses[id]
	if obj == nil {
		obj = fc.info().Defs[id]
	}
	if obj == nil {
		// Synthetic identifiers created by the transpiler (temporaries).
		return id.Name
	}
	switch o := obj.(type) {
	case *types.Nil:
		return "nil"
	case *types.Var:
		if ph, ok := fc.paramSubst[o]; ok {
			return ph
		}
		if o.Pkg() != nil && o.Parent() == o.Pkg().Scope() {
			return fc.e.pkgVarRef(o)
		}
		if view := fc.viewOfVar(o); view != nil {
			return view.materialize()
		}
		name := fc.rawRef(o)
		if fc.e.c.boxed[o] {
			return name + ".v"
		}
		return name
	case *types.Func:
		if inst, ok := fc.info().Instances[id]; ok && inst.TypeArgs.Len() > 0 {
			return fc.partialGeneric(o, inst.TypeArgs)
		}
		return fc.funcRef(o)
	case *types.Const:
		if platformConst(o) {
			return fc.e.importAlias(o.Pkg()) + "." + o.Name()
		}
		return fc.constant(o.Val(), o.Type())
	case *types.Builtin:
		return "rt.builtin_" + o.Name()
	case *types.TypeName:
		return fc.typeExpr(o.Type())
	}
	return luaIdent(id.Name)
}

func (fc *funcCtx) partialGeneric(fn *types.Func, targs *types.TypeList) string {
	var args []string
	for i := 0; i < targs.Len(); i++ {
		args = append(args, fc.typeExpr(targs.At(i)))
	}
	return fmt.Sprintf("rt.partial(%s, %s)", fc.funcRef(fn), strings.Join(args, ", "))
}

func (fc *funcCtx) genericValue(x ast.Expr, e ast.Expr) string {
	var id *ast.Ident
	switch x := unparen(x).(type) {
	case *ast.Ident:
		id = x
	case *ast.SelectorExpr:
		id = x.Sel
	}
	if id != nil {
		if fn, ok := fc.info().Uses[id].(*types.Func); ok {
			if inst, ok := fc.info().Instances[id]; ok {
				return fc.partialGeneric(fn, inst.TypeArgs)
			}
		}
	}
	fc.e.c.warnf(fc.e.pos(e.Pos()), "unsupported generic value")
	return "rt.unsupported()"
}

func (fc *funcCtx) selector(se *ast.SelectorExpr) string {
	sel := fc.info().Selections[se]
	if sel == nil {
		// Qualified identifier.
		return fc.ident(se.Sel)
	}
	switch sel.Kind() {
	case types.FieldVal:
		return fc.fieldPath(fc.expr(se.X), fc.typeOf(se.X), sel.Index())
	case types.MethodVal:
		return fc.methodValue(se, sel)
	case types.MethodExpr:
		return fc.methodExpr(se, sel)
	}
	return "nil"
}

// fieldPath walks a field selection index path from a base expression.
func (fc *funcCtx) fieldPath(base string, t types.Type, path []int) string {
	expr := base
	for _, idx := range path {
		st := derefStruct(t)
		if st == nil {
			fc.e.c.warnf(fc.e.pos(0), "field path on non-struct %s", t)
			return expr
		}
		f := st.Field(idx)
		info := fc.e.c.layout(st).fields[idx]
		if !info.flat {
			expr = info.access(expr)
		}
		t = f.Type()
	}
	return expr
}

// fieldPathType returns the type at the end of a field index path.
func fieldPathType(t types.Type, path []int) types.Type {
	for _, idx := range path {
		st := derefStruct(t)
		t = st.Field(idx).Type()
	}
	return t
}

// methodTarget resolves a method selection to how it is invoked: either an
// interface dispatch on recv, or a static call of fn with typeArgs and recv.
type methodTarget struct {
	fnObj    *types.Func // static callee, for inlining
	iface    bool
	recv     string
	fn       string
	typeArgs []string
	name     string
	// For methods of an atomic integer struct field: the table holding it, and its storage.
	atomicTab   string
	atomicInfo  fieldInfo
	atomicValue types.Type
}

func (fc *funcCtx) methodTargetOf(se *ast.SelectorExpr, sel *types.Selection) methodTarget {
	fn := sel.Obj().(*types.Func)
	path := sel.Index()
	xt := fc.typeOf(se.X)
	atomicTarget := func(tab string, info fieldInfo) methodTarget {
		return methodTarget{fn: fc.funcRef(fn), recv: fmt.Sprintf("rt.fieldptr(%s, %s)", tab, info.luaKey()), name: fn.Name(), atomicTab: tab, atomicInfo: info,
			atomicValue: atomicValueType(fn.Type().(*types.Signature).Recv().Type().(*types.Pointer).Elem())}
	}
	if len(path) == 1 {
		if tab, info, ok := fc.atomicField(se.X); ok {
			return atomicTarget(tab, info)
		}
	}
	recv := fc.expr(se.X)
	if len(path) > 1 {
		if tab, info, ok := fc.atomicFieldAt(recv, xt, path[:len(path)-1]); ok {
			return atomicTarget(tab, info)
		}
	}
	// Native types do not follow the Go field layout: call the promoted method on the outer type.
	if len(path) > 1 {
		base := types.Unalias(xt)
		if p, ok := base.(*types.Pointer); ok {
			base = types.Unalias(p.Elem())
		}
		if n, ok := base.(*types.Named); ok && fc.e.c.isNativeType(n) {
			ref := fc.e.importAlias(n.Obj().Pkg()) + "." + luaIdent(n.Obj().Name()) + "__" + luaIdent(fn.Name())
			return methodTarget{fn: ref, recv: recv, typeArgs: fc.recvTypeArgs(xt), name: fn.Name()}
		}
	}
	t := xt
	if len(path) > 1 {
		recv = fc.fieldPath(recv, xt, path[:len(path)-1])
		t = fieldPathType(xt, path[:len(path)-1])
	}
	if isInterface(t) {
		return methodTarget{iface: true, recv: recv, name: fn.Name()}
	}
	sig := fn.Type().(*types.Signature)
	recvT := sig.Recv().Type()
	_, wantPtr := types.Unalias(recvT).(*types.Pointer)
	_, havePtr := types.Unalias(t).(*types.Pointer)
	if isTypeParam(t) {
		// Method call through a type parameter's constraint: dispatch dynamically on the boxed value.
		return methodTarget{iface: true, recv: fmt.Sprintf("%s.box(%s)", fc.typeExpr(t), recv), name: fn.Name()}
	}
	if !isStructish(t) {
		if wantPtr && !havePtr {
			recv = fc.addrOfCode(se.X, path, recv)
		} else if !wantPtr && havePtr {
			recv = prefix(recv) + ".v"
		}
	} else if !wantPtr && fc.e.c.needsClone(t) && fc.e.c.mutatedRecv(fn) {
		recv = fc.cloneExpr(recv, t)
	}
	targs := fc.recvTypeArgs(t)
	// Generic methods (Go 1.27) take their own type arguments after the receiver's.
	if sig.TypeParams().Len() > 0 {
		if inst, ok := fc.info().Instances[se.Sel]; ok {
			for i := 0; i < inst.TypeArgs.Len(); i++ {
				targs = append(targs, fc.typeExpr(inst.TypeArgs.At(i)))
			}
		}
	}
	return methodTarget{fnObj: fn, fn: fc.funcRef(fn), recv: recv, typeArgs: targs, name: fn.Name()}
}

func (c *Compiler) mutatedRecv(fn *types.Func) bool {
	sig := fn.Type().(*types.Signature)
	if sig.Recv() == nil {
		return false
	}
	return c.mutatedVars[sig.Recv()]
}

// addrOfCode produces a pointer to a non-struct addressable receiver.
func (fc *funcCtx) addrOfCode(x ast.Expr, path []int, recv string) string {
	if len(path) <= 1 {
		if id, ok := unparen(x).(*ast.Ident); ok {
			if v, ok := fc.info().Uses[id].(*types.Var); ok && fc.e.c.boxed[v] {
				if v.Pkg() != nil && v.Parent() == v.Pkg().Scope() {
					return fc.e.importAlias(v.Pkg()) + "." + luaIdent(v.Name())
				}
				return fc.rawRef(v)
			}
		}
		return fc.addressOf(x)
	}
	// Embedded non-struct field reached through a struct: field pointer proxy.
	if m := slotRef.FindStringSubmatch(recv); m != nil {
		return fmt.Sprintf("rt.fieldptr(%s, %s)", m[1], m[2])
	}
	i := strings.LastIndex(recv, ".")
	if i > 0 {
		return fmt.Sprintf("rt.fieldptr(%s, %s)", recv[:i], luaString(recv[i+1:]))
	}
	return recv
}

func (fc *funcCtx) formatMethodCall(mt methodTarget, args []string) string {
	if mt.atomicTab != "" {
		if code, ok := fc.atomicCall(mt, args); ok {
			return code
		}
	}
	if mt.iface {
		return fmt.Sprintf("%s:%s(%s)", prefix(mt.recv), methodKey(mt.name), strings.Join(args, ", "))
	}
	if mt.fnObj != nil && len(mt.typeArgs) == 0 {
		if code, ok := fc.e.c.inlineCall(mt.fnObj, append([]string{mt.recv}, args...), fc); ok {
			return code
		}
	}
	all := append(append(append([]string{}, mt.typeArgs...), mt.recv), args...)
	return fmt.Sprintf("%s(%s)", mt.fn, strings.Join(all, ", "))
}

func (fc *funcCtx) methodValue(se *ast.SelectorExpr, sel *types.Selection) string {
	mt := fc.methodTargetOf(se, sel)
	if mt.iface {
		return fmt.Sprintf("rt.ibind(%s, %s)", mt.recv, luaString(methodKey(mt.name)))
	}
	args := append(append([]string{}, mt.typeArgs...), mt.recv)
	return fmt.Sprintf("rt.bind(%s, %s)", mt.fn, strings.Join(args, ", "))
}

func (fc *funcCtx) methodExpr(se *ast.SelectorExpr, sel *types.Selection) string {
	fn := sel.Obj().(*types.Func)
	recvT := sel.Recv()
	if isInterface(recvT) {
		return fmt.Sprintf("rt.imethod(%s)", luaString(methodKey(fn.Name())))
	}
	path := sel.Index()
	if len(path) > 1 {
		// Promoted method expression: adapt the receiver.
		recv := fc.fieldPath("r", recvT, path[:len(path)-1])
		t := fieldPathType(recvT, path[:len(path)-1])
		if isInterface(t) {
			return fmt.Sprintf("function(r, ...) return %s:%s(...) end", prefix(recv), methodKey(fn.Name()))
		}
		args := append(fc.recvTypeArgs(t), recv)
		return fmt.Sprintf("function(r, ...) return %s(%s, ...) end", fc.funcRef(fn), strings.Join(args, ", "))
	}
	sigRecv := fn.Type().(*types.Signature).Recv().Type()
	_, wantPtr := types.Unalias(sigRecv).(*types.Pointer)
	_, havePtr := types.Unalias(recvT).(*types.Pointer)
	targs := fc.recvTypeArgs(recvT)
	if !wantPtr && havePtr && !isStructish(recvT) {
		args := append(targs, "r.v")
		return fmt.Sprintf("function(r, ...) return %s(%s, ...) end", fc.funcRef(fn), strings.Join(args, ", "))
	}
	if len(targs) > 0 {
		return fmt.Sprintf("rt.partial(%s, %s)", fc.funcRef(fn), strings.Join(targs, ", "))
	}
	return fc.funcRef(fn)
}

// ---------------------------------------------------------------------------
// Indexing and slicing

func (fc *funcCtx) index(e *ast.IndexExpr) string {
	// Generic function instantiation used as a value.
	if tv, ok := fc.info().Types[e.X]; ok {
		if _, isSig := tv.Type.(*types.Signature); isSig {
			if id := identOf(e.X); id != nil {
				if _, ok := fc.info().Instances[id]; ok {
					return fc.genericValue(e.X, e)
				}
			}
		}
	}
	xt := fc.typeOf(e.X)
	x := fc.expr(e.X)
	switch u := coreType(xt).(type) {
	case *types.Basic: // string
		if view := fc.viewOf(e.X); view != nil {
			return fmt.Sprintf("sbyte(%s, %s)", view.s, addOffset(view.o, fc.plusOne(e.Index)))
		}
		return fmt.Sprintf("sbyte(%s, %s)", x, fc.plusOne(e.Index))
	case *types.Slice:
		return fc.sliceIndex(x, asLuaNumber(fc.expr(e.Index), fc.typeOf(e.Index)))
	case *types.Array:
		return fmt.Sprintf("%s[%s]", prefix(x), fc.plusOne(e.Index))
	case *types.Pointer:
		return fmt.Sprintf("%s[%s]", prefix(x), fc.plusOne(e.Index))
	case *types.Map:
		return fc.mapIndex(x, e.Index, u)
	}
	fc.e.c.warnf(fc.e.pos(e.Pos()), "unsupported index on %s", xt)
	return "rt.unsupported()"
}

func identOf(e ast.Expr) *ast.Ident {
	switch x := unparen(e).(type) {
	case *ast.Ident:
		return x
	case *ast.SelectorExpr:
		return x.Sel
	}
	return nil
}

// coreType returns the underlying type, resolving type parameters with a single core type.
func coreType(t types.Type) types.Type {
	if tp, ok := types.Unalias(t).(*types.TypeParam); ok {
		var core types.Type
		iface := tp.Constraint().Underlying().(*types.Interface)
		for i := 0; i < iface.NumEmbeddeds(); i++ {
			et := iface.EmbeddedType(i)
			if u, ok := et.(*types.Union); ok {
				for j := 0; j < u.Len(); j++ {
					ut := u.Term(j).Type().Underlying()
					if core == nil {
						core = ut
					} else if !types.Identical(core, ut) {
						// Mixed string/[]byte style constraints: fall back to the first.
						return core
					}
				}
			} else {
				core = et.Underlying()
			}
		}
		if core != nil {
			return core
		}
		return t
	}
	return t.Underlying()
}

func (fc *funcCtx) plusOne(e ast.Expr) string {
	if tv, ok := fc.info().Types[e]; ok && tv.Value != nil {
		if i, ok := constant.Int64Val(constant.ToInt(tv.Value)); ok {
			return fmt.Sprintf("%d", i+1)
		}
	}
	return fmt.Sprintf("%s + 1", fc.intOperand(e))
}

func (fc *funcCtx) sliceIndex(x, idx string) string {
	if isSimple(x) {
		return fmt.Sprintf("%s[1][%s[2] + %s]", x, x, prefixOrParen(idx))
	}
	return fmt.Sprintf("rt.sget(%s, %s)", x, idx)
}

func (fc *funcCtx) mapIndex(m string, key ast.Expr, mt *types.Map) string {
	k := fc.mapKey(fc.convertExpr(key, mt.Key()), mt.Key())
	return fc.mapRead(m, k, mt)
}

// mapKey converts a key to its Lua table key representation.
func (fc *funcCtx) mapKey(code string, kt types.Type) string {
	// Non-direct keys are serialized by the runtime map helpers, which also keep the original key.
	if fc.mapKeyKind(kt) == "direct" && nilableKey(kt) {
		// Lua tables cannot hold nil keys; a nil pointer key is stored under NILKEY.
		if code == "nil" {
			return "NILKEY"
		}
		return fmt.Sprintf("(%s or NILKEY)", code)
	}
	return code
}

// nilableKey reports direct map key types that can be nil.
func nilableKey(kt types.Type) bool {
	switch u := kt.Underlying().(type) {
	case *types.Pointer, *types.Chan:
		return true
	case *types.Basic:
		return u.Kind() == types.UnsafePointer
	}
	return false
}

// mapKeyKind classifies how keys of type kt are stored.
func (fc *funcCtx) mapKeyKind(kt types.Type) string {
	if isTypeParam(kt) {
		return "dyn"
	}
	switch u := kt.Underlying().(type) {
	case *types.Basic:
		// FFI integers are distinct objects for each value, so they key tables by a serialization.
		if u.Info()&types.IsComplex != 0 || is64Rep(repOf(kt)) {
			return "struct"
		}
		return "direct"
	case *types.Pointer, *types.Chan:
		return "direct"
	case *types.Struct, *types.Array:
		return "struct"
	}
	return "dyn"
}

// mapStoresPairs reports maps whose entries are stored as {key, value} pairs
// because the Lua key is a serialization of the Go key.
func (fc *funcCtx) mapStoresPairs(mt *types.Map) bool {
	return fc.mapKeyKind(mt.Key()) != "direct"
}

// valueKind classifies map values: "nilable" ones store false for nil,
// "iface" ones store NIL for nil, "plain" ones never hold nil.
func valueKind(t types.Type) string {
	if isTypeParam(t) {
		return "dyn"
	}
	switch t.Underlying().(type) {
	case *types.Interface:
		return "iface"
	case *types.Pointer, *types.Slice, *types.Map, *types.Signature, *types.Chan:
		return "nilable"
	case *types.Basic:
		if b := t.Underlying().(*types.Basic); b.Kind() == types.UnsafePointer {
			return "nilable"
		}
	}
	return "plain"
}

func (fc *funcCtx) mapRead(m, k string, mt *types.Map) string {
	if fc.mapStoresPairs(mt) || valueKind(mt.Elem()) == "dyn" {
		return fmt.Sprintf("rt.mgetp(%s, %s, %s)", m, k, fc.mapDesc(mt))
	}
	raw := fmt.Sprintf("(%s or E)[%s]", m, k)
	switch valueKind(mt.Elem()) {
	case "nilable":
		return fmt.Sprintf("(%s or nil)", raw)
	case "iface":
		return fmt.Sprintf("nn(%s)", raw)
	}
	if z := fc.simpleZero(mt.Elem()); z != "" {
		return fmt.Sprintf("(%s or %s)", raw, z)
	}
	return fmt.Sprintf("(%s or %s)", raw, fc.zeroValue(mt.Elem()))
}

func (fc *funcCtx) mapDesc(mt *types.Map) string {
	return fc.typeExpr(mt)
}

// mapValueStore converts a value for storage in a map (nil sentinels).
func (fc *funcCtx) mapValueStore(code string, mt *types.Map) string {
	if fc.mapStoresPairs(mt) {
		return code
	}
	switch valueKind(mt.Elem()) {
	case "nilable":
		if code == "nil" {
			return "false"
		}
		return fmt.Sprintf("(%s or false)", code)
	case "iface":
		if code == "nil" {
			return "NIL"
		}
		return fmt.Sprintf("rt.nilv(%s)", code)
	}
	return code
}

func (fc *funcCtx) sliceExpr(e *ast.SliceExpr) string {
	x := fc.expr(e.X)
	lo, hi, max := "nil", "nil", ""
	if e.Low != nil {
		lo = asLuaNumber(fc.expr(e.Low), fc.typeOf(e.Low))
	}
	if e.High != nil {
		hi = asLuaNumber(fc.expr(e.High), fc.typeOf(e.High))
	}
	switch u := coreType(fc.typeOf(e.X)).(type) {
	case *types.Basic:
		if e.Low == nil && e.High == nil {
			return x
		}
		base, start, end := fc.stringRange(e)
		if end == "" {
			return fmt.Sprintf("ssub(%s, %s)", base, addOffset(start, "1"))
		}
		return fmt.Sprintf("ssub(%s, %s, %s)", base, addOffset(start, "1"), end)
	case *types.Array:
		x = fmt.Sprintf("rt.arrslice(%s, %d)", x, u.Len())
	case *types.Pointer:
		x = fmt.Sprintf("rt.arrslice(%s, %d)", x, u.Elem().Underlying().(*types.Array).Len())
	}
	if e.Slice3 {
		max = asLuaNumber(fc.expr(e.Max), fc.typeOf(e.Max))
		return fmt.Sprintf("rt.slice3(%s, %s, %s, %s)", x, lo, hi, max)
	}
	return fmt.Sprintf("slice(%s, %s, %s)", x, lo, hi)
}

// bitcastSource matches *(*T)(unsafe.Pointer(p)) and returns p when it points to a different basic type.
func (fc *funcCtx) bitcastSource(e *ast.StarExpr) (ast.Expr, *types.Basic, *types.Basic) {
	outer, ok := unparen(e.X).(*ast.CallExpr)
	if !ok || len(outer.Args) != 1 {
		return nil, nil, nil
	}
	if tv, ok := fc.info().Types[outer.Fun]; !ok || !tv.IsType() {
		return nil, nil, nil
	}
	inner, ok := unparen(outer.Args[0]).(*ast.CallExpr)
	if !ok || len(inner.Args) != 1 {
		return nil, nil, nil
	}
	if b, ok := fc.typeOf(inner).Underlying().(*types.Basic); !ok || b.Kind() != types.UnsafePointer {
		return nil, nil, nil
	}
	src := inner.Args[0]
	sp, ok := fc.typeOf(src).Underlying().(*types.Pointer)
	if !ok {
		return nil, nil, nil
	}
	from, ok1 := sp.Elem().Underlying().(*types.Basic)
	to, ok2 := fc.typeOf(e).Underlying().(*types.Basic)
	if !ok1 || !ok2 || from.Kind() == to.Kind() {
		return nil, nil, nil
	}
	return src, from, to
}

func (fc *funcCtx) deref(e *ast.StarExpr) string {
	if src, from, to := fc.bitcastSource(e); src != nil {
		val := prefix(fc.expr(src)) + ".v"
		return fmt.Sprintf("rt.bitcast(%s, %s, %s)", luaString(basicKind(from)), luaString(basicKind(to)), val)
	}
	x := fc.expr(e.X)
	t := fc.typeOf(e)
	if isTypeParam(t) {
		return fmt.Sprintf("rt.derefT(%s, %s)", fc.typeExpr(t), x)
	}
	if isStructType(t) || isArrayType(t) {
		return x
	}
	return prefix(x) + ".v"
}

func isArrayType(t types.Type) bool {
	_, ok := t.Underlying().(*types.Array)
	return ok
}

// addressOf produces a pointer for &x.
func (fc *funcCtx) addressOf(x ast.Expr) string {
	x = unparen(x)
	t := fc.typeOf(x)
	if cl, ok := x.(*ast.CompositeLit); ok {
		return fc.compositeLit(cl, t)
	}
	if tab, info, ok := fc.atomicField(x); ok {
		return fmt.Sprintf("rt.fieldptr(%s, %s)", tab, info.luaKey())
	}
	if isStructType(t) || isArrayType(t) {
		return fc.expr(x)
	}
	switch x := x.(type) {
	case *ast.Ident:
		if v, ok := fc.info().Uses[x].(*types.Var); ok {
			if v.Pkg() != nil && v.Parent() == v.Pkg().Scope() {
				return fc.e.importAlias(v.Pkg()) + "." + luaIdent(v.Name())
			}
			if isTypeParam(t) {
				// Boxed type-parameter variable: a struct value is its own pointer.
				return fmt.Sprintf("rt.addrT(%s, %s)", fc.typeExpr(t), fc.rawRef(v))
			}
			return fc.rawRef(v)
		}
	case *ast.SelectorExpr:
		sel := fc.info().Selections[x]
		if sel == nil {
			if v, ok := fc.info().Uses[x.Sel].(*types.Var); ok {
				return fc.e.importAlias(v.Pkg()) + "." + luaIdent(v.Name())
			}
		}
		if sel != nil && sel.Kind() == types.FieldVal {
			path := sel.Index()
			base := fc.fieldPath(fc.expr(x.X), fc.typeOf(x.X), path[:len(path)-1])
			st := derefStruct(fieldPathType(fc.typeOf(x.X), path[:len(path)-1]))
			info := fc.e.c.layout(st).fields[path[len(path)-1]]
			if isTypeParam(t) {
				return fmt.Sprintf("rt.fieldptrT(%s, %s, %s)", fc.typeExpr(t), base, info.luaKey())
			}
			return fmt.Sprintf("rt.fieldptr(%s, %s)", base, info.luaKey())
		}
	case *ast.IndexExpr:
		xt := fc.typeOf(x.X)
		switch coreType(xt).(type) {
		case *types.Slice:
			if isTypeParam(t) {
				return fmt.Sprintf("rt.elemptrT(%s, %s, %s)", fc.typeExpr(t), fc.expr(x.X), fc.expr(x.Index))
			}
			return fmt.Sprintf("rt.elemptr(%s, %s)", fc.expr(x.X), fc.expr(x.Index))
		case *types.Array, *types.Pointer:
			return fmt.Sprintf("rt.fieldptr(%s, %s)", fc.expr(x.X), fc.plusOne(x.Index))
		}
	case *ast.StarExpr:
		return fc.expr(x.X)
	}
	fc.e.c.warnf(fc.e.pos(x.Pos()), "unsupported address-of %T", x)
	return "rt.unsupported()"
}

// ---------------------------------------------------------------------------
// Operators

func (fc *funcCtx) unary(e *ast.UnaryExpr) string {
	switch e.Op {
	case token.AND:
		return fc.addressOf(e.X)
	case token.NOT:
		return fmt.Sprintf("not %s", fc.operand(e.X))
	case token.SUB:
		t := fc.typeOf(e)
		if isComplex(t) {
			return fmt.Sprintf("rt.cneg(%s)", fc.expr(e.X))
		}
		return fc.negate(fc.operand(e.X), t)
	case token.ADD:
		return fc.expr(e.X)
	case token.XOR:
		return fc.complement(fc.expr(e.X), fc.typeOf(e))
	case token.ARROW:
		return fmt.Sprintf("rt.recv(%s)", fc.expr(e.X))
	}
	fc.e.c.warnf(fc.e.pos(e.Pos()), "unsupported unary %s", e.Op)
	return "rt.unsupported()"
}

// operand renders a subexpression for use inside an operator expression.
func (fc *funcCtx) operand(e ast.Expr) string {
	s := fc.expr(e)
	if isPrefixExp(s) || isLiteral(s) {
		return s
	}
	return "(" + s + ")"
}

func isLiteral(s string) bool {
	if s == "" {
		return false
	}
	if s == "nil" || s == "true" || s == "false" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c == '.' || c == 'x' || c == 'X' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == 'e' || c == '+') {
			return false
		}
	}
	return s[0] >= '0' && s[0] <= '9'
}

func isComplex(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsComplex != 0
}

func basicOf(t types.Type) *types.Basic {
	if t == nil {
		return nil
	}
	b, _ := coreType(t).(*types.Basic)
	return b
}

func isFloat(t types.Type) bool {
	b := basicOf(t)
	return b != nil && b.Info()&types.IsFloat != 0
}

func isString(t types.Type) bool {
	b := basicOf(t)
	return b != nil && b.Info()&types.IsString != 0
}

func isIntegerType(t types.Type) bool {
	b := basicOf(t)
	return b != nil && b.Info()&types.IsInteger != 0
}

func (fc *funcCtx) binary(e *ast.BinaryExpr) string {
	switch e.Op {
	case token.LAND, token.LOR:
		return fc.logicalChain(e)
	case token.EQL, token.NEQ:
		return fc.equality(e)
	}
	xt := fc.typeOf(e.X)
	t := fc.typeOf(e)
	switch e.Op {
	case token.LSS, token.GTR, token.LEQ, token.GEQ:
		return fc.compare(e.Op, e.X, e.Y, fc.operand(e.X), fc.operand(e.Y), xt)
	case token.SHL, token.SHR:
		return fc.shift(e.Op, fc.expr(e.X), e.Y, t)
	}
	return fc.arith(e.Op, e.X, e.Y, t)
}

// arith renders an arithmetic or bitwise operation, flattening left-associative chains.
func (fc *funcCtx) arith(op token.Token, xe, ye ast.Expr, t types.Type) string {
	if isComplex(t) {
		names := map[token.Token]string{token.ADD: "cadd", token.SUB: "csub", token.MUL: "cmul", token.QUO: "cdiv"}
		return fmt.Sprintf("rt.%s(%s, %s)", names[op], fc.expr(xe), fc.expr(ye))
	}
	if op == token.ADD && isString(t) {
		parts := fc.concatParts(xe, ye)
		return strings.Join(parts, " .. ")
	}
	if isTypeParam(t) && (op == token.ADD || op == token.SUB || op == token.MUL || op == token.QUO || op == token.REM) {
		return fmt.Sprintf("%s.op(%s, %s, %s)", fc.typeExpr(t), luaString(op.String()), fc.expr(xe), fc.expr(ye))
	}
	x := fc.operand(xe)
	if be, ok := unparen(xe).(*ast.BinaryExpr); ok && be.Op == op && !needsWrap(t) && (op == token.ADD || op == token.MUL || op == token.OR || op == token.AND || op == token.XOR) && !isFloatOrStringMismatch(fc, be) {
		x = fc.expr(xe)
	}
	y := fc.operand(ye)
	return fc.arithCode(op, x, y, t)
}

func isFloatOrStringMismatch(fc *funcCtx, be *ast.BinaryExpr) bool {
	tv, ok := fc.info().Types[be]
	return ok && tv.Value != nil
}

func needsWrap(t types.Type) bool {
	b := basicOf(t)
	if b == nil {
		return false
	}
	switch b.Kind() {
	case types.Uint8, types.Uint16, types.Uint32, types.Int8, types.Int16, types.Int32, types.Float32:
		return true
	}
	return false
}

func prefixOrParen(s string) string {
	if isPrefixExp(s) || isLiteral(s) {
		return s
	}
	return "(" + s + ")"
}

func (fc *funcCtx) concatParts(xe, ye ast.Expr) []string {
	var parts []string
	var add func(e ast.Expr)
	add = func(e ast.Expr) {
		e = unparen(e)
		if be, ok := e.(*ast.BinaryExpr); ok && be.Op == token.ADD && isString(fc.typeOf(be)) {
			if tv := fc.info().Types[be]; tv.Value == nil {
				add(be.X)
				add(be.Y)
				return
			}
		}
		parts = append(parts, fc.operand(e))
	}
	add(xe)
	add(ye)
	return parts
}

func (fc *funcCtx) logicalChain(e *ast.BinaryExpr) string {
	op := "and"
	if e.Op == token.LOR {
		op = "or"
	}
	var parts []string
	var add func(x ast.Expr)
	add = func(x ast.Expr) {
		if be, ok := unparen(x).(*ast.BinaryExpr); ok && be.Op == e.Op {
			add(be.X)
			add(be.Y)
			return
		}
		parts = append(parts, fc.operand(x))
	}
	add(e)
	return strings.Join(parts, " "+op+" ")
}

func (fc *funcCtx) equality(e *ast.BinaryExpr) string {
	return fc.equalityCode(e.X, e.Y, e.Op == token.NEQ)
}

// equalityCode renders x == y (or x ~= y when neg is set).
func (fc *funcCtx) equalityCode(xe, ye ast.Expr, neg bool) string {
	plain := func(x, y string) string {
		if neg {
			return x + " ~= " + y
		}
		return x + " == " + y
	}
	call := func(s string) string {
		if neg {
			return "not " + s
		}
		return s
	}
	xt, yt := fc.typeOf(xe), fc.typeOf(ye)
	xNil := fc.info().Types[xe].IsNil()
	yNil := fc.info().Types[ye].IsNil()
	if xNil || yNil {
		return plain(fc.operand(xe), fc.operand(ye))
	}
	xi, yi := isInterface(xt), isInterface(yt)
	if xi || yi {
		x, y := fc.expr(xe), fc.expr(ye)
		// Comparing against a raw bool or string is a plain comparison (numbers need a kind check).
		if !xi && rawPlainEq(xt) || !yi && rawPlainEq(yt) {
			return plain(prefixOrParen(x), prefixOrParen(y))
		}
		if !xi {
			x = fc.box(x, xt)
		}
		if !yi {
			y = fc.box(y, yt)
		}
		return call(fmt.Sprintf("ieq(%s, %s)", x, y))
	}
	if isTypeParam(xt) {
		return call(fmt.Sprintf("%s.eq(%s, %s)", fc.typeExpr(xt), fc.expr(xe), fc.expr(ye)))
	}
	if isAggregate(xt) {
		return call(fmt.Sprintf("rt.eq(%s, %s, %s)", fc.typeExpr(xt), fc.expr(xe), fc.expr(ye)))
	}
	if isComplex(xt) {
		return call(fmt.Sprintf("rt.ceq(%s, %s)", fc.expr(xe), fc.expr(ye)))
	}
	return plain(fc.operand(xe), fc.operand(ye))
}

// ---------------------------------------------------------------------------
// Function literals

func (fc *funcCtx) funcLit(fl *ast.FuncLit) string {
	sig := fc.typeOf(fl).(*types.Signature)
	inner := fc.e.newFuncCtx(fc, sig)
	// A literal that captures nothing is created once, at module level, rather than on every
	// evaluation (comparators and predicates are passed this way in hot code). Keeping it out
	// of the enclosing function also matters to LuaJIT: a function containing any closure
	// closes upvalues at every return, which its JIT cannot compile. For the same reason, a
	// literal that captures variables becomes a callable table (closures.go).
	hoist := !fc.capturesLocals(fl)
	caps, convert := fc.e.c.closureCaps[fl]
	params := inner.paramNames(fl.Type.Params)
	// In generic code, the type descriptors are captured after the variables and become
	// locals of the same names, so the body refers to them as usual.
	var typeParams []string
	if convert {
		inner.captureRefs = map[*types.Var]string{}
		for i, v := range caps {
			inner.captureRefs[v] = fmt.Sprintf("__c[%d]", i+1)
		}
		params = append([]string{"__c"}, params...)
		for _, tp := range fc.enclosingTypeParams() {
			typeParams = append(typeParams, typeParamName(tp))
		}
	}
	inner.params = params
	inner.topLevel = hoist || convert
	w := &writer{}
	w.line("function(%s)", strings.Join(params, ", "))
	w.indent++
	if len(typeParams) > 0 {
		var slots []string
		for i := range typeParams {
			slots = append(slots, fmt.Sprintf("__c[%d]", len(caps)+i+1))
		}
		w.line("local %s = %s", strings.Join(typeParams, ", "), strings.Join(slots, ", "))
	}
	inner.prologue(w, nil, fl.Type)
	w.indent--
	inner.body(w, fl.Body)
	w.raw("end")
	code := reindent(w.b.String())
	// A function-local type is a local of the enclosing function, out of reach at module level.
	if hoist && !usesLocalType(code) {
		fc.e.hoisted = append(fc.e.hoisted, code)
		return fmt.Sprintf("FN[%d]", len(fc.e.hoisted))
	}
	if convert {
		refs := make([]string, len(caps))
		for i, v := range caps {
			refs[i] = fc.rawRef(v)
		}
		refs = append(refs, typeParams...)
		code, localTypes := captureLocalTypes(code, "__c", len(refs))
		refs = append(refs, localTypes...)
		fc.e.hoisted = append(fc.e.hoisted, "rt.closure("+code+")")
		return fmt.Sprintf("setmetatable({%s}, FN[%d])", strings.Join(refs, ", "), len(fc.e.hoisted))
	}
	return code
}

// capturesLocals reports whether a function literal refers to a variable declared in an
// enclosing function (or sits in a generic function, whose type parameters it may use).
func (fc *funcCtx) capturesLocals(fl *ast.FuncLit) bool {
	if len(fc.enclosingTypeParams()) > 0 {
		return true
	}
	captures := false
	ast.Inspect(fl.Body, func(n ast.Node) bool {
		if captures {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := fc.info().Uses[id].(*types.Var)
		if !ok || v.IsField() || v.Pkg() == nil || v.Parent() == v.Pkg().Scope() {
			return true
		}
		if v.Pos() < fl.Pos() || v.Pos() >= fl.End() {
			captures = true
		}
		return true
	})
	return captures
}

// reindent marks a multi-line expression so the statement writer can indent it.
func reindent(s string) string {
	return s
}

// ---------------------------------------------------------------------------
// Type assertions

func (fc *funcCtx) typeAssert(e *ast.TypeAssertExpr, commaOk bool) string {
	x := fc.expr(e.X)
	t := fc.typeOf(e.Type)
	suffix := ""
	if commaOk {
		suffix = "2"
	}
	if isInterface(t) {
		return fmt.Sprintf("rt.asserti%s(%s, %s)", suffix, x, fc.typeExpr(t))
	}
	if commaOk {
		return fmt.Sprintf("rt.assert2(%s, %s, %s)", x, fc.typeExpr(t), fc.zeroValue(t))
	}
	return fmt.Sprintf("rt.assert(%s, %s)", x, fc.typeExpr(t))
}

// rawPlainEq reports raw interface representations where Lua == matches Go interface equality.
func rawPlainEq(t types.Type) bool {
	b, ok := types.Unalias(t).(*types.Basic)
	if !ok {
		return false
	}
	switch b.Kind() {
	case types.Bool, types.String, types.UntypedBool, types.UntypedString:
		return true
	}
	return false
}
