package main

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

type Inner struct {
	A int `json:"a"`
	B string
}

type Outer struct {
	Inner
	Name  string            `json:"name,omitempty"`
	M     map[string]string `json:"m"`
	P     *Inner            `json:"p,omitzero"`
	L     []int             `json:"l"`
	Raw   jsontext.Value    `json:"raw,omitzero"`
	Skip  int               `json:"-"`
	inner int
}

type Custom struct {
	x int
}

func (c *Custom) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	n, _ := tok.Int()
	c.x = int(n)
	return nil
}

func (c Custom) MarshalJSONTo(enc *jsontext.Encoder) error {
	return enc.WriteToken(jsontext.Int(int64(c.x)))
}

type Tristate byte

const (
	TSUnknown Tristate = iota
	TSFalse
	TSTrue
)

func (t *Tristate) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true":
		*t = TSTrue
	case "false":
		*t = TSFalse
	default:
		*t = TSUnknown
	}
	return nil
}

func (t Tristate) MarshalJSON() ([]byte, error) {
	switch t {
	case TSTrue:
		return []byte("true"), nil
	case TSFalse:
		return []byte("false"), nil
	default:
		return []byte("null"), nil
	}
}

type Key string

type Loc struct {
	Line, Col int
}

func (l Loc) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("%d:%d", l.Line, l.Col)), nil
}

func (l *Loc) UnmarshalText(b []byte) error {
	_, err := fmt.Sscanf(string(b), "%d:%d", &l.Line, &l.Col)
	return err
}

type Omit struct {
	S   string           `json:"s,omitempty"`
	I   int              `json:"i,omitempty"`
	B   bool             `json:"b,omitzero"`
	Z   int              `json:"z,omitzero"`
	Sl  []string         `json:"sl,omitempty"`
	Sz  []string         `json:"sz,omitzero"`
	M   map[string]int   `json:"mm,omitempty"`
	P   *string          `json:"ps,omitempty"`
	E   struct{}         `json:"e,omitempty"`
	N   *Inner           `json:"n,omitempty"`
	Any any              `json:"any,omitempty"`
	F   float64          `json:"f,omitzero"`
	T   Tristate         `json:"t,omitempty"`
	Arr [2]int           `json:"arr,omitzero"`
	Mp  map[Key]Tristate `json:"mp,omitzero"`
	Ip  map[int]string   `json:"ip,omitempty"`
	Lc  map[Loc]bool     `json:"lc,omitempty"`
	Nn  []*Inner         `json:"nn,omitzero"`
}

type Diag struct {
	File        int     `json:"file,omitzero"`
	Code        int32   `json:"code,omitzero"`
	Message     string  `json:"messageText,omitzero"`
	Chain       []*Diag `json:"messageChain,omitzero"`
	Unnecessary bool    `json:"reportsUnnecessary,omitzero"`
}

type Root struct {
	Start, End int
	Single     bool
}

func (r *Root) MarshalJSON() ([]byte, error) {
	if r.Single {
		return json.Marshal(r.Start)
	}
	return json.Marshal([2]int{r.Start, r.End})
}

func (r *Root) UnmarshalJSON(data []byte) error {
	var pair [2]int
	if err := json.Unmarshal(data, &pair); err == nil {
		r.Start, r.End = pair[0], pair[1]
		return nil
	}
	var single int
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	r.Start, r.End, r.Single = single, single, true
	return nil
}

type Info struct {
	Version string           `json:"version,omitzero"`
	Roots   []*Root          `json:"root,omitzero"`
	Names   []string         `json:"fileNames,omitzero"`
	Diags   []*Diag          `json:"diags,omitzero"`
	Lists   [][]int          `json:"lists,omitzero"`
	Opts    map[string]any   `json:"options,omitzero"`
	Values  []jsontext.Value `json:"values,omitzero"`
	Tri     Tristate         `json:"tri"`
	Errors  bool             `json:"errors,omitzero"`
}

type Base struct {
	ID   int    `json:"id"`
	Kind string `json:"kind"`
}

type Derived struct {
	*Base
	Kind  string `json:"kind"`
	Extra string `json:"extra"`
}

type Conflict struct {
	X1 int `json:"x"`
	X2 int `json:"x"`
}

type Folded struct {
	FooBar int `json:"fooBar,case:ignore"`
	Strict int `json:"strict"`
}

type Nums struct {
	I8  int8    `json:"i8"`
	U8  uint8   `json:"u8"`
	U64 uint64  `json:"u64"`
	I64 int64   `json:"i64"`
	F32 float32 `json:"f32"`
	F64 float64 `json:"f64"`
}

func show(label string, v any, opts ...json.Options) {
	b, err := json.Marshal(v, opts...)
	if err != nil {
		fmt.Println(label, "error:", err != nil)
		return
	}
	fmt.Println(label, string(b))
}

func main() {
	// Basic marshal.
	o := Outer{Name: "x", Inner: Inner{A: 3, B: "b"}}
	show("outer", o)
	show("outer-ptr", &o)
	o.P = &Inner{A: 1}
	o.M = map[string]string{"z": "1", "a": "2", "m": "3"}
	o.L = []int{1, 2, 3}
	o.Raw = jsontext.Value(`{ "k" : [1, 2] }`)
	show("outer-full", o, json.Deterministic(true))

	// Basic unmarshal.
	var o2 Outer
	err := json.Unmarshal([]byte(`{"a":1,"B":"bb","name":"n","m":{"k":"v"},"l":[1,2],"p":{"a":7},"raw": [1, {"x": null}] , "unknown": {"deep":[1,2,{}]}}`), &o2)
	fmt.Println(o2.A, o2.B, o2.Name, o2.M, o2.L, o2.P.A, string(o2.Raw), err)

	// Case sensitivity: v2 is case-sensitive by default.
	var o3 Outer
	err = json.Unmarshal([]byte(`{"A":1,"b":"x","NAME":"y"}`), &o3)
	fmt.Println(o3.A, o3.B, o3.Name, err)
	err = json.Unmarshal([]byte(`{"A":1,"b":"x","NAME":"y"}`), &o3, json.MatchCaseInsensitiveNames(true))
	fmt.Println(o3.A, o3.B, o3.Name, err)

	var fo Folded
	err = json.Unmarshal([]byte(`{"foo_bar":5,"STRICT":3}`), &fo)
	fmt.Println(fo.FooBar, fo.Strict, err)

	// Scalars.
	var s string
	err = json.Unmarshal([]byte(`"hi\né😀\"\\\/"`), &s)
	fmt.Println(s, len(s), err)
	var c Custom
	err = json.Unmarshal([]byte(` 5 `), &c)
	fmt.Println(c.x, err)
	var cs []Custom
	err = json.Unmarshal([]byte(`[5,6, 7]`), &cs)
	fmt.Println(cs, err)
	show("customs", cs)

	// Numbers.
	var n Nums
	err = json.Unmarshal([]byte(`{"i8":-128,"u8":255,"u64":18446744073709551615,"i64":-9223372036854775808,"f32":1.1,"f64":1e300}`), &n)
	fmt.Println(n.I8, n.U8, n.U64, n.I64, n.F64, err)
	show("nums", n)
	err = json.Unmarshal([]byte(`{"i8":128}`), &n)
	fmt.Println("i8 overflow", err != nil)
	err = json.Unmarshal([]byte(`{"u8":-1}`), &n)
	fmt.Println("u8 negative", err != nil)
	err = json.Unmarshal([]byte(`{"i64":1.5}`), &n)
	fmt.Println("i64 fraction", err != nil)
	err = json.Unmarshal([]byte(`{"i64":"5"}`), &n)
	fmt.Println("i64 string", err != nil)
	for _, f := range []float64{0, 1, -1, 0.1, 1.5, 1e20, 1e21, 1e-6, 1e-7, 123456789.123, 5e-324, math.MaxFloat64, 100, 1.0 / 3, -0.000001234, 12345678901234567890} {
		b, _ := json.Marshal(f)
		fmt.Print(string(b), " ")
	}
	fmt.Println()
	_, err = json.Marshal(math.NaN())
	fmt.Println("nan", err != nil)
	b, _ := json.Marshal(float32(0.1))
	fmt.Println("f32", string(b))
	b, _ = json.Marshal(uint64(math.MaxUint64))
	fmt.Println("u64", string(b))

	// Strings and escaping.
	show("esc", "a\"b\\c\n\t\x01<>& é😀")
	show("esc-html", "<>&", jsontext.EscapeForHTML(true))
	_, err = json.Marshal("bad\xffutf8")
	fmt.Println("invalid utf8", err != nil)
	show("invalid-allowed", "bad\xffutf8", jsontext.AllowInvalidUTF8(true))
	var bad string
	err = json.Unmarshal([]byte("\"x\xff\""), &bad)
	fmt.Println("decode invalid utf8", err != nil)

	// Omit semantics.
	empty := ""
	show("omit-empty", Omit{})
	show("omit-some", Omit{S: "s", I: 0, B: true, P: &empty, Any: 0, Sz: []string{}, Sl: []string{}, M: map[string]int{}, T: TSTrue, Arr: [2]int{0, 1}})
	show("omit-maps", Omit{Mp: map[Key]Tristate{"b": TSTrue, "a": TSFalse}, Ip: map[int]string{10: "ten", 2: "two"}, Lc: map[Loc]bool{{1, 2}: true}, Nn: []*Inner{nil, {A: 1}}}, json.Deterministic(true))
	var om Omit
	err = json.Unmarshal([]byte(`{"mp":{"x":true,"y":false,"z":null},"ip":{"5":"five"},"lc":{"3:4":true},"nn":[null,{"a":2}]}`), &om)
	fmt.Println(om.Mp, om.Ip, om.Lc, om.Nn[0], om.Nn[1].A, err)

	// Nil slices and maps.
	var nilSlice []int
	var nilMap map[string]int
	show("nil-slice", nilSlice)
	show("nil-map", nilMap)
	show("nil-slice-null", nilSlice, json.FormatNilSliceAsNull(true))
	show("nil-ptr", (*Inner)(nil))
	show("nil-any", nil)
	show("bytes", []byte("hello"))
	var bs []byte
	err = json.Unmarshal([]byte(`"aGVsbG8="`), &bs)
	fmt.Println(string(bs), err)

	// Interfaces.
	var anyv any
	err = json.Unmarshal([]byte(`{"a":[1,"x",true,null,{"b":2.5}],"c":null,"d":false}`), &anyv)
	fmt.Println(anyv, err)
	m := anyv.(map[string]any)
	fmt.Println(m["a"].([]any)[0].(float64), m["c"] == nil, len(m))
	show("any-roundtrip", anyv, json.Deterministic(true))
	var anyStr any = "existing"
	err = json.Unmarshal([]byte(`"new"`), &anyStr)
	fmt.Println(anyStr, err)
	var anyPtr any = &Inner{}
	err = json.Unmarshal([]byte(`{"a":9}`), &anyPtr)
	fmt.Println(anyPtr.(*Inner).A, err)
	var errIface error
	err = json.Unmarshal([]byte(`"x"`), &errIface)
	fmt.Println("nil non-empty iface", err != nil)

	// Pointers.
	var ip *int
	err = json.Unmarshal([]byte(`42`), &ip)
	fmt.Println(*ip, err)
	err = json.Unmarshal([]byte(`null`), &ip)
	fmt.Println(ip == nil, err)
	var pp **Inner
	err = json.Unmarshal([]byte(`{"a":3}`), &pp)
	fmt.Println((*pp).A, err)

	// Embedded pointer struct and field dominance.
	d := Derived{Base: &Base{ID: 1, Kind: "base"}, Kind: "derived", Extra: "e"}
	show("derived", d)
	show("derived-nil", Derived{Kind: "k"})
	var d2 Derived
	err = json.Unmarshal([]byte(`{"id":5,"kind":"k","extra":"x"}`), &d2)
	fmt.Println(d2.Base.ID, d2.Base.Kind, d2.Kind, d2.Extra, err)
	show("conflict", Conflict{X1: 1, X2: 2})

	// Custom MarshalJSON/UnmarshalJSON on pointer receivers inside slices.
	info := Info{
		Version: "1.0",
		Roots:   []*Root{{Start: 1, End: 3}, {Start: 4, Single: true}},
		Names:   []string{"a.ts", "b.ts"},
		Diags:   []*Diag{{File: 1, Code: 2304, Message: "Cannot find name 'x'.", Chain: []*Diag{{Code: 1}}}},
		Lists:   [][]int{{1, 2}, {}, nil},
		Opts:    map[string]any{"strict": true, "target": float64(99), "lib": []any{"es5"}},
		Values:  []jsontext.Value{jsontext.Value(`1`), jsontext.Value(`"s"`), jsontext.Value(` [ 1 , 2 ] `)},
		Tri:     TSTrue,
	}
	b, err = json.Marshal(&info, json.Deterministic(true))
	fmt.Println(string(b), err)
	var info2 Info
	err = json.Unmarshal(b, &info2)
	fmt.Println(info2.Version, len(info2.Roots), *info2.Roots[0], *info2.Roots[1], info2.Diags[0].Chain[0].Code, info2.Lists, info2.Opts["lib"], len(info2.Values), string(info2.Values[2]), info2.Tri, err)
	b2, _ := json.Marshal(&info2, json.Deterministic(true))
	fmt.Println(bytes.Equal(b, b2))

	// Indentation.
	b, err = json.Marshal(map[string]any{"a": []any{1.0, map[string]any{}}, "b": []any{}, "c": "x"}, jsontext.WithIndent("  "), json.Deterministic(true))
	fmt.Println(string(b), err)
	b, err = json.Marshal(&info, jsontext.WithIndentPrefix(" "), jsontext.WithIndent("\t"), json.Deterministic(true))
	fmt.Println(string(b), err)
	b, err = json.Marshal(Inner{A: 1}, jsontext.Multiline(true))
	fmt.Println(string(b), err)
	b, err = json.Marshal([]int{1, 2}, jsontext.SpaceAfterComma(true))
	fmt.Println(string(b), err)

	// Errors.
	var x Inner
	for _, in := range []string{``, `{`, `{"a":}`, `{"a":1,}`, `[1,2]`, `{"a":"s"}`, `{"a":1}x`, `{"a":1,"a":2}`, `nul`, `{"a" 1}`, `"\x01"`, `{"a":01}`, `{"a":1.}`, `{"a":-}`} {
		err := json.Unmarshal([]byte(in), &x)
		fmt.Printf("%q err=%v\n", in, err != nil)
	}
	err = json.Unmarshal([]byte(`{"a":1,"a":2}`), &x, jsontext.AllowDuplicateNames(true))
	fmt.Println("dup allowed", x.A, err)
	var mm map[string]int
	err = json.Unmarshal([]byte(`{"k":1,"k":2}`), &mm)
	fmt.Println("map dup", err != nil)
	err = json.Unmarshal([]byte(`{"zz":1}`), &x, json.RejectUnknownMembers(true))
	fmt.Println("unknown", err != nil)
	err = json.Unmarshal([]byte(`{}`), x)
	fmt.Println("non-pointer", err != nil)
	err = json.Unmarshal([]byte(`{"a":"s"}`), &x)
	_, isSem := err.(*json.SemanticError)
	fmt.Println("semantic", isSem)
	err = json.Unmarshal([]byte(`{"a":1,}`), &x)
	_, isSyn := err.(*jsontext.SyntacticError)
	fmt.Println("syntactic", isSyn)
	err = json.Unmarshal([]byte(`[1]`), new([]int))
	fmt.Println("eof wrap", errors.Is(err, io.EOF), err)

	// GetOption.
	opts := json.JoinOptions(json.Deterministic(true), jsontext.WithIndent("  "))
	det, ok := json.GetOption(opts, json.Deterministic)
	ind, ok2 := json.GetOption(opts, jsontext.WithIndent)
	_, ok3 := json.GetOption(opts, json.FormatNilMapAsNull)
	fmt.Println(det, ok, ind == "  ", ok2, ok3)

	// MarshalWrite / UnmarshalRead.
	var sb strings.Builder
	err = json.MarshalWrite(&sb, map[string]int{"a": 1}, json.Deterministic(true))
	fmt.Printf("%q %v\n", sb.String(), err)
	var fromReader map[string]int
	err = json.UnmarshalRead(strings.NewReader(`{"r":2}`), &fromReader)
	fmt.Println(fromReader, err)

	// Text marshaler as value.
	show("loc", Loc{3, 4})
	var loc Loc
	err = json.Unmarshal([]byte(`"5:6"`), &loc)
	fmt.Println(loc, err)

	// Kind.
	fmt.Println(jsontext.EndArray.Kind(), jsontext.Null, jsontext.Kind('"'), jsontext.Kind('0'))
}
