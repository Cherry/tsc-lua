package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
)

type Header struct {
	Magic   uint32
	Version uint16
	Flags   [2]uint8
	Size    int64
	Ratio   float32
	OK      bool
}

type Base struct {
	ID   int
	name string
}

type Embeds struct {
	*Base
	Extra string
	inner
}

type inner struct {
	Hidden int
	secret string
}

type Spec struct {
	Discriminator string `json:"kind"`
	Payload       *Header
	Items         []int `json:"items,omitempty"`
}

type Diag struct {
	Code    int
	Message string
	Related []*Diag
}

type Color int

func (c Color) String() string { return [...]string{"red", "green", "blue"}[c] }

type ifaceKey interface{ Key() string }

func main() {
	// encoding/binary uses reflection for structs.
	var buf bytes.Buffer
	h := Header{Magic: 0xCAFEBABE, Version: 3, Flags: [2]uint8{1, 2}, Size: -7, Ratio: 1.5, OK: true}
	if err := binary.Write(&buf, binary.LittleEndian, &h); err != nil {
		fmt.Println("write error", err)
	}
	fmt.Println(buf.Len(), binary.Size(h), fmt.Sprintf("%x", buf.Bytes()))
	var h2 Header
	if err := binary.Read(bytes.NewReader(buf.Bytes()), binary.LittleEndian, &h2); err != nil {
		fmt.Println("read error", err)
	}
	fmt.Println(h2.Magic == h.Magic, h2.Version, h2.Flags, h2.Size, h2.Ratio, h2.OK)
	vals := []uint16{1, 2, 3}
	buf.Reset()
	binary.Write(&buf, binary.BigEndian, vals)
	fmt.Printf("%x\n", buf.Bytes())
	out := make([]uint16, 3)
	binary.Read(bytes.NewReader(buf.Bytes()), binary.BigEndian, out)
	fmt.Println(out)
	enc, err := binary.Append(nil, binary.LittleEndian, struct{ A, B int32 }{1, -1})
	fmt.Println(enc, err)

	// flag.PrintDefaults uses reflect.New/Zero to detect zero defaults.
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.String("name", "", "the name")
	fs.String("mode", "fast", "the mode")
	fs.Int("count", 0, "how many")
	fs.Bool("v", false, "verbose")
	fs.Duration("timeout", 0, "timeout")
	fs.PrintDefaults()
	fs.Parse([]string{"-name", "x", "-count", "3"})
	fmt.Println(fs.Lookup("name").Value.String(), fs.Lookup("count").Value.String())

	// Embedded pointer and unexported embedded struct.
	e := &Embeds{Base: &Base{ID: 5, name: "b"}, Extra: "x", inner: inner{Hidden: 9, secret: "s"}}
	ev := reflect.ValueOf(e).Elem()
	et := ev.Type()
	for i := range et.NumField() {
		f := et.Field(i)
		fmt.Println(f.Name, f.Anonymous, f.Type, f.PkgPath, f.IsExported(), ev.Field(i).CanSet(), ev.Field(i).Kind())
	}
	fmt.Println(ev.FieldByName("ID").Int(), ev.FieldByName("Hidden").Int(), ev.FieldByName("Hidden").CanSet())
	ev.FieldByName("ID").SetInt(6)
	ev.FieldByName("Hidden").SetInt(10)
	fmt.Println(e.ID, e.Hidden, ev.FieldByName("secret").CanSet(), ev.FieldByName("secret").String())
	fmt.Println(ev.FieldByIndex([]int{0, 0}).Int())
	if f, ok := et.FieldByName("ID"); ok {
		fmt.Println(f.Index, f.Type)
	}

	// lsproto-style struct codec.
	spec := &Spec{}
	sv := reflect.ValueOf(spec).Elem()
	st := sv.Type()
	for i := range st.NumField() {
		f := st.Field(i)
		tag := f.Tag.Get("json")
		switch f.Type.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			fmt.Println("nilable", f.Name, tag)
		default:
			fmt.Println("plain", f.Name, tag)
		}
	}
	setString := func(out any) { *(out.(*string)) = "hello" }
	setString(sv.Field(0).Addr().Interface())
	// Avoids `*(out.(**Header)) = ...`, which the transpiler currently emits as a struct copy.
	setPtr := func(out any) { reflect.ValueOf(out).Elem().Set(reflect.ValueOf(&Header{Version: 9})) }
	setPtr(sv.Field(1).Addr().Interface())
	setSlice := func(out any) { *(out.(*[]int)) = []int{4, 5} }
	setSlice(sv.Field(2).Addr().Interface())
	fmt.Println(spec.Discriminator, spec.Payload.Version, spec.Items)
	fmt.Println(sv.Field(1).Elem().Field(1).Uint(), sv.Field(1).Elem().FieldByName("Flags").Len())

	// DeepEqual on diagnostics.
	d1 := []*Diag{{Code: 1, Message: "a", Related: []*Diag{{Code: 2}}}}
	d2 := []*Diag{{Code: 1, Message: "a", Related: []*Diag{{Code: 2}}}}
	d3 := []*Diag{{Code: 1, Message: "a", Related: []*Diag{{Code: 3}}}}
	fmt.Println(reflect.DeepEqual(d1, d2), reflect.DeepEqual(d1, d3), reflect.DeepEqual(d1, []*Diag(nil)))
	fmt.Println(reflect.DeepEqual(map[string][]int{"a": {1}}, map[string][]int{"a": {1}}), reflect.DeepEqual(h, h2))
	fmt.Println(reflect.DeepEqual([]any{map[string]any{"k": []any{1.0, "x"}}}, []any{map[string]any{"k": []any{1.0, "x"}}}))
	fmt.Println(reflect.DeepEqual(Color(1), 1), reflect.DeepEqual(Color(1), Color(1)), reflect.DeepEqual(&h, &h2))

	// Interface-keyed maps, Append, Equal, MakeMap.
	m := map[any]int{"a": 1, 2: 2, Color(2): 3}
	mv := reflect.ValueOf(m)
	keys := []string{}
	for _, k := range mv.MapKeys() {
		keys = append(keys, fmt.Sprintf("%v:%v:%d", k.Kind(), k.Elem().Kind(), mv.MapIndex(k).Int()))
	}
	slices.Sort(keys)
	fmt.Println(keys)
	fmt.Println(mv.MapIndex(reflect.ValueOf(Color(2))).Int(), mv.MapIndex(reflect.ValueOf(2)).Int())
	nm := reflect.MakeMap(reflect.TypeOf(map[string]Color{}))
	nm.SetMapIndex(reflect.ValueOf("x"), reflect.ValueOf(Color(1)))
	nm.SetMapIndex(reflect.ValueOf("y"), reflect.ValueOf(Color(2)))
	nm.SetMapIndex(reflect.ValueOf("y"), reflect.Value{})
	fmt.Println(nm.Interface(), nm.Len())
	sl := reflect.Append(reflect.ValueOf([]string{"a"}), reflect.ValueOf("b"), reflect.ValueOf("c"))
	fmt.Println(sl.Interface(), sl.Len(), sl.Slice(1, 3).Interface())
	fmt.Println(reflect.ValueOf(3).Equal(reflect.ValueOf(3)), reflect.ValueOf(3).Equal(reflect.ValueOf("3")))
	fmt.Println(reflect.ValueOf(Color(2)).Interface(), reflect.ValueOf(Color(2)).Type(), reflect.ValueOf(Color(2)).Kind())

	// Types of interfaces and funcs.
	var k ifaceKey
	kt := reflect.TypeOf(&k).Elem()
	fmt.Println(kt.Kind(), kt.Name(), kt.NumMethod(), reflect.TypeOf(Color(0)).NumMethod())
	fmt.Println(reflect.TypeOf((*error)(nil)).Elem() == reflect.TypeFor[error]())
	fmt.Println(reflect.TypeOf(make(<-chan int)).ChanDir(), reflect.TypeOf(make(chan<- int)).ChanDir(), reflect.TypeOf(make(chan int)).ChanDir())
	fmt.Println(reflect.TypeOf(Color(0)).ConvertibleTo(reflect.TypeOf(0.0)), reflect.TypeOf("").ConvertibleTo(reflect.TypeOf(0)))
	fmt.Println(reflect.TypeOf([]Color{}).AssignableTo(reflect.TypeOf([]Color{})), reflect.TypeOf(Color(0)).AssignableTo(reflect.TypeFor[fmt.Stringer]()))
	fmt.Println(reflect.ArrayOf(3, reflect.TypeOf(0)) == reflect.TypeOf([3]int{}), reflect.TypeOf(struct{ A int }{}).String())
	type local struct{ X int }
	fmt.Println(reflect.TypeOf(local{}).Name(), reflect.TypeOf(local{}).String(), reflect.TypeOf(&local{}).String())

	// Swapper and Value.String.
	words := []string{"c", "a", "b"}
	sw := reflect.Swapper(words)
	sw(0, 1)
	fmt.Println(words, reflect.ValueOf(3).String(), reflect.Value{}.String(), strings.ToUpper(reflect.ValueOf("ok").String()))
	var zv reflect.Value
	fmt.Println(zv.IsValid(), zv.Kind())

	// Panics are recoverable with Go-like messages.
	func() {
		defer func() { fmt.Println("recovered:", recover()) }()
		reflect.ValueOf(3).SetInt(4)
	}()
	func() {
		defer func() { fmt.Println("recovered:", recover()) }()
		reflect.ValueOf("x").Int()
	}()
}
