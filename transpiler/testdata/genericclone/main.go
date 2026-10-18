// Generic code copies values of type parameters; aggregates must be copied, everything else
// (including nil and false) passed through.
package main

import "fmt"

type point struct{ x, y int }

type quad [4]int

type box[T any] struct{ items []T }

func (b *box[T]) put(v T) { b.items = append(b.items, v) }

func copyOf[T any](v T) T {
	c := v
	return c
}

func main() {
	p := point{1, 2}
	q := copyOf(p)
	q.x = 9
	a := [3]int{1, 2, 3}
	b := copyOf(a)
	b[0] = 9
	n := quad{1, 2, 3, 4}
	m := copyOf(n)
	m[3] = 9
	fmt.Println(p, q, a, b, n, m)

	var nilSlice []int
	var nilPtr *point
	fmt.Println(copyOf(nilSlice) == nil, copyOf(nilPtr) == nil, copyOf(false), copyOf(""), copyOf(0))

	var pts box[point]
	pts.put(p)
	p.y = 7
	pts.items[0].x = 5
	fmt.Println(p, pts.items)

	var arrs box[quad]
	arrs.put(n)
	n[0] = 0
	fmt.Println(n, arrs.items)
}
