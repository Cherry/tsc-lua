package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

type Tristate uint8

type CompilerOptions struct {
	AllowJs         Tristate          `json:"allowJs,omitzero"`
	BaseUrl         string            `json:"baseUrl,omitzero"`
	Lib             []string          `json:"lib,omitzero"`
	Paths           map[string]string `json:"paths,omitzero"`
	MaxNodeModuleJs int               `json:"maxNodeModuleJsDepth,omitzero"`
	Internal        bool              `json:"-"`
	Escaped         string            `json:"esc\"aped" other:"x y"`
}

var compilerOptionFields = sync.OnceValue(func() map[string]int {
	t := reflect.TypeFor[CompilerOptions]()
	fields := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[name] = i
		}
	}
	return fields
})

func declared(options *CompilerOptions, names []string) []string {
	var out []string
	fields := compilerOptionFields()
	v := reflect.ValueOf(options).Elem()
	for _, name := range names {
		i, ok := fields[name]
		if !ok {
			continue
		}
		field := v.Field(i)
		if field.IsZero() {
			continue
		}
		out = append(out, fmt.Sprintf("%s=%v", name, field.Interface()))
	}
	return out
}

type A struct{ X int }
type B struct{ Y string }

type Union struct {
	First  *A
	Second *B
	Third  *[]int
}

func countNonNil(v any) int {
	rv := reflect.ValueOf(v).Elem()
	count := 0
	for _, f := range rv.Fields() {
		if !f.IsNil() {
			count++
		}
	}
	return count
}

func setOf(v any) any {
	rv := reflect.ValueOf(v).Elem()
	var set reflect.Value
	for sf, f := range rv.Fields() {
		if !f.IsNil() && !set.IsValid() {
			set = f
			fmt.Println("set field", sf.Name)
		}
	}
	if !set.IsValid() {
		return nil
	}
	return set.Interface()
}

func add(a, b int) int { return a + b }

func join(sep string, parts ...string) (string, int) {
	return strings.Join(parts, sep), len(parts)
}

func main() {
	opts := &CompilerOptions{AllowJs: 1, Lib: []string{"es5"}, Paths: map[string]string{"a": "b"}, Internal: true}
	fmt.Println(declared(opts, []string{"allowJs", "baseUrl", "lib", "paths", "maxNodeModuleJsDepth", "nope", "Internal"}))
	fmt.Println(len(compilerOptionFields()))
	tag := reflect.TypeFor[CompilerOptions]().Field(6).Tag
	fmt.Println(tag.Get("json"), tag.Get("other"), tag.Get("missing") == "")

	u := &Union{Second: &B{Y: "b"}}
	fmt.Println(countNonNil(u), setOf(u).(*B).Y)
	fmt.Println(setOf(&Union{}) == nil)
	u.Third = &[]int{1}
	fmt.Println(countNonNil(u))

	for f := range reflect.TypeFor[Union]().Fields() {
		fmt.Println(f.Name, f.Type, f.Type.Elem().Kind())
	}
	ft := reflect.TypeOf(join)
	for in := range ft.Ins() {
		fmt.Println("in", in)
	}
	for out := range ft.Outs() {
		fmt.Println("out", out)
	}

	for i := range reflect.ValueOf(3).Seq() {
		fmt.Print(i.Int(), " ")
	}
	fmt.Println()
	for i, v := range reflect.ValueOf([]string{"x", "y"}).Seq2() {
		fmt.Print(i.Int(), v.String(), " ")
	}
	for i, r := range reflect.ValueOf("hé!").Seq2() {
		fmt.Print(i.Int(), string(rune(r.Int())), " ")
	}
	fmt.Println()
	var ks []string
	for k, v := range reflect.ValueOf(map[string]int{"a": 1, "b": 2}).Seq2() {
		ks = append(ks, fmt.Sprint(k.String(), v.Int()))
	}
	slices.Sort(ks)
	fmt.Println(ks)

	res := reflect.ValueOf(add).Call([]reflect.Value{reflect.ValueOf(2), reflect.ValueOf(3)})
	fmt.Println(len(res), res[0].Int())
	res = reflect.ValueOf(join).Call([]reflect.Value{reflect.ValueOf("-"), reflect.ValueOf("a"), reflect.ValueOf("b")})
	fmt.Println(res[0].String(), res[1].Int())

	m := map[string]int{"a": 1}
	reflect.ValueOf(m).Clear()
	s := []int{1, 2}
	reflect.ValueOf(s).Clear()
	fmt.Println(len(m), s)

	var mu sync.Map
	mu.Store(reflect.TypeFor[A](), 1)
	got, ok := mu.Load(reflect.TypeOf(A{}))
	fmt.Println(got, ok)
	cache := map[reflect.Type]string{reflect.TypeFor[[]byte](): "bytes", reflect.TypeFor[string](): "string"}
	fmt.Println(cache[reflect.TypeOf([]byte(nil))], cache[reflect.TypeOf("")], len(cache))
	fmt.Println(reflect.TypeFor[A]().String(), reflect.TypeFor[*A](), reflect.TypeFor[map[string][]*A]())
	var ty reflect.Type = reflect.TypeFor[int]()
	fmt.Printf("%v %s\n", ty, ty)

	i8 := reflect.ValueOf(int8(0))
	u8 := reflect.ValueOf(uint8(0))
	f32 := reflect.ValueOf(float32(0))
	fmt.Println(i8.OverflowInt(127), i8.OverflowInt(128), i8.OverflowInt(-129), u8.OverflowUint(255), u8.OverflowUint(256))
	fmt.Println(f32.OverflowFloat(1e38), f32.OverflowFloat(1e39), reflect.ValueOf(0.0).OverflowFloat(1e300))

	gs := []int{1}
	gv := reflect.ValueOf(&gs).Elem()
	gv.Grow(10)
	fmt.Println(len(gs), cap(gs) >= 11, gs)
	gv.SetLen(3)
	fmt.Println(gs)
	gv.SetCap(4)
	fmt.Println(len(gs), cap(gs))

	src := map[string]int{"k": 5}
	var key string
	var val int
	kv := reflect.ValueOf(&key).Elem()
	vv := reflect.ValueOf(&val).Elem()
	iter := reflect.ValueOf(src).MapRange()
	for iter.Next() {
		kv.SetIterKey(iter)
		vv.SetIterValue(iter)
	}
	fmt.Println(key, val)

	ch := make(chan int, 1)
	cv := reflect.ValueOf(ch)
	fmt.Println(cv.TrySend(reflect.ValueOf(1)), cv.TrySend(reflect.ValueOf(2)), cv.Len(), cv.Cap())
	r, ok := cv.TryRecv()
	fmt.Println(r.Int(), ok)
	r, ok = cv.TryRecv()
	fmt.Println(r.IsValid(), ok)
	cv.Send(reflect.ValueOf(7))
	r, ok = cv.Recv()
	fmt.Println(r.Int(), ok)
	cv.Close()
	r, ok = cv.Recv()
	fmt.Println(r.Int(), ok)
}
