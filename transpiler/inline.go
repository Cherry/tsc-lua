package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"strings"
)

// Trivial functions (a single return of field selections, parameters, constants and
// operators) are inlined at static call sites: Lua function calls are expensive relative
// to Go, and the compiler is full of accessors like `func (s *Symbol) Name() string`.

type inlineTemplate struct {
	expr   string // Lua expression with placeholders \x01<i>\x01 for parameters (receiver first)
	nparam int
	uses   []int // occurrences of each placeholder
	// typeArgs leading parameters are a generic function's type descriptors: pure, and unused
	// by any template (templates may not name them), so their arguments may be dropped.
	typeArgs int
	pkg      *types.Package
	local    bool        // names module tables or calls: inline only in pkg
	from     *pkgEmitter // the emitter that built the template, whose TY slots and aliases it uses
	calls    bool        // calls something with possible effects: arguments must be simple
}

// maxTemplate bounds the length of an inlined expression, to limit code growth.
const maxTemplate = 400

func placeholder(i int) string { return fmt.Sprintf("__INL%d_", i) }

// buildInlineTemplates computes templates for every eligible function, iterating so that
// trivial functions calling other trivial functions are inlined transitively.
func (c *Compiler) buildInlineTemplates() {
	c.inlines = map[*types.Func]*inlineTemplate{}
	type candidate struct {
		pkgEm *pkgEmitter
		decl  *ast.FuncDecl
		fn    *types.Func
	}
	var candidates []candidate
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		// Inlining would bypass a patched function, so skip what the patch replaces (or the
		// whole package when the patch does not say).
		var overrides map[string]bool
		if c.hasPatch(p.PkgPath) {
			names, declared := c.patchOverrides(p.PkgPath)
			if !declared {
				continue
			}
			overrides = names
		}
		e := &pkgEmitter{c: c, pkg: p, info: p.TypesInfo, fset: p.Fset, imports: map[string]string{}, tyIndex: map[string]int{}}
		for _, f := range p.Syntax {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				fn, ok := p.TypesInfo.Defs[fd.Name].(*types.Func)
				if !ok || overrides[funcKeyOf(fn)] {
					continue
				}
				sig := fn.Type().(*types.Signature)
				if sig.RecvTypeParams().Len() > 0 || sig.Variadic() || sig.Results().Len() != 1 {
					continue
				}
				if _, ok := inlineBody(p.TypesInfo, fd.Body, sig.Results().At(0).Type()); !ok {
					continue
				}
				candidates = append(candidates, candidate{e, fd, fn})
			}
		}
	}
	for round := 0; round < 3; round++ {
		changed := false
		for _, cand := range candidates {
			t := c.templateFor(cand.pkgEm, cand.decl, cand.fn)
			if t == nil {
				continue
			}
			if old := c.inlines[cand.fn]; old == nil || old.expr != t.expr {
				c.inlines[cand.fn] = t
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// guardedReturn is `if cond { return result }` or `if cond { panic("message") }`; a nil cond is
// the final, unconditional return.
type guardedReturn struct {
	cond, result ast.Expr
	panicMsg     string // constant message of a guard that panics
}

// constPanic returns the message of a `panic("constant string")` statement.
func constPanic(info *types.Info, st ast.Stmt) (string, bool) {
	es, ok := st.(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	id, ok := call.Fun.(*ast.Ident)
	if !ok {
		return "", false
	}
	if b, isBuiltin := info.Uses[id].(*types.Builtin); !isBuiltin || b.Name() != "panic" {
		return "", false
	}
	tv, ok := info.Types[call.Args[0]]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	msg := constant.StringVal(tv.Value)
	return msg, plainStringLiteral.MatchString("'" + msg + "'")
}

// inlineBody accepts a single return, or early returns guarded by conditions before a final
// return, when the guarded results cannot be Lua-falsy (so `cond and x or rest` is exact) or are
// boolean literals.
func inlineBody(info *types.Info, body *ast.BlockStmt, result types.Type) ([]guardedReturn, bool) {
	var steps []guardedReturn
	for i, st := range body.List {
		last := i == len(body.List)-1
		if last {
			ret, ok := st.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 || !inlineableExpr(info, ret.Results[0]) {
				return nil, false
			}
			return append(steps, guardedReturn{result: ret.Results[0]}), true
		}
		ifs, ok := st.(*ast.IfStmt)
		if !ok || ifs.Init != nil || ifs.Else != nil || len(ifs.Body.List) != 1 || !inlineableExpr(info, ifs.Cond) {
			return nil, false
		}
		// A guard that panics never yields a value, so `cond and rt.panic(msg) or rest` is exact.
		if msg, isPanic := constPanic(info, ifs.Body.List[0]); isPanic {
			steps = append(steps, guardedReturn{cond: ifs.Cond, panicMsg: msg})
			continue
		}
		ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 || !inlineableExpr(info, ret.Results[0]) {
			return nil, false
		}
		if !neverFalsy(result) && boolLiteral(info, ret.Results[0]) == nil {
			return nil, false
		}
		steps = append(steps, guardedReturn{cond: ifs.Cond, result: ret.Results[0]})
	}
	return nil, false
}

// neverFalsy reports Go types whose Lua values are never nil or false.
func neverFalsy(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&(types.IsNumeric|types.IsString) != 0
}

// boolLiteral returns the value of a true/false constant expression, or nil.
func boolLiteral(info *types.Info, e ast.Expr) *bool {
	tv, ok := info.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.Bool {
		return nil
	}
	v := constant.BoolVal(tv.Value)
	return &v
}

// inlineableExpr reports expressions built only from identifiers, field selections, method
// calls on those (which may themselves inline), constants and pure operators.
func inlineableExpr(info *types.Info, e ast.Expr) bool {
	ok := true
	ast.Inspect(e, func(n ast.Node) bool {
		if !ok {
			return false
		}
		// Constant expressions (such as -1) are emitted as literals.
		if expr, isExpr := n.(ast.Expr); isExpr {
			if tv, known := info.Types[expr]; known && tv.Value != nil {
				return false
			}
		}
		switch n := n.(type) {
		case nil, *ast.Ident, *ast.BasicLit, *ast.ParenExpr:
		case *ast.SelectorExpr:
			if sel := info.Selections[n]; sel != nil && sel.Kind() == types.MethodExpr {
				ok = false
			}
		case *ast.BinaryExpr:
			switch n.Op {
			case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ, token.LAND, token.LOR, token.AND, token.OR,
				token.ADD, token.SUB, token.MUL, token.QUO, token.REM, token.XOR, token.SHL, token.SHR, token.AND_NOT:
			default:
				ok = false
			}
		case *ast.UnaryExpr:
			if n.Op != token.NOT && n.Op != token.SUB && n.Op != token.XOR {
				ok = false
			}
		case *ast.CallExpr:
			// Conversions are pure; any that need the runtime are rejected by the template checks.
			// Only the argument needs checking, not the type expression.
			if tv, isType := info.Types[n.Fun]; isType && tv.IsType() && len(n.Args) == 1 {
				ok = inlineableExpr(info, n.Args[0])
				return false
			}
			// Calls are allowed (the template then applies only within its package, with simple
			// arguments), except builtins other than len and cap, and calls of function literals.
			if id, isIdent := unparen(n.Fun).(*ast.Ident); isIdent {
				if b, isBuiltin := info.Uses[id].(*types.Builtin); isBuiltin && b.Name() != "len" && b.Name() != "cap" {
					ok = false
					break
				}
			}
			if _, isLit := unparen(n.Fun).(*ast.FuncLit); isLit || n.Ellipsis.IsValid() {
				ok = false
			}
		default:
			ok = false
		}
		return ok
	})
	return ok
}

func (c *Compiler) templateFor(e *pkgEmitter, fd *ast.FuncDecl, fn *types.Func) *inlineTemplate {
	sig := fn.Type().(*types.Signature)
	fc := e.newFuncCtx(nil, sig)
	fc.paramSubst = map[*types.Var]string{}
	// A generic function's type descriptors come first in its calls; placeholders keep the
	// argument positions aligned.
	n := sig.TypeParams().Len()
	var params []*types.Var
	if sig.Recv() != nil {
		params = append(params, sig.Recv())
	}
	for i := 0; i < sig.Params().Len(); i++ {
		params = append(params, sig.Params().At(i))
	}
	for _, v := range params {
		if c.boxed[v] || c.mutatedVars[v] || (v.Name() == "" || v.Name() == "_") {
			if v.Name() == "" || v.Name() == "_" {
				fc.paramSubst[v] = placeholder(n)
				n++
				continue
			}
			return nil
		}
		fc.paramSubst[v] = placeholder(n)
		n++
	}
	resultType := sig.Results().At(0).Type()
	steps, _ := inlineBody(e.info, fd.Body, resultType)
	// Build from the final return outwards: `cond and x or rest`, or for boolean literals
	// `cond or rest` / `not cond and rest`.
	code := fc.convertExpr(steps[len(steps)-1].result, resultType)
	for i := len(steps) - 2; i >= 0; i-- {
		cond := inlineOperand(fc.expr(steps[i].cond))
		rest := inlineOperand(code)
		if steps[i].result == nil {
			code = cond + " and rt.panic(" + luaString(steps[i].panicMsg) + ") or " + rest
		} else if b := boolLiteral(e.info, steps[i].result); b != nil && !neverFalsy(resultType) {
			if *b {
				code = cond + " or " + rest
			} else {
				code = "not " + cond + " and " + rest
			}
		} else {
			code = cond + " and " + inlineOperand(fc.convertExpr(steps[i].result, resultType)) + " or " + rest
		}
	}
	// A template may call functions and name module tables (P, TY, import aliases), but then it
	// only applies inside its own package, where those names mean the same thing, and only
	// with simple arguments, so no argument's evaluation moves past a call in the template.
	scan := plainStringLiteral.ReplaceAllString(code, "''")
	// Guards may raise a panic with a constant message; that call is safe to repeat inline.
	scan = strings.ReplaceAll(scan, "rt.panic('')", "PANIC")
	// Type descriptors (__tp_*) and temporaries computed before the expression (fc.pre) belong to
	// the function itself and cannot move to a call site.
	if len(code) > maxTemplate || strings.ContainsAny(strings.ReplaceAll(scan, "''", ""), "{'\"") ||
		strings.Contains(scan, "__tp_") || len(fc.pre) > 0 {
		return nil
	}
	t := &inlineTemplate{expr: code, nparam: n, uses: make([]int, n), typeArgs: sig.TypeParams().Len(), pkg: fn.Pkg(), from: e}
	// sbyte and ssub are every module's aliases of string.byte and string.sub, which are pure.
	pure := strings.NewReplacer("sbyte(", "SBYTE ", "ssub(", "SSUB ").Replace(scan)
	t.calls = hasCall(pure)
	t.local = t.calls || strings.Contains(scan, "TY") || strings.Contains(scan, "P.") || strings.Contains(stripPlaceholders(scan, n), "__")
	for i := 0; i < n; i++ {
		t.uses[i] = strings.Count(code, placeholder(i))
	}
	return t
}

// inlineCall substitutes arguments into a template; it declines when an argument with side
// effects or cost would be evaluated more than once or not at all.
func (c *Compiler) inlineCall(fn *types.Func, args []string, caller *funcCtx) (string, bool) {
	if fn.Origin() != nil {
		fn = fn.Origin()
	}
	t := c.inlines[fn]
	if t == nil || len(args) != t.nparam || (t.local && caller.e.pkg.Types != t.pkg) {
		return "", false
	}
	complex := 0
	for i, a := range args {
		if i < t.typeArgs {
			continue
		}
		simple := isSimple(a) || isLiteral(a) || a == "nil"
		if !simple {
			if t.calls {
				return "", false
			}
			complex++
			if t.uses[i] != 1 {
				return "", false
			}
		}
	}
	if complex > 1 {
		return "", false
	}
	// Substitute all placeholders at once: an argument may itself be a placeholder of the
	// template being built (when inlining into another template), so one at a time would
	// rewrite earlier substitutions.
	var pairs []string
	for i, a := range args {
		pairs = append(pairs, placeholder(i), prefix(a))
	}
	body := t.expr
	if t.local {
		body = t.from.remapInto(body, caller)
	}
	code := strings.NewReplacer(pairs...).Replace(body)
	if !isPrefixExp(code) && !isLiteral(code) {
		code = "(" + code + ")"
	}
	return code, true
}

// hasCall reports whether Lua code contains a call: "(" after an identifier or closing
// bracket, other than the keywords not/and/or.
func hasCall(code string) bool {
	isWord := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	for i := 0; i < len(code); i++ {
		if code[i] != '(' {
			continue
		}
		j := i - 1
		for j >= 0 && code[j] == ' ' {
			j--
		}
		if j < 0 {
			continue
		}
		if code[j] == ')' || code[j] == ']' {
			return true
		}
		if isWord(code[j]) {
			k := j
			for k >= 0 && isWord(code[k]) {
				k--
			}
			word := code[k+1 : j+1]
			if word != "not" && word != "and" && word != "or" {
				return true
			}
		}
	}
	return false
}

func stripPlaceholders(code string, n int) string {
	for i := 0; i < n; i++ {
		code = strings.ReplaceAll(code, placeholder(i), "p")
	}
	return code
}

// plainStringLiteral matches Lua string literals that the template checks can skip: no escapes,
// quotes, brackets or underscores that could look like calls, indexing or placeholders.
var plainStringLiteral = regexp.MustCompile(`'[A-Za-z0-9 .,:;!?@#$%^&*+=<>/|~-]*'`)

// inlineOperand parenthesizes code for use as an and/or operand.
func inlineOperand(code string) string {
	if isLiteral(code) {
		return code
	}
	return prefix(code)
}

// moduleName matches the per-module names a template can contain: TY slots and import aliases.
var moduleName = regexp.MustCompile(`TY\[(\d+)\]|\b__[A-Za-z][A-Za-z0-9_]*\b`)

// remapInto rewrites a template built by e for use in the module of fc: the template emitter
// numbers TY slots and names import aliases on its own, so each is looked up again there.
func (e *pkgEmitter) remapInto(code string, fc *funcCtx) string {
	aliasPkg := map[string]*types.Package{}
	for path, alias := range e.imports {
		aliasPkg[alias] = e.importPkgs[path]
	}
	return moduleName.ReplaceAllStringFunc(code, func(m string) string {
		if strings.HasPrefix(m, "TY[") {
			var slot int
			fmt.Sscanf(m, "TY[%d]", &slot)
			return fc.e.typeRef(fc, e.tyTypes[slot-1])
		}
		if pkg := aliasPkg[m]; pkg != nil {
			return fc.e.importAlias(pkg)
		}
		return m
	})
}
