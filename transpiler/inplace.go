package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// A Go slice header is a value, so `s = append(s, v)` and `s = s[:n]` make a new header
// table in Lua. When no other variable, field or value can hold the header that a slice
// field or local variable refers to, those self-updates can change the header in place
// instead. analyzeInPlaceSlices finds the slice variables (fields and locals) whose header
// never escapes: every use is len/cap, indexing, slicing (which makes a new header), range
// (which reads the length once), a nil comparison, the first argument of append, copy, or an
// assignment of a freshly made header. Struct copies duplicate slice headers (see rt.lua), so
// copies of a struct never share them.

func (c *Compiler) analyzeInPlaceSlices() {
	c.sliceEscapes = map[*types.Var]bool{}
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		info := p.TypesInfo
		for _, f := range p.Syntax {
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				c.inPlaceNode(info, n, stack)
				return true
			})
		}
	}
}

// inPlaceSlice reports whether self-updates of v may change its slice header in place.
func (c *Compiler) inPlaceSlice(v *types.Var) bool {
	if v == nil {
		return false
	}
	v = v.Origin()
	if _, ok := v.Type().Underlying().(*types.Slice); !ok {
		return false
	}
	if c.sliceEscapes[v] {
		return false
	}
	// Package variables could be reached through natives; locals captured by a closure may
	// be updated after the function has returned the header.
	if !v.IsField() && (v.Parent() == nil || v.Pkg() == nil || v.Parent() == v.Pkg().Scope() || c.captured[v]) {
		return false
	}
	return true
}

func sliceVar(v *types.Var) bool {
	if v == nil {
		return false
	}
	_, ok := v.Type().Underlying().(*types.Slice)
	return ok
}

func (c *Compiler) escape(v *types.Var) {
	if v != nil {
		c.sliceEscapes[v.Origin()] = true
	}
}

func (c *Compiler) inPlaceNode(info *types.Info, n ast.Node, stack []ast.Node) {
	switch n := n.(type) {
	case *ast.SelectorExpr:
		if sel := info.Selections[n]; sel != nil && sel.Kind() == types.FieldVal {
			if v, ok := sel.Obj().(*types.Var); ok && sliceVar(v) {
				c.classifySliceUse(info, n, v, stack)
			}
		}
	case *ast.Ident:
		if v, ok := info.Uses[n].(*types.Var); ok && sliceVar(v) && !v.IsField() {
			c.classifySliceUse(info, n, v, stack)
		}
		if v, ok := info.Defs[n].(*types.Var); ok && sliceVar(v) && !v.IsField() && len(stack) >= 3 {
			switch stack[len(stack)-2].(type) {
			case *ast.Field:
				// Parameters and receivers start out holding the caller's header; named
				// results start out nil.
				if fl, isList := stack[len(stack)-3].(*ast.FieldList); !isList || !isResultList(fl, stack) {
					c.escape(v)
				}
			case *ast.RangeStmt:
				// A range variable of slice type holds a copy of an element's header.
				c.escape(v)
			case *ast.AssignStmt, *ast.ValueSpec:
				// Declarations store their initial value.
				c.classifySliceUse(info, n, v, stack)
			default:
				c.escape(v)
			}
		}
	case *ast.CompositeLit:
		c.inPlaceLiteral(info, n)
	case *ast.ReturnStmt:
		// Returning into named results stores the values in them.
		if sig := enclosingSignature(info, stack); sig != nil && len(n.Results) == sig.Results().Len() && len(n.Results) > 0 {
			for i, r := range n.Results {
				if v := sig.Results().At(i); v.Name() != "" && sliceVar(v) && !freshSlice(info, r) {
					c.escape(v)
				}
			}
		}
	}
}

// classifySliceUse marks v as escaping unless the use e (which reads or writes v) is one that
// cannot hold on to v's header.
func (c *Compiler) classifySliceUse(info *types.Info, e ast.Expr, v *types.Var, stack []ast.Node) {
	// stack ends with e (or, for a field selector's Sel, with the selector).
	i := len(stack) - 2
	for i >= 0 {
		if _, ok := stack[i].(*ast.ParenExpr); !ok {
			break
		}
		i--
	}
	if i < 0 {
		c.escape(v)
		return
	}
	child := ast.Node(e)
	if i+1 < len(stack) {
		child = stack[i+1]
	}
	switch p := stack[i].(type) {
	case *ast.SelectorExpr:
		// The Sel identifier of a field selection (classified with the selector itself).
		if p.Sel == child {
			return
		}
		c.escape(v) // a method value or call on a named slice type receives the header
	case *ast.CallExpr:
		if id, ok := unparen(p.Fun).(*ast.Ident); ok {
			if b, ok := info.Uses[id].(*types.Builtin); ok {
				switch b.Name() {
				case "len", "cap", "clear", "copy":
					return
				case "append":
					if len(p.Args) > 0 && p.Args[0] == child {
						return
					}
					if p.Ellipsis.IsValid() && p.Args[len(p.Args)-1] == child {
						return
					}
				}
			}
		}
		c.escape(v)
	case *ast.IndexExpr:
		if p.X != child {
			c.escape(v)
		}
	case *ast.SliceExpr:
		if p.X != child {
			c.escape(v)
		}
	case *ast.RangeStmt:
		if p.X != child {
			c.escape(v)
		}
	case *ast.BinaryExpr:
		other := p.Y
		if p.Y == child {
			other = p.X
		}
		if (p.Op != token.EQL && p.Op != token.NEQ) || !info.Types[other].IsNil() {
			c.escape(v)
		}
	case *ast.AssignStmt:
		for j, l := range p.Lhs {
			if l == child {
				if (p.Tok != token.ASSIGN && p.Tok != token.DEFINE) || len(p.Lhs) != len(p.Rhs) || !freshSlice(info, p.Rhs[j]) {
					c.escape(v)
				}
				return
			}
		}
		c.escape(v)
	case *ast.ValueSpec:
		for j, name := range p.Names {
			if name == child {
				if len(p.Values) != 0 && (len(p.Values) != len(p.Names) || !freshSlice(info, p.Values[j])) {
					c.escape(v)
				}
				return
			}
		}
		c.escape(v)
	case *ast.ReturnStmt:
		// A local's header may go to the caller: the function is done with it.
		if v.IsField() {
			c.escape(v)
		}
	default:
		c.escape(v)
	}
}

// inPlaceLiteral checks values stored into slice fields by a struct literal.
func (c *Compiler) inPlaceLiteral(info *types.Info, cl *ast.CompositeLit) {
	t := info.TypeOf(cl)
	if t == nil {
		return
	}
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return
	}
	for i, el := range cl.Elts {
		var field *types.Var
		value := el
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok {
				field, _ = info.Uses[id].(*types.Var)
			}
			value = kv.Value
		} else if i < st.NumFields() {
			field = st.Field(i)
		}
		if sliceVar(field) && !freshSlice(info, value) {
			c.escape(field)
		}
	}
}

// freshSlice reports expressions that make a new slice header nothing else holds.
func freshSlice(info *types.Info, e ast.Expr) bool {
	e = unparen(e)
	if info.Types[e].IsNil() {
		return true
	}
	switch e := e.(type) {
	case *ast.SliceExpr, *ast.CompositeLit:
		return true
	case *ast.CallExpr:
		if id, ok := unparen(e.Fun).(*ast.Ident); ok {
			if b, ok := info.Uses[id].(*types.Builtin); ok {
				return b.Name() == "append" || b.Name() == "make"
			}
		}
		// []byte(s) and []rune(s) copy a string.
		if tv, ok := info.Types[e.Fun]; ok && tv.IsType() && len(e.Args) == 1 {
			if b, ok := info.TypeOf(e.Args[0]).Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
				return true
			}
		}
	}
	return false
}

// selfSliceUpdate reports `v = append(v, ...)` and `v = v[i:j]` for a slice variable whose
// header may be updated in place. v must be a plain variable or field chain, so evaluating it
// again yields the same header.
func (fc *funcCtx) selfSliceUpdate(lhs, rhs ast.Expr) bool {
	var v *types.Var
	switch l := unparen(lhs).(type) {
	case *ast.Ident:
		v, _ = fc.info().Uses[l].(*types.Var)
	case *ast.SelectorExpr:
		if sel := fc.info().Selections[l]; sel != nil && sel.Kind() == types.FieldVal {
			v, _ = sel.Obj().(*types.Var)
		}
	}
	if !fc.e.c.inPlaceSlice(v) || !plainChain(fc.info(), lhs) {
		return false
	}
	var target ast.Expr
	switch r := unparen(rhs).(type) {
	case *ast.CallExpr:
		if id, ok := unparen(r.Fun).(*ast.Ident); ok && len(r.Args) > 0 {
			if b, ok := fc.info().Uses[id].(*types.Builtin); ok && b.Name() == "append" {
				target = r.Args[0]
			}
		}
	case *ast.SliceExpr:
		target = r.X
	}
	return target != nil && types.ExprString(unparen(target)) == types.ExprString(unparen(lhs))
}

// plainChain reports identifiers and field selections through them (x, x.f, x.f.g).
func plainChain(info *types.Info, e ast.Expr) bool {
	switch e := unparen(e).(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		sel := info.Selections[e]
		return sel != nil && sel.Kind() == types.FieldVal && plainChain(info, e.X)
	}
	return false
}

// inPlaceForm switches an append or reslice call to its in-place runtime form.
func inPlaceForm(code string) string {
	for _, f := range [][2]string{
		{"append1(", "rt.push1("},
		{"append(", "rt.pushn("},
		{"rt.appendT(", "rt.pushT("},
		{"rt.appendslice(", "rt.pushslice("},
		{"rt.appendstr(", "rt.pushstr("},
		{"slice(", "rt.reslice("},
		{"rt.slice3(", "rt.reslice3("},
	} {
		if strings.HasPrefix(code, f[0]) && wrappedInParens(code[len(f[0])-1:]) {
			return f[1] + code[len(f[0]):]
		}
	}
	return code
}

// isResultList reports whether fl is the result list of the function type that holds it.
func isResultList(fl *ast.FieldList, stack []ast.Node) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		if ft, ok := stack[i].(*ast.FuncType); ok {
			return ft.Results == fl
		}
	}
	return false
}

// enclosingSignature returns the signature of the innermost function around the stack top.
func enclosingSignature(info *types.Info, stack []ast.Node) *types.Signature {
	for i := len(stack) - 1; i >= 0; i-- {
		switch f := stack[i].(type) {
		case *ast.FuncLit:
			sig, _ := info.TypeOf(f).(*types.Signature)
			return sig
		case *ast.FuncDecl:
			if obj, ok := info.Defs[f.Name].(*types.Func); ok {
				return obj.Type().(*types.Signature)
			}
			return nil
		}
	}
	return nil
}
