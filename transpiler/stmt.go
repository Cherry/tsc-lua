package main

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

func (fc *funcCtx) stmts(w *writer, list []ast.Stmt) {
	for _, s := range list {
		fc.stmt(w, s)
	}
}

// emitLine writes a (possibly multi-line) statement at the current indentation.
func emitLine(w *writer, s string) {
	// A statement starting with "(" would be parsed as a call continuing the previous line.
	if strings.HasPrefix(s, "(") {
		s = ";" + s
	}
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		w.line("%s", l)
	}
}

func (fc *funcCtx) stmt(w *writer, s ast.Stmt) {
	switch s := s.(type) {
	case *ast.BlockStmt:
		w.line("do")
		w.indent++
		fc.stmts(w, s.List)
		w.indent--
		w.line("end")
	case *ast.ExprStmt:
		code := fc.expr(s.X)
		fc.flushInto(w)
		// A call can stand as a statement; a parenthesized expression (an inlined call) cannot.
		if isPrefixExp(code) && strings.HasSuffix(code, ")") && !wrappedInParens(code) {
			emitLine(w, code)
		} else {
			discardLine(w, code)
		}
	case *ast.DeclStmt:
		fc.declStmt(w, s.Decl.(*ast.GenDecl))
	case *ast.AssignStmt:
		fc.assign(w, s)
	case *ast.IncDecStmt:
		op := token.ADD
		if s.Tok == token.DEC {
			op = token.SUB
		}
		fc.opAssign(w, s.X, op, &ast.BasicLit{Kind: token.INT, Value: "1"})
	case *ast.ReturnStmt:
		fc.returnStmt(w, s)
	case *ast.IfStmt:
		fc.ifStmt(w, s, false)
	case *ast.ForStmt:
		fc.forStmt(w, s, "")
	case *ast.RangeStmt:
		fc.rangeStmt(w, s, "")
	case *ast.SwitchStmt:
		fc.switchStmt(w, s, "")
	case *ast.TypeSwitchStmt:
		fc.typeSwitchStmt(w, s, "")
	case *ast.SelectStmt:
		fc.selectStmt(w, s, "")
	case *ast.LabeledStmt:
		fc.labeled(w, s)
	case *ast.BranchStmt:
		fc.branch(w, s)
	case *ast.DeferStmt:
		fc.deferStmt(w, s)
	case *ast.GoStmt:
		fn, args := fc.callThunk(w, s.Call)
		w.line("rt.go(%s)", strings.Join(append([]string{fn}, args...), ", "))
	case *ast.SendStmt:
		ct := coreType(fc.typeOf(s.Chan)).(*types.Chan)
		w.line("rt.send(%s, %s)", fc.expr(s.Chan), fc.valueForStore(s.Value, ct.Elem()))
	case *ast.EmptyStmt:
	default:
		fc.e.c.warnf(fc.e.pos(s.Pos()), "unsupported statement %T", s)
		w.line("rt.unsupported()")
	}
}

func (fc *funcCtx) flushInto(w *writer) {
	if len(fc.pre) > 0 {
		w.raw(fc.flushPre(w.indent))
	}
}

// ---------------------------------------------------------------------------
// Declarations

func (fc *funcCtx) declStmt(w *writer, d *ast.GenDecl) {
	switch d.Tok {
	case token.VAR:
		for _, spec := range d.Specs {
			vs := spec.(*ast.ValueSpec)
			fc.varSpec(w, vs)
		}
	case token.TYPE:
		for _, spec := range d.Specs {
			ts := spec.(*ast.TypeSpec)
			obj := fc.info().Defs[ts.Name].(*types.TypeName)
			if obj.IsAlias() {
				continue
			}
			name := fc.e.localTypeName(obj)
			// Build each local type once (per instantiation in a generic function): a new type
			// descriptor per call breaks type identity, and values keep every descriptor alive.
			fc.e.localTypes++
			slot := fc.e.localTypes
			tps := fc.enclosingTypeParams()
			if len(tps) == 0 || !mentionsTypeParam(obj.Type().Underlying(), map[types.Type]bool{}) {
				w.line("local %s = LT[%d]", name, slot)
				w.line("if %s == nil then", name)
				w.indent++
				fc.e.typeSpecAs(ts, w, w, name, false, false, "")
				w.line("LT[%d] = %s", slot, name)
				w.indent--
				w.line("end")
				continue
			}
			var ids, strs []string
			for _, tp := range tps {
				ids = append(ids, typeParamName(tp)+".id")
				strs = append(strs, typeParamName(tp)+".str")
			}
			cache, key := fc.e.newTmp("ltc"), fc.e.newTmp("ltk")
			w.line("local %s = LT[%d]", cache, slot)
			w.line("if %s == nil then %s = {}; LT[%d] = %s end", cache, cache, slot, cache)
			w.line("local %s = %s", key, strings.Join(ids, " .. ',' .. "))
			w.line("local %s = %s[%s]", name, cache, key)
			w.line("if %s == nil then", name)
			w.indent++
			fc.e.typeSpecAs(ts, w, w, name, false, false, strings.Join(strs, " .. ',' .. "))
			w.line("%s[%s] = %s", cache, key, name)
			w.indent--
			w.line("end")
		}
	case token.CONST:
	}
}

// enclosingTypeParams returns the type parameters of the nearest enclosing generic function
// (receiver type parameters first), looking through function literals.
func (fc *funcCtx) enclosingTypeParams() []*types.TypeParam {
	for c := fc; c != nil; c = c.parent {
		if c.sig == nil {
			continue
		}
		var tps []*types.TypeParam
		for i := 0; i < c.sig.RecvTypeParams().Len(); i++ {
			tps = append(tps, c.sig.RecvTypeParams().At(i))
		}
		for i := 0; i < c.sig.TypeParams().Len(); i++ {
			tps = append(tps, c.sig.TypeParams().At(i))
		}
		if len(tps) > 0 {
			return tps
		}
	}
	return nil
}

// mentionsTypeParam reports whether a type refers to a type parameter anywhere inside it.
func mentionsTypeParam(t types.Type, seen map[types.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := types.Unalias(t).(type) {
	case *types.TypeParam:
		return true
	case *types.Named:
		for i := 0; i < t.TypeArgs().Len(); i++ {
			if mentionsTypeParam(t.TypeArgs().At(i), seen) {
				return true
			}
		}
		return false
	case *types.Pointer:
		return mentionsTypeParam(t.Elem(), seen)
	case *types.Slice:
		return mentionsTypeParam(t.Elem(), seen)
	case *types.Array:
		return mentionsTypeParam(t.Elem(), seen)
	case *types.Chan:
		return mentionsTypeParam(t.Elem(), seen)
	case *types.Map:
		return mentionsTypeParam(t.Key(), seen) || mentionsTypeParam(t.Elem(), seen)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if mentionsTypeParam(t.Field(i).Type(), seen) {
				return true
			}
		}
		return false
	case *types.Signature:
		return mentionsTypeParam(t.Params(), seen) || mentionsTypeParam(t.Results(), seen)
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if mentionsTypeParam(t.At(i).Type(), seen) {
				return true
			}
		}
		return false
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			if mentionsTypeParam(t.Method(i).Type(), seen) {
				return true
			}
		}
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if mentionsTypeParam(t.EmbeddedType(i), seen) {
				return true
			}
		}
		return false
	}
	return false
}

func (fc *funcCtx) varSpec(w *writer, vs *ast.ValueSpec) {
	if len(vs.Names) == 1 && len(vs.Values) == 1 {
		if v, ok := fc.info().Defs[vs.Names[0]].(*types.Var); ok && v != nil {
			if view := fc.views[v]; view != nil {
				fc.declareView(w, view, vs.Values[0])
				return
			}
		}
	}
	var vars []*types.Var
	for _, n := range vs.Names {
		vars = append(vars, fc.info().Defs[n].(*types.Var))
	}
	if len(vs.Values) == 0 {
		for _, v := range vars {
			fc.declareVar(w, v, fc.zeroValue(v.Type()))
		}
		return
	}
	if len(vs.Values) == len(vars) {
		vals := make([]string, len(vars))
		for i, v := range vars {
			vals[i] = fc.valueForStore(vs.Values[i], v.Type())
		}
		fc.flushInto(w)
		if len(vars) == 1 {
			fc.declareVar(w, vars[0], vals[0])
			return
		}
		tmps := fc.tmps(len(vars))
		w.line("local %s = %s", strings.Join(tmps, ", "), strings.Join(vals, ", "))
		for i, v := range vars {
			fc.declareVar(w, v, tmps[i])
		}
		return
	}
	// var a, b = f()
	code := fc.expr(vs.Values[0])
	tmps := fc.tmps(len(vars))
	w.line("local %s = %s", strings.Join(tmps, ", "), code)
	tup := fc.typeOf(vs.Values[0]).(*types.Tuple)
	for i, v := range vars {
		fc.declareVar(w, v, fc.convertValue(tmps[i], tup.At(i).Type(), v.Type()))
	}
}

func (fc *funcCtx) tmps(n int) []string {
	var res []string
	for i := 0; i < n; i++ {
		res = append(res, fc.e.newTmp("t"))
	}
	return res
}

func (fc *funcCtx) declareVar(w *writer, v *types.Var, val string) {
	if v.Name() == "_" {
		if val != "nil" && !isLiteral(val) {
			discardLine(w, val)
		}
		return
	}
	name := fc.varName(v)
	if fc.e.c.boxed[v] {
		emitLine(w, fmt.Sprintf("local %s = {v = %s}", name, val))
	} else {
		emitLine(w, fmt.Sprintf("local %s = %s", name, val))
	}
}

// ---------------------------------------------------------------------------
// Assignment

// lvalue describes an assignment target.
type lvalue struct {
	kind  string // "blank", "var", "field", "index", "map", "deref", "copy"
	code  string // assignable Lua expression (var/field/index/deref)
	m     string // map expression
	key   string // map key
	mt    *types.Map
	typ   types.Type
	copy  bool // aggregate storage that must be updated in place
	setup []string
}

func (fc *funcCtx) lvalueOf(e ast.Expr, needTemps bool) lvalue {
	e = unparen(e)
	t := fc.typeOf(e)
	switch x := e.(type) {
	case *ast.Ident:
		if x.Name == "_" {
			return lvalue{kind: "blank"}
		}
		obj := fc.info().ObjectOf(x)
		v, _ := obj.(*types.Var)
		lv := lvalue{kind: "var", code: fc.ident(x), typ: t}
		if v != nil && isAggregate(v.Type()) && fc.e.c.addrTakenAgg[v] {
			lv.copy = true
		}
		return lv
	case *ast.SelectorExpr:
		sel := fc.info().Selections[x]
		if sel == nil {
			return lvalue{kind: "var", code: fc.ident(x.Sel), typ: t}
		}
		path := sel.Index()
		base := fc.expr(x.X)
		if needTemps && !isSimple(base) {
			tmp := fc.e.newTmp("o")
			lv := lvalue{setup: []string{fmt.Sprintf("local %s = %s", tmp, base)}}
			base = tmp
			lv.kind = "field"
			lv.code = fc.fieldPath(base, fc.typeOf(x.X), path)
			lv.typ = t
			lv.copy = fc.fieldNeedsCopy(sel)
			return lv
		}
		return lvalue{kind: "field", code: fc.fieldPath(base, fc.typeOf(x.X), path), typ: t, copy: fc.fieldNeedsCopy(sel)}
	case *ast.IndexExpr:
		xt := fc.typeOf(x.X)
		var setup []string
		base := fc.expr(x.X)
		idx := asLuaNumber(fc.expr(x.Index), fc.typeOf(x.Index))
		switch u := coreType(xt).(type) {
		case *types.Map:
			idx = fc.mapKey(fc.convertExpr(x.Index, u.Key()), u.Key())
		}
		if needTemps || !isSimple(base) {
			if !isSimple(base) {
				tmp := fc.e.newTmp("o")
				setup = append(setup, fmt.Sprintf("local %s = %s", tmp, base))
				base = tmp
			}
			if needTemps && !isLiteral(idx) && !isSimple(idx) {
				tmp := fc.e.newTmp("k")
				setup = append(setup, fmt.Sprintf("local %s = %s", tmp, idx))
				idx = tmp
			}
		}
		switch u := coreType(xt).(type) {
		case *types.Map:
			return lvalue{kind: "map", m: base, key: idx, mt: u, typ: t, setup: setup}
		case *types.Slice:
			return lvalue{kind: "index", code: fmt.Sprintf("%s[1][%s[2] + %s]", base, base, prefixOrParen(idx)), typ: t, setup: setup, copy: isAggregate(t) && fc.e.c.addrTakenElem[typeKey(t)]}
		case *types.Array, *types.Pointer:
			return lvalue{kind: "index", code: fmt.Sprintf("%s[%s + 1]", base, prefixOrParen(idx)), typ: t, setup: setup, copy: isAggregate(t) && fc.e.c.addrTakenElem[typeKey(t)]}
		}
	case *ast.StarExpr:
		p := fc.expr(x.X)
		if isTypeParam(t) {
			return lvalue{kind: "derefT", code: p, typ: t}
		}
		if isStructType(t) || isArrayType(t) {
			return lvalue{kind: "deref", code: p, typ: t, copy: true}
		}
		return lvalue{kind: "var", code: prefix(p) + ".v", typ: t}
	}
	fc.e.c.warnf(fc.e.pos(e.Pos()), "unsupported assignment target %T", e)
	return lvalue{kind: "blank"}
}

func (fc *funcCtx) fieldNeedsCopy(sel *types.Selection) bool {
	v, ok := sel.Obj().(*types.Var)
	if !ok || !isAggregate(v.Type()) {
		return false
	}
	// A flattened embedded struct shares the outer table, so it must be overwritten in place.
	path := sel.Index()
	st := derefStruct(fieldPathType(sel.Recv(), path[:len(path)-1]))
	if st != nil && fc.e.c.layout(st).fields[path[len(path)-1]].flat {
		return true
	}
	return fc.e.c.addrTakenAgg[v]
}

// load returns code reading the lvalue's current value.
func (fc *funcCtx) load(lv lvalue) string {
	switch lv.kind {
	case "map":
		return fc.mapRead(lv.m, lv.key, lv.mt)
	case "derefT":
		return fmt.Sprintf("rt.derefT(%s, %s)", fc.typeExpr(lv.typ), lv.code)
	}
	return lv.code
}

// store emits the assignment of val to lv.
func (fc *funcCtx) store(w *writer, lv lvalue, val string) {
	switch lv.kind {
	case "derefT":
		w.line("rt.storeT(%s, %s, %s)", fc.typeExpr(lv.typ), lv.code, val)
	case "blank":
		if !isLiteral(val) && val != "nil" && !isSimple(val) {
			discardLine(w, val)
		}
	case "map":
		v := fc.mapValueStore(val, lv.mt)
		if fc.mapStoresPairs(lv.mt) || valueKind(lv.mt.Elem()) == "dyn" {
			w.line("rt.msetp(%s, %s, %s, %s)", lv.m, lv.key, v, fc.mapDesc(lv.mt))
			return
		}
		m, k := lv.m, lv.key
		if !isSimple(m) || (!isSimple(k) && !isLiteral(k)) || strings.Contains(v, m) {
			w.line("mset(%s, %s, %s)", m, k, v)
			return
		}
		w.line("if %s[%s] == nil then %s[LEN] = %s[LEN] + 1 end", m, k, m, m)
		emitLine(w, fmt.Sprintf("%s[%s] = %s", m, k, v))
	default:
		if lv.copy {
			w.line("%s.copy(%s, %s)", prefix(fc.typeExpr(lv.typ)), lv.code, val)
			return
		}
		emitLine(w, fmt.Sprintf("%s = %s", lv.code, val))
	}
}

func (fc *funcCtx) assign(w *writer, s *ast.AssignStmt) {
	switch s.Tok {
	case token.DEFINE:
		fc.define(w, s)
		return
	case token.ASSIGN:
	default:
		op := map[token.Token]token.Token{
			token.ADD_ASSIGN: token.ADD, token.SUB_ASSIGN: token.SUB, token.MUL_ASSIGN: token.MUL,
			token.QUO_ASSIGN: token.QUO, token.REM_ASSIGN: token.REM, token.AND_ASSIGN: token.AND,
			token.OR_ASSIGN: token.OR, token.XOR_ASSIGN: token.XOR, token.SHL_ASSIGN: token.SHL,
			token.SHR_ASSIGN: token.SHR, token.AND_NOT_ASSIGN: token.AND_NOT,
		}[s.Tok]
		fc.opAssign(w, s.Lhs[0], op, s.Rhs[0])
		return
	}
	if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
		lv := fc.lvalueOf(s.Lhs[0], false)
		var val string
		if lv.kind == "blank" {
			val = fc.expr(s.Rhs[0])
		} else if lv.copy {
			// Copied field by field into the existing table, so a fresh copy would be garbage.
			val = fc.convertExpr(s.Rhs[0], fc.typeOf(s.Lhs[0]))
		} else {
			val = fc.valueForStore(s.Rhs[0], fc.typeOf(s.Lhs[0]))
			if fc.selfSliceUpdate(s.Lhs[0], s.Rhs[0]) {
				val = inPlaceForm(val)
			}
		}
		fc.flushInto(w)
		for _, st := range lv.setup {
			w.line("%s", st)
		}
		fc.store(w, lv, val)
		return
	}
	// Tuple assignment: evaluate everything first, then store left to right.
	var lvs []lvalue
	for _, l := range s.Lhs {
		lvs = append(lvs, fc.lvalueOf(l, true))
	}
	var vals []string
	if len(s.Rhs) == 1 {
		vals = []string{fc.multiValue(s.Rhs[0], s.Lhs)}
	} else {
		for i, r := range s.Rhs {
			vals = append(vals, fc.valueForStore(r, fc.typeOf(s.Lhs[i])))
		}
	}
	fc.flushInto(w)
	allPlain := true
	for _, lv := range lvs {
		if lv.kind != "var" && lv.kind != "field" && lv.kind != "index" || lv.copy || len(lv.setup) > 0 {
			allPlain = false
		}
	}
	if allPlain && len(s.Rhs) > 1 {
		var codes []string
		for _, lv := range lvs {
			codes = append(codes, lv.code)
		}
		emitLine(w, fmt.Sprintf("%s = %s", strings.Join(codes, ", "), strings.Join(vals, ", ")))
		return
	}
	for _, lv := range lvs {
		for _, st := range lv.setup {
			w.line("%s", st)
		}
	}
	tmps := fc.tmps(len(lvs))
	emitLine(w, fmt.Sprintf("local %s = %s", strings.Join(tmps, ", "), strings.Join(vals, ", ")))
	for i, lv := range lvs {
		fc.store(w, lv, tmps[i])
	}
}

// multiValue renders a single expression producing multiple values (call, comma-ok).
func (fc *funcCtx) multiValue(e ast.Expr, lhs []ast.Expr) string {
	e = unparen(e)
	switch x := e.(type) {
	case *ast.IndexExpr:
		mt := coreType(fc.typeOf(x.X)).(*types.Map)
		k := fc.mapKey(fc.convertExpr(x.Index, mt.Key()), mt.Key())
		return fc.mapRead2(fc.expr(x.X), k, mt)
	case *ast.TypeAssertExpr:
		return fc.typeAssert(x, true)
	case *ast.UnaryExpr:
		if x.Op == token.ARROW {
			ct := coreType(fc.typeOf(x.X)).(*types.Chan)
			return fmt.Sprintf("rt.recv2(%s, %s)", fc.expr(x.X), fc.zeroValue(ct.Elem()))
		}
	}
	code := fc.expr(e)
	// Convert tuple results to interface targets when needed.
	if tup, ok := fc.typeOf(e).(*types.Tuple); ok && lhs != nil {
		need := false
		for i := 0; i < tup.Len() && i < len(lhs); i++ {
			lt := fc.typeOf(lhs[i])
			if lt != nil && isInterface(lt) && !isInterface(tup.At(i).Type()) {
				need = true
			}
		}
		if need {
			var names, conv []string
			for i := 0; i < tup.Len(); i++ {
				n := fmt.Sprintf("__a%d", i)
				names = append(names, n)
				lt := fc.typeOf(lhs[i])
				conv = append(conv, fc.convertValue(n, tup.At(i).Type(), lt))
			}
			return fmt.Sprintf("call(function(%s) return %s end, %s)", strings.Join(names, ", "), strings.Join(conv, ", "), code)
		}
	}
	return code
}

func (fc *funcCtx) mapRead2(m, k string, mt *types.Map) string {
	return fmt.Sprintf("rt.mget2(%s, %s, %s)", m, k, fc.mapDesc(mt))
}

func (fc *funcCtx) define(w *writer, s *ast.AssignStmt) {
	if len(s.Lhs) == 1 && len(s.Rhs) == 1 {
		if v, ok := fc.info().Defs[s.Lhs[0].(*ast.Ident)].(*types.Var); ok && v != nil {
			if view := fc.views[v]; view != nil {
				fc.declareView(w, view, s.Rhs[0])
				return
			}
		}
	}
	type target struct {
		v     *types.Var
		isNew bool
		lhs   ast.Expr
	}
	var targets []target
	for _, l := range s.Lhs {
		id := l.(*ast.Ident)
		if id.Name == "_" {
			targets = append(targets, target{lhs: l})
			continue
		}
		if v, ok := fc.info().Defs[id].(*types.Var); ok && v != nil {
			targets = append(targets, target{v: v, isNew: true, lhs: l})
		} else {
			v, _ := fc.info().Uses[id].(*types.Var)
			targets = append(targets, target{v: v, lhs: l})
		}
	}
	var vals []string
	single := len(s.Rhs) == len(s.Lhs)
	if single {
		for i, r := range s.Rhs {
			t := targets[i]
			if t.v == nil {
				vals = append(vals, fc.expr(r))
				continue
			}
			vals = append(vals, fc.valueForStore(r, t.v.Type()))
		}
	} else {
		vals = []string{fc.multiValue(s.Rhs[0], s.Lhs)}
	}
	fc.flushInto(w)
	allNew := true
	anyBoxed := false
	for _, t := range targets {
		if t.v == nil {
			continue
		}
		if !t.isNew {
			allNew = false
		}
		if fc.e.c.boxed[t.v] || (!t.isNew && isAggregate(t.v.Type()) && fc.e.c.addrTakenAgg[t.v]) {
			anyBoxed = true
		}
	}
	if len(targets) == 1 {
		t := targets[0]
		if t.v == nil {
			if !isLiteral(vals[0]) {
				discardLine(w, vals[0])
			}
			return
		}
		if t.isNew {
			fc.declareVar(w, t.v, vals[0])
		} else {
			fc.store(w, fc.lvalueOf(t.lhs, false), vals[0])
		}
		return
	}
	if allNew && !anyBoxed {
		var names []string
		for _, t := range targets {
			if t.v == nil {
				names = append(names, "_")
			} else {
				names = append(names, fc.varName(t.v))
			}
		}
		emitLine(w, fmt.Sprintf("local %s = %s", strings.Join(names, ", "), strings.Join(vals, ", ")))
		return
	}
	tmps := fc.tmps(len(targets))
	emitLine(w, fmt.Sprintf("local %s = %s", strings.Join(tmps, ", "), strings.Join(vals, ", ")))
	for i, t := range targets {
		if t.v == nil {
			continue
		}
		if t.isNew {
			fc.declareVar(w, t.v, tmps[i])
		} else {
			fc.store(w, fc.lvalueOf(t.lhs, false), tmps[i])
		}
	}
}

func (fc *funcCtx) opAssign(w *writer, lhs ast.Expr, op token.Token, rhs ast.Expr) {
	lv := fc.lvalueOf(lhs, true)
	for _, st := range lv.setup {
		w.line("%s", st)
	}
	t := fc.typeOf(lhs)
	cur := fc.load(lv)
	var val string
	if op == token.SHL || op == token.SHR {
		val = fc.shift(op, cur, rhs, t)
	} else if op == token.ADD && isString(t) {
		val = fmt.Sprintf("%s .. %s", prefixOrParen(cur), fc.operand(rhs))
	} else if isComplex(t) {
		names := map[token.Token]string{token.ADD: "cadd", token.SUB: "csub", token.MUL: "cmul", token.QUO: "cdiv"}
		val = fmt.Sprintf("rt.%s(%s, %s)", names[op], cur, fc.convertExpr(rhs, t))
	} else if isTypeParam(t) {
		val = fmt.Sprintf("%s.op(%s, %s, %s)", fc.typeExpr(t), luaString(op.String()), cur, fc.convertExpr(rhs, t))
	} else {
		r := fc.convertExpr(rhs, t)
		if !isPrefixExp(r) && !isLiteral(r) {
			r = "(" + r + ")"
		}
		val = fc.arithCode(op, prefixOrParen(cur), r, t)
	}
	fc.flushInto(w)
	fc.store(w, lv, val)
}

// ---------------------------------------------------------------------------
// Return

func (fc *funcCtx) returnStmt(w *writer, s *ast.ReturnStmt) {
	var vals []string
	if len(s.Results) > 0 {
		results := fc.sig.Results()
		if len(s.Results) == 1 && results.Len() > 1 {
			vals = []string{fc.multiValueForResults(s.Results[0], results)}
		} else {
			for i, r := range s.Results {
				vals = append(vals, fc.convertExpr(r, results.At(i).Type()))
			}
		}
	}
	fc.flushInto(w)
	fc.emitReturn(w, vals, len(s.Results) == 0)
}

func (fc *funcCtx) multiValueForResults(e ast.Expr, results *types.Tuple) string {
	code := fc.expr(e)
	tup, ok := fc.typeOf(e).(*types.Tuple)
	if !ok {
		return code
	}
	need := false
	for i := 0; i < tup.Len(); i++ {
		if isInterface(results.At(i).Type()) && !isInterface(tup.At(i).Type()) {
			need = true
		}
	}
	if !need {
		return code
	}
	var names, conv []string
	for i := 0; i < tup.Len(); i++ {
		n := fmt.Sprintf("__a%d", i)
		names = append(names, n)
		conv = append(conv, fc.convertValue(n, tup.At(i).Type(), results.At(i).Type()))
	}
	return fmt.Sprintf("call(function(%s) return %s end, %s)", strings.Join(names, ", "), strings.Join(conv, ", "), code)
}

// emitReturn returns from the Go function, accounting for defer frames and
// range-over-func bodies (which are Lua closures).
func (fc *funcCtx) emitReturn(w *writer, vals []string, naked bool) {
	if fc.inRangeFn > 0 {
		rf := fc.innermostRangeFunc()
		if len(vals) > 0 && len(fc.results) > 0 {
			w.line("%s = %s", strings.Join(fc.resultRefs(), ", "), strings.Join(vals, ", "))
			w.line("%s = 'return'", rf.ctl)
		} else if len(vals) > 0 {
			w.line("%s = table.pack(%s)", rf.res, strings.Join(vals, ", "))
			w.line("%s = 'return'", rf.ctl)
		} else {
			w.line("%s = 'return'", rf.ctl)
		}
		w.line("do return false end")
		return
	}
	if fc.hasDefer && !fc.hoistedDefer {
		if len(vals) > 0 {
			w.line("%s = %s", strings.Join(fc.resultRefs(), ", "), strings.Join(vals, ", "))
		}
		w.line("do return end")
		return
	}
	if naked && len(fc.results) > 0 {
		w.line("do return %s end", strings.Join(fc.resultRefs(), ", "))
		return
	}
	if len(vals) == 0 {
		w.line("do return end")
		return
	}
	emitLine(w, fmt.Sprintf("do return %s end", strings.Join(vals, ", ")))
}

func (fc *funcCtx) innermostRangeFunc() *rangeFuncInfo {
	for i := len(fc.loops) - 1; i >= 0; i-- {
		if fc.loops[i].rangeFunc != nil {
			return fc.loops[i].rangeFunc
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// If

func (fc *funcCtx) ifStmt(w *writer, s *ast.IfStmt, isElse bool) {
	hasInit := s.Init != nil
	if hasInit {
		w.line("do")
		w.indent++
		fc.stmt(w, s.Init)
	}
	cond := fc.expr(s.Cond)
	fc.flushInto(w)
	emitLine(w, fmt.Sprintf("if %s then", cond))
	w.indent++
	fc.stmts(w, s.Body.List)
	w.indent--
	for s.Else != nil {
		if elif, ok := s.Else.(*ast.IfStmt); ok && elif.Init == nil {
			c := fc.expr(elif.Cond)
			if len(fc.pre) > 0 {
				// Conditions needing setup cannot use elseif.
				fc.pre = nil
				w.line("else")
				w.indent++
				fc.ifStmt(w, elif, true)
				w.indent--
				break
			}
			emitLine(w, fmt.Sprintf("elseif %s then", c))
			w.indent++
			fc.stmts(w, elif.Body.List)
			w.indent--
			s = elif
			continue
		}
		w.line("else")
		w.indent++
		switch e := s.Else.(type) {
		case *ast.BlockStmt:
			fc.stmts(w, e.List)
		case *ast.IfStmt:
			fc.ifStmt(w, e, true)
		}
		w.indent--
		break
	}
	w.line("end")
	if hasInit {
		w.indent--
		w.line("end")
	}
}

// ---------------------------------------------------------------------------
// Loops

func (fc *funcCtx) pushLoop(label string, isLoop bool) *loopInfo {
	l := &loopInfo{label: label, isLoop: isLoop}
	id := fc.e.newTmp("")
	if isLoop {
		l.contLbl = "__cont" + strings.TrimPrefix(id, "__")
	}
	l.breakLbl = "__brk" + strings.TrimPrefix(id, "__")
	fc.loops = append(fc.loops, l)
	return l
}

func (fc *funcCtx) popLoop() { fc.loops = fc.loops[:len(fc.loops)-1] }

// loopBody emits a loop body wrapped so that `goto continue` never jumps into a local's scope.
func (fc *funcCtx) loopBody(w *writer, l *loopInfo, body []ast.Stmt) {
	inner := &writer{indent: w.indent + 1}
	fc.stmts(inner, body)
	if l.usedCont {
		w.line("do")
		w.raw(inner.b.String())
		w.line("end")
		w.line("::%s::", l.contLbl)
	} else {
		fc.reemit(w, inner.b.String())
	}
}

// reemit dedents a buffer produced one level deeper.
func (fc *funcCtx) reemit(w *writer, s string) {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for _, l := range lines {
		if l == "" {
			continue
		}
		w.raw(strings.TrimPrefix(l, "\t") + "\n")
	}
}

func (fc *funcCtx) endLoop(w *writer, l *loopInfo) {
	if l.usedBreak {
		w.line("::%s::", l.breakLbl)
	}
}

func (fc *funcCtx) forStmt(w *writer, s *ast.ForStmt, label string) {
	w.line("do")
	w.indent++
	// Per-iteration loop variables (Go 1.22) matter only when closures capture them.
	var perIter []*types.Var
	if as, ok := s.Init.(*ast.AssignStmt); ok && as.Tok == token.DEFINE {
		for _, l := range as.Lhs {
			if v, ok := fc.info().Defs[l.(*ast.Ident)].(*types.Var); ok && fc.e.c.captured[v] {
				perIter = append(perIter, v)
			}
		}
	}
	if s.Init != nil {
		fc.stmt(w, s.Init)
	}
	l := fc.pushLoop(label, true)
	if len(perIter) > 0 {
		first := fc.e.newTmp("first")
		w.line("local %s = true", first)
		var outer []string
		for range perIter {
			outer = append(outer, fc.e.newTmp("iv"))
		}
		var names []string
		for _, v := range perIter {
			names = append(names, fc.varName(v))
		}
		w.line("local %s = %s", strings.Join(outer, ", "), strings.Join(names, ", "))
		w.line("while true do")
		w.indent++
		for i, v := range perIter {
			if fc.e.c.boxed[v] {
				w.line("local %s = {v = %s.v}", names[i], outer[i])
			} else {
				w.line("local %s = %s", names[i], outer[i])
			}
		}
		w.line("if %s then %s = false else", first, first)
		if s.Post != nil {
			w.indent++
			fc.stmt(w, s.Post)
			w.indent--
		}
		w.line("end")
		if s.Cond != nil {
			cond := fc.expr(s.Cond)
			fc.flushInto(w)
			emitLine(w, fmt.Sprintf("if not (%s) then break end", cond))
		}
		bodyW := &writer{indent: w.indent}
		fc.loopBody(bodyW, l, s.Body.List)
		w.raw(bodyW.b.String())
		w.line("%s = %s", strings.Join(outer, ", "), strings.Join(names, ", "))
		w.indent--
		w.line("end")
	} else {
		cond := "true"
		if s.Cond != nil {
			cond = fc.expr(s.Cond)
		}
		if len(fc.pre) > 0 {
			w.line("while true do")
			w.indent++
			fc.flushInto(w)
			emitLine(w, fmt.Sprintf("if not (%s) then break end", cond))
		} else {
			emitLine(w, fmt.Sprintf("while %s do", cond))
			w.indent++
		}
		fc.loopBody(w, l, s.Body.List)
		if s.Post != nil {
			fc.stmt(w, s.Post)
		}
		w.indent--
		w.line("end")
	}
	fc.popLoop()
	w.indent--
	w.line("end")
	fc.endLoop(w, l)
}

func (fc *funcCtx) rangeStmt(w *writer, s *ast.RangeStmt, label string) {
	xt := fc.typeOf(s.X)
	// Loop variables: either declared (:=) or assigned (=).
	keyName, valName := "", ""
	var keyLv, valLv *lvalue
	define := s.Tok == token.DEFINE
	var keyVar, valVar *types.Var
	if s.Key != nil && !isBlank(s.Key) {
		if define {
			keyVar = fc.info().Defs[s.Key.(*ast.Ident)].(*types.Var)
		} else {
			lv := fc.lvalueOf(s.Key, false)
			keyLv = &lv
		}
	}
	if s.Value != nil && !isBlank(s.Value) {
		if define {
			valVar = fc.info().Defs[s.Value.(*ast.Ident)].(*types.Var)
		} else {
			lv := fc.lvalueOf(s.Value, false)
			valLv = &lv
		}
	}
	// bindVars emits the per-iteration binding of loop variables from raw key/value expressions.
	bindVars := func(w *writer, k, v string, elemT types.Type, cloneVal bool) {
		if keyVar != nil {
			fc.declareVar(w, keyVar, k)
		} else if keyLv != nil {
			fc.store(w, *keyLv, k)
		}
		if v != "" {
			if cloneVal && elemT != nil {
				v = fc.cloneIfNeeded(v, elemT)
			}
			if valVar != nil {
				fc.declareVar(w, valVar, v)
			} else if valLv != nil {
				fc.store(w, *valLv, v)
			}
		}
	}
	_ = keyName
	_ = valName
	needVal := keyVar != nil || keyLv != nil
	_ = needVal
	hasVal := valVar != nil || valLv != nil

	w.line("do")
	w.indent++
	l := fc.pushLoop(label, true)
	switch u := coreType(xt).(type) {
	case *types.Basic:
		if u.Info()&types.IsInteger != 0 || u.Kind() == types.UntypedInt {
			n := fc.intOperand(s.X)
			fc.flushInto(w)
			iv := fc.e.newTmp("i")
			emitLine(w, fmt.Sprintf("for %s = 0, %s - 1 do", iv, prefixOrParen(n)))
			w.indent++
			bindVars(w, fc.numConversion(iv, types.Typ[types.Int], xt), "", nil, false)
		} else {
			// String: iterate runes.
			str := fc.expr(s.X)
			fc.flushInto(w)
			iv, rv := fc.e.newTmp("i"), fc.e.newTmp("r")
			emitLine(w, fmt.Sprintf("for %s, %s in rt.runes(%s) do", iv, rv, str))
			w.indent++
			if hasVal {
				bindVars(w, iv, rv, nil, false)
			} else {
				bindVars(w, iv, "", nil, false)
			}
		}
	case *types.Slice, *types.Array, *types.Pointer:
		x := fc.expr(s.X)
		fc.flushInto(w)
		sv := fc.e.newTmp("s")
		iv := fc.e.newTmp("i")
		var elemT types.Type
		switch u := u.(type) {
		case *types.Slice:
			elemT = u.Elem()
			emitLine(w, fmt.Sprintf("local %s = %s", sv, x))
			if hasVal {
				av, ov, nv := fc.e.newTmp("a"), fc.e.newTmp("o"), fc.e.newTmp("n")
				// Inline rather than rt.sparts: the call is a large share of short loops.
				w.line("local %s, %s, %s = E, 1, 0", av, ov, nv)
				w.line("if %s ~= nil then %s, %s, %s = %s[1], %s[2], %s[3] end", sv, av, ov, nv, sv, sv, sv)
				w.line("for %s = 0, %s - 1 do", iv, nv)
				w.indent++
				bindVars(w, iv, fmt.Sprintf("%s[%s + %s]", av, ov, iv), elemT, true)
			} else {
				w.line("for %s = 0, (%s and %s[3] or 0) - 1 do", iv, sv, sv)
				w.indent++
				bindVars(w, iv, "", nil, false)
			}
		case *types.Array:
			elemT = u.Elem()
			emitLine(w, fmt.Sprintf("local %s = %s", sv, x))
			w.line("for %s = 0, %d do", iv, u.Len()-1)
			w.indent++
			if hasVal {
				bindVars(w, iv, fmt.Sprintf("%s[%s + 1]", sv, iv), elemT, true)
			} else {
				bindVars(w, iv, "", nil, false)
			}
		case *types.Pointer:
			at := u.Elem().Underlying().(*types.Array)
			elemT = at.Elem()
			emitLine(w, fmt.Sprintf("local %s = %s", sv, x))
			w.line("for %s = 0, %d do", iv, at.Len()-1)
			w.indent++
			if hasVal {
				bindVars(w, iv, fmt.Sprintf("%s[%s + 1]", sv, iv), elemT, true)
			} else {
				bindVars(w, iv, "", nil, false)
			}
		}
	case *types.Map:
		x := fc.expr(s.X)
		fc.flushInto(w)
		kv, vv := fc.e.newTmp("k"), fc.e.newTmp("v")
		if isTypeParam(u.Key()) || valueKind(u.Elem()) == "dyn" {
			// Representation depends on the runtime key/value types.
			emitLine(w, fmt.Sprintf("for _, %s, %s in rt.mrange3(%s, %s) do", kv, vv, x, fc.mapDesc(u)))
			w.indent++
			w.line("do")
			w.indent++
			if hasVal {
				bindVars(w, kv, vv, u.Elem(), true)
			} else {
				bindVars(w, kv, "", nil, false)
			}
			break
		}
		emitLine(w, fmt.Sprintf("for %s, %s in next, %s or E do", kv, vv, prefixOrParen(x)))
		w.indent++
		w.line("if %s ~= LEN then", kv)
		w.indent++
		key, val := kv, vv
		if fc.mapStoresPairs(u) {
			key = vv + "[1]"
			val = vv + "[2]"
		} else {
			switch valueKind(u.Elem()) {
			case "nilable":
				val = fmt.Sprintf("(%s or nil)", vv)
			case "iface":
				val = fmt.Sprintf("nn(%s)", vv)
			}
			if nilableKey(u.Key()) {
				key = fmt.Sprintf("(%s ~= NILKEY and %s or nil)", kv, kv)
			}
		}
		if hasVal {
			bindVars(w, key, val, u.Elem(), true)
		} else {
			bindVars(w, key, "", nil, false)
		}
	case *types.Chan:
		x := fc.expr(s.X)
		fc.flushInto(w)
		cv, vv, okv := fc.e.newTmp("c"), fc.e.newTmp("v"), fc.e.newTmp("ok")
		emitLine(w, fmt.Sprintf("local %s = %s", cv, x))
		w.line("while true do")
		w.indent++
		w.line("local %s, %s = rt.recv2(%s)", vv, okv, cv)
		w.line("if not %s then break end", okv)
		bindVars(w, vv, "", nil, false)
	case *types.Signature:
		if arg := backwardSliceArg(fc.info(), s.X); arg != nil {
			x := fc.expr(arg)
			fc.flushInto(w)
			sv, iv := fc.e.newTmp("s"), fc.e.newTmp("i")
			av, ov, nv := fc.e.newTmp("a"), fc.e.newTmp("o"), fc.e.newTmp("n")
			emitLine(w, fmt.Sprintf("local %s = %s", sv, x))
			w.line("local %s, %s, %s = E, 1, 0", av, ov, nv)
			w.line("if %s ~= nil then %s, %s, %s = %s[1], %s[2], %s[3] end", sv, av, ov, nv, sv, sv, sv)
			w.line("for %s = %s - 1, 0, -1 do", iv, nv)
			w.indent++
			if hasVal {
				bindVars(w, iv, fmt.Sprintf("%s[%s + %s]", av, ov, iv), coreType(fc.typeOf(arg)).(*types.Slice).Elem(), true)
			} else {
				bindVars(w, iv, "", nil, false)
			}
			break
		}
		fc.popLoop()
		fc.rangeFunc(w, s, u, label, keyVar, valVar, keyLv, valLv)
		w.indent--
		w.line("end")
		return
	default:
		fc.e.c.warnf(fc.e.pos(s.Pos()), "unsupported range over %s", xt)
	}
	fc.loopBody(w, l, s.Body.List)
	if m, ok := coreType(xt).(*types.Map); ok {
		_ = m
		w.indent--
		w.line("end")
	}
	w.indent--
	w.line("end")
	fc.popLoop()
	w.indent--
	w.line("end")
	fc.endLoop(w, l)
}

func isBlank(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "_"
}

// rangeFunc lowers range-over-func into a call with a yield function; control flow leaving the
// body is signalled through a control variable. The yield function is a callable table when
// the body can be converted (closures.go): its table holds the captures and the control and
// result values, and the table's local has the same name as the function's first parameter.
func (fc *funcCtx) rangeFunc(w *writer, s *ast.RangeStmt, sig *types.Signature, label string, keyVar, valVar *types.Var, keyLv, valLv *lvalue) {
	rf := &rangeFuncInfo{ctl: fc.e.newTmp("ctl"), res: fc.e.newTmp("res"), signals: map[string]bool{}}
	caps, convert := fc.e.c.rangeCaps[s]
	// Returns assign result locals by name (resultRefs), which a converted body cannot reach.
	if convert && len(fc.results) > 0 && hasReturn(s.Body) {
		convert = false
	}
	tab := ""
	if convert {
		tab = fc.e.newTmp("r")
		rf.ctl, rf.res = tab+".ctl", tab+".res"
	}
	l := fc.pushLoop(label, true)
	l.rangeFunc = rf
	seq := fc.expr(s.X)
	fc.flushInto(w)
	yieldSig := sig.Params().At(0).Type().Underlying().(*types.Signature)
	var params []string
	for i := 0; i < yieldSig.Params().Len(); i++ {
		params = append(params, fc.e.newTmp("y"))
	}
	// fw receives the yield function.
	fw := w
	var refs []string
	savedRefs := fc.captureRefs
	if convert {
		var typeParams []string
		for _, tp := range fc.enclosingTypeParams() {
			typeParams = append(typeParams, typeParamName(tp))
		}
		refs = make([]string, len(caps))
		inner := map[*types.Var]string{}
		for v, r := range savedRefs {
			inner[v] = r
		}
		for i, v := range caps {
			refs[i] = fc.rawRef(v)
			inner[v] = fmt.Sprintf("%s[%d]", tab, i+1)
		}
		refs = append(refs, typeParams...)
		fc.captureRefs = inner
		fw = &writer{}
		fw.line("function(%s)", strings.Join(append([]string{tab}, params...), ", "))
		fw.indent++
		if len(typeParams) > 0 {
			var slots []string
			for i := range typeParams {
				slots = append(slots, fmt.Sprintf("%s[%d]", tab, len(caps)+i+1))
			}
			fw.line("local %s = %s", strings.Join(typeParams, ", "), strings.Join(slots, ", "))
		}
	} else {
		w.line("local %s, %s", rf.ctl, rf.res)
		emitLine(w, fmt.Sprintf("%s(function(%s)", prefix(seq), strings.Join(params, ", ")))
		w.indent++
	}
	fc.inRangeFn++
	if len(params) > 0 {
		if keyVar != nil {
			fc.declareVar(fw, keyVar, params[0])
		} else if keyLv != nil {
			fc.store(fw, *keyLv, params[0])
		}
	}
	if len(params) > 1 {
		if valVar != nil {
			fc.declareVar(fw, valVar, params[1])
		} else if valLv != nil {
			fc.store(fw, *valLv, params[1])
		}
	}
	body := &writer{indent: fw.indent}
	fc.stmts(body, s.Body.List)
	if l.usedCont {
		fw.line("do")
		fw.raw(indentBlock(body.b.String()))
		fw.line("end")
		fw.line("::%s::", l.contLbl)
	} else {
		fw.raw(body.b.String())
	}
	fw.line("return true")
	fc.inRangeFn--
	if convert {
		fc.captureRefs = savedRefs
		fw.indent--
		fw.raw("end")
		code, localTypes := captureLocalTypes(reindent(fw.b.String()), tab, len(refs))
		refs = append(refs, localTypes...)
		fc.e.hoisted = append(fc.e.hoisted, "rt.closure("+code+")")
		w.line("local %s = setmetatable({%s}, FN[%d])", tab, strings.Join(refs, ", "), len(fc.e.hoisted))
		emitLine(w, fmt.Sprintf("%s(%s)", prefix(seq), tab))
	} else {
		w.indent--
		w.line("end)")
	}
	fc.popLoop()
	// Propagate control flow signalled from inside the body.
	w.line("if %s == 'return' then", rf.ctl)
	w.indent++
	if len(fc.results) > 0 || fc.sig == nil || fc.sig.Results().Len() == 0 {
		fc.emitReturn(w, nil, true)
	} else {
		fc.emitReturn(w, []string{fmt.Sprintf("table.unpack(%s, 1, %s.n)", rf.res, rf.res)}, false)
	}
	w.indent--
	w.line("end")
	for _, sig := range sortedKeys(rf.signals) {
		w.line("if %s == %s then", rf.ctl, luaString(sig))
		w.indent++
		parts := strings.SplitN(sig, ":", 2)
		fc.jump(w, parts[0], parts[1])
		w.indent--
		w.line("end")
	}
	if l.usedBreak {
		w.line("::%s::", l.breakLbl)
	}
}

// hasReturn reports return statements of a range-over-func body, outside nested literals.
func hasReturn(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			found = true
		}
		return !found
	})
	return found
}

func indentBlock(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "\t" + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// ---------------------------------------------------------------------------
// Labels and branches

func (fc *funcCtx) labeled(w *writer, s *ast.LabeledStmt) {
	label := s.Label.Name
	switch st := s.Stmt.(type) {
	case *ast.ForStmt:
		w.line("::%s::", gotoLabel(label))
		fc.forStmt(w, st, label)
	case *ast.RangeStmt:
		w.line("::%s::", gotoLabel(label))
		fc.rangeStmt(w, st, label)
	case *ast.SwitchStmt:
		w.line("::%s::", gotoLabel(label))
		fc.switchStmt(w, st, label)
	case *ast.TypeSwitchStmt:
		w.line("::%s::", gotoLabel(label))
		fc.typeSwitchStmt(w, st, label)
	case *ast.SelectStmt:
		w.line("::%s::", gotoLabel(label))
		fc.selectStmt(w, st, label)
	default:
		w.line("::%s::", gotoLabel(label))
		fc.stmt(w, s.Stmt)
	}
}

func gotoLabel(name string) string { return "__L_" + luaIdent(name) }

func (fc *funcCtx) branch(w *writer, s *ast.BranchStmt) {
	label := ""
	if s.Label != nil {
		label = s.Label.Name
	}
	switch s.Tok {
	case token.BREAK:
		fc.jump(w, "break", label)
	case token.CONTINUE:
		fc.jump(w, "continue", label)
	case token.GOTO:
		w.line("goto %s", gotoLabel(label))
	case token.FALLTHROUGH:
		// Handled by switchStmt, which sets the next case index.
		w.line("%s = %s + 1", fc.fallVar, fc.fallVar)
	}
}

// jump emits break/continue to the loop identified by label (innermost when empty).
func (fc *funcCtx) jump(w *writer, kind, label string) {
	for i := len(fc.loops) - 1; i >= 0; i-- {
		l := fc.loops[i]
		var target bool
		switch {
		case label != "":
			target = l.label == label
		case kind == "continue":
			target = l.isLoop
		default:
			target = true
		}
		if !target {
			if l.rangeFunc != nil {
				// The target lies outside this range-over-func body: signal it and stop iterating.
				l.rangeFunc.signals[kind+":"+label] = true
				w.line("%s = %s", l.rangeFunc.ctl, luaString(kind+":"+label))
				w.line("do return false end")
				return
			}
			continue
		}
		if l.rangeFunc != nil {
			if kind == "continue" {
				w.line("do return true end")
			} else {
				w.line("do return false end")
			}
			return
		}
		if kind == "continue" {
			l.usedCont = true
			w.line("goto %s", l.contLbl)
		} else {
			l.usedBreak = true
			w.line("goto %s", l.breakLbl)
		}
		return
	}
	fc.e.c.warnf(fc.e.pos(0), "unresolved %s %s", kind, label)
}

// ---------------------------------------------------------------------------
// Switch

func (fc *funcCtx) switchStmt(w *writer, s *ast.SwitchStmt, label string) {
	w.line("do")
	w.indent++
	if s.Init != nil {
		fc.stmt(w, s.Init)
	}
	l := fc.pushLoop(label, false)
	tag := ""
	var tagT types.Type
	if s.Tag != nil {
		tagT = fc.typeOf(s.Tag)
		tag = fc.e.newTmp("sw")
		v := fc.expr(s.Tag)
		fc.flushInto(w)
		emitLine(w, fmt.Sprintf("local %s = %s", tag, v))
	}
	hasFall := false
	for _, c := range s.Body.List {
		cc := c.(*ast.CaseClause)
		if len(cc.Body) > 0 {
			if b, ok := cc.Body[len(cc.Body)-1].(*ast.BranchStmt); ok && b.Tok == token.FALLTHROUGH {
				hasFall = true
			}
		}
	}
	cond := func(cc *ast.CaseClause) string {
		var parts []string
		for _, e := range cc.List {
			if tag == "" {
				parts = append(parts, fc.operand(e))
			} else {
				parts = append(parts, fc.caseCompare(tag, tagT, e))
			}
		}
		return strings.Join(parts, " or ")
	}
	clauses := s.Body.List
	if tag != "" && fc.switchTree(w, tag, tagT, clauses) {
		// Emitted as a binary search with gotos.
	} else if !hasFall {
		first := true
		var def *ast.CaseClause
		for _, c := range clauses {
			cc := c.(*ast.CaseClause)
			if cc.List == nil {
				def = cc
				continue
			}
			kw := "elseif"
			if first {
				kw = "if"
				first = false
			}
			emitLine(w, fmt.Sprintf("%s %s then", kw, cond(cc)))
			w.indent++
			fc.stmts(w, cc.Body)
			w.indent--
		}
		if def != nil {
			if first {
				w.line("do")
			} else {
				w.line("else")
			}
			w.indent++
			fc.stmts(w, def.Body)
			w.indent--
			w.line("end")
		} else if !first {
			w.line("end")
		}
	} else {
		// Compute the selected case index, then run bodies in order so fallthrough can chain.
		cv := fc.e.newTmp("case")
		w.line("local %s = 0", cv)
		first := true
		defIdx := 0
		for i, c := range clauses {
			cc := c.(*ast.CaseClause)
			if cc.List == nil {
				defIdx = i + 1
				continue
			}
			kw := "elseif"
			if first {
				kw = "if"
				first = false
			}
			emitLine(w, fmt.Sprintf("%s %s then %s = %d", kw, cond(cc), cv, i+1))
		}
		if !first {
			if defIdx > 0 {
				w.line("else %s = %d end", cv, defIdx)
			} else {
				w.line("end")
			}
		} else if defIdx > 0 {
			w.line("%s = %d", cv, defIdx)
		}
		saved := fc.fallVar
		fc.fallVar = cv
		for i, c := range clauses {
			cc := c.(*ast.CaseClause)
			w.line("if %s == %d then", cv, i+1)
			w.indent++
			fc.stmts(w, cc.Body)
			w.indent--
			w.line("end")
		}
		fc.fallVar = saved
	}
	fc.popLoop()
	w.indent--
	w.line("end")
	fc.endLoop(w, l)
}

// switchTreeMin is the number of case values from which an integer switch becomes a tree.
const switchTreeMin = 8

// switchTree emits a switch over integer constants as a binary search that jumps to the case
// body, instead of a linear if/elseif chain: Go compiles large switches to jump tables, and
// the compiler switches over hundreds of node kinds and characters.
func (fc *funcCtx) switchTree(w *writer, tag string, tagT types.Type, clauses []ast.Stmt) bool {
	if b, ok := tagT.Underlying().(*types.Basic); !ok || b.Info()&types.IsInteger == 0 {
		return false
	}
	type caseValue struct {
		value  int64
		clause int
	}
	var values []caseValue
	defaultClause := -1
	for i, c := range clauses {
		cc := c.(*ast.CaseClause)
		if cc.List == nil {
			defaultClause = i
			continue
		}
		for _, e := range cc.List {
			val, _, ok := fc.folded(e)
			if !ok {
				return false
			}
			v, exact := constant.Int64Val(constant.ToInt(val))
			if !exact {
				return false
			}
			v, ok = switchCaseValue(v, tagT)
			if !ok {
				return false
			}
			values = append(values, caseValue{v, i})
		}
	}
	if len(values) < switchTreeMin {
		return false
	}
	slices.SortFunc(values, func(a, b caseValue) int { return cmp.Compare(a.value, b.value) })

	// Binary search on the tag that jumps straight to the clause's label. Bodies follow in
	// source order, each in its own block so a goto never enters a local's scope, and a body
	// ending in fallthrough simply runs on into the next label.
	base := fc.e.newTmp("sw")
	label := func(i int) string { return fmt.Sprintf("%s_%d", base, i) }
	done := base + "_done"
	missing := done
	if defaultClause >= 0 {
		missing = label(defaultClause)
	}
	var search func(vals []caseValue)
	search = func(vals []caseValue) {
		if len(vals) <= 4 {
			for _, v := range vals {
				w.line("if %s == %d then goto %s end", tag, v.value, label(v.clause))
			}
			return
		}
		mid := len(vals) / 2
		w.line("if %s < %d then", tag, vals[mid].value)
		w.indent++
		search(vals[:mid])
		w.indent--
		w.line("else")
		w.indent++
		search(vals[mid:])
		w.indent--
		w.line("end")
	}
	search(values)
	w.line("goto %s", missing)
	saved := fc.fallVar
	fc.fallVar = ""
	for i, c := range clauses {
		body := c.(*ast.CaseClause).Body
		falls := false
		if n := len(body); n > 0 {
			if b, ok := body[n-1].(*ast.BranchStmt); ok && b.Tok == token.FALLTHROUGH {
				falls = true
				body = body[:n-1]
			}
		}
		w.line("::%s::", label(i))
		w.line("do")
		w.indent++
		fc.stmts(w, body)
		w.indent--
		w.line("end")
		if !falls {
			w.line("goto %s", done)
		}
	}
	fc.fallVar = saved
	w.line("::%s::", done)
	return true
}

func (fc *funcCtx) caseCompare(tag string, tagT types.Type, e ast.Expr) string {
	et := fc.typeOf(e)
	if fc.info().Types[e].IsNil() {
		return tag + " == nil"
	}
	if isInterface(tagT) {
		v := fc.expr(e)
		if !isInterface(et) {
			if rawPlainEq(et) {
				return fmt.Sprintf("%s == %s", tag, prefixOrParen(v))
			}
			v = fc.box(v, et)
		}
		return fmt.Sprintf("ieq(%s, %s)", tag, v)
	}
	if isAggregate(tagT) {
		return fmt.Sprintf("rt.eq(%s, %s, %s)", fc.typeExpr(tagT), tag, fc.expr(e))
	}
	if isTypeParam(tagT) {
		return fmt.Sprintf("%s.eq(%s, %s)", fc.typeExpr(tagT), tag, fc.expr(e))
	}
	return fmt.Sprintf("%s == %s", tag, fc.operand(e))
}

func (fc *funcCtx) typeSwitchStmt(w *writer, s *ast.TypeSwitchStmt, label string) {
	w.line("do")
	w.indent++
	if s.Init != nil {
		fc.stmt(w, s.Init)
	}
	l := fc.pushLoop(label, false)
	var x ast.Expr
	bindName := ""
	switch a := s.Assign.(type) {
	case *ast.AssignStmt:
		x = a.Rhs[0].(*ast.TypeAssertExpr).X
		bindName = a.Lhs[0].(*ast.Ident).Name
	case *ast.ExprStmt:
		x = a.X.(*ast.TypeAssertExpr).X
	}
	xv := fc.e.newTmp("x")
	tv := fc.e.newTmp("ty")
	code := fc.expr(x)
	fc.flushInto(w)
	emitLine(w, fmt.Sprintf("local %s = %s", xv, code))
	w.line("local %s = typeof(%s)", tv, xv)
	first := true
	var def *ast.CaseClause
	for _, c := range s.Body.List {
		cc := c.(*ast.CaseClause)
		if cc.List == nil {
			def = cc
			continue
		}
		var conds []string
		for _, e := range cc.List {
			conds = append(conds, fc.typeCaseCond(xv, tv, e))
		}
		kw := "elseif"
		if first {
			kw = "if"
			first = false
		}
		w.line("%s %s then", kw, strings.Join(conds, " or "))
		w.indent++
		fc.bindTypeSwitchVar(w, cc, bindName, xv)
		fc.stmts(w, cc.Body)
		w.indent--
	}
	if def != nil {
		if first {
			w.line("do")
		} else {
			w.line("else")
		}
		w.indent++
		fc.bindTypeSwitchVar(w, def, bindName, xv)
		fc.stmts(w, def.Body)
		w.indent--
		w.line("end")
	} else if !first {
		w.line("end")
	}
	fc.popLoop()
	w.indent--
	w.line("end")
	fc.endLoop(w, l)
}

func (fc *funcCtx) typeCaseCond(xv, tv string, e ast.Expr) string {
	if fc.info().Types[e].IsNil() {
		return xv + " == nil"
	}
	t := fc.typeOf(e)
	if isInterface(t) {
		return fmt.Sprintf("rt.implements(%s, %s)", tv, fc.typeExpr(t))
	}
	return fmt.Sprintf("%s == %s", tv, fc.typeExpr(t))
}

func (fc *funcCtx) bindTypeSwitchVar(w *writer, cc *ast.CaseClause, name, xv string) {
	if name == "" {
		return
	}
	obj, ok := fc.info().Implicits[cc].(*types.Var)
	if !ok {
		return
	}
	if !fc.usedVar(obj, cc) {
		return
	}
	t := obj.Type()
	val := xv
	if !isInterface(t) {
		val = fmt.Sprintf("rt.unbox(%s)", xv)
		if fc.e.c.needsClone(t) {
			val = fc.cloneExpr(val, t)
		}
	}
	fc.declareVar(w, obj, val)
}

func (fc *funcCtx) usedVar(v *types.Var, n ast.Node) bool {
	used := false
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && fc.info().Uses[id] == v {
			used = true
		}
		return !used
	})
	return used
}

// ---------------------------------------------------------------------------
// Select

func (fc *funcCtx) selectStmt(w *writer, s *ast.SelectStmt, label string) {
	w.line("do")
	w.indent++
	l := fc.pushLoop(label, false)
	var cases []string
	hasDefault := false
	for _, c := range s.Body.List {
		cc := c.(*ast.CommClause)
		switch comm := cc.Comm.(type) {
		case nil:
			hasDefault = true
			cases = append(cases, "false")
		case *ast.SendStmt:
			ct := coreType(fc.typeOf(comm.Chan)).(*types.Chan)
			cases = append(cases, fmt.Sprintf("{%s, 'send', %s}", fc.expr(comm.Chan), fc.valueForStore(comm.Value, ct.Elem())))
		case *ast.ExprStmt:
			ch := unparen(comm.X).(*ast.UnaryExpr).X
			cases = append(cases, fmt.Sprintf("{%s, 'recv'}", fc.expr(ch)))
		case *ast.AssignStmt:
			ch := unparen(comm.Rhs[0]).(*ast.UnaryExpr).X
			cases = append(cases, fmt.Sprintf("{%s, 'recv'}", fc.expr(ch)))
		}
	}
	fc.flushInto(w)
	iv, vv, okv := fc.e.newTmp("si"), fc.e.newTmp("sv"), fc.e.newTmp("sok")
	w.line("local %s, %s, %s = rt.select({%s}, %v)", iv, vv, okv, strings.Join(cases, ", "), hasDefault)
	for i, c := range s.Body.List {
		cc := c.(*ast.CommClause)
		kw := "elseif"
		if i == 0 {
			kw = "if"
		}
		w.line("%s %s == %d then", kw, iv, i+1)
		w.indent++
		if as, ok := cc.Comm.(*ast.AssignStmt); ok {
			vals := []string{vv, okv}
			if as.Tok == token.DEFINE {
				for j, lhs := range as.Lhs {
					if v, ok := fc.info().Defs[lhs.(*ast.Ident)].(*types.Var); ok && v != nil {
						fc.declareVar(w, v, vals[j])
					}
				}
			} else {
				for j, lhs := range as.Lhs {
					fc.store(w, fc.lvalueOf(lhs, false), vals[j])
				}
			}
		}
		fc.stmts(w, cc.Body)
		w.indent--
	}
	if len(s.Body.List) > 0 {
		w.line("end")
	}
	fc.popLoop()
	w.indent--
	w.line("end")
	fc.endLoop(w, l)
}

// ---------------------------------------------------------------------------
// Defer and go

func (fc *funcCtx) deferStmt(w *writer, s *ast.DeferStmt) {
	fn, args := fc.callThunk(w, s.Call)
	if len(args) > 0 {
		fn = fmt.Sprintf("rt.thunk(%s, %s)", fn, strings.Join(args, ", "))
	}
	emitLine(w, fmt.Sprintf("%s[#%s + 1] = %s", fc.deferVar, fc.deferVar, fn))
}

// callThunk evaluates a call's function value and arguments now, returning code for
// a function and argument temporaries to invoke later.
func (fc *funcCtx) callThunk(w *writer, call *ast.CallExpr) (string, []string) {
	fun := unparen(call.Fun)
	if fl, ok := fun.(*ast.FuncLit); ok && len(call.Args) == 0 {
		return fc.funcLit(fl), nil
	}
	isBuiltin := false
	if id, ok := fun.(*ast.Ident); ok {
		if _, ok := fc.info().Uses[id].(*types.Builtin); ok {
			isBuiltin = true
		}
	}
	if tv, ok := fc.info().Types[call.Fun]; ok && tv.IsType() {
		isBuiltin = true
	}
	if isBuiltin {
		// Evaluate arguments now and replay the builtin inside a closure.
		var tmps []string
		var newArgs []ast.Expr
		for _, a := range call.Args {
			tmp := fc.e.newTmp("da")
			code := fc.expr(a)
			fc.flushInto(w)
			emitLine(w, fmt.Sprintf("local %s = %s", tmp, code))
			tmps = append(tmps, tmp)
			id := ast.NewIdent(tmp)
			fc.info().Types[id] = types.TypeAndValue{Type: fc.typeOf(a)}
			newArgs = append(newArgs, id)
		}
		_ = tmps
		nc := *call
		nc.Args = newArgs
		saved := fc.info().Types
		_ = saved
		code := fc.builtinWithRaw(&nc, tmps)
		return fmt.Sprintf("function() %s end", code), nil
	}
	sig, _ := coreType(fc.typeOf(call.Fun)).(*types.Signature)
	args := fc.args(call, sig)
	var argTmps []string
	for _, a := range args {
		if isLiteral(a) || a == "nil" {
			argTmps = append(argTmps, a)
			continue
		}
		tmp := fc.e.newTmp("da")
		fc.flushInto(w)
		emitLine(w, fmt.Sprintf("local %s = %s", tmp, a))
		argTmps = append(argTmps, tmp)
	}
	// Method calls: bind the receiver now.
	if se, ok := fun.(*ast.SelectorExpr); ok {
		if sel := fc.info().Selections[se]; sel != nil && sel.Kind() == types.MethodVal {
			mt := fc.methodTargetOf(se, sel)
			rtmp := fc.e.newTmp("dr")
			fc.flushInto(w)
			emitLine(w, fmt.Sprintf("local %s = %s", rtmp, mt.recv))
			mt.recv = rtmp
			// The method and receiver are fixed now, as Go evaluates the method value at the
			// defer statement; no closure is made (see closures.go).
			fn := mt.fn
			if mt.iface {
				fn = rtmp + "." + methodKey(mt.name)
			}
			return fn, append(append(append([]string{}, mt.typeArgs...), rtmp), argTmps...)
		}
	}
	// The callee expression itself must be evaluated now as well.
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.IndexExpr, *ast.IndexListExpr:
		return fmt.Sprintf("function() %s end", fc.callWith(call, argTmps)), nil
	}
	if id != nil {
		if _, ok := fc.info().Uses[id].(*types.Func); ok {
			return fc.expr(call.Fun), argTmps
		}
	}
	ftmp := fc.e.newTmp("df")
	emitLine(w, fmt.Sprintf("local %s = %s", ftmp, fc.expr(call.Fun)))
	return ftmp, argTmps
}

// builtinWithRaw renders a builtin call whose arguments are pre-evaluated temporaries.
func (fc *funcCtx) builtinWithRaw(call *ast.CallExpr, tmps []string) string {
	if tv, ok := fc.info().Types[call.Fun]; ok && tv.IsType() {
		return fc.conversion(call.Args[0], tv.Type)
	}
	name := fc.info().Uses[unparen(call.Fun).(*ast.Ident)].(*types.Builtin).Name()
	return fc.builtin(name, call)
}

// discardLine evaluates code for its side effects only. Code without calls is dropped,
// and the throwaway local is scoped so long runs of blank assignments stay under Lua's local limit.
func discardLine(w *writer, code string) {
	if !strings.Contains(code, "(") {
		return
	}
	emitLine(w, "do local _ = "+code+" end")
}

// backwardSliceArg returns s for a slices.Backward(s) range expression, which is ranged over as
// a reverse index loop: a yield function would be a closure per loop, which LuaJIT cannot compile.
func backwardSliceArg(info *types.Info, x ast.Expr) ast.Expr {
	call, ok := unparen(x).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil
	}
	sel, ok := unparen(call.Fun).(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	fn, ok := info.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "slices" || fn.Name() != "Backward" {
		return nil
	}
	if _, ok := coreType(info.TypeOf(call.Args[0])).(*types.Slice); !ok {
		return nil
	}
	return call.Args[0]
}
