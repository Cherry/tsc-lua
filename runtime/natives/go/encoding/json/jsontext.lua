-- Native encoding/json/jsontext: streaming JSON tokenizer and formatter over Lua strings.
-- deps: io errors strconv encoding/json/internal/jsonopts
local rt = require('rt')
local P = rt.package('encoding/json/jsontext')
local goio = rt.import('go.io')
local errors = rt.import('go.errors')
local strconv = rt.import('go.strconv')

local sbyte, ssub, sfind, schar, srep, sgsub, sformat =
	string.byte, string.sub, string.find, string.char, string.rep, string.gsub, string.format
local tconcat = table.concat
local setmetatable, getmetatable, pcall, error, type = setmetatable, getmetatable, pcall, error, type
local utf8valid, utf8char = rt.utf8valid, rt.runestr
local PKG = 'encoding/json/jsontext'

-- X holds internals shared with the native encoding/json/v2 package.
local X = {}
P.__impl = X

---------------------------------------------------------------------------
-- Error propagation: internal code raises JErr tables, API boundaries convert them to Go errors.

local JErr = { __name = 'jsonerror' }
local function throw(err)
	error(setmetatable({ err = err }, JErr), 0)
end
X.throw = throw

local function catch(ok, a, ...)
	if ok then
		return nil, a, ...
	end
	if getmetatable(a) == JErr then
		return a.err
	end
	error(a, 0)
end
-- try runs f and returns (err, results...).
local function try(f, ...)
	return catch(pcall(f, ...))
end
X.try = try

---------------------------------------------------------------------------
-- Types

P.Kind = rt.named(PKG, 'jsontext.Kind', 'uint8')
rt.define(P.Kind, rt.uint8)

local function quoteRune(c)
	if c >= 32 and c < 127 then
		if c == 39 then
			return "'\\''"
		end
		return "'" .. schar(c) .. "'"
	end
	return sformat("'\\x%02x'", c)
end
X.quoteRune = quoteRune

local kindNames = {
	[0] = 'invalid',
	[110] = 'null',
	[102] = 'false',
	[116] = 'true',
	[34] = 'string',
	[48] = 'number',
	[123] = '{',
	[125] = '}',
	[91] = '[',
	[93] = ']',
}
local function kindString(k)
	return kindNames[k] or ('<invalid jsontext.Kind: ' .. quoteRune(k) .. '>')
end
P.Kind__String = kindString
rt.methods(P.Kind, { _mString = kindString }, {
	_mString = function(p)
		return kindString(p.v)
	end,
})

P.Value = rt.named(PKG, 'jsontext.Value', 'slice')
rt.define(P.Value, rt.sliceOf(rt.uint8))

P.Pointer = rt.named(PKG, 'jsontext.Pointer', 'string')
rt.define(P.Pointer, rt.string)

P.Options = rt.import('go.encoding.json.internal.jsonopts').Options

P.Token = rt.named(PKG, 'jsontext.Token', 'struct')
rt.defineStruct(P.Token, { { 'k', rt.uint8, 'k', 0 }, { 's', rt.string, 's', 0 } })
local tokenMT = P.Token.pmt
local function tok(k, s)
	return setmetatable({ k = k, s = s }, tokenMT)
end
X.tok = tok

P.Null = tok(110, '')
P.False = tok(102, '')
P.True = tok(116, '')
P.BeginObject = tok(123, '')
P.EndObject = tok(125, '')
P.BeginArray = tok(91, '')
P.EndArray = tok(93, '')

---------------------------------------------------------------------------
-- Errors

P.SyntacticError = rt.named(PKG, 'jsontext.SyntacticError', 'struct')
rt.defineStruct(P.SyntacticError, {
	{ 'ByteOffset', rt.int64, 'ByteOffset', 0 },
	{ 'JSONPointer', P.Pointer, 'JSONPointer', 0 },
	{ 'Err', rt.error, 'Err', 0 },
})

P.ErrDuplicateName = errors.New('duplicate object member name')
P.ErrNonStringName = errors.New('object member name must be a string')
local errInvalidUTF8 = errors.New('invalid UTF-8')
local errMismatchDelim = errors.New('mismatching structural token for object or array')
local errInvalidToken = errors.New('invalid jsontext.Token')

local function goQuote(s)
	return strconv.Quote(s)
end

local function pointerLastToken(p)
	local i = 0
	for j = #p, 1, -1 do
		if sbyte(p, j) == 47 then
			i = j
			break
		end
	end
	return (sgsub(ssub(p, i + 1), '~[01]', { ['~1'] = '/', ['~0'] = '~' }))
end

local function pointerParent(p)
	for j = #p, 1, -1 do
		if sbyte(p, j) == 47 then
			return ssub(p, 1, j - 1)
		end
	end
	return ''
end

local function syntacticErrorString(e)
	local b = { 'jsontext: ' }
	local ptr, off = e.JSONPointer, e.ByteOffset
	if e.Err ~= nil then
		b[#b + 1] = e.Err:_mError()
		if e.Err == P.ErrDuplicateName then
			b[#b + 1] = ' ' .. goQuote(pointerLastToken(ptr))
			ptr = pointerParent(ptr)
			off = 0
		end
	else
		b[#b + 1] = 'syntactic error'
	end
	if ptr ~= '' then
		b[#b + 1] = ' within ' .. goQuote(ptr)
	end
	if off > 0 then
		b[#b + 1] = ' after offset ' .. off
	end
	return tconcat(b)
end
P.SyntacticError__Error = syntacticErrorString
local function syntacticErrorUnwrap(e)
	return e.Err
end
P.SyntacticError__Unwrap = syntacticErrorUnwrap
rt.methods(P.SyntacticError, {}, { _mError = syntacticErrorString, _mUnwrap = syntacticErrorUnwrap })

local function escapePointerToken(s)
	return (sgsub(s, '[~/]', { ['~'] = '~0', ['/'] = '~1' }))
end

---------------------------------------------------------------------------
-- Options: option values are tables {k = flag, v = value}, {list = {...}} or {snap = flags}.

local Opt = rt.named(PKG, 'jsonopts.option', 'struct')
rt.defineStruct(Opt, {})
local function noop() end
rt.methods(Opt, { _mJSONOptions = noop }, { _mJSONOptions = noop })
local optMT = Opt.pmt
local function mkopt(k, v)
	return setmetatable({ k = k, v = v }, optMT)
end
X.mkopt = mkopt
local function snapopt(flags)
	local c = {}
	for k, v in pairs(flags) do
		c[k] = v
	end
	return setmetatable({ snap = c }, optMT)
end
X.snapopt = snapopt
local function listopt(list)
	return setmetatable({ list = list }, optMT)
end
X.listopt = listopt

local function applyOne(o, x)
	if x == nil then
		return
	end
	if x.k ~= nil then
		o[x.k] = x.v
		if x.k == 'Indent' or x.k == 'IndentPrefix' then
			o.Multiline = true
		end
	elseif x.list then
		for _, y in ipairs(x.list) do
			applyOne(o, y)
		end
	elseif x.snap then
		for k, v in pairs(x.snap) do
			o[k] = v
		end
	end
end
X.applyOne = applyOne

-- applyOpts merges a Go slice of Options into the flags table o.
local function applyOpts(o, opts)
	if opts ~= nil then
		local a, off = opts[1], opts[2]
		for i = 0, opts[3] - 1 do
			applyOne(o, a[off + i])
		end
	end
	return o
end
X.applyOpts = applyOpts

local setterKeys = {}
X.setterKeys = setterKeys
local function boolOption(name)
	local f = function(v)
		return mkopt(name, v)
	end
	P[name] = f
	setterKeys[f] = name
end
for _, name in ipairs({
	'AllowDuplicateNames',
	'AllowInvalidUTF8',
	'EscapeForHTML',
	'EscapeForJS',
	'PreserveRawStrings',
	'CanonicalizeRawInts',
	'CanonicalizeRawFloats',
	'ReorderRawObjects',
	'SpaceAfterColon',
	'SpaceAfterComma',
	'Multiline',
}) do
	boolOption(name)
end

local function checkIndent(s, what)
	local bad = sfind(s, '[^ \t]')
	if bad then
		rt.panic('json: invalid character ' .. quoteRune(sbyte(s, bad)) .. ' in indent' .. what)
	end
end

function P.WithIndent(indent)
	checkIndent(indent, '')
	return mkopt('Indent', indent)
end
setterKeys[P.WithIndent] = 'Indent'

function P.WithIndentPrefix(prefix)
	checkIndent(prefix, ' prefix')
	return mkopt('IndentPrefix', prefix)
end
setterKeys[P.WithIndentPrefix] = 'IndentPrefix'

---------------------------------------------------------------------------
-- String quoting (encoding)

local asciiEsc = {
	['"'] = '\\"',
	['\\'] = '\\\\',
	['\b'] = '\\b',
	['\f'] = '\\f',
	['\n'] = '\\n',
	['\r'] = '\\r',
	['\t'] = '\\t',
}
for c = 0, 31 do
	local ch = schar(c)
	if not asciiEsc[ch] then
		asciiEsc[ch] = sformat('\\u%04x', c)
	end
end
asciiEsc['<'] = '\\u003c'
asciiEsc['>'] = '\\u003e'
asciiEsc['&'] = '\\u0026'

-- fixUTF8 replaces invalid UTF-8 bytes with U+FFFD, like Go's rune decoding.
local function fixUTF8(s)
	local out, n, i, last = {}, 0, 1, 1
	local len = #s
	local decode = rt.decodeRune
	while i <= len do
		local c = sbyte(s, i)
		if c < 0x80 then
			i = i + 1
		else
			local r, w = decode(s, i)
			if r == 0xFFFD and w == 1 then
				n = n + 1
				out[n] = ssub(s, last, i - 1) .. '\239\191\189'
				last = i + 1
			end
			i = i + w
		end
	end
	n = n + 1
	out[n] = ssub(s, last)
	return tconcat(out)
end
X.fixUTF8 = fixUTF8

-- quote returns s as a JSON string literal; flags is an options table.
local function quote(s, flags)
	-- LuaJIT patterns end at a NUL byte, so %z stands for it.
	if not sfind(s, '[%z\1-\31"\\<>&\128-\255]') then
		return '"' .. s .. '"'
	end
	if sfind(s, '[\128-\255]') then
		if not utf8valid(s) then
			if not flags.AllowInvalidUTF8 then
				return nil, errInvalidUTF8
			end
			s = fixUTF8(s)
		end
		if flags.EscapeForJS then
			s = sgsub(s, '\226\128[\168\169]', { ['\226\128\168'] = '\\u2028', ['\226\128\169'] = '\\u2029' })
		end
	end
	if flags.EscapeForHTML then
		s = sgsub(s, '[%z\1-\31"\\<>&]', asciiEsc)
	else
		s = sgsub(s, '[%z\1-\31"\\]', asciiEsc)
	end
	return '"' .. s .. '"'
end
X.quote = quote

---------------------------------------------------------------------------
-- Number formatting

local function fmtUint(v)
	return rt.itoa(v)
end
X.fmtUint = fmtUint

-- shortestDigits returns the shortest decimal digits that round-trip a (finite, positive) and the
-- decimal exponent of the first digit; the nearest N-digit decimal round-trips whenever any does.
local function shortestDigits(a, bits)
	local f32 = rt.f32
	local s
	for p = 0, 16 do
		s = sformat('%.' .. p .. 'e', a)
		local back = tonumber(s)
		if bits == 32 then
			back = f32(back)
		end
		if back == a then
			break
		end
	end
	local d1, frac, exp = s:match('^(%d)%.?(%d*)e([-+]%d+)$')
	local digits = sgsub(d1 .. frac, '0+$', '')
	if digits == '' then
		digits = '0'
	end
	return digits, tonumber(exp)
end

-- fmtFloat matches jsonwire.AppendFloat (ES6 number formatting).
local function fmtFloat(f, bits)
	if bits == 32 then
		f = rt.f32(f)
	end
	if f == 0 then
		if 1 / f < 0 then
			return '-0'
		end
		return '0'
	end
	local a = f < 0 and -f or f
	local sign = f < 0 and '-' or ''
	if a < 9007199254740992.0 and f == math.floor(f) then
		return sformat('%d', f)
	end
	local digits, exp = shortestDigits(a, bits)
	local n = #digits
	if a < 1e-6 or a >= 1e21 then
		local mant = ssub(digits, 1, 1)
		if n > 1 then
			mant = mant .. '.' .. ssub(digits, 2)
		end
		return sign .. mant .. 'e' .. (exp < 0 and '-' or '+') .. (exp < 0 and -exp or exp)
	end
	if exp >= n - 1 then
		return sign .. digits .. srep('0', exp - n + 1)
	elseif exp >= 0 then
		return sign .. ssub(digits, 1, exp + 1) .. '.' .. ssub(digits, exp + 2)
	end
	return sign .. '0.' .. srep('0', -exp - 1) .. digits
end
X.fmtFloat = fmtFloat

---------------------------------------------------------------------------
-- Token constructors and accessors

function P.Bool(b)
	if b then
		return P.True
	end
	return P.False
end

function P.String(s)
	return tok(34, s)
end

function P.Int(n)
	return tok(48, rt.itoa(n))
end

function P.Uint(n)
	return tok(48, fmtUint(n))
end

local function floatToken(f, bits)
	if f ~= f then
		return tok(34, 'NaN')
	elseif f == math.huge then
		return tok(34, 'Infinity')
	elseif f == -math.huge then
		return tok(34, '-Infinity')
	end
	return tok(48, fmtFloat(f, bits))
end

function P.Float(f)
	return floatToken(f, 64)
end

function P.Float32(f)
	return floatToken(f, 32)
end

local function tokenKind(t)
	return t.k
end
P.Token__Kind = tokenKind

local function tokenString(t)
	local k = t.k
	if k == 34 or k == 48 then
		return t.s
	elseif k == 0 then
		return '<invalid jsontext.Token>'
	end
	return kindNames[k]
end
P.Token__String = tokenString

local function tokenClone(t)
	return tok(t.k, t.s)
end
P.Token__Clone = tokenClone

local function badKind(t)
	rt.panic('invalid JSON token kind: ' .. kindString(t.k))
end

function P.Token__Bool(t)
	if t.k == 116 then
		return true
	elseif t.k == 102 then
		return false
	end
	badKind(t)
end

P.numError = rt.named(PKG, 'jsontext.numError', 'struct')
rt.defineStruct(P.numError, {
	{ 'accessor', rt.string, 'accessor', 0 },
	{ 'value', rt.string, 'value', 0 },
	{ 'err', rt.error, 'err', 0 },
})
rt.methods(P.numError, {}, {
	_mError = function(e)
		return 'jsontext.Token(' .. e.value .. ').' .. e.accessor .. ' error: ' .. e.err:_mError()
	end,
	_mUnwrap = function(e)
		return e.err
	end,
})
local function numError(acc, value, err)
	return setmetatable({ accessor = acc, value = value, err = err }, P.numError.pmt)
end

local U64, I64 = rt.U64, rt.I64
local MAXINT64 = 0x7FFFFFFFFFFFFFFFLL
local MININT64 = -MAXINT64 - 1
local MAXUINT64 = 0xFFFFFFFFFFFFFFFFULL

-- parseUintDigits parses a string of decimal digits into a uint64 FFI integer; returns value,
-- overflowed.
local function parseUintDigits(s)
	local st = sfind(s, '[^0]')
	if not st then
		return U64(0), false
	end
	local t = ssub(s, st)
	if #t > 20 or (#t == 20 and t > '18446744073709551615') then
		return MAXUINT64, true
	end
	if #t < 16 then
		return U64(tonumber(t)), false
	end
	local v = U64(0)
	for i = 1, #t do
		v = v * 10 + (sbyte(t, i) - 48)
	end
	return v, false
end
X.parseUintDigits = parseUintDigits

local function floatToInt(f)
	if f ~= f then
		return I64(0)
	elseif f >= 9223372036854775808.0 then
		return MAXINT64
	elseif f < -9223372036854775808.0 then
		return MININT64
	end
	return I64(f)
end

function P.Token__Int(t)
	if t.k ~= 48 then
		badKind(t)
	end
	local s = t.s
	local neg = sbyte(s, 1) == 45
	local digits = neg and ssub(s, 2) or s
	if not sfind(digits, '[^%d]') then
		local v, over = parseUintDigits(digits)
		if neg then
			if over or v > 9223372036854775808ULL then
				return MININT64, numError('Int', s, strconv.ErrRange)
			end
			return -I64(v), nil
		end
		if over or v > 9223372036854775807ULL then
			return MAXINT64, numError('Int', s, strconv.ErrRange)
		end
		return I64(v), nil
	end
	return floatToInt(tonumber(s)), numError('Int', s, strconv.ErrSyntax)
end

function P.Token__Uint(t)
	if t.k ~= 48 then
		badKind(t)
	end
	local s = t.s
	if not sfind(s, '[^%d]') then
		local v, over = parseUintDigits(s)
		if over then
			return MAXUINT64, numError('Uint', s, strconv.ErrRange)
		end
		return v, nil
	end
	local f = tonumber(s)
	local u
	if f ~= f or f < 0 then
		u = U64(0)
	elseif f >= 18446744073709551616.0 then
		u = MAXUINT64
	else
		u = U64(f)
	end
	return u, numError('Uint', s, strconv.ErrSyntax)
end

local function tokenFloat(t, bits)
	if t.k == 48 then
		local f = tonumber(t.s) + 0.0
		if bits == 32 then
			local g = rt.f32(f)
			if g == math.huge or g == -math.huge then
				return g, numError('Float', t.s, strconv.ErrRange)
			end
			return g, nil
		end
		if f == math.huge or f == -math.huge then
			return f, numError('Float', t.s, strconv.ErrRange)
		end
		return f, nil
	elseif t.k == 34 then
		if t.s == 'NaN' then
			return 0.0 / 0.0, nil
		elseif t.s == 'Infinity' then
			return math.huge, nil
		elseif t.s == '-Infinity' then
			return -math.huge, nil
		end
	end
	badKind(t)
end

function P.Token__Float(t)
	return tokenFloat(t, 64)
end

function P.Token__Float32(t)
	return tokenFloat(t, 32)
end

rt.methods(P.Token, {
	_mBool = P.Token__Bool,
	_mClone = tokenClone,
	_mFloat = P.Token__Float,
	_mFloat32 = P.Token__Float32,
	_mInt = P.Token__Int,
	_mKind = tokenKind,
	_mString = tokenString,
	_mUint = P.Token__Uint,
}, {
	_mBool = P.Token__Bool,
	_mClone = tokenClone,
	_mFloat = P.Token__Float,
	_mFloat32 = P.Token__Float32,
	_mInt = P.Token__Int,
	_mKind = tokenKind,
	_mString = tokenString,
	_mUint = P.Token__Uint,
})

---------------------------------------------------------------------------
-- Decoder
--
-- Fields: s (input), p (next byte, 1-based), depth, fk/fn/fw/fnm/fd (per depth: kind 1=object 2=array,
-- completed member count, expecting a member value, last member name, seen names), pk (peeked kind,
-- with p at that token), o (options), dup (check duplicate names), utf (allow invalid UTF-8).

P.Decoder = rt.named(PKG, 'jsontext.Decoder', 'struct')
rt.defineStruct(P.Decoder, {})
local decMT = P.Decoder.pmt

local function dconfig(d)
	local o = d.o
	d.dup = not o.AllowDuplicateNames
	d.utf = o.AllowInvalidUTF8 and true or false
end
X.dconfig = dconfig

local function newDecoder(s, o)
	local d = setmetatable({ s = s, p = 1, depth = 0, fk = {}, fn = { [0] = 0 }, fw = {}, fnm = {}, fd = {}, o = o }, decMT)
	dconfig(d)
	return d
end
X.newDecoder = newDecoder

local function readAll(r)
	local b, err = goio.ReadAll(r)
	if err ~= nil then
		return rt.bytestr(b), err
	end
	return rt.bytestr(b), nil
end

function P.NewDecoder(r, opts)
	local s, err = readAll(r)
	local d = newDecoder(s, applyOpts({}, opts))
	d.rerr = err
	return d
end

function P.Decoder__Reset(d, r, opts)
	local s, err = readAll(r)
	local nd = newDecoder(s, applyOpts({}, opts))
	nd.rerr = err
	for k in pairs(d) do
		d[k] = nil
	end
	for k, v in pairs(nd) do
		d[k] = v
	end
end

local function decPointer(d)
	local parts = {}
	for i = 1, d.depth do
		if d.fk[i] == 1 then
			if d.fw[i] or d.fn[i] > 0 then
				parts[#parts + 1] = '/' .. escapePointerToken(d.fnm[i] or '')
			end
		elseif d.fn[i] > 0 or true then
			parts[#parts + 1] = '/' .. d.fn[i]
		end
	end
	return tconcat(parts)
end
X.decPointer = decPointer

local function synerr(d, p, err)
	throw(setmetatable({ ByteOffset = p - 1, JSONPointer = decPointer(d), Err = err }, P.SyntacticError.pmt))
end
X.synerr = synerr

local function invalidChar(d, p, where)
	local c = sbyte(d.s, p)
	if c == nil then
		if d.rerr ~= nil then
			throw(d.rerr)
		end
		synerr(d, p, goio.ErrUnexpectedEOF)
	end
	local what
	if c >= 0x80 then
		local _, w = rt.decodeRune(d.s, p)
		what = "'" .. ssub(d.s, p, p + w - 1) .. "'"
	else
		what = quoteRune(c)
	end
	synerr(d, p, errors.New('invalid character ' .. what .. ' ' .. where))
end
X.invalidChar = invalidChar

local KIND = { [34] = 34, [45] = 48, [110] = 110, [116] = 116, [102] = 102, [123] = 123, [125] = 125, [91] = 91, [93] = 93 }
for c = 48, 57 do
	KIND[c] = 48
end

local function ws(s, p)
	local c = sbyte(s, p)
	if c == 32 or c == 10 or c == 13 or c == 9 then
		return sfind(s, '[^ \t\r\n]', p + 1) or (#s + 1)
	end
	return p
end
X.ws = ws

-- peek positions d.p at the next token (consuming separators) and returns its kind.
local function peek(d)
	local k = d.pk
	if k then
		return k
	end
	local s = d.s
	local p = ws(s, d.p)
	local c = sbyte(s, p)
	local depth = d.depth
	if depth > 0 then
		if d.fk[depth] == 1 then
			if d.fw[depth] then
				if c ~= 58 then
					invalidChar(d, p, "after object name (expecting ':')")
				end
				p = ws(s, p + 1)
				c = sbyte(s, p)
			elseif c == 125 then
				d.p = p
				d.pk = 125
				return 125
			else
				if d.fn[depth] > 0 then
					if c ~= 44 then
						invalidChar(d, p, "after object value (expecting ',' or '}')")
					end
					p = ws(s, p + 1)
					c = sbyte(s, p)
				end
				if c ~= 34 then
					invalidChar(d, p, "at start of string (expecting '\"')")
				end
			end
		else
			if c == 93 then
				d.p = p
				d.pk = 93
				return 93
			end
			if d.fn[depth] > 0 then
				if c ~= 44 then
					invalidChar(d, p, "after array element (expecting ',' or ']')")
				end
				p = ws(s, p + 1)
				c = sbyte(s, p)
			end
		end
	end
	if c == nil then
		if d.rerr ~= nil then
			throw(d.rerr)
		end
		if depth == 0 then
			throw(goio.EOF)
		end
		synerr(d, p, goio.ErrUnexpectedEOF)
	end
	k = KIND[c]
	if k == nil or k == 125 or k == 93 then
		if k ~= nil then
			synerr(d, p, errMismatchDelim)
		end
		invalidChar(d, p, 'at start of value')
	end
	d.p = p
	d.pk = k
	return k
end
X.peek = peek

local function done(d)
	local depth = d.depth
	if depth > 0 and d.fk[depth] == 1 then
		d.fw[depth] = false
	end
	d.fn[depth] = d.fn[depth] + 1
end
X.done = done

local unesc = { [34] = '"', [92] = '\\', [47] = '/', [98] = '\b', [102] = '\f', [110] = '\n', [114] = '\r', [116] = '\t' }

local function checkUTF8(d, v, p)
	if sfind(v, '[\128-\255]') and not utf8valid(v) then
		if not d.utf then
			synerr(d, p, errInvalidUTF8)
		end
		return fixUTF8(v)
	end
	return v
end

local function hex4(d, s, i)
	local h = ssub(s, i, i + 3)
	if #h < 4 then
		synerr(d, #s + 1, goio.ErrUnexpectedEOF)
	end
	if not sfind(h, '^%x%x%x%x$') then
		synerr(d, i - 2, errors.New('invalid escape sequence ' .. goQuote('\\u' .. h) .. ' in string'))
	end
	return tonumber(h, 16)
end

local function readStringSlow(d, p)
	local s = d.s
	local parts, n = {}, 0
	local i = p + 1
	while true do
		local q = sfind(s, '[%z\1-\31"\\]', i)
		if not q then
			synerr(d, #s + 1, goio.ErrUnexpectedEOF)
		end
		if q > i then
			n = n + 1
			parts[n] = checkUTF8(d, ssub(s, i, q - 1), i)
		end
		local c = sbyte(s, q)
		if c == 34 then
			return tconcat(parts), q + 1
		elseif c == 92 then
			local e = sbyte(s, q + 1)
			local r = unesc[e]
			if r then
				n = n + 1
				parts[n] = r
				i = q + 2
			elseif e == 117 then
				local cp = hex4(d, s, q + 2)
				i = q + 6
				if cp >= 0xD800 and cp < 0xDC00 then
					if ssub(s, i, i + 1) == '\\u' then
						local lo = hex4(d, s, i + 2)
						if lo >= 0xDC00 and lo < 0xE000 then
							cp = 0x10000 + (cp - 0xD800) * 1024 + (lo - 0xDC00)
							i = i + 6
						else
							cp = -1
						end
					else
						cp = -1
					end
				elseif cp >= 0xDC00 and cp < 0xE000 then
					cp = -1
				end
				if cp < 0 then
					if not d.utf then
						synerr(d, q, errors.New('invalid surrogate in string'))
					end
					cp = 0xFFFD
				end
				n = n + 1
				parts[n] = utf8char(cp)
			elseif e == nil then
				synerr(d, #s + 1, goio.ErrUnexpectedEOF)
			else
				synerr(d, q, errors.New('invalid escape sequence ' .. goQuote(ssub(s, q, q + 1)) .. ' in string'))
			end
		else
			synerr(d, q, errors.New('invalid character ' .. quoteRune(c) .. ' in string (expecting non-control character)'))
		end
	end
end

-- readString decodes the JSON string starting at p (a '"'); returns value and the next position.
local function readString(d, p)
	local s = d.s
	local q = sfind(s, '[%z\1-\31"\\]', p + 1)
	if q and sbyte(s, q) == 34 then
		local v = ssub(s, p + 1, q - 1)
		if sfind(v, '[\128-\255]') and not utf8valid(v) then
			v = checkUTF8(d, v, p + 1)
		end
		return v, q + 1
	end
	return readStringSlow(d, p)
end
X.readString = readString

local function scanNumber(d, p)
	local s = d.s
	local q = p
	if sbyte(s, q) == 45 then
		q = q + 1
	end
	local c = sbyte(s, q)
	if c == 48 then
		q = q + 1
	elseif c and c >= 49 and c <= 57 then
		local _, e = sfind(s, '^%d*', q + 1)
		q = e + 1
	else
		invalidChar(d, q, 'in number (expecting digit)')
	end
	if sbyte(s, q) == 46 then
		local _, e = sfind(s, '^%d+', q + 1)
		if not e then
			invalidChar(d, q + 1, 'in number (expecting digit)')
		end
		q = e + 1
	end
	c = sbyte(s, q)
	if c == 101 or c == 69 then
		q = q + 1
		c = sbyte(s, q)
		if c == 43 or c == 45 then
			q = q + 1
		end
		local _, e = sfind(s, '^%d+', q)
		if not e then
			invalidChar(d, q, 'in number (expecting digit)')
		end
		q = e + 1
	end
	return q
end
X.scanNumber = scanNumber

local lits = { [110] = 'null', [116] = 'true', [102] = 'false' }
local function scanLit(d, p, k)
	local lit = lits[k]
	local n = #lit
	local got = ssub(d.s, p, p + n - 1)
	if got ~= lit then
		if #got < n and got == ssub(lit, 1, #got) then
			synerr(d, p + #got, goio.ErrUnexpectedEOF)
		end
		for i = 1, n do
			if sbyte(got, i) ~= sbyte(lit, i) then
				invalidChar(d, p + i - 1, 'in literal ' .. lit .. ' (expecting ' .. quoteRune(sbyte(lit, i)) .. ')')
			end
		end
	end
	return p + n
end
X.scanLit = scanLit

local function dupName(d, p, name)
	local ptr = decPointer(d)
	throw(
		setmetatable(
			{ ByteOffset = p - 1, JSONPointer = ptr .. '/' .. escapePointerToken(name), Err = P.ErrDuplicateName },
			P.SyntacticError.pmt
		)
	)
end
X.dupName = dupName

-- nameRead records a member name at the current depth (after peek returned '"' in name position).
local function nameRead(d, name, p, check)
	local depth = d.depth
	if check and d.dup then
		local t = d.fd[depth]
		if not t then
			t = {}
			d.fd[depth] = t
		end
		if t[name] then
			dupName(d, p, name)
		end
		t[name] = true
	end
	d.fnm[depth] = name
	d.fw[depth] = true
end
X.nameRead = nameRead

local function atName(d)
	local depth = d.depth
	return depth > 0 and d.fk[depth] == 1 and not d.fw[depth]
end
X.atName = atName

-- readStr reads a string token (value or member name) after peek returned '"'.
local function readStr(d)
	local p = d.p
	local v, np = readString(d, p)
	d.p = np
	d.pk = nil
	if atName(d) then
		nameRead(d, v, p, true)
	else
		done(d)
	end
	return v
end
X.readStr = readStr

local function push(d, kind)
	local depth = d.depth + 1
	d.depth = depth
	d.fk[depth] = kind
	d.fn[depth] = 0
	d.fw[depth] = false
	d.fd[depth] = nil
end

-- open consumes '{' or '[' after peek returned it.
local function open(d, k)
	d.p = d.p + 1
	d.pk = nil
	push(d, k == 123 and 1 or 2)
end
X.open = open

-- close consumes '}' or ']' after peek returned it.
local function close(d)
	d.p = d.p + 1
	d.pk = nil
	d.fd[d.depth] = nil
	d.depth = d.depth - 1
	done(d)
end
X.close = close

local function readNumber(d)
	local p = d.p
	local np = scanNumber(d, p)
	d.p = np
	d.pk = nil
	done(d)
	return ssub(d.s, p, np - 1)
end
X.readNumber = readNumber

local function readLit(d, k)
	d.p = scanLit(d, d.p, k)
	d.pk = nil
	done(d)
end
X.readLit = readLit

local function readToken(d)
	local k = peek(d)
	if k == 34 then
		return tok(34, readStr(d))
	elseif k == 48 then
		return tok(48, readNumber(d))
	elseif k == 123 then
		open(d, k)
		return P.BeginObject
	elseif k == 91 then
		open(d, k)
		return P.BeginArray
	elseif k == 125 then
		close(d)
		return P.EndObject
	elseif k == 93 then
		close(d)
		return P.EndArray
	end
	readLit(d, k)
	if k == 110 then
		return P.Null
	elseif k == 116 then
		return P.True
	end
	return P.False
end
X.readToken = readToken

-- scanValue validates the value starting at p and returns the position after it.
local scanValue
local function scanObject(d, p)
	local s = d.s
	p = ws(s, p + 1)
	if sbyte(s, p) == 125 then
		return p + 1
	end
	local seen = d.dup and {} or nil
	while true do
		if sbyte(s, p) ~= 34 then
			invalidChar(d, p, "at start of string (expecting '\"')")
		end
		local name, np = readString(d, p)
		if seen then
			if seen[name] then
				dupName(d, p, name)
			end
			seen[name] = true
		end
		p = ws(s, np)
		if sbyte(s, p) ~= 58 then
			invalidChar(d, p, "after object name (expecting ':')")
		end
		p = ws(s, scanValue(d, ws(s, p + 1)))
		local c = sbyte(s, p)
		if c == 44 then
			p = ws(s, p + 1)
		elseif c == 125 then
			return p + 1
		else
			invalidChar(d, p, "after object value (expecting ',' or '}')")
		end
	end
end

local function scanArray(d, p)
	local s = d.s
	p = ws(s, p + 1)
	if sbyte(s, p) == 93 then
		return p + 1
	end
	while true do
		p = ws(s, scanValue(d, p))
		local c = sbyte(s, p)
		if c == 44 then
			p = ws(s, p + 1)
		elseif c == 93 then
			return p + 1
		else
			invalidChar(d, p, "after array element (expecting ',' or ']')")
		end
	end
end

function scanValue(d, p)
	local c = sbyte(d.s, p)
	local k = KIND[c]
	if k == 123 then
		return scanObject(d, p)
	elseif k == 91 then
		return scanArray(d, p)
	elseif k == 34 then
		local _, np = readString(d, p)
		return np
	elseif k == 48 then
		return scanNumber(d, p)
	elseif k == 110 or k == 116 or k == 102 then
		return scanLit(d, p, k)
	end
	invalidChar(d, p, 'at start of value')
end
X.scanValue = scanValue

-- readRaw reads the next value (or member name) and returns its raw text.
local function readRaw(d)
	local k = peek(d)
	local p = d.p
	if k == 125 or k == 93 then
		synerr(d, p, errMismatchDelim)
	end
	local np
	if k == 34 then
		local v
		v, np = readString(d, p)
		d.p = np
		d.pk = nil
		if atName(d) then
			nameRead(d, v, p, true)
			return ssub(d.s, p, np - 1)
		end
	else
		np = scanValue(d, p)
		d.p = np
		d.pk = nil
	end
	done(d)
	return ssub(d.s, p, np - 1)
end
X.readRaw = readRaw

-- finish checks that only whitespace follows the top-level value.
local function finish(d)
	local p = ws(d.s, d.p)
	if p <= #d.s then
		invalidChar(d, p, 'after top-level value')
	end
	if d.rerr ~= nil then
		throw(d.rerr)
	end
end
X.finish = finish

local zeroToken = tok(0, '')

function P.Decoder__PeekKind(d)
	local ok, k = pcall(peek, d)
	if ok then
		return k
	end
	if getmetatable(k) ~= JErr then
		error(k, 0)
	end
	return 0
end

function P.Decoder__ReadToken(d)
	local err, t = try(readToken, d)
	if err ~= nil then
		return zeroToken, err
	end
	return t, nil
end

function P.Decoder__ReadValue(d)
	local err, raw = try(readRaw, d)
	if err ~= nil then
		return nil, err
	end
	return rt.strbytes(raw), nil
end

function P.Decoder__SkipValue(d)
	local err = try(readRaw, d)
	return err
end

function P.Decoder__InputOffset(d)
	return d.p - 1
end

function P.Decoder__UnreadBuffer(d)
	return rt.strbytes(ssub(d.s, d.p))
end

function P.Decoder__StackDepth(d)
	return d.depth
end

function P.Decoder__StackIndex(d, i)
	if i == 0 then
		return 0, d.fn[0]
	end
	local n = d.fn[i]
	if d.fk[i] == 1 then
		return 123, n * 2 + (d.fw[i] and 1 or 0)
	end
	return 91, n
end

function P.Decoder__StackPointer(d)
	return decPointer(d)
end

function P.Decoder__Options(d)
	return snapopt(d.o)
end

rt.methods(P.Decoder, {}, {
	_mPeekKind = P.Decoder__PeekKind,
	_mReadToken = P.Decoder__ReadToken,
	_mReadValue = P.Decoder__ReadValue,
	_mSkipValue = P.Decoder__SkipValue,
	_mInputOffset = P.Decoder__InputOffset,
	_mUnreadBuffer = P.Decoder__UnreadBuffer,
	_mStackDepth = P.Decoder__StackDepth,
	_mStackIndex = P.Decoder__StackIndex,
	_mStackPointer = P.Decoder__StackPointer,
	_mOptions = P.Decoder__Options,
	_mReset = P.Decoder__Reset,
})

---------------------------------------------------------------------------
-- Encoder
--
-- Fields: buf/bn (output parts), depth, fk/fn/fw/fd (as for Decoder), o (options), w (io.Writer
-- for streaming), omitNL (no newline after top-level values), ml/colon/comma/nl (formatting).

P.Encoder = rt.named(PKG, 'jsontext.Encoder', 'struct')
rt.defineStruct(P.Encoder, {})
local encMT = P.Encoder.pmt

local function econfig(e)
	local o = e.o
	local ml = o.Multiline and true or false
	e.ml = ml
	local sc = o.SpaceAfterColon
	if sc == nil then
		sc = ml
	end
	e.colon = sc and ': ' or ':'
	e.comma = o.SpaceAfterComma and ', ' or ','
	e.indent = o.Indent or '\t'
	e.prefix = o.IndentPrefix or ''
	e.nl = {}
	e.dup = not o.AllowDuplicateNames
end
X.econfig = econfig

local function newEncoder(o, w)
	local e = setmetatable({ buf = {}, bn = 0, depth = 0, fk = {}, fn = { [0] = 0 }, fw = {}, fd = {}, o = o, w = w, written = 0 }, encMT)
	econfig(e)
	return e
end
X.newEncoder = newEncoder

function P.NewEncoder(w, opts)
	return newEncoder(applyOpts({}, opts), w)
end

local function eput(e, str)
	local n = e.bn + 1
	e.buf[n] = str
	e.bn = n
end
X.eput = eput

local function nlindent(e, depth)
	local s = e.nl[depth]
	if not s then
		s = '\n' .. e.prefix .. srep(e.indent, depth)
		e.nl[depth] = s
	end
	return s
end
X.nlindent = nlindent

local function encErr(e, err)
	local parts = {}
	for i = 1, e.depth do
		parts[#parts + 1] = '/' .. (e.fk[i] == 1 and escapePointerToken(e.fd[i] and e.fd[i].last or '') or tostring(e.fn[i]))
	end
	throw(setmetatable({ ByteOffset = e.written, JSONPointer = tconcat(parts), Err = err }, P.SyntacticError.pmt))
end
X.encErr = encErr

-- epre writes the separator and indentation before a value or member name.
local function epre(e, isString)
	local depth = e.depth
	if depth == 0 then
		return
	end
	if e.fk[depth] == 1 then
		if e.fw[depth] then
			eput(e, e.colon)
			return
		end
		if not isString then
			encErr(e, P.ErrNonStringName)
		end
	end
	if e.fn[depth] > 0 then
		eput(e, e.comma)
	end
	if e.ml then
		eput(e, nlindent(e, depth))
	end
end
X.epre = epre

local function flush(e)
	local s = tconcat(e.buf, '', 1, e.bn)
	e.buf = {}
	e.bn = 0
	e.written = e.written + #s
	local w = e.w
	local _, err
	local T = rt.typeof(w)
	if T and T.mset and T.mset._mWriteString then
		_, err = w:_mWriteString(s)
	else
		_, err = w:_mWrite(rt.strbytes(s))
	end
	if err ~= nil then
		throw(err)
	end
end
X.flush = flush

local function edone(e)
	local depth = e.depth
	if depth == 0 then
		e.fn[0] = e.fn[0] + 1
		if not e.omitNL then
			eput(e, '\n')
		end
		if e.w ~= nil then
			flush(e)
		end
		return
	end
	if e.fk[depth] == 1 then
		e.fw[depth] = false
	end
	e.fn[depth] = e.fn[depth] + 1
end
X.edone = edone

local function eatName(e)
	local depth = e.depth
	return depth > 0 and e.fk[depth] == 1 and not e.fw[depth]
end
X.eatName = eatName

-- ename records that a member name was written; check enables duplicate detection.
local function ename(e, name, check)
	local depth = e.depth
	local t = e.fd[depth]
	if not t then
		t = {}
		e.fd[depth] = t
	end
	if check and e.dup then
		if t[name] then
			t.last = name
			encErr(e, P.ErrDuplicateName)
		end
		t[name] = true
	end
	t.last = name
	e.fw[depth] = true
end
X.ename = ename

-- wstr writes a string token (member name or value).
local function wstr(e, s)
	local q, err = quote(s, e.o)
	if q == nil then
		encErr(e, err)
	end
	if eatName(e) then
		local t = e.fd[e.depth]
		if e.dup and t and t[s] then
			encErr(e, P.ErrDuplicateName)
		end
		epre(e, true)
		eput(e, q)
		ename(e, s, true)
	else
		epre(e, true)
		eput(e, q)
		edone(e)
	end
end
X.wstr = wstr

-- wscalar writes a non-string scalar (number or literal text).
local function wscalar(e, text)
	epre(e, false)
	eput(e, text)
	edone(e)
end
X.wscalar = wscalar

local function wopen(e, k)
	epre(e, false)
	eput(e, k == 123 and '{' or '[')
	local depth = e.depth + 1
	e.depth = depth
	e.fk[depth] = k == 123 and 1 or 2
	e.fn[depth] = 0
	e.fw[depth] = false
	e.fd[depth] = nil
end
X.wopen = wopen

local function wclose(e, k)
	local depth = e.depth
	local want = k == 125 and 1 or 2
	if depth == 0 or e.fk[depth] ~= want or e.fw[depth] then
		encErr(e, errMismatchDelim)
	end
	local n = e.fn[depth]
	e.fd[depth] = nil
	e.depth = depth - 1
	if e.ml and n > 0 then
		eput(e, nlindent(e, depth - 1))
	end
	eput(e, k == 125 and '}' or ']')
	edone(e)
end
X.wclose = wclose

local function writeToken(e, t)
	local k = t.k
	if k == 34 then
		wstr(e, t.s)
	elseif k == 48 then
		if eatName(e) then
			encErr(e, P.ErrNonStringName)
		end
		wscalar(e, t.s)
	elseif k == 123 or k == 91 then
		wopen(e, k)
	elseif k == 125 or k == 93 then
		wclose(e, k)
	elseif lits[k] then
		wscalar(e, lits[k])
	else
		encErr(e, errInvalidToken)
	end
end
X.writeToken = writeToken

-- writeRaw re-emits a raw JSON value through the encoder, validating and reformatting it.
local function writeRaw(e, raw)
	local d = newDecoder(raw, { AllowDuplicateNames = e.o.AllowDuplicateNames, AllowInvalidUTF8 = e.o.AllowInvalidUTF8 })
	local err, t = try(readToken, d)
	if err ~= nil then
		if err == goio.EOF then
			err = goio.ErrUnexpectedEOF
		end
		encErr(e, err)
	end
	writeToken(e, t)
	while d.depth > 0 do
		err, t = try(readToken, d)
		if err ~= nil then
			encErr(e, err)
		end
		writeToken(e, t)
	end
	err = try(finish, d)
	if err ~= nil then
		encErr(e, err)
	end
end
X.writeRaw = writeRaw

function P.Encoder__WriteToken(e, t)
	return (try(writeToken, e, t))
end

function P.Encoder__WriteValue(e, v)
	return (try(writeRaw, e, rt.bytestr(v)))
end

function P.Encoder__OutputOffset(e)
	local n = e.written
	for i = 1, e.bn do
		n = n + #e.buf[i]
	end
	return n
end

function P.Encoder__AvailableBuffer(_)
	return nil
end

function P.Encoder__StackDepth(e)
	return e.depth
end

function P.Encoder__StackIndex(e, i)
	if i == 0 then
		return 0, e.fn[0]
	end
	local n = e.fn[i]
	if e.fk[i] == 1 then
		return 123, n * 2 + (e.fw[i] and 1 or 0)
	end
	return 91, n
end

function P.Encoder__StackPointer(e)
	local parts = {}
	for i = 1, e.depth do
		if e.fk[i] == 1 then
			if e.fd[i] and e.fd[i].last then
				parts[#parts + 1] = '/' .. escapePointerToken(e.fd[i].last)
			end
		else
			parts[#parts + 1] = '/' .. e.fn[i]
		end
	end
	return tconcat(parts)
end

function P.Encoder__Options(e)
	return snapopt(e.o)
end

function P.Encoder__Reset(e, w, opts)
	local ne = newEncoder(applyOpts({}, opts), w)
	for k in pairs(e) do
		e[k] = nil
	end
	for k, v in pairs(ne) do
		e[k] = v
	end
end

rt.methods(P.Encoder, {}, {
	_mWriteToken = P.Encoder__WriteToken,
	_mWriteValue = P.Encoder__WriteValue,
	_mOutputOffset = P.Encoder__OutputOffset,
	_mAvailableBuffer = P.Encoder__AvailableBuffer,
	_mStackDepth = P.Encoder__StackDepth,
	_mStackIndex = P.Encoder__StackIndex,
	_mStackPointer = P.Encoder__StackPointer,
	_mOptions = P.Encoder__Options,
	_mReset = P.Encoder__Reset,
})

---------------------------------------------------------------------------
-- Value

local function valueString(v)
	if v == nil then
		return 'null'
	end
	return rt.bytestr(v)
end
P.Value__String = valueString

local function valueClone(v)
	if v == nil then
		return nil
	end
	return rt.strbytes(rt.bytestr(v))
end
P.Value__Clone = valueClone

local function validate(s, o)
	local d = newDecoder(s, o)
	local err = try(readRaw, d)
	if err == nil then
		err = try(finish, d)
	end
	return err
end

function P.Value__IsValid(v, opts)
	return validate(rt.bytestr(v), applyOpts({}, opts)) == nil
end

local function valueKind(v)
	local s = rt.bytestr(v)
	local c = sbyte(s, ws(s, 1))
	local k = c and KIND[c]
	return k or 0
end
P.Value__Kind = valueKind

-- reformat rewrites the JSON text s according to the flags table o.
local function reformat(s, o)
	local e = newEncoder(o, nil)
	e.omitNL = true
	local err = try(writeRaw, e, s)
	if err ~= nil then
		return nil, err
	end
	return tconcat(e.buf, '', 1, e.bn)
end
X.reformat = reformat

local function valueFormat(p, base, opts)
	local o = applyOpts(base, opts)
	local out, err = reformat(rt.bytestr(p.v), o)
	if err ~= nil then
		return err
	end
	p.v = rt.strbytes(out)
	return nil
end

function P.Value__Format(p, opts)
	return valueFormat(p, {}, opts)
end

function P.Value__Compact(p, opts)
	return valueFormat(
		p,
		{ Multiline = false, SpaceAfterColon = false, SpaceAfterComma = false, AllowDuplicateNames = true, AllowInvalidUTF8 = true },
		opts
	)
end

function P.Value__Indent(p, opts)
	return valueFormat(p, { Multiline = true, AllowDuplicateNames = true, AllowInvalidUTF8 = true }, opts)
end

function P.Value__Canonicalize(p, opts)
	return valueFormat(p, {}, opts)
end

local function valueMarshalJSON(v)
	if v == nil then
		return rt.strbytes('null'), nil
	end
	return v, nil
end
P.Value__MarshalJSON = valueMarshalJSON

local function valueUnmarshalJSON(p, b)
	p.v = rt.strbytes(rt.bytestr(b))
	return nil
end
P.Value__UnmarshalJSON = valueUnmarshalJSON

local function deref(f)
	return function(p, ...)
		return f(p.v, ...)
	end
end
rt.methods(P.Value, {
	_mClone = valueClone,
	_mIsValid = P.Value__IsValid,
	_mKind = valueKind,
	_mMarshalJSON = valueMarshalJSON,
	_mString = valueString,
}, {
	_mCanonicalize = P.Value__Canonicalize,
	_mClone = deref(valueClone),
	_mCompact = P.Value__Compact,
	_mFormat = P.Value__Format,
	_mIndent = P.Value__Indent,
	_mIsValid = deref(P.Value__IsValid),
	_mKind = deref(valueKind),
	_mMarshalJSON = deref(valueMarshalJSON),
	_mString = deref(valueString),
	_mUnmarshalJSON = valueUnmarshalJSON,
})

---------------------------------------------------------------------------
-- Pointer

function P.Pointer__IsValid(p)
	return p == '' or (sbyte(p, 1) == 47 and utf8valid(p) and not sfind(p, '~[^01]') and sbyte(p, #p) ~= 126)
end

function P.Pointer__Contains(p, pc)
	return ssub(pc, 1, #p) == p and (#pc == #p or sbyte(pc, #p + 1) == 47)
end

P.Pointer__Parent = pointerParent
P.Pointer__LastToken = pointerLastToken

function P.Pointer__AppendToken(p, t)
	return p .. '/' .. escapePointerToken(t)
end

rt.methods(P.Pointer, {
	_mIsValid = P.Pointer__IsValid,
	_mContains = P.Pointer__Contains,
	_mParent = pointerParent,
	_mLastToken = pointerLastToken,
	_mAppendToken = P.Pointer__AppendToken,
}, {
	_mIsValid = deref(P.Pointer__IsValid),
	_mContains = deref(P.Pointer__Contains),
	_mParent = deref(pointerParent),
	_mLastToken = deref(pointerLastToken),
	_mAppendToken = deref(P.Pointer__AppendToken),
})

---------------------------------------------------------------------------
-- Package functions

local function bytesOrString(src)
	if type(src) == 'string' then
		return src
	end
	return rt.bytestr(src)
end

function P.AppendQuote(_, dst, src)
	local q, err = quote(bytesOrString(src), {})
	if q == nil then
		q = quote(fixUTF8(bytesOrString(src)), { AllowInvalidUTF8 = true })
	end
	return rt.appendstr(dst, q), err
end

function P.AppendUnquote(_, dst, src)
	local s = bytesOrString(src)
	local d = newDecoder(s, {})
	local err, v = try(function()
		local k = peek(d)
		if k ~= 34 then
			invalidChar(d, d.p, "at start of string (expecting '\"')")
		end
		local val = readStr(d)
		finish(d)
		return val
	end)
	if err ~= nil then
		return dst, err
	end
	return rt.appendstr(dst, v), nil
end

function P.AppendFormat(_, dst, src, opts)
	local s = bytesOrString(src)
	local out, err = reformat(s, applyOpts({}, opts))
	if err ~= nil then
		return rt.appendstr(dst, s), err
	end
	return rt.appendstr(dst, out), nil
end

function P.AppendFloat(dst, f, bits)
	return rt.appendstr(dst, fmtFloat(f, bits))
end

return P
