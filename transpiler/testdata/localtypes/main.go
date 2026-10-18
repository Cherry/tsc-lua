// Types declared inside functions: one type per declaration (shared across calls), except where
// they are built from the enclosing generic function's type parameters.
package main

import "fmt"

type named interface{ Name() string }

type base struct{ name string }

func (b base) Name() string { return b.name }

func make1(i int) any {
	type entry struct {
		base
		index int
	}
	return entry{base{fmt.Sprint("e", i)}, i}
}

func pair[T any](a, b T) any {
	type twin struct{ left, right T }
	return twin{a, b}
}

func main() {
	x, y := make1(1), make1(2)
	fmt.Println(x, y)
	// Same declaration, so the same type: interface comparison and type identity hold across calls.
	fmt.Println(x == make1(1), fmt.Sprintf("%T", x) == fmt.Sprintf("%T", y))
	if n, ok := y.(named); ok {
		fmt.Println(n.Name())
	}

	a, b := pair(1, 2), pair("p", "q")
	fmt.Println(a, b, a == pair(1, 2))
	fmt.Println(fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b))
}
