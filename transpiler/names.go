package main

import (
	"fmt"
	"strings"
	"unicode"
)

var luaKeywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true, "end": true,
	"false": true, "for": true, "function": true, "goto": true, "if": true, "in": true,
	"local": true, "nil": true, "not": true, "or": true, "repeat": true, "return": true,
	"then": true, "true": true, "until": true, "while": true, "global": true,
}

// Names reserved by the generated module prologue; Go identifiers with these names are escaped.
var reservedLocals = map[string]bool{
	"rt": true, "P": true, "TY": true, "LT": true, "FN": true, "self": true, "_ENV": true, "arg": true,
	// Module prologue aliases (see prologue in emit.go).
	"setmetatable": true, "getmetatable": true, "rawget": true, "type": true, "select": true,
	"error": true, "sbyte": true, "ssub": true, "fmod": true, "LEN": true, "E": true, "NIL": true, "NILKEY": true,
	"append": true, "append1": true, "mkslice": true, "slice": true, "mset": true, "nn": true,
	"ieq": true, "box": true, "typeof": true, "idiv": true, "call": true,
	"band": true, "bor": true, "bxor": true, "bnot": true, "lshift": true, "rshift": true, "arshift": true, "tobit": true,
	"floor": true, "imul": true, "udiv": true, "I64": true, "U64": true, "bit": true,
	// Lua globals referenced by generated code.
	"math": true, "table": true, "string": true, "coroutine": true, "pcall": true, "xpcall": true,
	"require": true, "next": true, "tostring": true, "tonumber": true, "ipairs": true, "pairs": true,
	"os": true, "io": true, "debug": true, "utf8": true, "unpack": true,
}

// luaIdent turns a Go identifier into a Lua identifier that cannot collide with
// generated names (which always start with "__") or Lua keywords.
func luaIdent(name string) string {
	if name == "_" {
		return "_"
	}
	ascii := true
	for _, r := range name {
		if r > unicode.MaxASCII {
			ascii = false
			break
		}
	}
	if !ascii {
		var b strings.Builder
		b.WriteString("_U")
		for _, r := range name {
			if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				b.WriteRune(r)
			} else {
				fmt.Fprintf(&b, "_%x_", r)
			}
		}
		return b.String()
	}
	if luaKeywords[name] || reservedLocals[name] || strings.HasPrefix(name, "_") {
		return "_E" + name
	}
	return name
}

// fieldKey is the Lua table key used for a struct field; it is always a valid identifier.
func fieldKey(name string) string {
	return luaIdent(name)
}

// methodKey is the key used for dynamic dispatch through metatables. The "_m" prefix
// keeps method keys disjoint from field keys, which never start with a lowercase "_m".
func methodKey(name string) string {
	if !isASCII(name) {
		return "_m" + luaIdent(name)
	}
	return "_m" + name
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// luaModuleName maps a Go package path to a dotted Lua module name.
func luaModuleName(pkgPath string) string {
	var prefix string
	rest := pkgPath
	switch {
	case strings.HasPrefix(pkgPath, "github.com/microsoft/TypeScript/tsc/internal/"):
		prefix = "ts"
		rest = strings.TrimPrefix(pkgPath, "github.com/microsoft/TypeScript/tsc/internal/")
	case strings.HasPrefix(pkgPath, "github.com/microsoft/TypeScript/tsc/"):
		prefix = "tsc"
		rest = strings.TrimPrefix(pkgPath, "github.com/microsoft/TypeScript/tsc/")
	case strings.HasPrefix(pkgPath, "golang.org/x/"):
		prefix = "x"
		rest = strings.TrimPrefix(pkgPath, "golang.org/x/")
	case strings.HasPrefix(pkgPath, "github.com/"):
		prefix = "gh"
		rest = strings.TrimPrefix(pkgPath, "github.com/")
	case !strings.Contains(strings.Split(pkgPath, "/")[0], "."):
		prefix = "go"
	default:
		prefix = "ext"
	}
	parts := strings.Split(rest, "/")
	for i, p := range parts {
		var b strings.Builder
		for _, r := range p {
			if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
				b.WriteRune(r)
			} else {
				b.WriteRune('_')
			}
		}
		parts[i] = b.String()
	}
	return prefix + "." + strings.Join(parts, ".")
}

func luaString(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03d`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('\'')
	return b.String()
}
