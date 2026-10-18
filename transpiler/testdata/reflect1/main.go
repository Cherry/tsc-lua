package main

import (
	"encoding"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type Tristate uint8

type Inner struct {
	A int
	B string
}

type Options struct {
	Inner
	Name    string         `json:"name,omitempty"`
	Count   int            `json:"count"`
	Flag    Tristate       `json:"flag"`
	List    []string       `json:"list,omitempty"`
	Ptr     *bool          `json:"ptr"`
	M       map[string]int `json:"m"`
	Any     any            `json:"any"`
	Arr     [3]int         `json:"-"`
	private int
}

type Key struct{ N int }

func (k Key) MarshalText() ([]byte, error) { return []byte("key" + strconv.Itoa(k.N)), nil }

type MyMap[K comparable, V any] struct {
	keys []K
	m    map[K]V
}

var _ encoding.TextMarshaler = Key{}

func resolveKeyName(k reflect.Value) (string, error) {
	if k.Kind() == reflect.String {
		return k.String(), nil
	}
	if tm, ok := reflect.TypeAssert[encoding.TextMarshaler](k); ok {
		if k.Kind() == reflect.Pointer && k.IsNil() {
			return "", nil
		}
		buf, err := tm.MarshalText()
		return string(buf), err
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(k.Uint(), 10), nil
	}
	panic("unexpected map key type")
}

func kindOf[T any]() string {
	switch reflect.TypeFor[T]().Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map:
		return "map"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "number"
	}
	return "other"
}

var myMapType = reflect.TypeFor[*MyMap[string, any]]()

func normalize(value any) any {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() || (reflected.Kind() != reflect.Slice && reflected.Kind() != reflect.Array) {
		return value
	}
	if reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return nil
	}
	result := make([]any, reflected.Len())
	for i := range reflected.Len() {
		result[i] = normalize(reflected.Index(i).Interface())
	}
	return result
}

func main() {
	// Kinds and TypeOf.
	vals := []any{true, 1, int8(2), int16(3), int32(4), int64(5), uint(6), uint8(7), uint16(8), uint32(9), uint64(10),
		uintptr(11), float32(1.5), 2.5, "s", []int{1}, [2]int{}, map[string]int{}, &Inner{}, Inner{}, Tristate(1),
		func() {}, make(chan int), new(int)}
	for _, v := range vals {
		t := reflect.TypeOf(v)
		fmt.Println(int(t.Kind()), t.Kind().String(), t.String(), t.Name())
	}
	fmt.Println(reflect.TypeOf(nil) == nil)
	fmt.Println(reflect.TypeOf(1) == reflect.TypeOf(2), reflect.TypeOf(1) == reflect.TypeOf("x"))
	fmt.Println(reflect.TypeOf(&MyMap[string, any]{}) == myMapType, reflect.TypeOf(&MyMap[string, int]{}) == myMapType)
	fmt.Println(reflect.TypeFor[int]() == reflect.TypeOf(0))
	fmt.Println(kindOf[string](), kindOf[[]int](), kindOf[map[string]bool](), kindOf[Tristate](), kindOf[bool](), kindOf[*int]())
	fmt.Println(reflect.TypeOf(Inner{}).PkgPath(), reflect.TypeOf(1).PkgPath() == "")
	fmt.Println(reflect.TypeOf([]string{}).Elem().Kind() == reflect.String, reflect.TypeOf(map[int]bool{}).Key().Kind())
	fmt.Println(reflect.TypeOf([4]int{}).Len())
	errT := reflect.TypeFor[error]()
	fmt.Println(errT.Kind(), errT.String(), errT.Name())
	fmt.Println(reflect.TypeOf(fmt.Errorf("x")).Implements(errT), reflect.TypeOf(1).Implements(errT))
	fmt.Println(reflect.PointerTo(reflect.TypeOf(1)) == reflect.TypeOf(new(int)))
	fmt.Println(reflect.SliceOf(reflect.TypeOf("")) == reflect.TypeOf([]string{}))
	fmt.Println(reflect.MapOf(reflect.TypeOf(""), reflect.TypeOf(1)) == reflect.TypeOf(map[string]int{}))
	fmt.Println(reflect.TypeOf(1).Comparable(), reflect.TypeOf([]int{}).Comparable())
	fmt.Println(reflect.TypeOf(int32(0)).Bits(), reflect.TypeOf(int32(0)).Size(), reflect.TypeOf("").Size(), reflect.TypeOf(Inner{}).Size())
	ft := reflect.TypeOf(strings.Cut)
	fmt.Println(ft.NumIn(), ft.NumOut(), ft.In(0).Kind(), ft.Out(2).Kind(), ft.IsVariadic())
	fmt.Println(reflect.TypeOf(fmt.Sprintf).IsVariadic())

	// Struct type fields.
	ot := reflect.TypeFor[Options]()
	fmt.Println(ot.NumField())
	for i := range ot.NumField() {
		f := ot.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		v, ok := f.Tag.Lookup("json")
		fmt.Println(i, f.Name, f.Type.String(), f.Anonymous, f.PkgPath, f.Index, name, v, ok, f.Type.Kind())
	}
	if f, ok := ot.FieldByName("B"); ok {
		fmt.Println("FieldByName", f.Name, f.Index, f.Type)
	}
	_, ok := ot.FieldByName("Nope")
	fmt.Println(ok)

	// Values.
	b := true
	opts := &Options{Inner: Inner{A: 1, B: "b"}, Name: "n", Count: 3, Flag: 2, List: []string{"x", "y"}, Ptr: &b,
		M: map[string]int{"a": 1, "b": 2}, Any: 4.5, Arr: [3]int{7, 8, 9}}
	v := reflect.ValueOf(opts).Elem()
	fmt.Println(v.Kind(), v.Type().Name(), v.NumField(), v.CanSet(), v.CanAddr())
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() == reflect.Pointer {
			fmt.Println(i, f.Kind(), f.IsZero(), f.CanSet(), f.Elem().Interface())
		} else if f.CanInterface() {
			fmt.Println(i, f.Kind(), f.IsZero(), f.CanSet(), f.Interface())
		} else {
			fmt.Println(i, f.Kind(), f.IsZero(), f.CanSet(), "unexported")
		}
	}
	fmt.Println(v.Field(0).Field(1).String(), v.FieldByName("A").Int(), v.FieldByName("Name").String())
	fmt.Println(v.Field(4).Len(), v.Field(4).Index(1).String(), v.Field(5).Elem().Bool(), v.Field(5).IsNil())
	fmt.Println(v.Field(7).Elem().Float(), v.Field(7).Kind(), v.Field(7).Elem().Kind(), v.Field(8).Index(2).Int())
	fmt.Println(v.Field(3).Uint(), v.Field(2).Int())
	keys := v.Field(6).MapKeys()
	ks := []string{}
	for _, k := range keys {
		ks = append(ks, k.String()+"="+strconv.Itoa(int(v.Field(6).MapIndex(k).Int())))
	}
	sort.Strings(ks)
	fmt.Println(ks, v.Field(6).Len())
	fmt.Println(v.Field(6).MapIndex(reflect.ValueOf("zz")).IsValid())
	it := v.Field(6).MapRange()
	n := 0
	for it.Next() {
		n += int(it.Value().Int()) + len(it.Key().String())
	}
	fmt.Println("maprange", n)

	// Setting.
	v.Field(1).SetString("changed")
	v.Field(2).SetInt(42)
	v.Field(3).SetUint(300)
	v.Field(0).Field(0).SetInt(-5)
	v.FieldByName("Count").Set(reflect.ValueOf(43))
	v.Field(4).Index(0).SetString("z")
	v.Field(8).Index(0).SetInt(70)
	v.Field(5).Elem().SetBool(false)
	v.Field(7).Set(reflect.ValueOf("iface"))
	v.Field(6).SetMapIndex(reflect.ValueOf("c"), reflect.ValueOf(3))
	fmt.Println(opts.Name, opts.Count, opts.Flag, opts.A, opts.List, opts.Arr, b, opts.Any, len(opts.M), opts.M["c"])
	v.Field(0).Set(reflect.ValueOf(Inner{A: 9, B: "nine"}))
	fmt.Println(opts.A, opts.B)
	p := v.Field(2).Addr().Interface().(*int)
	*p = 100
	fmt.Println(opts.Count)
	ip := v.Field(0).Addr().Interface().(*Inner)
	ip.B = "viaaddr"
	fmt.Println(opts.B)

	// New / Zero / Indirect.
	nv := reflect.New(reflect.TypeOf(0))
	nv.Elem().SetInt(7)
	fmt.Println(*(nv.Interface().(*int)), nv.Kind(), nv.Elem().Kind())
	ns := reflect.New(reflect.TypeFor[Inner]())
	ns.Elem().Field(1).SetString("new")
	fmt.Println(ns.Interface().(*Inner).B, reflect.Indirect(ns).Field(1).String(), reflect.Indirect(reflect.ValueOf(3)).Int())
	z := reflect.Zero(reflect.TypeFor[Inner]())
	fmt.Println(z.IsZero(), z.Interface().(Inner).B == "", reflect.Zero(reflect.TypeOf("")).String() == "")
	fmt.Println(reflect.ValueOf(nil).IsValid(), reflect.ValueOf((*int)(nil)).IsNil(), reflect.ValueOf((*int)(nil)).Elem().IsValid())
	ms := reflect.MakeSlice(reflect.TypeOf([]int{}), 2, 4)
	ms.Index(1).SetInt(5)
	fmt.Println(ms.Len(), ms.Cap(), ms.Interface())
	fmt.Println(reflect.ValueOf([]byte("hi")).Bytes(), reflect.ValueOf("héllo").Len(), reflect.ValueOf("abc").Index(1).Uint())

	// Map key names.
	for _, k := range []any{"s", 5, uint16(6), Key{7}, &Key{8}} {
		name, err := resolveKeyName(reflect.ValueOf(k))
		fmt.Println(name, err)
	}

	// TypeAssert.
	iv, ok := reflect.TypeAssert[int](reflect.ValueOf(3))
	fmt.Println(iv, ok)
	sv, ok := reflect.TypeAssert[string](reflect.ValueOf(3))
	fmt.Printf("%q %v\n", sv, ok)
	inv, ok := reflect.TypeAssert[Inner](reflect.ValueOf(Inner{A: 2}))
	fmt.Println(inv.A, ok)
	sg, ok := reflect.TypeAssert[fmt.Stringer](reflect.ValueOf(3))
	fmt.Println(sg == nil, ok)

	// Normalize like tsconfig parsing.
	fmt.Println(normalize([]string{"a", "b"}), normalize([2]int{1, 2}), normalize(5), normalize([]int(nil)) == nil)

	// DeepEqual.
	type node struct {
		Val  int
		Next *node
		Tags []string
		M    map[string]any
	}
	n1 := &node{Val: 1, Tags: []string{"a"}, M: map[string]any{"x": []int{1}}}
	n2 := &node{Val: 1, Tags: []string{"a"}, M: map[string]any{"x": []int{1}}}
	fmt.Println(reflect.DeepEqual(n1, n2), reflect.DeepEqual(*n1, *n2))
	n2.M["x"] = []int{2}
	fmt.Println(reflect.DeepEqual(n1, n2))
	n1.Next = n1
	n3 := &node{Val: 1, Tags: []string{"a"}, M: map[string]any{"x": []int{1}}}
	n3.Next = n3
	fmt.Println(reflect.DeepEqual(n1, n3))
	fmt.Println(reflect.DeepEqual([]int{}, []int(nil)), reflect.DeepEqual([]int(nil), []int(nil)), reflect.DeepEqual(1, int64(1)))
	fmt.Println(reflect.DeepEqual(nil, nil), reflect.DeepEqual(nil, 1), reflect.DeepEqual(map[int]int{1: 2}, map[int]int{1: 2}))
	fmt.Println(reflect.DeepEqual([]any{1, "a", nil}, []any{1, "a", nil}), reflect.DeepEqual([]error{nil}, []error{nil}))

	// Convert and Kind on named types.
	fmt.Println(reflect.ValueOf(Tristate(3)).Convert(reflect.TypeOf(0)).Interface(), reflect.ValueOf(65).Convert(reflect.TypeOf(1.0)).Float())
	fmt.Println(reflect.TypeOf(reflect.Value{}).Kind(), reflect.Ptr == reflect.Pointer)
	fmt.Println(reflect.ValueOf(Inner{}).Field(0).CanSet(), reflect.ValueOf(&Inner{}).Elem().Field(0).CanSet())
	fmt.Println(reflect.ValueOf(map[string]int(nil)).IsNil(), reflect.ValueOf(map[string]int(nil)).Len())
	fmt.Println(reflect.ValueOf(-0.0).IsZero(), reflect.ValueOf(0.0).IsZero(), reflect.ValueOf([2]int{}).IsZero(), reflect.ValueOf([2]int{0, 1}).IsZero())
	fmt.Println(reflect.ValueOf(&Options{}).Elem().IsZero(), reflect.ValueOf(Options{Name: "x"}).IsZero())
}
