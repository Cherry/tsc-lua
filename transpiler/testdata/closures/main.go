// Function literals that capture variables (emitted as callable tables), deferred calls and
// recovered panics, and integer expressions whose Lua forms need parentheses.
package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type counter struct{ n int }

func (c *counter) inc(by int) { c.n += by }

func (c counter) value() int { return c.n }

type namer interface{ name() string }

type thing struct{ id string }

func (t thing) name() string { return "thing " + t.id }

// Captured variables that are assigned later are shared with the literal.
func shared() (int, string) {
	total := 0
	label := "start"
	add := func(n int) {
		total += n
		label = fmt.Sprint("added ", n)
	}
	add(2)
	add(3)
	total *= 10
	add(1)
	return total, label
}

// Captured variables that are never assigned are copied.
func copied(prefix string, xs []string) []string {
	var out []string
	sep := ": "
	for _, x := range xs {
		out = append(out, strings.ToUpper(func() string { return prefix + sep + x }()))
	}
	return out
}

// Each iteration has its own loop variable (Go 1.22).
func perIteration() []int {
	var fs []func() int
	for i := 0; i < 3; i++ {
		fs = append(fs, func() int { return i * i })
	}
	var res []int
	for _, f := range fs {
		res = append(res, f())
	}
	return res
}

// Nested literals capture from every enclosing level.
func nested(a int) int {
	b := 10
	outer := func(c int) func(int) int {
		d := c * 2
		return func(e int) int {
			b++
			return a + b + c + d + e
		}
	}
	f := outer(1)
	return f(100) + f(1000) + b
}

// A literal that calls itself through a captured variable.
func recursive(n int) int {
	var fib func(int) int
	fib = func(k int) int {
		if k < 2 {
			return k
		}
		return fib(k-1) + fib(k-2)
	}
	return fib(n)
}

// Sorting with a comparator that captures a slice.
func sorted(words []string) []string {
	keys := map[string]int{"b": 2, "a": 1, "c": 3}
	sort.Slice(words, func(i, j int) bool { return keys[words[i]] < keys[words[j]] })
	return words
}

// Deferred calls run in reverse order, with arguments evaluated at the defer statement.
func deferOrder() (log []string) {
	add := func(s string) { log = append(log, s) }
	x := 1
	defer add(fmt.Sprint("first ", x))
	x = 2
	defer add(fmt.Sprint("second ", x))
	defer func() { add(fmt.Sprint("closure sees ", x)) }()
	x = 3
	return nil
}

// A recovered panic returns the zero values of unnamed results.
func recovered(fail bool) (int, string, []int, *counter) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
		}
	}()
	if fail {
		panic("boom")
	}
	return 7, "ok", []int{1}, &counter{n: 1}
}

// A deferred function changes a named result after the return.
func namedResult() (n int, err error) {
	defer func() {
		if err != nil {
			n = -1
		}
	}()
	return 5, errors.New("bad")
}

// Deferred method calls through a pointer, a value and an interface.
func deferredMethods() int {
	c := &counter{}
	var t namer = thing{id: "x"}
	func() {
		defer c.inc(5)
		defer fmt.Println("deferred interface call:", t.name())
		defer c.inc(c.value())
		c.inc(1)
	}()
	return c.n
}

// Method values bind their receiver.
func methodValues() int {
	c := &counter{}
	inc := c.inc
	inc(4)
	inc(6)
	var t namer = thing{id: "y"}
	name := t.name
	fmt.Println(name())
	return c.n
}

func main() {
	fmt.Println(shared())
	fmt.Println(copied("p", []string{"a", "b"}))
	fmt.Println(perIteration())
	fmt.Println(nested(5))
	fmt.Println(recursive(15))
	fmt.Println(sorted([]string{"c", "a", "b"}))
	fmt.Println(deferOrder())
	fmt.Println(recovered(false))
	fmt.Println(recovered(true))
	fmt.Println(namedResult())
	fmt.Println(deferredMethods())
	fmt.Println(methodValues())
	// ^ of a sum needs the sum parenthesized.
	a := 5
	fmt.Println(^(a + 1), -(a + 1), ^a)
}
