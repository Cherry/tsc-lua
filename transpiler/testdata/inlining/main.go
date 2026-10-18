// Guarded-return functions that the transpiler inlines as and/or chains, including the
// edge cases where a guarded result is a Lua-falsy-looking Go value (0, "") or a bool.
package main

import (
	"fmt"
	"unsafe"
)

type node struct {
	flags int
	name  string
	next  *node
}

func sortFlags(n *node) int {
	if n.flags&6 != 0 && n.flags&8 == 0 {
		return 0
	}
	return n.flags
}

func label(n *node) string {
	if n.flags == 1 {
		return ""
	}
	if n.flags == 2 {
		return "two"
	}
	return n.name
}

func isNamed(n *node) bool {
	if n.name == "" {
		return false
	}
	return n.flags != 0
}

func isSpecial(n *node) bool {
	if n.flags > 10 {
		return true
	}
	return n.name == "special"
}

func kindOf(n *node) int {
	if n.flags < 0 {
		return -1
	}
	return int(uint8(n.flags))
}

// A cast in the AST's style: a guard that panics, then the value (which may be nil).
func (n *node) asWide() *node {
	if n.flags < 300 {
		panic("Invalid node cast to wide")
	}
	return n.next
}

func tryAsWide(n *node) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprint("recovered: ", r)
		}
	}()
	return fmt.Sprint(n.asWide() == nil)
}

// The checker's castType: generic, with a panic guard, through unsafe.Pointer.
type wideNode struct {
	flags int
	name  string
	next  *node
}

func castAs[T any](n *node, flags int) *T {
	if n.flags != flags {
		panic("Invalid type cast")
	}
	return (*T)(unsafe.Pointer(n))
}

func tryCast(n *node) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprint(r)
		}
	}()
	return castAs[wideNode](n, 300).name
}

// Templates inlined into templates, with parameters passed on in a different order.
func minus(a, b int) int { return a - b }

func reverseMinus(a, b int) int { return minus(b, a) }

func pick(n *node, a, b int) int {
	if n.flags > 100 {
		return reverseMinus(b, a)
	}
	return minus(a, b)
}

// Pointer results can be nil, so this must stay a call.
func successor(n *node) *node {
	if n.flags == 0 {
		return nil
	}
	return n.next
}

func main() {
	tail := &node{flags: 3, name: "tail"}
	nodes := []*node{
		{flags: 0, name: ""},
		{flags: 1, name: "one"},
		{flags: 2, name: "x"},
		{flags: 4, name: "four", next: tail},
		{flags: 12, name: ""},
		{flags: 14, name: "special"},
		{flags: 5, name: "special"},
		{flags: 300, name: "wide"},
	}
	for _, n := range nodes {
		fmt.Printf("%d %q %v %v %v %d %s %s\n", sortFlags(n), label(n), isNamed(n), isSpecial(n), successor(n) != nil, kindOf(n), tryAsWide(n), tryCast(n))
		fmt.Println(minus(10, 3), reverseMinus(10, 3), pick(n, 10, 3), pick(n, 3, 10))
	}
}
