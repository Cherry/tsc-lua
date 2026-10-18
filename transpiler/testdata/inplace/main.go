// Self-updates of slice headers (s = append(s, ...), s = s[i:j]) that the transpiler may do in
// place, next to every way a header can be shared.
package main

import "fmt"

type stack struct {
	items []int
	other []int
}

func (s *stack) push(v int) { s.items = append(s.items, v) }

func (s *stack) pop() int {
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v
}

// The parameter's header belongs to the caller.
func addOne(s []int) []int {
	s = append(s, 1)
	return s
}

func build(n int) (out []int) {
	for i := 0; i < n; i++ {
		out = append(out, i*i)
	}
	return
}

func collect(n int) []int {
	var acc []int
	add := func(v int) { acc = append(acc, v) }
	for i := 0; i < n; i++ {
		add(i)
	}
	return acc
}

func main() {
	var st stack
	for i := 0; i < 10; i++ {
		st.push(i)
	}
	fmt.Println(st.pop(), st.pop(), st.items)

	// Struct copies must not share a header.
	copied := st
	copied.items = append(copied.items, 100)
	st.items = append(st.items, 200)
	fmt.Println(len(copied.items), len(st.items), copied.items[len(copied.items)-1], st.items[len(st.items)-1])

	// Sub-slices share the array (as in Go), not the header.
	s := make([]int, 0, 8)
	t := s[:0]
	s = append(s, 1, 2)
	t = append(t, 9)
	fmt.Println(s, t, len(s), len(t))
	s = s[1:]
	fmt.Println(s, len(s), cap(s))

	base := []int{1, 2, 3}
	grown := addOne(base[:2])
	fmt.Println(base, grown, len(base))

	fmt.Println(build(5), collect(4))

	// Appending nothing still gives a separate header.
	a := make([]int, 2, 4)
	b := append(a, []int{}...)
	b = append(b, 7)
	fmt.Println(len(a), len(b), a, b)

	st.other = st.items[:2]
	st.other = append(st.other, 42)
	fmt.Println(st.items[:3], st.other)
}
