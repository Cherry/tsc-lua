// Named struct fields stored flattened in their parent table must keep Go value semantics.
package main

import (
	"fmt"
	"sync/atomic"
)

type textRange struct {
	pos, end int32
}

func (r textRange) Len() int32     { return r.end - r.pos }
func (r *textRange) Shift(d int32) { r.pos += d; r.end += d }

type node struct {
	Kind  int
	Loc   textRange
	id    atomic.Uint64
	Other textRange // same leaves as Loc, so it cannot be flattened too
}

type holder struct {
	saved textRange
}

func move(r *textRange) { r.Shift(100) }

func main() {
	n := &node{Kind: 1, Loc: textRange{1, 5}}
	n.Other = textRange{7, 9}

	copied := n.Loc
	h := holder{saved: n.Loc}
	list := []textRange{n.Loc}
	byLoc := map[textRange]string{n.Loc: "first"}

	// Overwrite in place: earlier copies must not change.
	n.Loc = textRange{10, 20}
	fmt.Println(copied, h.saved, list[0], n.Loc, n.Loc.Len())

	n.Loc.Shift(1)
	move(&n.Loc)
	p := &n.Loc
	p.end = 999
	fmt.Println(n.Loc, copied == textRange{1, 5}, byLoc[textRange{1, 5}], n.Other)

	n.id.Store(42)
	n.id.Add(1)
	fmt.Println(n.id.Load(), n.Kind)

	clone := *n
	clone.Loc.pos = -1
	fmt.Println(n.Loc.pos, clone.Loc.pos, clone.id.Load())
	fmt.Printf("%+v\n", n.Loc)
	literalOrder()
	atomicFields()
	sharedTables()
}

var order []string

func step(name string, v int) int {
	order = append(order, name)
	return v
}

// literalOrder checks that calls in a literal run in field order even though Loc's slots are
// stored before Kind's.
func literalOrder() {
	n := node{Kind: step("kind", 3), Loc: textRange{int32(step("pos", 4)), int32(step("end", 9))}}
	m := node{Kind: step("kind2", 5), Loc: textRange{pos: 1}}
	fmt.Println(order, n.Kind, n.Loc, m.Kind, m.Loc)
}

// counters holds several atomics, which share no storage key with each other.
type counters struct {
	atomic.Int32
	hits  atomic.Uint32
	total atomic.Int64
}

func bump(p *atomic.Int64) { p.Add(10) }

func atomicFields() {
	var c counters
	c.Store(2147483647)
	c.Add(1) // wraps
	c.hits.Add(3)
	swapped := c.hits.CompareAndSwap(3, 7)
	old := c.hits.Swap(8)
	bump(&c.total)
	p := &c.total
	p.Store(p.Load() + 1)
	add := c.total.Add
	add(100)
	copied := counters{}
	fmt.Println(c.Load(), c.hits.Load(), swapped, old, c.total.Load(), copied.hits.Load(), copied.Load())
}

// item flattens two structs; extra's fields keep their own slots inside item and standalone.
type base struct {
	kind int
	loc  textRange
}

type extra struct {
	flags, depth int
}

func (e *extra) Bump() { e.depth++ }

type item struct {
	base
	extra
	name string
}

func sharedTables() {
	it := item{base: base{kind: 1, loc: textRange{2, 3}}, extra: extra{flags: 4}, name: "a"}
	it.Bump()
	e := &it.extra
	e.Bump()
	alone := extra{flags: 9}
	alone.Bump()
	copied := it
	copied.depth = 100
	seen := map[item]bool{it: true}
	fmt.Printf("%+v %+v %v %v %v\n", it, alone, copied.depth, seen[it], seen[copied])
	// Assigning into a flattened field copies the value: later changes to either side stay apart.
	it.extra = alone
	alone.depth = 50
	it.loc = it.loc
	fmt.Println(it.extra, alone, it.loc)
}
