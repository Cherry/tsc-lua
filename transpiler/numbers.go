package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"strconv"
)

// numRep is how values of a numeric Go type are held in LuaJIT, whose numbers are doubles and
// whose bit operations work on 32 bits (or on 64-bit FFI integers).
type numRep int

const (
	repNone numRep = iota
	// repFloat: float64 and float32 (rounded to single precision after each operation).
	repFloat
	// repInt: int and the 64-bit ID types in narrow64: the exact value as a double. Go's
	// wraparound at 2^64 is not reproduced; values stay far below 2^53.
	repInt
	// repInt32: int32 and uint32, held as the signed 32-bit pattern, which is what LuaJIT's
	// bit operations return, so flag tests and updates need no normalization.
	repInt32
	// repSmall: int8, int16, uint8 and uint16, held as their value.
	repSmall
	// repInt64 and repUint64: int64_t and uint64_t FFI integers, exact with wraparound
	// (repUint64 also holds uint and uintptr).
	repInt64
	repUint64
)

// narrow64 lists named 64-bit integer types whose values are counters or IDs that stay far
// below 2^53, so they are held as doubles rather than FFI integers, which cost an allocation
// per operation in the interpreter and cannot be table keys.
var narrow64 = map[string]bool{
	"github.com/microsoft/TypeScript/tsc/internal/ast.NodeId":            true,
	"github.com/microsoft/TypeScript/tsc/internal/ast.SymbolId":          true,
	"github.com/microsoft/TypeScript/tsc/internal/ast.sourceFileDataKey": true,
}

// isNarrow64 reports named types listed in narrow64.
func isNarrow64(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return false
	}
	return narrow64[n.Obj().Pkg().Path()+"."+n.Obj().Name()]
}

// repOf classifies the Lua representation of a numeric type (repNone for other types and
// for type parameters, whose operations go through their runtime descriptor).
func repOf(t types.Type) numRep {
	if t == nil || isTypeParam(t) {
		return repNone
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return repNone
	}
	switch b.Kind() {
	case types.Float32, types.Float64, types.UntypedFloat:
		return repFloat
	case types.Int, types.UntypedInt:
		return repInt
	case types.Uint, types.Uintptr:
		// The standard library converts full 64-bit values to uint (strconv, math/bits).
		return repUint64
	case types.Int32, types.Uint32, types.UntypedRune:
		return repInt32
	case types.Int8, types.Int16, types.Uint8, types.Uint16:
		return repSmall
	case types.Int64:
		if isNarrow64(t) {
			return repInt
		}
		return repInt64
	case types.Uint64:
		if isNarrow64(t) {
			return repInt
		}
		return repUint64
	}
	return repNone
}

func is64Rep(r numRep) bool { return r == repInt64 || r == repUint64 }

func basicKindOf(t types.Type) types.BasicKind {
	if b := basicOf(t); b != nil {
		return b.Kind()
	}
	return types.Invalid
}

// intOperand renders an integer expression as a plain Lua number, for indexes, lengths and
// shift counts, which Go accepts in any integer type.
func (fc *funcCtx) intOperand(e ast.Expr) string {
	return asLuaNumber(fc.operand(e), fc.typeOf(e))
}

func asLuaNumber(code string, t types.Type) string {
	switch repOf(t) {
	case repInt64, repUint64:
		return fmt.Sprintf("tonumber(%s)", code)
	case repInt32:
		if basicKindOf(t) == types.Uint32 {
			return fmt.Sprintf("(%s %% 4294967296)", prefixOrParen(code))
		}
	}
	return code
}

// isSmallConst reports a numeric literal below 2^21 in magnitude, whose product with a 32-bit
// value is exact in a double.
func isSmallConst(s string) bool {
	s = trimParens(s)
	v, err := strconv.ParseInt(s, 0, 64)
	return err == nil && v > -(1<<21) && v < 1<<21
}

func trimParens(s string) string {
	for len(s) > 2 && s[0] == '(' && s[len(s)-1] == ')' {
		s = s[1 : len(s)-1]
	}
	return s
}

// constInt returns the value of a constant operand that fits in an int64.
func (fc *funcCtx) constInt(e ast.Expr) (int64, bool) {
	val, _, ok := fc.folded(e)
	if !ok {
		return 0, false
	}
	return constant.Int64Val(constant.ToInt(val))
}

// wrap brings the result of + - * or a conversion back into the representation of t.
func (fc *funcCtx) wrap(code string, t types.Type) string {
	if isTypeParam(t) {
		return fmt.Sprintf("%s.wrap(%s)", fc.typeExpr(t), code)
	}
	switch basicKindOf(t) {
	case types.Uint8:
		return fmt.Sprintf("band(%s, 0xFF)", code)
	case types.Uint16:
		return fmt.Sprintf("band(%s, 0xFFFF)", code)
	case types.Int8:
		return fmt.Sprintf("arshift(lshift(%s, 24), 24)", code)
	case types.Int16:
		return fmt.Sprintf("arshift(lshift(%s, 16), 16)", code)
	case types.Int32, types.Uint32:
		return fmt.Sprintf("tobit(%s)", code)
	case types.Float32:
		return fmt.Sprintf("rt.f32(%s)", code)
	}
	return code
}

func signedKind(t types.Type) bool {
	b := basicOf(t)
	return b == nil || b.Info()&types.IsUnsigned == 0
}

func (fc *funcCtx) arithCode(op token.Token, x, y string, t types.Type) string {
	rep := repOf(t)
	switch op {
	case token.ADD, token.SUB:
		return fc.wrap(fmt.Sprintf("%s %s %s", x, op.String(), y), t)
	case token.MUL:
		if rep == repInt32 && !isSmallConst(x) && !isSmallConst(y) {
			return fmt.Sprintf("imul(%s, %s)", x, y)
		}
		return fc.wrap(fmt.Sprintf("%s * %s", x, y), t)
	case token.QUO:
		switch rep {
		case repFloat:
			return fc.wrap(fmt.Sprintf("%s / %s", x, y), t)
		case repInt64, repUint64:
			return fmt.Sprintf("rt.div64(%s, %s)", x, y)
		case repInt32:
			if !signedKind(t) {
				return fmt.Sprintf("rt.udiv32(%s, %s)", x, y)
			}
		}
		if !signedKind(t) {
			return fmt.Sprintf("udiv(%s, %s)", x, y)
		}
		return fc.wrap(fmt.Sprintf("idiv(%s, %s)", x, y), t)
	case token.REM:
		switch rep {
		case repInt64, repUint64:
			return fmt.Sprintf("rt.mod64(%s, %s)", x, y)
		case repInt32:
			if !signedKind(t) {
				return fmt.Sprintf("rt.umod32(%s, %s)", x, y)
			}
		}
		if !signedKind(t) {
			return fmt.Sprintf("%s %% %s", x, y)
		}
		return fmt.Sprintf("fmod(%s, %s)", x, y)
	case token.AND, token.OR, token.XOR, token.AND_NOT:
		return fc.bitCode(op, x, y, t)
	}
	return "rt.unsupported()"
}

// bitCode renders & | ^ &^. LuaJIT's bit operations take the low 32 bits of a double, so
// 32-bit and smaller types use them directly and int/uint go through 53-bit helpers.
func (fc *funcCtx) bitCode(op token.Token, x, y string, t types.Type) string {
	rep := repOf(t)
	if rep == repInt {
		// Masking with a non-negative 31-bit constant only needs the low 32 bits.
		if op == token.AND {
			if c, err := strconv.ParseInt(trimParens(y), 0, 64); err == nil && c >= 0 && c < 1<<31 {
				return fmt.Sprintf("band(%s, %s)", x, y)
			}
			if c, err := strconv.ParseInt(trimParens(x), 0, 64); err == nil && c >= 0 && c < 1<<31 {
				return fmt.Sprintf("band(%s, %s)", x, y)
			}
		}
		name := map[token.Token]string{token.AND: "iand", token.OR: "ior", token.XOR: "ixor", token.AND_NOT: "iandnot"}[op]
		return fmt.Sprintf("rt.%s(%s, %s)", name, x, y)
	}
	switch op {
	case token.AND:
		return fmt.Sprintf("band(%s, %s)", x, y)
	case token.OR:
		return fmt.Sprintf("bor(%s, %s)", x, y)
	case token.XOR:
		return fmt.Sprintf("bxor(%s, %s)", x, y)
	}
	return fmt.Sprintf("band(%s, bnot(%s))", x, y)
}

func (fc *funcCtx) shift(op token.Token, x string, ye ast.Expr, t types.Type) string {
	if isTypeParam(t) {
		return fmt.Sprintf("%s.op(%s, %s, %s)", fc.typeExpr(t), luaString(op.String()), x, fc.intOperand(ye))
	}
	n := fc.intOperand(ye)
	c, isConst := fc.constInt(ye)
	rep := repOf(t)
	signed := signedKind(t)
	switch rep {
	case repInt:
		if op == token.SHL {
			if isConst && c < 63 {
				return fmt.Sprintf("%s * %d", prefixOrParen(x), int64(1)<<c)
			}
			return fmt.Sprintf("rt.shl(%s, %s)", x, n)
		}
		if isConst && c < 63 {
			return fmt.Sprintf("floor(%s / %d)", prefixOrParen(x), int64(1)<<c)
		}
		return fmt.Sprintf("rt.shr(%s, %s)", x, n)
	case repInt32, repSmall:
		if op == token.SHL {
			if isConst {
				if c >= 32 {
					return "0"
				}
				return fc.wrap(fmt.Sprintf("lshift(%s, %d)", x, c), t)
			}
			return fc.wrap(fmt.Sprintf("rt.shl32(%s, %s)", x, n), t)
		}
		if signed {
			if isConst {
				return fmt.Sprintf("arshift(%s, %d)", x, min(c, 31))
			}
			return fmt.Sprintf("rt.sar32(%s, %s)", x, n)
		}
		if isConst {
			if c >= 32 {
				return "0"
			}
			return fmt.Sprintf("rshift(%s, %d)", x, c)
		}
		return fmt.Sprintf("rt.shr32(%s, %s)", x, n)
	case repInt64, repUint64:
		zero := fc.constant(constant.MakeInt64(0), t)
		if op == token.SHL {
			if isConst {
				if c >= 64 {
					return zero
				}
				return fmt.Sprintf("lshift(%s, %d)", x, c)
			}
			return fmt.Sprintf("rt.shl64(%s, %s)", x, n)
		}
		if signed {
			if isConst {
				return fmt.Sprintf("arshift(%s, %d)", x, min(c, 63))
			}
			return fmt.Sprintf("rt.sar64(%s, %s)", x, n)
		}
		if isConst {
			if c >= 64 {
				return zero
			}
			return fmt.Sprintf("rshift(%s, %d)", x, c)
		}
		return fmt.Sprintf("rt.shr64(%s, %s)", x, n)
	}
	return fmt.Sprintf("rt.unsupported(%s)", x)
}

// negate renders -x.
func (fc *funcCtx) negate(x string, t types.Type) string {
	if isTypeParam(t) {
		return fmt.Sprintf("%s.op('-', %s)", fc.typeExpr(t), x)
	}
	return fc.wrap("-"+x, t)
}

// complement renders ^x.
func (fc *funcCtx) complement(x string, t types.Type) string {
	if isTypeParam(t) {
		return fmt.Sprintf("%s.op('^', %s)", fc.typeExpr(t), x)
	}
	switch repOf(t) {
	case repInt:
		if signedKind(t) {
			return fmt.Sprintf("(-%s - 1)", prefixOrParen(x))
		}
		return fmt.Sprintf("rt.unot(%s)", x)
	case repSmall:
		if !signedKind(t) {
			return fc.wrap(fmt.Sprintf("bnot(%s)", x), t)
		}
	}
	return fmt.Sprintf("bnot(%s)", x)
}

// compare renders an ordering comparison of two operands of type t.
func (fc *funcCtx) compare(op token.Token, xe, ye ast.Expr, x, y string, t types.Type) string {
	if basicKindOf(t) == types.Uint32 && !isTypeParam(t) {
		// A value of 2^31 or more is negative in its 32-bit pattern; against a constant below
		// 2^31, the sign test settles that case without a call.
		if code, ok := fc.unsignedConstCompare(op, xe, ye, x, y); ok {
			return code
		}
		return fmt.Sprintf("%s %% 4294967296 %s %s %% 4294967296", prefixOrParen(x), op.String(), prefixOrParen(y))
	}
	return fmt.Sprintf("%s %s %s", x, op.String(), y)
}

// unsignedConstCompare emits a uint32 ordering comparison where one side is a simple variable
// and the other a constant in [0, 2^31).
func (fc *funcCtx) unsignedConstCompare(op token.Token, xe, ye ast.Expr, x, y string) (string, bool) {
	small := func(e ast.Expr) bool {
		v, ok := fc.constInt(e)
		return ok && v >= 0 && v < 1<<31
	}
	// Normalize to "variable OP constant".
	switch {
	case small(ye) && isSimple(x):
	case small(xe) && isSimple(y):
		x, y = y, x
		switch op {
		case token.LSS:
			op = token.GTR
		case token.GTR:
			op = token.LSS
		case token.LEQ:
			op = token.GEQ
		case token.GEQ:
			op = token.LEQ
		}
	default:
		return "", false
	}
	switch op {
	case token.LSS, token.LEQ:
		return fmt.Sprintf("(%s >= 0 and %s %s %s)", x, x, op.String(), y), true
	default:
		return fmt.Sprintf("(%s < 0 or %s %s %s)", x, x, op.String(), y), true
	}
}

// intConstant renders an integer constant in the representation of t.
func intConstant(iv constant.Value, t types.Type) string {
	switch repOf(t) {
	case repInt64:
		if v, ok := constant.Int64Val(iv); ok {
			if v == math.MinInt64 {
				return "(-0x7FFFFFFFFFFFFFFFLL - 1)"
			}
			if v < 0 {
				return fmt.Sprintf("(%dLL)", v)
			}
			return fmt.Sprintf("%dLL", v)
		}
	case repUint64:
		if v, ok := constant.Uint64Val(iv); ok {
			return fmt.Sprintf("%dULL", v)
		}
		// A negative constant only reaches here through unsafe conversions; keep its bits.
		if v, ok := constant.Int64Val(iv); ok {
			return fmt.Sprintf("%dULL", uint64(v))
		}
	case repInt32:
		if v, ok := constant.Int64Val(iv); ok {
			v = int64(int32(uint32(v)))
			if v < 0 {
				return fmt.Sprintf("(%d)", v)
			}
			return strconv.FormatInt(v, 10)
		}
	}
	if i, ok := constant.Int64Val(iv); ok {
		if i < 0 {
			return "(" + strconv.FormatInt(i, 10) + ")"
		}
		return strconv.FormatInt(i, 10)
	}
	if u, ok := constant.Uint64Val(iv); ok {
		return strconv.FormatUint(u, 10)
	}
	return "0"
}

// genericConst renders a constant passed to a type parameter's T.const; values beyond 2^53
// travel as FFI integers so they keep every bit.
func genericConst(iv constant.Value) string {
	if v, ok := constant.Int64Val(iv); ok {
		if v > 1<<53 || v < -(1<<53) {
			return fmt.Sprintf("(%dLL)", v)
		}
		return intConstant(iv, nil)
	}
	if u, ok := constant.Uint64Val(iv); ok {
		return fmt.Sprintf("%dULL", u)
	}
	return "0"
}

// numConversion converts a numeric value between Go types.
func (fc *funcCtx) numConversion(code string, from, to types.Type) string {
	fr, tr := repOf(from), repOf(to)
	fk, tk := basicKindOf(from), basicKindOf(to)
	if fr == repFloat {
		switch tr {
		case repFloat:
			if tk == types.Float32 && fk != types.Float32 {
				return fmt.Sprintf("rt.f32(%s)", code)
			}
			return code
		case repInt64:
			return fmt.Sprintf("I64(%s)", code)
		case repUint64:
			return fmt.Sprintf("U64(%s)", code)
		}
		if !signedKind(to) && tr == repInt {
			return fmt.Sprintf("rt.f2u(%s)", code)
		}
		return fc.wrap(fmt.Sprintf("rt.f2i(%s)", code), to)
	}
	// From here, from is an integer type. Read it as a plain Lua number when the target is.
	switch tr {
	case repFloat:
		n := asLuaNumber(code, from)
		if tk == types.Float32 {
			return fmt.Sprintf("rt.f32(%s)", n)
		}
		return n
	case repInt64, repUint64:
		if fr == tr {
			return code
		}
		ctor := "I64"
		if tr == repUint64 {
			ctor = "U64"
		}
		if is64Rep(fr) {
			return fmt.Sprintf("%s(%s)", ctor, code)
		}
		return fmt.Sprintf("%s(%s)", ctor, asLuaNumber(code, from))
	case repInt:
		switch {
		case fr == repInt64:
			return fmt.Sprintf("tonumber(%s)", code)
		case fr == repUint64:
			if signedKind(to) {
				return fmt.Sprintf("tonumber(I64(%s))", code)
			}
			return fmt.Sprintf("tonumber(%s)", code)
		case fk == types.Uint32:
			return asLuaNumber(code, from)
		}
		if fr != repInt {
			// Smaller types fit in int and uint64 ID types unchanged.
			return code
		}
		// Between int and uint, a value out of the other's range moves by 2^64.
		if signedKind(from) && !signedKind(to) {
			return fmt.Sprintf("rt.i2u(%s)", code)
		}
		if !signedKind(from) && signedKind(to) {
			return fmt.Sprintf("rt.u2i(%s)", code)
		}
		return code
	case repInt32, repSmall:
		if is64Rep(fr) {
			code = fmt.Sprintf("rt.lo32(%s)", code)
			fr = repInt32
		}
		if fr == repInt32 && tr == repInt32 {
			return code
		}
		if fr == repSmall && sameSmallRange(fk, tk) {
			return code
		}
		return fc.wrap(code, to)
	}
	return code
}

// sameSmallRange reports conversions from an 8 or 16-bit type whose values the target holds unchanged.
func sameSmallRange(from, to types.BasicKind) bool {
	size := map[types.BasicKind]int{types.Int8: 8, types.Int16: 16, types.Int32: 32, types.Uint8: 8, types.Uint16: 16, types.Uint32: 32}
	signed := map[types.BasicKind]bool{types.Int8: true, types.Int16: true, types.Int32: true}
	fs, ts := size[from], size[to]
	switch {
	case signed[from] == signed[to]:
		return fs <= ts
	case !signed[from] && signed[to]:
		return fs < ts
	}
	// Negative values of a small signed type already have their uint32 bit pattern.
	return to == types.Uint32
}

// switchCaseValue returns the Lua value a switch tree compares the tag with, or false when
// the tag's representation does not order like its Go values.
func switchCaseValue(v int64, t types.Type) (int64, bool) {
	switch repOf(t) {
	case repInt64, repUint64:
		return 0, false
	case repInt32:
		return int64(int32(uint32(v))), true
	}
	return v, v > -(1<<53) && v < 1<<53
}
