package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Struct layout: embedded and struct-typed fields are flattened into the containing table when
// their keys do not collide, so a pointer to an embedded struct is the same table.
//
// Structs of the compiler's own packages are "indexed": their fields live in the table's array
// part, which takes 8 bytes a field where a hash entry takes 24 and rounds up to a power of two.
// Each indexed struct type has its own slot range, the same wherever it is stored, so its
// methods find their fields whichever table it is flattened into; structs sharing a table get
// ranges that do not overlap (numberSlots). Structs Lua code reads by name use string keys
// ("named").

type structLayout struct {
	fields []fieldInfo // indexed by field index
	keys   []layoutKey // all leaf storage keys (flattened)
}

type fieldInfo struct {
	key  string // storage key when not flattened, for named fields
	slot int    // storage slot when not flattened, for indexed fields
	flat bool   // embedded struct whose fields live in the same table
	skip bool   // blank field, no storage
}

type layoutKey struct {
	key  string
	slot int
	typ  types.Type
}

// structShape is what a struct's table holds, before slots are numbered.
type structShape struct {
	flat    []bool   // by field index: fields flattened into the table
	own     int      // fields stored under this struct's own keys or slots
	keys    []string // named keys in the table
	members []string // modes of the indexed structs with fields of their own in the table
}

// access renders the field's storage location in the table expr.
func (i fieldInfo) access(expr string) string {
	if i.slot > 0 {
		return prefix(expr) + "[" + strconv.Itoa(i.slot) + "]"
	}
	return prefix(expr) + "." + i.key
}

// luaKey renders the field's storage key as a Lua value.
func (i fieldInfo) luaKey() string {
	if i.slot > 0 {
		return strconv.Itoa(i.slot)
	}
	return luaString(i.key)
}

// structMode returns the key identical struct types share: values convert between them by copying.
// Fields of generic instances use their origin's types, so instances share their origin's layout.
func (c *Compiler) structMode(s *types.Struct) string {
	if k, ok := c.structModes[s]; ok {
		return k
	}
	var b strings.Builder
	qualifier := func(p *types.Package) string { return p.Path() }
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		b.WriteString(f.Name())
		if f.Embedded() {
			b.WriteByte('*')
		}
		b.WriteByte(' ')
		b.WriteString(types.TypeString(f.Origin().Type(), qualifier))
		b.WriteByte(';')
	}
	k := b.String()
	c.structModes[s] = k
	return k
}

func (c *Compiler) indexed(s *types.Struct) bool {
	_, ok := c.slotOffsets[c.structMode(s)]
	return ok
}

// shape decides which struct-typed fields are flattened: those whose named keys and indexed
// structs are not in the table yet (a struct type twice in one table would need its slots twice).
func (c *Compiler) shape(s *types.Struct) *structShape {
	if sh, ok := c.shapes[s]; ok {
		return sh
	}
	sh := &structShape{flat: make([]bool, s.NumFields())}
	c.shapes[s] = sh
	indexed := c.indexed(s)
	used := map[string]bool{}
	members := map[string]bool{}
	if !indexed {
		// Depth-0 names win over anything promoted from embedded structs.
		for i := 0; i < s.NumFields(); i++ {
			f := s.Field(i)
			if f.Name() != "_" && !c.flattenable(f) {
				used[fieldKey(f.Name())] = true
				sh.keys = append(sh.keys, fieldKey(f.Name()))
			}
		}
	}
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		if f.Name() == "_" {
			continue
		}
		if c.flattenable(f) {
			sub := c.shape(f.Type().Underlying().(*types.Struct))
			ok := indexed || !used[fieldKey(f.Name())]
			for _, k := range sub.keys {
				ok = ok && !used[k]
			}
			for _, m := range sub.members {
				ok = ok && !members[m]
			}
			if ok {
				sh.flat[i] = true
				for _, k := range sub.keys {
					used[k] = true
				}
				sh.keys = append(sh.keys, sub.keys...)
				for _, m := range sub.members {
					members[m] = true
				}
				sh.members = append(sh.members, sub.members...)
				continue
			}
			if !indexed {
				used[fieldKey(f.Name())] = true
				sh.keys = append(sh.keys, fieldKey(f.Name()))
			}
		}
		sh.own++
	}
	if indexed && sh.own > 0 {
		sh.members = append(sh.members, c.structMode(s))
	}
	return sh
}

func (c *Compiler) layout(s *types.Struct) *structLayout {
	if l, ok := c.layouts[s]; ok {
		return l
	}
	l := &structLayout{fields: make([]fieldInfo, s.NumFields())}
	c.layouts[s] = l
	sh := c.shape(s)
	indexed := c.indexed(s)
	slot := c.slotOffsets[c.structMode(s)]
	used := map[int]bool{}
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		switch {
		case f.Name() == "_":
			l.fields[i] = fieldInfo{skip: true}
		case sh.flat[i]:
			l.fields[i] = fieldInfo{flat: true}
			for _, k := range c.layout(f.Type().Underlying().(*types.Struct)).keys {
				if k.slot > 0 && used[k.slot] {
					fatalf("struct slot %d used twice in %s", k.slot, s)
				}
				used[k.slot] = true
				l.keys = append(l.keys, k)
			}
		case indexed:
			slot++
			if used[slot] {
				fatalf("struct slot %d used twice in %s", slot, s)
			}
			used[slot] = true
			l.fields[i] = fieldInfo{slot: slot}
			l.keys = append(l.keys, layoutKey{slot: slot, typ: f.Type()})
		default:
			key := fieldKey(f.Name())
			l.fields[i] = fieldInfo{key: key}
			l.keys = append(l.keys, layoutKey{key: key, typ: f.Type()})
		}
	}
	return l
}

// analyzeLayouts decides which struct types are indexed and numbers their slots. Every struct
// reachable from the transpiled packages is visited; those of other packages keep string keys,
// because native Lua code reads their fields by name (patches look slots up with rt.fieldkey).
func (c *Compiler) analyzeLayouts() {
	seen := map[types.Type]bool{}
	var structs []*types.Struct
	named := map[string]bool{}
	var visit func(t types.Type, own bool)
	visit = func(t types.Type, own bool) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		switch t := t.(type) {
		case *types.Alias:
			visit(types.Unalias(t), own)
		case *types.Named:
			pkg := t.Obj().Pkg()
			isOwn := pkg != nil && c.ownPackage(pkg.Path())
			if st, ok := t.Underlying().(*types.Struct); ok && !isOwn {
				named[c.structMode(st)] = true
			}
			visit(t.Underlying(), isOwn)
			for i := 0; i < t.TypeArgs().Len(); i++ {
				visit(t.TypeArgs().At(i), own)
			}
		case *types.Struct:
			structs = append(structs, t)
			if !own {
				named[c.structMode(t)] = true
			}
			for i := 0; i < t.NumFields(); i++ {
				visit(t.Field(i).Type(), own)
			}
		case *types.Pointer:
			visit(t.Elem(), own)
		case *types.Slice:
			visit(t.Elem(), own)
		case *types.Array:
			visit(t.Elem(), own)
		case *types.Map:
			visit(t.Key(), own)
			visit(t.Elem(), own)
		case *types.Chan:
			visit(t.Elem(), own)
		case *types.Signature:
			for _, tuple := range []*types.Tuple{t.Params(), t.Results()} {
				for i := 0; i < tuple.Len(); i++ {
					visit(tuple.At(i).Type(), own)
				}
			}
		}
	}
	for _, p := range c.pkgs {
		if c.isFullNative(p.PkgPath) {
			continue
		}
		own := c.ownPackage(p.PkgPath)
		for _, obj := range p.TypesInfo.Defs {
			if obj != nil {
				visit(obj.Type(), own)
			}
		}
		for _, tv := range p.TypesInfo.Types {
			visit(tv.Type, own)
		}
		for _, inst := range p.TypesInfo.Instances {
			visit(inst.Type, own)
		}
	}
	// Indexed modes get a placeholder offset first: shapes depend on which structs are indexed.
	for _, s := range structs {
		if k := c.structMode(s); !named[k] {
			c.slotOffsets[k] = 0
		}
	}
	c.numberSlots(structs)
}

// numberSlots gives each indexed struct type a slot range that overlaps none of the structs it
// shares a table with. Types in the most tables (such as Node, in every AST node) go first so
// they get the lowest slots, then smaller ones (a struct stored on its own wastes the slots before
// its range), and each takes the first gap that fits, which keeps tables dense.
func (c *Compiler) numberSlots(structs []*types.Struct) {
	size := map[string]int{}
	tables := map[string]int{}
	conflicts := map[string]map[string]bool{}
	for _, s := range structs {
		sh := c.shape(s)
		if c.indexed(s) {
			size[c.structMode(s)] = sh.own
		}
		for _, a := range sh.members {
			tables[a]++
			if conflicts[a] == nil {
				conflicts[a] = map[string]bool{}
			}
			for _, b := range sh.members {
				if a != b {
					conflicts[a][b] = true
				}
			}
		}
	}
	modes := make([]string, 0, len(size))
	for m := range size {
		modes = append(modes, m)
	}
	sort.Slice(modes, func(i, j int) bool {
		a, b := modes[i], modes[j]
		if tables[a] != tables[b] {
			return tables[a] > tables[b]
		}
		if size[a] != size[b] {
			return size[a] < size[b]
		}
		return a < b
	})
	placed := map[string]bool{}
	for _, m := range modes {
		var taken [][2]int
		for other := range conflicts[m] {
			if placed[other] {
				taken = append(taken, [2]int{c.slotOffsets[other], c.slotOffsets[other] + size[other]})
			}
		}
		sort.Slice(taken, func(i, j int) bool { return taken[i][0] < taken[j][0] })
		offset := 0
		for _, r := range taken {
			if offset+size[m] <= r[0] {
				break
			}
			offset = max(offset, r[1])
		}
		c.slotOffsets[m] = offset
		placed[m] = true
	}
}

// ownPackage reports packages of the module being compiled, which native modules (other than
// its patches) never read by name.
func (c *Compiler) ownPackage(pkgPath string) bool {
	p := c.byPath[pkgPath]
	return p != nil && p.Module != nil && c.ownModules[p.Module.Path]
}

// isNativeType reports named types whose representation is defined by a native Lua module.
func (c *Compiler) isNativeType(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	return c.isFullNative(n.Obj().Pkg().Path())
}

// flattenable reports struct-typed fields whose leaves can live in the containing table. Named
// fields qualify as well as embedded ones: every AST node's Loc would otherwise be its own
// table, which dominates memory. Type parameter fields of generic instances do not, so
// instances keep their origin's layout.
func (c *Compiler) flattenable(f *types.Var) bool {
	if isTypeParam(f.Origin().Type()) {
		return false
	}
	// Native types have their own representation, and atomic integer fields hold the value.
	if c.isNativeType(f.Type()) || atomicValueType(f.Type()) != nil {
		return false
	}
	_, ok := f.Type().Underlying().(*types.Struct)
	return ok
}

// slotRef splits a reference to an indexed field into its table and slot.
var slotRef = regexp.MustCompile(`^(.+)\[(\d+)\]$`)

// atomicValueType returns the value type of sync/atomic's integer types, or nil. Struct fields
// of these types hold the plain value, so they need no key of their own (which would collide
// between two atomics in one table), and their methods are compiled inline.
func atomicValueType(t types.Type) types.Type {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != "sync/atomic" {
		return nil
	}
	switch n.Obj().Name() {
	case "Int32", "Int64", "Uint32", "Uint64", "Uintptr":
	default:
		return nil
	}
	st := n.Underlying().(*types.Struct)
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == "v" {
			return st.Field(i).Type()
		}
	}
	return nil
}

// atomicField resolves a selector of an atomic integer struct field to the table holding it
// and the field's storage.
func (fc *funcCtx) atomicField(x ast.Expr) (tab string, info fieldInfo, ok bool) {
	se, isSel := unparen(x).(*ast.SelectorExpr)
	if !isSel {
		return "", fieldInfo{}, false
	}
	sel := fc.info().Selections[se]
	if sel == nil || sel.Kind() != types.FieldVal || atomicValueType(sel.Type()) == nil {
		return "", fieldInfo{}, false
	}
	return fc.atomicFieldAt(fc.expr(se.X), fc.typeOf(se.X), sel.Index())
}

// atomicFieldAt resolves the field at the end of path from base when it is an atomic integer.
func (fc *funcCtx) atomicFieldAt(base string, t types.Type, path []int) (tab string, info fieldInfo, ok bool) {
	parent := fieldPathType(t, path[:len(path)-1])
	st := derefStruct(parent)
	if st == nil || atomicValueType(st.Field(path[len(path)-1]).Type()) == nil {
		return "", fieldInfo{}, false
	}
	info = fc.e.c.layout(st).fields[path[len(path)-1]]
	return fc.fieldPath(base, t, path[:len(path)-1]), info, true
}

// atomicCall compiles common methods of an atomic integer field; ok is false for the others,
// which are called with a pointer to the field.
func (fc *funcCtx) atomicCall(mt methodTarget, args []string) (string, bool) {
	tab, key := mt.atomicTab, mt.atomicInfo.luaKey()
	switch mt.name {
	case "Load":
		return mt.atomicInfo.access(tab), true
	case "Store":
		return fmt.Sprintf("rt.astore(%s, %s, %s)", tab, key, args[0]), true
	case "Swap":
		return fmt.Sprintf("rt.aswap(%s, %s, %s)", tab, key, args[0]), true
	case "CompareAndSwap":
		return fmt.Sprintf("rt.acas(%s, %s, %s, %s)", tab, key, args[0], args[1]), true
	case "Add":
		if repOf(mt.atomicValue) == repInt32 {
			return fmt.Sprintf("rt.aadd32(%s, %s, %s)", tab, key, args[0]), true
		}
		return fmt.Sprintf("rt.aadd(%s, %s, %s)", tab, key, args[0]), true
	}
	return "", false
}
