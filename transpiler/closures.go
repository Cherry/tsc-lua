package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"
)

// Closure conversion. LuaJIT's JIT cannot compile closure creation (FNEW) or the closing of
// upvalues that every return of a function containing a closure performs (UCLO), so Go
// function literals that capture variables are emitted as callable tables instead: a table of
// the captured values whose metatable's __call is a module-level function taking that table
// first. A captured variable that is assigned after its declaration is boxed, so the table
// holds the box and both sides see assignments. The bodies of range-over-func loops, which are
// yield functions, are converted the same way.

// analyzeClosures records the function literals and range-over-func bodies to convert with the
// variables each captures, and boxes the captured variables that need it.
func (c *Compiler) analyzeClosures() {
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		info := p.TypesInfo
		for _, f := range p.Syntax {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				generic := isGenericDecl(info, fd)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.FuncLit:
						caps := capturedVars(info, n.Body, n.Pos(), n.End())
						// In generic code even a literal that captures no variable uses the type descriptors.
						if (len(caps) > 0 || generic) && c.convertible(caps) {
							c.closureCaps[n] = caps
							c.boxCaptured(caps)
						}
					case *ast.RangeStmt:
						if _, ok := coreType(info.TypeOf(n.X)).(*types.Signature); !ok || backwardSliceArg(info, n.X) != nil {
							return true
						}
						caps := capturedVars(info, n.Body, n.Pos(), n.End())
						if !c.convertible(caps) || hasDeferOrGoto(n.Body) {
							return true
						}
						c.rangeCaps[n] = caps
						c.boxCaptured(caps)
						for _, v := range caps {
							c.captured[v] = true
						}
					}
					return true
				})
			}
		}
	}
}

// convertible reports whether a callable table can hold the captured variables: assigning a
// struct or array variable rebinds its table, which a boxed copy would not follow.
func (c *Compiler) convertible(caps []*types.Var) bool {
	for _, v := range caps {
		if c.assignedVars[v] && isAggregate(v.Type()) {
			return false
		}
	}
	return true
}

func (c *Compiler) boxCaptured(caps []*types.Var) {
	for _, v := range caps {
		if c.assignedVars[v] {
			c.boxed[v] = true
		}
	}
}

// hasDeferOrGoto reports statements of a range-over-func body that act on the enclosing
// function's frame (outside nested function literals), which a converted body cannot reach.
func hasDeferOrGoto(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.DeferStmt:
			found = true
		case *ast.BranchStmt:
			found = found || n.Tok == token.GOTO
		}
		return !found
	})
	return found
}

// isGenericDecl reports functions with type parameters, of their own or of their receiver,
// whose literals capture the type descriptors as well (see funcLit).
func isGenericDecl(info *types.Info, fd *ast.FuncDecl) bool {
	fn, ok := info.Defs[fd.Name].(*types.Func)
	if !ok {
		return true
	}
	sig := fn.Type().(*types.Signature)
	return sig.TypeParams().Len() > 0 || sig.RecvTypeParams().Len() > 0
}

// capturedVars lists, in order of first use, the local variables that body (or any literal
// nested in it) uses and that are declared outside [lo, hi).
func capturedVars(info *types.Info, body *ast.BlockStmt, lo, hi token.Pos) []*types.Var {
	var caps []*types.Var
	seen := map[*types.Var]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		v, ok := info.Uses[id].(*types.Var)
		if !ok || v.IsField() || v.Pkg() == nil || v.Parent() == v.Pkg().Scope() {
			return true
		}
		if (v.Pos() < lo || v.Pos() >= hi) && !seen[v] {
			seen[v] = true
			caps = append(caps, v)
		}
		return true
	})
	return caps
}

// noteAssigned records a variable assigned other than by its declaration.
func (a *analyzer) noteAssigned(lhs ast.Expr, define bool) {
	id, ok := unparen(lhs).(*ast.Ident)
	if !ok {
		return
	}
	// := assigns to the variables it does not declare.
	if define && a.info.Defs[id] != nil {
		return
	}
	if v, ok := a.info.Uses[id].(*types.Var); ok {
		a.c.assignedVars[v] = true
	}
}

// noteNamedResults marks named results as assigned: return statements assign them.
func (a *analyzer) noteNamedResults(ft *ast.FuncType) {
	if ft.Results == nil {
		return
	}
	for _, f := range ft.Results.List {
		for _, n := range f.Names {
			if v, ok := a.info.Defs[n].(*types.Var); ok {
				a.c.assignedVars[v] = true
			}
		}
	}
}

// rawRef is the Lua expression for a local variable's storage: its box when boxed, otherwise
// its value. Inside a converted literal or range body, captured variables live in its table.
func (fc *funcCtx) rawRef(v *types.Var) string {
	for f := fc; f != nil; f = f.parent {
		if r, ok := f.captureRefs[v]; ok {
			return r
		}
	}
	return fc.varName(v)
}

// localTypeRef matches the names of function-local type descriptors (localTypeName).
var localTypeRef = regexp.MustCompile(`\b__lt_[A-Za-z0-9_]+`)

// captureLocalTypes makes the function-local type descriptors that a hoisted function's code
// uses but does not declare (locals of the enclosing function) captures too: they are read from
// slots of its table, after the first used slots. It returns the code and the descriptors'
// names, to be stored in those slots.
func captureLocalTypes(code, table string, used int) (string, []string) {
	var names, slots []string
	seen := map[string]bool{}
	for _, name := range localTypeRef.FindAllString(code, -1) {
		if seen[name] || strings.Contains(code, "local "+name+" = ") {
			continue
		}
		seen[name] = true
		names = append(names, name)
		slots = append(slots, fmt.Sprintf("%s[%d]", table, used+len(names)))
	}
	if len(names) == 0 {
		return code, nil
	}
	header, rest, _ := strings.Cut(code, "\n")
	return header + "\n\tlocal " + strings.Join(names, ", ") + " = " + strings.Join(slots, ", ") + "\n" + rest, names
}

// usesLocalType reports code that refers to a function-local type descriptor it does not declare.
func usesLocalType(code string) bool {
	_, names := captureLocalTypes(code, "", 0)
	return len(names) > 0
}
