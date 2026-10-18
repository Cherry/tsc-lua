package main

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// OrderedMap mirrors typescript-go's collections.OrderedMap JSON methods.
type OrderedMap[K comparable, V any] struct {
	keys []K
	mp   map[K]V
}

func (m *OrderedMap[K, V]) Set(k K, v V) {
	if m.mp == nil {
		m.mp = make(map[K]V)
	}
	if _, ok := m.mp[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.mp[k] = v
}

func (m *OrderedMap[K, V]) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, k := range m.keys {
		if err := json.MarshalEncode(enc, fmt.Sprint(k)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, m.mp[k]); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

func (m *OrderedMap[K, V]) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	token, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() == 'n' {
		return nil
	}
	if token.Kind() != '{' {
		return errors.New("cannot unmarshal non-object JSON value into Map")
	}
	for dec.PeekKind() != '}' {
		var key K
		var value V
		if err := json.UnmarshalDecode(dec, &key); err != nil {
			return err
		}
		if err := json.UnmarshalDecode(dec, &value); err != nil {
			return err
		}
		m.Set(key, value)
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	return nil
}

type JSONValueType int8

const (
	NotPresent JSONValueType = iota
	Null
	String
	Number
	Boolean
	Array
	Object
)

type JSONValue struct {
	Type  JSONValueType
	Value any
}

func (v *JSONValue) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return unmarshalJSONValueFrom[JSONValue](v, dec)
}

func unmarshalJSONValueFrom[T any](v *JSONValue, dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case 'n':
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		v.Value = nil
		v.Type = Null
		return nil
	case '"':
		v.Type = String
		if err := json.UnmarshalDecode(dec, &v.Value); err != nil {
			return err
		}
	case '[':
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		var elements []T
		for dec.PeekKind() != jsontext.EndArray.Kind() {
			var element T
			if err := json.UnmarshalDecode(dec, &element); err != nil {
				return err
			}
			elements = append(elements, element)
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		v.Type = Array
		v.Value = elements
	case '{':
		var object OrderedMap[string, T]
		if err := json.UnmarshalDecode(dec, &object); err != nil {
			return err
		}
		v.Type = Object
		v.Value = &object
	case 't', 'f':
		v.Type = Boolean
		if err := json.UnmarshalDecode(dec, &v.Value); err != nil {
			return err
		}
	default:
		v.Type = Number
		if err := json.UnmarshalDecode(dec, &v.Value); err != nil {
			return err
		}
	}
	return nil
}

func (v JSONValue) String() string {
	switch v.Type {
	case Object:
		m := v.Value.(*OrderedMap[string, JSONValue])
		var parts []string
		for _, k := range m.keys {
			parts = append(parts, k+"="+m.mp[k].String())
		}
		return "{" + strings.Join(parts, ",") + "}"
	case Array:
		var parts []string
		for _, e := range v.Value.([]JSONValue) {
			parts = append(parts, e.String())
		}
		return "[" + strings.Join(parts, ",") + "]"
	case Null:
		return "null"
	}
	return fmt.Sprintf("%v:%v", v.Type, v.Value)
}

type Expected[T any] struct {
	actualJSONType string
	Null           bool
	Valid          bool
	Value          T
}

func (e *Expected[T]) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*e = Expected[T]{Null: true, actualJSONType: "null"}
		return nil
	}
	if json.Unmarshal(data, &e.Value) == nil {
		e.Valid = true
	}
	switch data[0] {
	case '"':
		e.actualJSONType = "string"
	case 't', 'f':
		e.actualJSONType = "boolean"
	case '[':
		e.actualJSONType = "array"
	case '{':
		e.actualJSONType = "object"
	default:
		e.actualJSONType = "number"
	}
	return nil
}

type HeaderFields struct {
	Name    Expected[string] `json:"name"`
	Version Expected[string] `json:"version"`
	Type    Expected[string] `json:"type"`
}

type PathFields struct {
	Main          Expected[string] `json:"main"`
	Types         Expected[string] `json:"types"`
	TypesVersions JSONValue        `json:"typesVersions"`
	Exports       JSONValue        `json:"exports"`
}

type DependencyFields struct {
	Dependencies    Expected[map[string]string] `json:"dependencies"`
	DevDependencies Expected[map[string]string] `json:"devDependencies"`
}

type ContentMapperFields struct {
	Exec          Expected[[]string] `json:"exec"`
	DynamicConfig Expected[bool]     `json:"dynamicConfig"`
}

type typeScriptFields struct {
	ContentMapper Expected[ContentMapperFields] `json:"contentMapper"`
}

type Fields struct {
	HeaderFields
	PathFields
	DependencyFields
	ContentMapper Expected[ContentMapperFields] `json:"-"`
}

func Parse(data []byte) (Fields, error) {
	var parsed struct {
		HeaderFields
		PathFields
		DependencyFields
		TypeScript Expected[typeScriptFields] `json:"typescript"`
	}
	if err := json.Unmarshal(data, &parsed, jsontext.AllowDuplicateNames(true)); err != nil {
		return Fields{}, err
	}
	return Fields{
		HeaderFields:     parsed.HeaderFields,
		PathFields:       parsed.PathFields,
		DependencyFields: parsed.DependencyFields,
		ContentMapper:    parsed.TypeScript.Value.ContentMapper,
	}, nil
}

// RawOptions mirrors tsoptions.RawCompilerOptions.
type RawOptions struct {
	values *OrderedMap[string, any]
}

func (o *RawOptions) MarshalJSONTo(enc *jsontext.Encoder) error {
	if o == nil || o.values == nil {
		return (&OrderedMap[string, any]{}).MarshalJSONTo(enc)
	}
	return o.values.MarshalJSONTo(enc)
}

func (o *RawOptions) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	if o.values == nil {
		o.values = &OrderedMap[string, any]{}
	}
	token, err := dec.ReadToken()
	if err != nil {
		return err
	}
	if token.Kind() == 'n' {
		return nil
	}
	for dec.PeekKind() != '}' {
		var key string
		if err := json.UnmarshalDecode(dec, &key); err != nil {
			return err
		}
		var value any
		if key == "paths" && dec.PeekKind() == '{' {
			paths := &OrderedMap[string, any]{}
			if err := json.UnmarshalDecode(dec, paths); err != nil {
				return err
			}
			value = paths
		} else if err := json.UnmarshalDecode(dec, &value); err != nil {
			return err
		}
		o.values.Set(key, value)
	}
	_, err = dec.ReadToken()
	return err
}

type Config struct {
	Options *RawOptions `json:"compilerOptions,omitzero"`
	Files   []string    `json:"files,omitzero"`
}

func dumpTokens(input string) {
	dec := jsontext.NewDecoder(strings.NewReader(input))
	var parts []string
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			parts = append(parts, "err:"+fmt.Sprint(err != nil))
			break
		}
		parts = append(parts, tok.Kind().String()+"("+tok.String()+")@"+fmt.Sprint(dec.StackDepth()))
	}
	fmt.Println(strings.Join(parts, " "))
}

func main() {
	pkg := `{
		"name": "pkg", "version": "1.0.0", "type": 5,
		"main": null,
		"types": "./index.d.ts",
		"typesVersions": {">=4.0": {"*": ["ts4/*"]}},
		"exports": {".": {"import": "./a.mjs", "require": ["./a.cjs", null, true, 1.5]}, "./sub": null},
		"dependencies": {"a": "^1", "b": "~2"},
		"devDependencies": {"bad": 1},
		"name": "dup",
		"typescript": {"contentMapper": {"exec": ["node", "x.js"], "dynamicConfig": true}}
	}`
	f, err := Parse([]byte(pkg))
	fmt.Println(err)
	fmt.Println(f.Name, f.Version, f.Type, f.Main)
	fmt.Println(f.Types.Value, f.Types.Valid, f.TypesVersions, f.Exports)
	fmt.Println(f.Dependencies.Valid, len(f.Dependencies.Value), f.Dependencies.Value["a"], f.DevDependencies.Valid, f.DevDependencies.actualJSONType)
	fmt.Println(f.ContentMapper.Valid, f.ContentMapper.Value.Exec.Value, f.ContentMapper.Value.DynamicConfig.Value)

	_, err = Parse([]byte(`{"name": `))
	fmt.Println("truncated", err != nil)
	_, err = Parse([]byte(`[]`))
	fmt.Println("array", err != nil)

	// RawOptions round trip with key order preserved.
	var cfg Config
	err = json.Unmarshal([]byte(`{"compilerOptions":{"target":"es2020","strict":true,"paths":{"@/*":["src/*"],"a":["b"]},"lib":["dom","es5"],"n":null,"num":3},"files":["a.ts"]}`), &cfg)
	fmt.Println(err, cfg.Options.values.keys, cfg.Files)
	b, err := json.Marshal(&cfg)
	fmt.Println(string(b), err)
	b, err = json.Marshal(cfg, jsontext.WithIndent("    "))
	fmt.Println(string(b), err)
	b, err = json.Marshal(Config{})
	fmt.Println(string(b), err)
	b, err = json.Marshal(Config{Options: &RawOptions{}})
	fmt.Println(string(b), err)

	// Tokens and values.
	dumpTokens(`{"a":[1,-2.5e3,"xA",true,false,null,{}],"b":{"c":[]}} 7 "top"`)
	dumpTokens(`[1,]`)
	dec := jsontext.NewDecoder(bytes.NewReader([]byte(`{"skip":{"x":[1,2]},"name":"v","val":[ 1, 2 ] }`)))
	dec.ReadToken()
	for dec.PeekKind() != '}' {
		name, _ := dec.ReadValue()
		key := string(name[1 : len(name)-1])
		if key == "skip" {
			fmt.Println("skip", dec.SkipValue())
			continue
		}
		val, err := dec.ReadValue()
		fmt.Println(key, string(val), val.Kind(), err)
	}
	_, err = dec.ReadToken()
	fmt.Println(err, dec.PeekKind())
	_, err = dec.ReadToken()
	fmt.Println("eof", err)

	// Encoder token API.
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf, jsontext.WithIndent("  "))
	enc.WriteToken(jsontext.BeginObject)
	enc.WriteToken(jsontext.String("k"))
	enc.WriteToken(jsontext.Float(1.5))
	enc.WriteToken(jsontext.String("arr"))
	enc.WriteToken(jsontext.BeginArray)
	enc.WriteToken(jsontext.Int(-3))
	enc.WriteToken(jsontext.Uint(18446744073709551615))
	enc.WriteToken(jsontext.Bool(false))
	enc.WriteToken(jsontext.Null)
	enc.WriteValue(jsontext.Value(` { "z" : "A" } `))
	enc.WriteToken(jsontext.EndArray)
	err = enc.WriteToken(jsontext.Int(1))
	fmt.Println("non-string name", err != nil)
	enc.WriteToken(jsontext.String("k"))
	err = enc.WriteToken(jsontext.Null)
	fmt.Println("dup name", err != nil)
	enc.WriteToken(jsontext.String("k2"))
	enc.WriteToken(jsontext.True)
	enc.WriteToken(jsontext.EndObject)
	enc.WriteToken(jsontext.String("second"))
	fmt.Printf("%q\n", buf.String())

	// Token accessors.
	t := jsontext.Float(2.5)
	fl, _ := t.Float()
	in, ierr := t.Int()
	fmt.Println(t.Kind(), t.String(), fl, in, ierr != nil)
	big := jsontext.Uint(1 << 63)
	_, ierr = big.Int()
	u, _ := big.Uint()
	fmt.Println(ierr != nil, u, jsontext.String("s").String(), jsontext.True.Bool())

	// Value helpers.
	v := jsontext.Value(`{ "b" : 1, "a" : [ true ] }`)
	fmt.Println(v.IsValid(), jsontext.Value(`{"a":1,"a":2}`).IsValid(), jsontext.Value(`{`).IsValid())
	v2 := v.Clone()
	v2.Compact()
	fmt.Println(string(v2))
	v3 := v.Clone()
	v3.Indent()
	fmt.Println(string(v3))
	var nilv jsontext.Value
	b, _ = json.Marshal(nilv)
	fmt.Println(string(b))

	// UnmarshalDecode over a stream.
	sdec := jsontext.NewDecoder(strings.NewReader(`1 "two" [3] {"four":4}`))
	for {
		var x any
		if err := json.UnmarshalDecode(sdec, &x); err != nil {
			fmt.Println("stream end", err)
			break
		}
		fmt.Println(x)
	}
}
