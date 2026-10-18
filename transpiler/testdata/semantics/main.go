package main

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type Point struct{ X, Y int }

type Named interface{ Name() string }

type Animal struct {
	Named
	Legs int
}

type dog struct{ n string }

func (d dog) Name() string { return "dog:" + d.n }

type Counter struct{ n int }

func (c *Counter) Inc() int { c.n++; return c.n }

type MyErr struct{ Code int }

func (e *MyErr) Error() string { return "myerr " + strconv.Itoa(e.Code) }

func work(fail bool) (result string, err error) {
	defer func() {
		if err != nil {
			result = "recovered:" + result
		}
	}()
	if fail {
		return "partial", fmt.Errorf("wrapped: %w", &MyErr{Code: 7})
	}
	return "ok", nil
}

type Number interface {
	~int | ~int64 | ~float64
}

func Sum[T Number](xs ...T) T {
	var s T
	for _, x := range xs {
		s += x
	}
	return s
}

type Pair[K comparable, V any] struct {
	Key K
	Val V
}

func SortedPairs[K cmp.Ordered, V any](m map[K]V) []Pair[K, V] {
	var res []Pair[K, V]
	for k, v := range m {
		res = append(res, Pair[K, V]{k, v})
	}
	slices.SortFunc(res, func(a, b Pair[K, V]) int { return cmp.Compare(a.Key, b.Key) })
	return res
}

type Celsius float64

func (c Celsius) String() string { return strconv.FormatFloat(float64(c), 'f', 1, 64) + "C" }

func main() {
	// Struct keyed maps and value semantics.
	grid := map[Point]string{{1, 2}: "a", {3, 4}: "b"}
	grid[Point{1, 2}] = "c"
	fmt.Println(len(grid), grid[Point{1, 2}], grid[Point{9, 9}] == "")
	p := Point{1, 1}
	q := p
	q.X = 5
	arr := []Point{p, q}
	arr[0].Y = 42
	fmt.Println(p, q, arr)
	pp := &arr[1]
	pp.X = 100
	fmt.Println(arr[1])

	// Embedded interface and method values.
	a := Animal{Named: dog{"rex"}, Legs: 4}
	fmt.Println(a.Name(), a.Legs)
	f := a.Name
	c := &Counter{}
	inc := c.Inc
	inc()
	inc()
	fmt.Println(f(), c.n)

	// Append aliasing.
	base := make([]int, 3, 10)
	x := append(base, 1)
	y := append(base, 2)
	fmt.Println(x[3], y[3], len(base), cap(x))
	z := slices.Clone(base)
	z = append(z, 9)
	fmt.Println(len(z), base)

	// Named results with defer, errors.As.
	r1, e1 := work(false)
	r2, e2 := work(true)
	var me *MyErr
	fmt.Println(r1, e1, r2, e2, errors.As(e2, &me), me.Code)

	// Generics.
	fmt.Println(Sum(1, 2, 3), Sum(1.5, 2.25), Sum[Celsius](1, 2))
	fmt.Println(SortedPairs(map[string]int{"b": 2, "a": 1, "c": 3}))

	// Integer semantics.
	var i8 int8 = 127
	i8++
	var u16 uint16 = 0
	u16--
	var i32 int32 = math.MaxInt32
	i32 *= 2
	var u64 uint64 = math.MaxUint64
	fmt.Println(i8, u16, i32, u64, u64/3, u64%7, u64 > 1<<63)
	n := -17
	fmt.Println(n/4, n%4, n>>2, uint32(n), int64(math.MinInt64)/-1)
	fmt.Println(strconv.FormatUint(u64, 16), strconv.Itoa(-42), 7&^5, ^0)

	v399 := 3.99
	// Floats.
	fmt.Println(math.Floor(-2.5), math.Ceil(2.1), math.Sqrt(2), 1e21, 1e20, 0.1+0.2, float32(0.1))
	fmt.Println(strconv.FormatFloat(123.456, 'e', 3, 64), math.Inf(1), -math.Inf(1), int(v399), int(-v399))
	v, _ := strconv.ParseFloat("6.02e23", 64)
	fmt.Println(v, strconv.Quote("tab\there "))

	// Strings.
	var sb strings.Builder
	for i := range 3 {
		fmt.Fprintf(&sb, "[%d]", i)
	}
	sb.WriteByte('!')
	sb.WriteRune('é')
	fmt.Println(sb.String(), sb.Len())
	fmt.Println(strings.Fields("  a b   c "), strings.Split("a,b,,c", ","), strings.TrimLeft("xxabc", "x"))
	fmt.Println(strings.Index("chicken", "ken"), strings.LastIndex("go gopher", "go"), strings.EqualFold("Go", "GO"))
	fmt.Println(strings.Map(func(r rune) rune { return r + 1 }, "HAL"), strings.Title("hello"))
	bs := []byte("hello")
	bs[0] = 'j'
	fmt.Println(string(bs), []rune("héllo")[1], string(rune(0x10FFFF+1)) == "�")

	// Sorting and maps package.
	people := []struct {
		Name string
		Age  int
	}{{"A", 30}, {"B", 25}, {"C", 30}, {"D", 20}}
	sort.SliceStable(people, func(i, j int) bool { return people[i].Age < people[j].Age })
	fmt.Println(people)
	m := map[string]int{"x": 1, "y": 2, "z": 3}
	keys := slices.Sorted(maps.Keys(m))
	fmt.Println(keys, slices.Index(keys, "y"), slices.Contains(keys, "q"))
	ints := []int{5, 2, 8, 1, 9, 3}
	slices.Sort(ints)
	idx, found := slices.BinarySearch(ints, 8)
	fmt.Println(ints, idx, found, slices.Max(ints))

	// Control flow: goto, labeled break in switch, select.
	k := 0
loop:
	if k < 3 {
		k++
		goto loop
	}
	fmt.Println("k", k)
outer:
	for i := 0; i < 5; i++ {
		switch {
		case i == 3:
			break outer
		case i%2 == 0:
			continue
		}
		fmt.Println("odd", i)
	}
	ch := make(chan int, 1)
	select {
	case v := <-ch:
		fmt.Println("got", v)
	default:
		fmt.Println("empty")
	}
	ch <- 5
	select {
	case v, ok := <-ch:
		fmt.Println("got", v, ok)
	default:
		fmt.Println("empty")
	}

	// Closures capturing range vars and shadowing.
	var fs []func() string
	for _, s := range []string{"a", "b"} {
		fs = append(fs, func() string { return s })
	}
	fmt.Println(fs[0](), fs[1]())
	val := 1
	{
		val := val + 1
		val++
		fmt.Println("inner", val)
	}
	fmt.Println("outer", val)
	fmt.Printf("%v %s %d %5.2f|%-6s|%x %08b %o %U %c\n", Celsius(21.5), Celsius(3), 255, math.Pi, "ab", "hi", 5, 64, 0x1F600, 'Z')
	fmt.Printf("%#v %+v %T %v %v\n", Point{1, 2}, &Point{3, 4}, grid, []string(nil), map[string]int(nil))
	var np *Point
	var ni Named
	fmt.Println(np == nil, ni == nil, np, ni)
}
