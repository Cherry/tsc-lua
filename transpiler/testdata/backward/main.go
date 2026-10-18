// Ranging over slices.Backward compiles to a reverse index loop; it must keep the semantics of
// the iterator: break, continue, return, key-only loops and per-iteration variables.
package main

import (
	"fmt"
	"slices"
)

type point struct{ x, y int }

func lastIndex(list []int, f func(int) bool) int {
	for i, v := range slices.Backward(list) {
		if f(v) {
			return i
		}
	}
	return -1
}

func main() {
	list := []int{1, 2, 3, 4, 5}
	var visited []int
	for i, v := range slices.Backward(list) {
		if v == 4 {
			continue
		}
		if v == 2 {
			break
		}
		visited = append(visited, i*10+v)
	}
	fmt.Println(visited, lastIndex(list, func(v int) bool { return v%2 == 0 }), lastIndex(nil, nil))

	var keys []int
	for i := range slices.Backward(list[1:3]) {
		keys = append(keys, i)
	}
	fmt.Println(keys)

	// Each iteration has its own variables, and values are copies.
	var funcs []func() int
	points := []point{{1, 2}, {3, 4}}
	for _, p := range slices.Backward(points) {
		p.x = 100
		funcs = append(funcs, func() int { return p.y })
	}
	fmt.Println(funcs[0](), funcs[1](), points)

	// The slice is evaluated once, before the loop.
	calls := 0
	get := func() []int {
		calls++
		return list
	}
	sum := 0
	for _, v := range slices.Backward(get()) {
		sum += v
	}
	fmt.Println(sum, calls)
}
