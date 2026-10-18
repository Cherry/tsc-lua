package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strings"
)

func (fc *funcCtx) call(call *ast.CallExpr) string {
	// Conversion.
	if tv, ok := fc.info().Types[call.Fun]; ok && tv.IsType() {
		return fc.conversion(call.Args[0], tv.Type)
	}
	fun := unparen(call.Fun)
	// Builtins.
	if id, ok := fun.(*ast.Ident); ok {
		if b, ok := fc.info().Uses[id].(*types.Builtin); ok {
			return fc.builtin(b.Name(), call)
		}
	}
	if se, ok := fun.(*ast.SelectorExpr); ok {
		if b, ok := fc.info().Uses[se.Sel].(*types.Builtin); ok {
			// unsafe.Sizeof etc. that were not constant folded.
			return fc.builtin(b.Name(), call)
		}
	}
	if code, ok := fc.rangeCall(call); ok {
		return code
	}
	sig, _ := coreType(fc.typeOf(call.Fun)).(*types.Signature)
	args := fc.args(call, sig)
	return fc.callWith(call, args)
}

// callWith renders the call with pre-rendered argument code.
func (fc *funcCtx) callWith(call *ast.CallExpr, args []string) string {
	fun := unparen(call.Fun)
	// Method call, possibly with explicit type arguments for a generic method (x.m[T](...)).
	methodFun := fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		methodFun = unparen(f.X)
	case *ast.IndexListExpr:
		methodFun = unparen(f.X)
	}
	if se, ok := methodFun.(*ast.SelectorExpr); ok {
		if sel := fc.info().Selections[se]; sel != nil && sel.Kind() == types.MethodVal {
			mt := fc.methodTargetOf(se, sel)
			return fc.formatMethodCall(mt, args)
		}
	}
	// Static function (possibly generic).
	var id *ast.Ident
	var inner ast.Expr = fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		inner = unparen(f.X)
	case *ast.IndexListExpr:
		inner = unparen(f.X)
	}
	switch f := inner.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		if fc.info().Selections[f] == nil {
			id = f.Sel
		}
	}
	if id != nil {
		if fn, ok := fc.info().Uses[id].(*types.Func); ok {
			var targs []string
			if inst, ok := fc.info().Instances[id]; ok {
				for i := 0; i < inst.TypeArgs.Len(); i++ {
					targs = append(targs, fc.typeExpr(inst.TypeArgs.At(i)))
				}
			}
			all := append(targs, args...)
			if code, ok := fc.e.c.inlineCall(fn, all, fc); ok {
				return code
			}
			return fmt.Sprintf("%s(%s)", fc.funcRef(fn), strings.Join(all, ", "))
		}
	}
	return fmt.Sprintf("%s(%s)", prefix(fc.expr(call.Fun)), strings.Join(args, ", "))
}

// args renders call arguments with conversions and variadic packing.
func (fc *funcCtx) args(call *ast.CallExpr, sig *types.Signature) []string {
	if sig == nil {
		var res []string
		for _, a := range call.Args {
			res = append(res, fc.expr(a))
		}
		return res
	}
	params := sig.Params()
	// f(g()) with a multi-value g.
	if len(call.Args) == 1 {
		if tup, ok := fc.typeOf(call.Args[0]).(*types.Tuple); ok {
			code := fc.expr(call.Args[0])
			if fc.tupleNeedsConversion(tup, params, sig.Variadic()) {
				return []string{fc.spreadTuple(code, tup, params, sig.Variadic())}
			}
			return []string{code}
		}
	}
	var res []string
	n := params.Len()
	for i, a := range call.Args {
		if sig.Variadic() && i >= n-1 {
			if call.Ellipsis.IsValid() {
				res = append(res, fc.expr(a))
				break
			}
			elem := params.At(n - 1).Type().(*types.Slice).Elem()
			var rest []string
			for _, ra := range call.Args[n-1:] {
				rest = append(rest, fc.valueForStore(ra, elem))
			}
			res = append(res, fc.sliceLiteral(rest))
			break
		}
		res = append(res, fc.valueForArg(a, params.At(i).Type()))
	}
	if sig.Variadic() && len(call.Args) < n {
		res = append(res, "nil")
	}
	return res
}

// valueForArg converts an argument; aggregates are copied by the callee when it mutates them.
func (fc *funcCtx) valueForArg(e ast.Expr, t types.Type) string {
	return fc.convertExpr(e, t)
}

func (fc *funcCtx) tupleNeedsConversion(tup *types.Tuple, params *types.Tuple, variadic bool) bool {
	if variadic {
		return true
	}
	for i := 0; i < tup.Len() && i < params.Len(); i++ {
		if isInterface(params.At(i).Type()) && !isInterface(tup.At(i).Type()) {
			return true
		}
	}
	return false
}

func (fc *funcCtx) spreadTuple(code string, tup *types.Tuple, params *types.Tuple, variadic bool) string {
	var names, conv []string
	for i := 0; i < tup.Len(); i++ {
		names = append(names, fmt.Sprintf("__a%d", i))
	}
	n := params.Len()
	for i := 0; i < tup.Len(); i++ {
		if variadic && i >= n-1 {
			elem := params.At(n - 1).Type().(*types.Slice).Elem()
			var rest []string
			for j := i; j < tup.Len(); j++ {
				rest = append(rest, fc.convertValue(names[j], tup.At(j).Type(), elem))
			}
			conv = append(conv, fc.sliceLiteral(rest))
			break
		}
		conv = append(conv, fc.convertValue(names[i], tup.At(i).Type(), params.At(i).Type()))
	}
	return fmt.Sprintf("call(function(%s) return %s end, %s)", strings.Join(names, ", "), strings.Join(conv, ", "), code)
}

func (fc *funcCtx) sliceLiteral(elems []string) string {
	if len(elems) == 0 {
		return "nil"
	}
	return fmt.Sprintf("{{%s}, 1, %d, %d}", strings.Join(elems, ", "), len(elems), len(elems))
}

// ---------------------------------------------------------------------------
// Conversions

func (fc *funcCtx) conversion(arg ast.Expr, to types.Type) string {
	from := fc.typeOf(arg)
	if tv, ok := fc.info().Types[arg]; ok && tv.IsNil() {
		return "nil"
	}
	code := fc.expr(arg)
	if isInterface(to) {
		if isInterface(from) {
			return code
		}
		return fc.box(code, from)
	}
	if isTypeParam(to) || isTypeParam(from) {
		if types.Identical(from, to) {
			return code
		}
		return fmt.Sprintf("rt.convert(%s, %s, %s)", fc.typeExpr(from), fc.typeExpr(to), code)
	}
	tu, fu := to.Underlying(), from.Underlying()
	switch t := tu.(type) {
	case *types.Basic:
		f, _ := fu.(*types.Basic)
		if f == nil {
			if sl, ok := fu.(*types.Slice); ok && t.Info()&types.IsString != 0 {
				if b, ok := sl.Elem().Underlying().(*types.Basic); ok && b.Kind() == types.Int32 {
					return fmt.Sprintf("rt.runesstr(%s)", code)
				}
				return fmt.Sprintf("rt.bytestr(%s)", code)
			}
			if t.Kind() == types.UnsafePointer {
				return code
			}
			if _, ok := fu.(*types.Pointer); ok {
				return code
			}
			fc.e.c.warnf(fc.e.pos(arg.Pos()), "unsupported conversion %s -> %s", from, to)
			return code
		}
		if repOf(from) != repNone && repOf(to) != repNone {
			return fc.numConversion(code, from, to)
		}
		return fc.basicConversion(code, f, t)
	case *types.Slice:
		if f, ok := fu.(*types.Basic); ok && f.Info()&types.IsString != 0 {
			if b, ok := t.Elem().Underlying().(*types.Basic); ok && b.Kind() == types.Int32 {
				return fmt.Sprintf("rt.strrunes(%s)", code)
			}
			return fmt.Sprintf("rt.strbytes(%s)", code)
		}
		return code
	case *types.Struct:
		if !types.Identical(types.Unalias(to), types.Unalias(from)) {
			return fmt.Sprintf("rt.restruct(%s, %s)", fc.typeExpr(to), code)
		}
		return code
	case *types.Pointer:
		return code
	case *types.Array:
		if _, ok := fu.(*types.Slice); ok {
			return fmt.Sprintf("rt.slice2array(%s, %d)", code, t.Len())
		}
		return code
	}
	return code
}

// basicConversion handles conversions between basic types that are not both numeric.
func (fc *funcCtx) basicConversion(code string, from, to *types.Basic) string {
	switch {
	case to.Info()&types.IsString != 0:
		if from.Info()&types.IsInteger != 0 {
			return fmt.Sprintf("rt.runestr(%s)", asLuaNumber(code, from))
		}
	case to.Info()&types.IsInteger != 0:
		if from.Kind() == types.UnsafePointer {
			return fmt.Sprintf("rt.ptrint(%s)", code)
		}
	}
	return code
}

// ---------------------------------------------------------------------------
// Builtins

func (fc *funcCtx) builtin(name string, call *ast.CallExpr) string {
	args := call.Args
	switch name {
	case "len":
		t := fc.typeOf(args[0])
		x := fc.expr(args[0])
		switch u := coreType(t).(type) {
		case *types.Basic:
			if view := fc.viewOf(args[0]); view != nil {
				return view.n
			}
			return "#" + prefixOrParen(x)
		case *types.Slice:
			if isSimple(x) {
				return fmt.Sprintf("(%s and %s[3] or 0)", x, x)
			}
			return fmt.Sprintf("rt.len(%s)", x)
		case *types.Map:
			if isSimple(x) {
				return fmt.Sprintf("(%s and %s[LEN] or 0)", x, x)
			}
			return fmt.Sprintf("rt.mlen(%s)", x)
		case *types.Chan:
			return fmt.Sprintf("rt.chlen(%s)", x)
		case *types.Array:
			return fmt.Sprintf("%d", u.Len())
		case *types.Pointer:
			if a, ok := u.Elem().Underlying().(*types.Array); ok {
				return fmt.Sprintf("%d", a.Len())
			}
		}
		return fmt.Sprintf("rt.len(%s)", x)
	case "cap":
		t := fc.typeOf(args[0])
		x := fc.expr(args[0])
		switch u := coreType(t).(type) {
		case *types.Slice:
			if isSimple(x) {
				return fmt.Sprintf("(%s and %s[4] or 0)", x, x)
			}
			return fmt.Sprintf("rt.cap(%s)", x)
		case *types.Chan:
			return fmt.Sprintf("rt.chcap(%s)", x)
		case *types.Array:
			return fmt.Sprintf("%d", u.Len())
		}
		return fmt.Sprintf("rt.cap(%s)", x)
	case "append":
		st := fc.typeOf(call)
		s := fc.expr(args[0])
		if len(args) == 1 {
			return s
		}
		elem := coreType(st).(*types.Slice).Elem()
		// Aggregate elements are copied when appended or moved to a new backing array.
		cloneT := fc.elemCloneType(elem)
		if call.Ellipsis.IsValid() {
			if isString(fc.typeOf(args[1])) {
				return fmt.Sprintf("rt.appendstr(%s, %s)", s, fc.expr(args[1]))
			}
			if cloneT != "" {
				return fmt.Sprintf("rt.appendslice(%s, %s, %s)", s, fc.expr(args[1]), cloneT)
			}
			return fmt.Sprintf("rt.appendslice(%s, %s)", s, fc.expr(args[1]))
		}
		var vals []string
		for _, a := range args[1:] {
			vals = append(vals, fc.valueForStore(a, elem))
		}
		if cloneT != "" {
			if len(vals) == 1 {
				return fmt.Sprintf("append1(%s, %s, %s)", s, vals[0], cloneT)
			}
			return fmt.Sprintf("rt.appendT(%s, %s, %s)", cloneT, s, strings.Join(vals, ", "))
		}
		if len(vals) == 1 {
			return fmt.Sprintf("append1(%s, %s)", s, vals[0])
		}
		return fmt.Sprintf("append(%s, %s)", s, strings.Join(vals, ", "))
	case "copy":
		if isString(fc.typeOf(args[1])) {
			return fmt.Sprintf("rt.copystr(%s, %s)", fc.expr(args[0]), fc.expr(args[1]))
		}
		if cloneT := fc.elemCloneType(coreType(fc.typeOf(args[0])).(*types.Slice).Elem()); cloneT != "" {
			return fmt.Sprintf("rt.copy(%s, %s, %s)", fc.expr(args[0]), fc.expr(args[1]), cloneT)
		}
		return fmt.Sprintf("rt.copy(%s, %s)", fc.expr(args[0]), fc.expr(args[1]))
	case "delete":
		mt := coreType(fc.typeOf(args[0])).(*types.Map)
		k := fc.mapKey(fc.convertExpr(args[1], mt.Key()), mt.Key())
		if fc.mapStoresPairs(mt) {
			return fmt.Sprintf("rt.mdelp(%s, %s, %s)", fc.expr(args[0]), k, fc.mapDesc(mt))
		}
		return fmt.Sprintf("rt.mdel(%s, %s)", fc.expr(args[0]), k)
	case "make":
		t := fc.typeOf(args[0])
		switch u := coreType(t).(type) {
		case *types.Slice:
			n := asLuaNumber(fc.expr(args[1]), fc.typeOf(args[1]))
			c := "nil"
			if len(args) > 2 {
				c = asLuaNumber(fc.expr(args[2]), fc.typeOf(args[2]))
			}
			return fmt.Sprintf("mkslice(%s, %s, %s)", n, c, fc.zeroArg(u.Elem()))
		case *types.Map:
			if len(args) > 1 {
				return fmt.Sprintf("rt.mkmap(%s)", asLuaNumber(fc.expr(args[1]), fc.typeOf(args[1])))
			}
			return "rt.mkmap()"
		case *types.Chan:
			n := "0"
			if len(args) > 1 {
				n = asLuaNumber(fc.expr(args[1]), fc.typeOf(args[1]))
			}
			return fmt.Sprintf("rt.mkchan(%s)", n)
		}
	case "new":
		t := fc.typeOf(args[0])
		if isStructType(t) || isArrayType(t) {
			return fc.zeroValue(t)
		}
		if isTypeParam(t) {
			return fmt.Sprintf("%s.new()", fc.typeExpr(t))
		}
		return fmt.Sprintf("{v = %s}", fc.zeroValue(t))
	case "panic":
		return fmt.Sprintf("rt.panic(%s)", fc.convertExpr(args[0], types.NewInterfaceType(nil, nil)))
	case "recover":
		return "rt.recover()"
	case "print", "println":
		var as []string
		for _, a := range args {
			as = append(as, fc.box(fc.expr(a), fc.typeOf(a)))
		}
		return fmt.Sprintf("rt.%s(%s)", name, strings.Join(as, ", "))
	case "min", "max":
		t := fc.typeOf(call)
		var as []string
		for _, a := range args {
			as = append(as, fc.convertExpr(a, t))
		}
		if len(as) == 1 {
			return as[0]
		}
		if isFloat(t) {
			return fmt.Sprintf("rt.f%s(%s)", name, strings.Join(as, ", "))
		}
		switch repOf(t) {
		case repInt, repSmall:
			return fmt.Sprintf("math.%s(%s)", name, strings.Join(as, ", "))
		case repInt32:
			if signedKind(t) {
				return fmt.Sprintf("math.%s(%s)", name, strings.Join(as, ", "))
			}
		}
		// uint32 patterns, FFI integers and strings compare through Lua operators.
		return fmt.Sprintf("rt.%s(%s, %s)", name, fc.typeExpr(t), strings.Join(as, ", "))
	case "clear":
		t := fc.typeOf(args[0])
		switch u := coreType(t).(type) {
		case *types.Map:
			return fmt.Sprintf("rt.mclear(%s)", fc.expr(args[0]))
		case *types.Slice:
			return fmt.Sprintf("rt.sclear(%s, %s)", fc.expr(args[0]), fc.zeroArg(u.Elem()))
		}
	case "close":
		return fmt.Sprintf("rt.close(%s)", fc.expr(args[0]))
	case "complex":
		return fmt.Sprintf("rt.complex(%s, %s)", fc.expr(args[0]), fc.expr(args[1]))
	case "real":
		return fmt.Sprintf("%s.re", prefix(fc.expr(args[0])))
	case "imag":
		return fmt.Sprintf("%s.im", prefix(fc.expr(args[0])))
	case "Sizeof", "Alignof":
		// Only non-constant (type parameter dependent) sizes reach here.
		return fmt.Sprintf("rt.sizeof(%s)", fc.typeExpr(fc.typeOf(args[0])))
	case "Offsetof":
		return "0"
	case "String":
		// unsafe.String(ptr, len)
		return fmt.Sprintf("rt.unsafestring(%s, %s)", fc.expr(args[0]), fc.expr(args[1]))
	case "StringData", "SliceData":
		return fmt.Sprintf("rt.unsafedata(%s)", fc.expr(args[0]))
	case "Slice":
		return fmt.Sprintf("rt.unsafeslice(%s, %s)", fc.expr(args[0]), fc.expr(args[1]))
	case "Add":
		return fc.expr(args[0])
	}
	fc.e.c.warnf(fc.e.pos(call.Pos()), "unsupported builtin %s", name)
	return "rt.unsupported()"
}

// zeroArg is the zero argument for slice construction: a literal or a constructor function.
func (fc *funcCtx) zeroArg(t types.Type) string {
	if isTypeParam(t) || isAggregate(t) {
		return fmt.Sprintf("rt.zerofn(%s)", fc.typeExpr(t))
	}
	return fc.zeroValue(t)
}

// ---------------------------------------------------------------------------
// Composite literals

func (fc *funcCtx) compositeLit(cl *ast.CompositeLit, t types.Type) string {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	switch u := coreType(t).(type) {
	case *types.Struct:
		return fc.structLit(cl, t, u)
	case *types.Slice:
		elems, n, sparse := fc.indexedElems(cl, u.Elem())
		if sparse {
			return fmt.Sprintf("rt.sparseslice(%d, %s, {%s})", n, fc.zeroArg(u.Elem()), strings.Join(elems, ", "))
		}
		if n == 0 {
			return "{{}, 1, 0, 0}"
		}
		return fmt.Sprintf("{{%s}, 1, %d, %d}", strings.Join(elems, ", "), n, n)
	case *types.Array:
		elems, n, sparse := fc.indexedElems(cl, u.Elem())
		if sparse || int64(n) < u.Len() {
			if sparse {
				return fmt.Sprintf("rt.sparsearray(%d, %s, {%s})", u.Len(), fc.zeroArg(u.Elem()), strings.Join(elems, ", "))
			}
			return fmt.Sprintf("rt.fillarray({%s}, %d, %d, %s)", strings.Join(elems, ", "), n, u.Len(), fc.zeroArg(u.Elem()))
		}
		return fmt.Sprintf("{%s}", strings.Join(elems, ", "))
	case *types.Map:
		return fc.mapLit(cl, u)
	}
	fc.e.c.warnf(fc.e.pos(cl.Pos()), "unsupported composite literal %s", t)
	return "rt.unsupported()"
}

// indexedElems renders slice/array elements; sparse literals return index/value pairs.
func (fc *funcCtx) indexedElems(cl *ast.CompositeLit, elem types.Type) ([]string, int, bool) {
	sparse := false
	for _, e := range cl.Elts {
		if _, ok := e.(*ast.KeyValueExpr); ok {
			sparse = true
		}
	}
	var res []string
	if !sparse {
		for _, e := range cl.Elts {
			res = append(res, fc.elemValue(e, elem))
		}
		return res, len(cl.Elts), false
	}
	idx := int64(0)
	max := int64(0)
	for _, e := range cl.Elts {
		v := e
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			tv := fc.info().Types[kv.Key]
			idx, _ = constant.Int64Val(constant.ToInt(tv.Value))
			v = kv.Value
		}
		res = append(res, fmt.Sprintf("%d", idx), fc.elemValue(v, elem))
		idx++
		if idx > max {
			max = idx
		}
	}
	return res, int(max), true
}

func (fc *funcCtx) elemValue(e ast.Expr, t types.Type) string {
	if cl, ok := e.(*ast.CompositeLit); ok && cl.Type == nil {
		return fc.compositeLit(cl, t)
	}
	if ue, ok := e.(*ast.UnaryExpr); ok {
		if cl, ok := ue.X.(*ast.CompositeLit); ok && cl.Type == nil {
			return fc.compositeLit(cl, t)
		}
	}
	return fc.valueForStore(e, t)
}

func (fc *funcCtx) mapLit(cl *ast.CompositeLit, mt *types.Map) string {
	var pairs []string
	for _, e := range cl.Elts {
		kv := e.(*ast.KeyValueExpr)
		k := fc.elemValue(kv.Key, mt.Key())
		v := fc.elemValue(kv.Value, mt.Elem())
		pairs = append(pairs, k, fc.mapValueStore(v, mt))
	}
	if fc.mapStoresPairs(mt) {
		return fmt.Sprintf("rt.maplitp(%s, {%s})", fc.typeExpr(mt), strings.Join(pairs, ", "))
	}
	return fmt.Sprintf("rt.maplit({%s})", strings.Join(pairs, ", "))
}

func (fc *funcCtx) structLit(cl *ast.CompositeLit, t types.Type, st *types.Struct) string {
	if fc.e.c.isNativeType(t) {
		// Native layouts differ from the Go declaration: start from the native zero value.
		var sets []string
		for i, e := range cl.Elts {
			var f *types.Var
			var v ast.Expr
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				name := kv.Key.(*ast.Ident).Name
				for j := 0; j < st.NumFields(); j++ {
					if st.Field(j).Name() == name {
						f = st.Field(j)
					}
				}
				v = kv.Value
			} else {
				f = st.Field(i)
				v = e
			}
			sets = append(sets, fmt.Sprintf("%s = %s", fieldKey(f.Name()), fc.elemValue(v, f.Type())))
		}
		if len(sets) == 0 {
			return fc.zeroValue(t)
		}
		return fmt.Sprintf("rt.setfields(%s, {%s})", fc.zeroValue(t), strings.Join(sets, ", "))
	}
	vals := fc.litValues(cl, st)
	return fc.structFromVals(t, st, vals)
}

// litVal is a struct literal entry: a value for the field, or (Go 1.27 promoted fields)
// values for fields of an embedded struct.
type litVal struct {
	expr ast.Expr
	sub  map[int]*litVal
}

// litValues maps field indexes to literal entries, routing promoted field keys through
// their embedded structs.
func (fc *funcCtx) litValues(cl *ast.CompositeLit, st *types.Struct) map[int]*litVal {
	vals := map[int]*litVal{}
	for i, e := range cl.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			vals[i] = &litVal{expr: e}
			continue
		}
		key := kv.Key.(*ast.Ident)
		target, _ := fc.info().Uses[key].(*types.Var)
		path := promotedPath(st, key.Name, target)
		cur := vals
		for depth, idx := range path {
			if depth == len(path)-1 {
				cur[idx] = &litVal{expr: kv.Value}
				break
			}
			lv := cur[idx]
			if lv == nil {
				lv = &litVal{sub: map[int]*litVal{}}
				cur[idx] = lv
			}
			cur = lv.sub
		}
	}
	return vals
}

// promotedPath finds the field index path for a (possibly promoted) field name.
func promotedPath(st *types.Struct, name string, target *types.Var) []int {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f == target || (target == nil && f.Name() == name) {
			return []int{i}
		}
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if !f.Embedded() {
			continue
		}
		sub, ok := f.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		if p := promotedPath(sub, name, target); p != nil {
			return append([]int{i}, p...)
		}
	}
	return nil
}

func (fc *funcCtx) structFromVals(t types.Type, st *types.Struct, vals map[int]*litVal) string {
	var entries []litEntry
	var embeds []string
	fc.structLitFields(st, vals, &entries, &embeds)
	lit := fc.structConstructor(entries, fmt.Sprintf("%s.pmt", prefix(fc.typeExpr(t))))
	for _, emb := range embeds {
		lit = fmt.Sprintf("rt.embedcopy(%s, %s)", lit, emb)
	}
	return lit
}

// litEntry is the value of one leaf in a struct literal.
type litEntry struct {
	slot int // array slot, or 0 for a named key
	key  string
	code string
	call bool // evaluating code may call a function
}

// structConstructor renders a struct table from its leaves in field order. Slots are written
// positionally, so they must be in slot order; when that differs from field order and would
// change the order of calls, the values go in field order to a module-level constructor.
func (fc *funcCtx) structConstructor(entries []litEntry, mt string) string {
	inOrder := true
	last := 0
	for _, en := range entries {
		if en.slot > 0 {
			inOrder = inOrder && en.slot > last
			last = en.slot
		}
	}
	// Slots the struct does not use (other structs' ranges) are filled with nil.
	items := func(entries []litEntry, code func(i int) string) string {
		var parts []string
		next := 1
		for i, en := range entries {
			c := code(i)
			if en.slot == 0 {
				parts = append(parts, en.key+" = "+c)
				continue
			}
			for ; next < en.slot; next++ {
				parts = append(parts, "nil")
			}
			next = en.slot + 1
			if strings.HasSuffix(c, ")") && !wrappedInParens(c) {
				// A call in last position would spread all its results.
				c = "(" + c + ")"
			}
			parts = append(parts, c)
		}
		return strings.Join(parts, ", ")
	}
	if inOrder {
		return fmt.Sprintf("setmetatable({%s}, %s)", items(entries, func(i int) string { return entries[i].code }), mt)
	}
	// Slots first in slot order, then named keys; perm maps each to its field order position.
	perm := make([]int, len(entries))
	for i := range perm {
		perm[i] = i
	}
	sort.SliceStable(perm, func(a, b int) bool {
		sa, sb := entries[perm[a]].slot, entries[perm[b]].slot
		if sa == 0 || sb == 0 {
			return sa != 0 && sb == 0
		}
		return sa < sb
	})
	sorted := make([]litEntry, len(entries))
	var calls []int
	reordered := false
	for i, j := range perm {
		sorted[i] = entries[j]
		if entries[j].call {
			reordered = reordered || (len(calls) > 0 && calls[len(calls)-1] > j)
			calls = append(calls, j)
		}
	}
	if !reordered {
		return fmt.Sprintf("setmetatable({%s}, %s)", items(sorted, func(i int) string { return sorted[i].code }), mt)
	}
	params := make([]string, len(entries))
	args := []string{mt}
	for i, en := range entries {
		params[i] = fmt.Sprintf("a%d", i+1)
		args = append(args, en.code)
	}
	body := items(sorted, func(i int) string { return params[perm[i]] })
	fc.e.hoisted = append(fc.e.hoisted, fmt.Sprintf("function(mt, %s) return setmetatable({%s}, mt) end", strings.Join(params, ", "), body))
	return fmt.Sprintf("FN[%d](%s)", len(fc.e.hoisted), strings.Join(args, ", "))
}

// structLitFields collects the leaves of a struct literal in field order, inlining flattened
// embedded structs given as nested literals or promoted field values.
func (fc *funcCtx) structLitFields(st *types.Struct, vals map[int]*litVal, entries *[]litEntry, embeds *[]string) {
	l := fc.e.c.layout(st)
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		info := l.fields[i]
		if info.skip {
			continue
		}
		lv := vals[i]
		if info.flat {
			sub := f.Type().Underlying().(*types.Struct)
			switch {
			case lv == nil:
				fc.structLitFields(sub, nil, entries, embeds)
			case lv.sub != nil:
				fc.structLitFields(sub, lv.sub, entries, embeds)
			default:
				if cl, ok := unparen(lv.expr).(*ast.CompositeLit); ok {
					fc.structLitFields(sub, fc.litValues(cl, sub), entries, embeds)
				} else {
					// Arbitrary embedded value: fill zeros, then copy the value's fields in.
					fc.structLitFields(sub, nil, entries, embeds)
					*embeds = append(*embeds, fmt.Sprintf("%s, %s", fc.typeExpr(f.Type()), fc.expr(lv.expr)))
				}
			}
			continue
		}
		en := litEntry{slot: info.slot, key: info.key}
		switch {
		case atomicValueType(f.Type()) != nil:
			// Atomic integer fields hold the plain value, and a literal can only give their zero.
			en.code = fc.zeroValue(atomicValueType(f.Type()))
		case lv == nil:
			en.code = fc.zeroValue(f.Type())
		case lv.sub != nil:
			en.code = fc.structFromVals(f.Type(), f.Type().Underlying().(*types.Struct), lv.sub)
			en.call = lv.hasCall()
		default:
			en.code = fc.elemValue(lv.expr, f.Type())
			en.call = lv.hasCall()
		}
		*entries = append(*entries, en)
	}
}

// hasCall reports literal entries whose evaluation may call a function or receive.
func (lv *litVal) hasCall() bool {
	for _, sub := range lv.sub {
		if sub.hasCall() {
			return true
		}
	}
	found := false
	if lv.expr != nil {
		ast.Inspect(lv.expr, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				found = true
			case *ast.UnaryExpr:
				found = found || n.Op == token.ARROW
			}
			return !found
		})
	}
	return found
}

// sortedKeys is a helper for deterministic output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// elemCloneType returns the descriptor for slice elements that need copying (mutable
// aggregates, or type parameters which may be aggregates), or "" when references can be shared.
func (fc *funcCtx) elemCloneType(elem types.Type) string {
	if isTypeParam(elem) || fc.e.c.needsClone(elem) {
		return fc.typeExpr(elem)
	}
	return ""
}
