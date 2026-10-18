package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Kind int16

const (
	KindA Kind = iota
	KindB
	KindC
)

func (k Kind) String() string {
	switch k {
	case KindA:
		return "A"
	case KindB:
		return "B"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

type TextRange struct {
	pos, end int
}

func (t TextRange) Len() int { return t.end - t.pos }

type Node struct {
	Kind   Kind
	Loc    TextRange
	Parent *Node
}

type NodeBase struct {
	Node
}

type Identifier struct {
	NodeBase
	Text string
}

func (n *Node) AsIdentifier() *Identifier { return (*Identifier)(nil) }

type Shape interface {
	Area() float64
	Name() string
}

type Rect struct{ W, H float64 }
type Circle struct{ R float64 }

func (r Rect) Area() float64    { return r.W * r.H }
func (r Rect) Name() string     { return "rect" }
func (c *Circle) Area() float64 { return 3 * c.R * c.R }
func (c *Circle) Name() string  { return "circle" }

type Stack[T any] struct {
	items []T
}

func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }
func (s *Stack[T]) Pop() (T, bool) {
	var zero T
	if len(s.items) == 0 {
		return zero, false
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v, true
}

func Map[T, U any](xs []T, f func(T) U) []U {
	res := make([]U, 0, len(xs))
	for _, x := range xs {
		res = append(res, f(x))
	}
	return res
}

func divmod(a, b int) (q, r int) {
	defer func() {
		if e := recover(); e != nil {
			q, r = -1, -1
		}
	}()
	q = a / b
	r = a % b
	return
}

var ErrNotFound = errors.New("not found")

func find(m map[string]int, k string) (int, error) {
	if v, ok := m[k]; ok {
		return v, nil
	}
	return 0, fmt.Errorf("find %q: %w", k, ErrNotFound)
}

func seq(n int) func(yield func(int) bool) {
	return func(yield func(int) bool) {
		for i := 0; i < n; i++ {
			if !yield(i) {
				return
			}
		}
	}
}

func firstOver(n int) int {
	for v := range seq(100) {
		if v*v > n {
			return v
		}
	}
	return -1
}

func main() {
	fmt.Println("hello", 42, 3.5, true, KindB, KindC)
	var k Kind = 2
	k++
	u8 := uint8(250)
	fmt.Println(k, int8(127)+int8(k), u8+uint8(10))
	fmt.Println(-7/2, -7%2, 7>>1, -7>>1, 1<<10)

	id := &Identifier{Text: "foo"}
	id.Kind = KindB
	id.Loc = TextRange{1, 5}
	n := &id.Node
	fmt.Println(n.Kind, n.Loc.Len(), id.Text, id.Loc)

	shapes := []Shape{Rect{2, 3}, &Circle{1}}
	total := 0.0
	for _, s := range shapes {
		total += s.Area()
		fmt.Printf("%s %.2f %T\n", s.Name(), s.Area(), s)
	}
	fmt.Println("total", total)

	st := &Stack[string]{}
	st.Push("a")
	st.Push("b")
	v, ok := st.Pop()
	fmt.Println(v, ok, len(st.items))
	is := &Stack[int]{}
	iv, iok := is.Pop()
	fmt.Println(iv, iok)

	sq := Map([]int{1, 2, 3}, func(x int) string { return strings.Repeat("x", x) })
	fmt.Println(sq, len(sq), cap(sq))

	fmt.Println(divmod(17, 5))
	fmt.Println(divmod(1, 0))

	m := map[string]int{"a": 1, "b": 2}
	m["c"] = 3
	delete(m, "a")
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println(keys, len(m))
	_, err := find(m, "zz")
	fmt.Println(err, errors.Is(err, ErrNotFound))

	var funcs []func() int
	for i := 0; i < 3; i++ {
		funcs = append(funcs, func() int { return i * 10 })
	}
	for _, f := range funcs {
		fmt.Print(f(), " ")
	}
	fmt.Println()

	s := "héllo, 世界"
	for i, r := range s {
		if r > 127 {
			fmt.Print(i, ":", string(r), " ")
		}
	}
	fmt.Println(len(s), s[1:3] == "é", strings.ToUpper(s))

	a := [3]int{1, 2, 3}
	b := a
	b[0] = 100
	fmt.Println(a, b, a == [3]int{1, 2, 3})

	r1 := TextRange{1, 2}
	r2 := r1
	r2.pos = 9
	fmt.Println(r1, r2, r1 == TextRange{1, 2})

	fmt.Println(firstOver(50))

	ch := make(chan int)
	done := make(chan bool)
	go func() {
		sum := 0
		for v := range ch {
			sum += v
		}
		fmt.Println("sum", sum)
		done <- true
	}()
	for i := 1; i <= 4; i++ {
		ch <- i
	}
	close(ch)
	<-done

outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if j == 2 {
				continue outer
			}
			if i == 2 {
				break outer
			}
			fmt.Print(i, j, ";")
		}
	}
	fmt.Println()

	switch x := 5; {
	case x > 3:
		fmt.Println("big")
		fallthrough
	case x > 1:
		fmt.Println("medium")
	default:
		fmt.Println("small")
	}

	var any1 interface{} = KindB
	switch t := any1.(type) {
	case int:
		fmt.Println("int", t)
	case fmt.Stringer:
		fmt.Println("stringer", t.String())
	}
	fmt.Printf("%v %d %s %q %x %5.1f|%-4d|%04d %t %v\n", []int{1, 2}, 42, "str", "q\"s", 255, 3.14159, 7, 7, false, map[string]int{"z": 1, "a": 2})
	fmt.Printf("%+v %v\n", TextRange{1, 2}, &TextRange{3, 4})
}
