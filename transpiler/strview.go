package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// Go substrings are O(1) while Lua's string.sub copies, so scanning code written as
// f(s[i:]) or `text := s[a:b]` would become quadratic. A string view represents such a local
// as a (base, offset, length) triple, and range-aware runtime helpers read s[i:e] in place.

type strView struct {
	s, o, n string // Lua locals: base string, 0-based offset, length
}

// findStringViews selects string locals initialized from a slice expression that are never
// reassigned or address-taken.
func (fc *funcCtx) findStringViews(body *ast.BlockStmt) {
	candidates := map[*types.Var]bool{}
	disqualified := map[*types.Var]bool{}
	check := func(id *ast.Ident, rhs ast.Expr) {
		v, ok := fc.info().Defs[id].(*types.Var)
		if !ok || v == nil || id.Name == "_" {
			return
		}
		se, ok := unparen(rhs).(*ast.SliceExpr)
		if !ok || se.Slice3 {
			return
		}
		if !isString(fc.typeOf(se.X)) || !isString(v.Type()) || fc.e.c.boxed[v] || fc.e.c.captured[v] {
			return
		}
		candidates[v] = true
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE && len(n.Lhs) == 1 && len(n.Rhs) == 1 {
				if id, ok := n.Lhs[0].(*ast.Ident); ok {
					check(id, n.Rhs[0])
				}
			}
			if n.Tok != token.DEFINE || len(n.Lhs) != 1 {
				for _, lhs := range n.Lhs {
					if id, ok := unparen(lhs).(*ast.Ident); ok {
						if v, ok := fc.info().Uses[id].(*types.Var); ok {
							disqualified[v] = true
						}
					}
				}
			}
		case *ast.DeclStmt:
			gd, ok := n.Decl.(*ast.GenDecl)
			if ok && gd.Tok == token.VAR {
				for _, spec := range gd.Specs {
					vs := spec.(*ast.ValueSpec)
					if len(vs.Names) == 1 && len(vs.Values) == 1 {
						check(vs.Names[0], vs.Values[0])
					}
				}
			}
		case *ast.RangeStmt:
			if n.Tok == token.ASSIGN {
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if id, ok := e.(*ast.Ident); ok {
						if v, ok := fc.info().Uses[id].(*types.Var); ok {
							disqualified[v] = true
						}
					}
				}
			}
		}
		return true
	})
	for v := range candidates {
		if disqualified[v] {
			continue
		}
		if fc.views == nil {
			fc.views = map[*types.Var]*strView{}
		}
		name := luaIdent(v.Name())
		fc.views[v] = &strView{s: name + "__s", o: name + "__o", n: name + "__n"}
	}
}

// viewOf returns the string view an expression refers to, if any.
func (fc *funcCtx) viewOf(e ast.Expr) *strView {
	id, ok := unparen(e).(*ast.Ident)
	if !ok {
		return nil
	}
	v, ok := fc.info().Uses[id].(*types.Var)
	if !ok {
		return nil
	}
	return fc.viewOfVar(v)
}

// viewOfVar looks up a view through enclosing functions (closures can reference views).
func (fc *funcCtx) viewOfVar(v *types.Var) *strView {
	for c := fc; c != nil; c = c.parent {
		if view := c.views[v]; view != nil {
			return view
		}
	}
	return nil
}

// addOffset renders base + delta, folding zeros.
func addOffset(base, delta string) string {
	switch {
	case base == "0":
		return delta
	case delta == "0":
		return base
	}
	return fmt.Sprintf("%s + %s", base, prefixOrParen(delta))
}

// stringRange decomposes a string expression into (base, start, end) where end may be ""
// for "to the end of base". Offsets are 0-based and end is exclusive.
func (fc *funcCtx) stringRange(e ast.Expr) (base, start, end string) {
	e = unparen(e)
	if v := fc.viewOf(e); v != nil {
		return v.s, v.o, addOffset(v.o, v.n)
	}
	if se, ok := e.(*ast.SliceExpr); ok && !se.Slice3 && isString(fc.typeOf(se.X)) {
		b, s0, e0 := fc.stringRange(se.X)
		start = s0
		if se.Low != nil {
			start = addOffset(s0, fc.intOperand(se.Low))
		}
		end = e0
		if se.High != nil {
			end = addOffset(s0, fc.intOperand(se.High))
		}
		return b, start, end
	}
	return fc.expr(e), "0", ""
}

// declareView emits the locals of a string view initialized from rhs.
func (fc *funcCtx) declareView(w *writer, v *strView, rhs ast.Expr) {
	base, start, end := fc.stringRange(rhs)
	if !isSimple(base) {
		tmp := fc.e.newTmp("vs")
		emitLine(w, fmt.Sprintf("local %s = %s", tmp, base))
		base = tmp
	}
	if end == "" {
		end = "#" + base
	}
	length := end
	if start != "0" {
		length = fmt.Sprintf("%s - %s", prefixOrParen(end), prefixOrParen(start))
	}
	emitLine(w, fmt.Sprintf("local %s, %s, %s = %s, %s, %s", v.s, v.o, v.n, base, start, length))
}

// materialize renders a view as a real string (for uses the view cannot serve).
func (v *strView) materialize() string {
	return fmt.Sprintf("ssub(%s, %s + 1, %s + %s)", v.s, v.o, v.o, v.n)
}

// rangeCall rewrites calls to known prefix-reading functions on substrings.
func (fc *funcCtx) rangeCall(call *ast.CallExpr) (string, bool) {
	var id *ast.Ident
	switch f := unparen(call.Fun).(type) {
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return "", false
	}
	fn, ok := fc.info().Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil {
		return "", false
	}
	name := fn.Pkg().Path() + "." + fn.Name()
	isRange := func(e ast.Expr) bool {
		if fc.viewOf(e) != nil {
			return true
		}
		se, ok := unparen(e).(*ast.SliceExpr)
		return ok && isString(fc.typeOf(se.X))
	}
	if len(call.Args) == 0 || !isRange(call.Args[0]) {
		return "", false
	}
	endArg := func(end string) string {
		if end == "" {
			return "nil"
		}
		return end
	}
	switch name {
	case "unicode/utf8.DecodeRuneInString":
		s, i, e := fc.stringRange(call.Args[0])
		return fmt.Sprintf("rt.decodeRuneAt(%s, %s, %s)", s, i, endArg(e)), true
	case "unicode/utf8.DecodeLastRuneInString":
		s, i, e := fc.stringRange(call.Args[0])
		if e == "" {
			e = "#" + prefixOrParen(s)
		}
		return fmt.Sprintf("rt.decodeLastRuneAt(%s, %s, %s)", s, e, i), true
	case "strings.HasPrefix":
		s, i, e := fc.stringRange(call.Args[0])
		return fmt.Sprintf("rt.hasPrefixAt(%s, %s, %s, %s)", s, i, fc.expr(call.Args[1]), endArg(e)), true
	case "strings.IndexByte":
		s, i, e := fc.stringRange(call.Args[0])
		return fmt.Sprintf("rt.indexByteFrom(%s, %s, %s, %s)", s, i, fc.expr(call.Args[1]), endArg(e)), true
	case "strings.Index":
		s, i, e := fc.stringRange(call.Args[0])
		return fmt.Sprintf("rt.indexFrom(%s, %s, %s, %s)", s, i, fc.expr(call.Args[1]), endArg(e)), true
	case "strings.IndexAny":
		s, i, e := fc.stringRange(call.Args[0])
		return fmt.Sprintf("rt.indexAnyFrom(%s, %s, %s, %s)", s, i, fc.expr(call.Args[1]), endArg(e)), true
	}
	return "", false
}
