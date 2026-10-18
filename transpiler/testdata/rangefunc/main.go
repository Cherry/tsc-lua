// Range-over-func bodies are emitted as callable tables holding the variables they capture;
// control flow and captured state must behave as with Go's yield functions.
package main

import (
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
)

func count(n int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range n {
			if !yield(i) {
				return
			}
		}
	}
}

func pairs(words []string) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		for i, w := range words {
			if !yield(i, w) {
				return
			}
		}
	}
}

func firstLong(words []string, min int) (int, string) {
	for i, w := range pairs(words) {
		if len(w) >= min {
			return i, w
		}
	}
	return -1, ""
}

func sumGeneric[T int | float64](seq iter.Seq[T], limit T) T {
	var total T
	for v := range seq {
		if total+v > limit {
			break
		}
		total += v
	}
	return total
}

func floats(values ...float64) iter.Seq[float64] {
	return slices.Values(values)
}

type tree struct {
	value       int
	left, right *tree
}

func (t *tree) All() iter.Seq[int] {
	return func(yield func(int) bool) {
		t.walk(yield)
	}
}

func (t *tree) walk(yield func(int) bool) bool {
	if t == nil {
		return true
	}
	return t.left.walk(yield) && yield(t.value) && t.right.walk(yield)
}

func main() {
	// Captured variables read and assigned inside the body.
	total, seen := 0, ""
	prefix := "n"
	for i := range count(5) {
		total += i
		seen += prefix + fmt.Sprint(i)
	}
	fmt.Println(total, seen)

	// break and continue, to this loop and to an outer labeled loop.
	var out []string
outer:
	for _, row := range []int{1, 2, 3} {
		for i := range count(10) {
			if i == row {
				continue outer
			}
			if i > 5 {
				break outer
			}
			if i == 0 {
				continue
			}
			out = append(out, fmt.Sprintf("%d.%d", row, i))
		}
	}
	fmt.Println(out)

	// return from inside the body, with values.
	fmt.Println(firstLong([]string{"a", "abc", "abcdef"}, 3))
	fmt.Println(firstLong(nil, 1))

	// Nested iterators and closures capturing both loops' variables.
	var funcs []func() string
	for i := range count(2) {
		for j, w := range pairs([]string{"x", "y"}) {
			funcs = append(funcs, func() string { return fmt.Sprint(i, j, w, prefix) })
		}
	}
	for _, f := range funcs {
		fmt.Print(f(), " ")
	}
	fmt.Println()

	// Generic code, where the body uses the type parameter.
	fmt.Println(sumGeneric(count(10), 20), sumGeneric(floats(0.5, 1.5, 2.5), 3.0))

	// A string captured by the body (no substring views) and a recursive iterator.
	text := "hello world"
	var b strings.Builder
	t := &tree{2, &tree{1, nil, nil}, &tree{3, nil, nil}}
	for v := range t.All() {
		b.WriteString(text[:v])
		b.WriteByte(',')
	}
	fmt.Println(b.String())

	// Map iterators, with the keys sorted for a stable order.
	m := map[string]int{"b": 2, "a": 1, "c": 3}
	keys := slices.Sorted(maps.Keys(m))
	product := 1
	for k := range maps.Keys(m) {
		product *= m[k]
	}
	fmt.Println(keys, product)
	localTypes()
}

// localTypes uses a function-local type from a hoisted literal, a converted literal and a
// range body, which are module-level functions in Lua.
func localTypes() {
	type pair struct{ a, b int }
	zero := func() pair { return pair{} }
	base := 10
	shifted := func(p pair) pair { return pair{p.a + base, p.b + base} }
	var all []pair
	for i := range count(3) {
		all = append(all, shifted(pair{i, i * i}))
	}
	fmt.Println(zero(), all)
}
