-- Native fmt package: formatting driven by the runtime type descriptors.
-- deps: strconv os io errors
local rt = require('rt')
local P = rt.package('fmt')
local strconv = rt.import('go.strconv')
local os = rt.import('go.os')

local sformat, srep, sbyte, ssub, tconcat = string.format, string.rep, string.byte, string.sub, table.concat
local typeof, unbox = rt.typeof, rt.unbox

P.Stringer = rt.named('fmt', 'fmt.Stringer', 'interface')
rt.define(P.Stringer, rt.ifaceOf({ 'String' }))
P.GoStringer = rt.named('fmt', 'fmt.GoStringer', 'interface')
rt.define(P.GoStringer, rt.ifaceOf({ 'GoString' }))
P.Formatter = rt.named('fmt', 'fmt.Formatter', 'interface')
rt.define(P.Formatter, rt.ifaceOf({ 'Format' }))
P.State = rt.named('fmt', 'fmt.State', 'interface')
rt.define(P.State, rt.ifaceOf({ 'Flag', 'Precision', 'Width', 'Write' }))

local errorType = rt.error
local stringerType = P.Stringer
local goStringerType = P.GoStringer

---------------------------------------------------------------------------
-- Number formatting

local function utoa(v, base, upper)
	-- v is a non-negative Lua number or a uint64 FFI integer.
	if v == 0 then
		return '0'
	end
	local digits = upper and '0123456789ABCDEF' or '0123456789abcdef'
	local parts = {}
	local wide = type(v) == 'cdata'
	while v ~= 0 do
		local q, r
		if wide then
			q = v / base
			r = tonumber(v - q * base)
		else
			q = math.floor(v / base)
			r = v - q * base
		end
		parts[#parts + 1] = ssub(digits, r + 1, r + 1)
		v = q
	end
	local n = #parts
	for i = 1, math.floor(n / 2) do
		parts[i], parts[n - i + 1] = parts[n - i + 1], parts[i]
	end
	return tconcat(parts)
end

local unsignedKinds = { uint = true, uint8 = true, uint16 = true, uint32 = true, uint64 = true, uintptr = true }

-- runeCount counts UTF-8 lead bytes, the rune count for valid text.
local function runeCount(s)
	local _, n = s:gsub('[^\128-\191]', '')
	return n
end

local function pad(st, s)
	local w = st.width
	if not w then
		return s
	end
	local n = runeCount(s)
	if n >= w then
		return s
	end
	if st.minus then
		return s .. srep(' ', w - n)
	end
	if st.zero and not st.noZeroPad then
		-- Zero padding goes after the sign.
		local sign = ''
		local c = ssub(s, 1, 1)
		if c == '-' or c == '+' or c == ' ' then
			sign = c
			s = ssub(s, 2)
		end
		return sign .. srep('0', w - n) .. s
	end
	return srep(' ', w - n) .. s
end

local function fmtInteger(st, v, kind, verb)
	local unsigned = unsignedKinds[kind]
	if kind == 'uint32' and type(v) == 'number' then
		-- uint32 values are held as their signed 32-bit pattern.
		v = v % 4294967296
	end
	local neg = not unsigned and v < 0
	local base, upper = 10, false
	if verb == 'x' then
		base = 16
	elseif verb == 'X' then
		base, upper = 16, true
	elseif verb == 'o' or verb == 'O' then
		base = 8
	elseif verb == 'b' then
		base = 2
	end
	local mag = v
	if neg then
		mag = -v
	end
	if type(mag) == 'cdata' then
		-- The magnitude of the most negative int64 only fits unsigned.
		mag = rt.U64(mag)
	end
	local digits = utoa(mag, base, upper)
	if st.prec then
		if st.prec == 0 and v == 0 then
			digits = ''
		elseif #digits < st.prec then
			digits = srep('0', st.prec - #digits) .. digits
		end
	end
	if st.sharp then
		if base == 16 then
			digits = (upper and '0X' or '0x') .. digits
		elseif base == 8 and ssub(digits, 1, 1) ~= '0' then
			digits = '0' .. digits
		elseif base == 2 then
			digits = '0b' .. digits
		end
	end
	if verb == 'O' then
		digits = '0o' .. digits
	end
	if neg then
		digits = '-' .. digits
	elseif st.plus then
		digits = '+' .. digits
	elseif st.space then
		digits = ' ' .. digits
	end
	local saved = st.noZeroPad
	if st.prec then
		st.noZeroPad = true
	end
	local r = pad(st, digits)
	st.noZeroPad = saved
	return r
end

local function fmtFloat(st, v, size, verb)
	local prec = st.prec
	local fverb = verb
	if verb == 'v' then
		fverb = 'g'
		prec = prec or -1
	elseif verb == 'F' then
		fverb = 'f'
	end
	if prec == nil then
		if fverb == 'g' or fverb == 'G' or fverb == 'x' or fverb == 'X' or fverb == 'b' then
			prec = -1
		else
			prec = 6
		end
	end
	local s = strconv.FormatFloat(v + 0.0, sbyte(fverb), prec, size)
	if ssub(s, 1, 1) ~= '-' and ssub(s, 1, 1) ~= '+' then
		if st.plus then
			s = '+' .. s
		elseif st.space then
			s = ' ' .. s
		end
	end
	if s == 'NaN' or s == '+Inf' or s == '-Inf' or s == '+NaN' then
		local saved = st.zero
		st.zero = false
		if s == '+Inf' and not st.plus then
			s = '+Inf'
		end
		local r = pad(st, s)
		st.zero = saved
		return r
	end
	if st.sharp and verb ~= 'b' then
		if not s:find('%.') and not s:find('[eEpP]') then
			s = s .. '.'
		end
	end
	return pad(st, s)
end

local function quoteString(st, s)
	if st.sharp and strconv.CanBackquote(s) then
		return '`' .. s .. '`'
	end
	if st.plus then
		return strconv.QuoteToASCII(s)
	end
	return strconv.Quote(s)
end

local function hexString(st, s, upper)
	local parts = {}
	local fmtc = upper and '%02X' or '%02x'
	for i = 1, #s do
		if st.space and i > 1 then
			parts[#parts + 1] = ' '
		end
		if st.sharp and (st.space or i == 1) then
			parts[#parts + 1] = upper and '0X' or '0x'
		end
		parts[#parts + 1] = sformat(fmtc, sbyte(s, i))
	end
	return tconcat(parts)
end

local function fmtString(st, s, verb)
	if st.prec then
		-- Precision limits the number of runes.
		local n = 0
		local cut = #s
		for i in rt.runes(s) do
			if n == st.prec then
				cut = i
				break
			end
			n = n + 1
		end
		s = ssub(s, 1, cut)
	end
	if verb == 'q' then
		return pad(st, quoteString(st, s))
	elseif verb == 'x' then
		return pad(st, hexString(st, s, false))
	elseif verb == 'X' then
		return pad(st, hexString(st, s, true))
	end
	return pad(st, s)
end

---------------------------------------------------------------------------
-- Value printing

local printValue

local function badVerb(st, T, v, verb)
	local inner
	if T == nil then
		inner = '<nil>'
	else
		local sub = { depth = 1 }
		inner = T.str .. '=' .. printValue(sub, T, v, 'v', 1)
	end
	return '%!' .. verb .. '(' .. inner .. ')'
end

local function addrOf(v)
	local s = tostring(v)
	local hex = s:match('0x(%x+)') or s:match(': (%x+)')
	return '0x' .. (hex or '0')
end

-- callMethod formats a value through Error/String/GoString when applicable.
local function handleMethods(st, T, v, iv, verb)
	if verb == 'T' or verb == 'p' then
		return nil
	end
	if T == nil then
		return nil
	end
	if st.sharpV then
		if rt.implements(T, goStringerType) then
			return iv:_mGoString()
		end
		return nil
	end
	if verb == 'v' or verb == 's' or verb == 'x' or verb == 'X' or verb == 'q' then
		local ok, res
		if rt.implements(T, errorType) then
			ok, res = pcall(function()
				return iv:_mError()
			end)
		elseif rt.implements(T, stringerType) then
			ok, res = pcall(function()
				return iv:_mString()
			end)
		else
			return nil
		end
		if not ok then
			if (T.kind == 'ptr') and v == nil then
				return '<nil>'
			end
			return '%!'
				.. verb
				.. '(PANIC='
				.. (verb == 'v' and 'String' or 'String')
				.. ' method: '
				.. rt.panicString(rt.panicValue(res))
				.. ')'
		end
		return fmtString(st, res, verb)
	end
	return nil
end

local function sortedMapEntries(T, m)
	local entries = {}
	for k, v in rt.mrange(m, T) do
		entries[#entries + 1] = { k, v }
	end
	local K = T.key
	local function cmp(a, b)
		local ka, kb = a[1], b[1]
		if K.kind == 'interface' then
			local ta, tb = typeof(ka), typeof(kb)
			if ta ~= tb then
				return (ta and ta.id or 0) < (tb and tb.id or 0)
			end
			ka, kb = unbox(ka), unbox(kb)
			if type(ka) ~= type(kb) or type(ka) == 'table' then
				return tostring(ka) < tostring(kb)
			end
		elseif K.kind == 'bool' then
			return (not ka) and kb
		elseif K.kind == 'ptr' or K.kind == 'chan' or K.kind == 'struct' or K.kind == 'array' then
			return tostring(ka) < tostring(kb)
		elseif unsignedKinds[K.kind] then
			if K.kind == 'uint32' then
				return ka % 4294967296 < kb % 4294967296
			end
			return ka < kb
		end
		if type(ka) == 'boolean' then
			return (not ka) and kb
		end
		return ka < kb
	end
	table.sort(entries, cmp)
	return entries
end

local function structFields(T)
	return T.fields or {}
end

function printValue(st, T, v, verb, depth)
	if T == nil then
		if verb == 'v' or verb == 's' or verb == 'T' then
			return pad(st, '<nil>')
		end
		return '%!' .. verb .. '(<nil>)'
	end
	local k = T.kind
	if k == 'interface' then
		if v == nil then
			if st.sharpV then
				return T.str .. '(nil)'
			end
			return pad(st, '<nil>')
		end
		local DT = typeof(v)
		return printValue(st, DT, unbox(v), verb, depth)
	end
	-- Methods take priority (only meaningful on named types or pointers).
	if depth > 0 or true then
		local iv = T.box and T.box(v) or v
		local r = handleMethods(st, T, v, iv, verb)
		if r then
			return r
		end
	end
	if k == 'bool' then
		if verb == 't' or verb == 'v' then
			return pad(st, v and 'true' or 'false')
		end
		return badVerb(st, T, v, verb)
	elseif rt.isIntKind(k) then
		if verb == 'v' or verb == 'd' then
			if st.sharpV and T.named then
				return T.str .. '(' .. fmtInteger(st, v, k, 'd') .. ')'
			end
			return fmtInteger(st, v, k, 'd')
		elseif verb == 'x' or verb == 'X' or verb == 'o' or verb == 'O' or verb == 'b' then
			return fmtInteger(st, v, k, verb)
		elseif verb == 'c' then
			return pad(st, rt.runestr(v))
		elseif verb == 'q' then
			return pad(st, strconv.QuoteRune(v))
		elseif verb == 'U' then
			local s = sformat('U+%04X', v)
			if st.sharp then
				s = s .. " '" .. rt.runestr(v) .. "'"
			end
			return pad(st, s)
		end
		return badVerb(st, T, v, verb)
	elseif k == 'float32' or k == 'float64' then
		local size = k == 'float32' and 32 or 64
		if
			verb == 'v'
			or verb == 'e'
			or verb == 'E'
			or verb == 'f'
			or verb == 'F'
			or verb == 'g'
			or verb == 'G'
			or verb == 'x'
			or verb == 'X'
			or verb == 'b'
		then
			return fmtFloat(st, v, size, verb)
		end
		return badVerb(st, T, v, verb)
	elseif k == 'complex64' or k == 'complex128' then
		local size = k == 'complex64' and 32 or 64
		local saved = st.plus
		local re = fmtFloat(st, v.re, size, verb)
		st.plus = true
		local im = fmtFloat(st, v.im, size, verb)
		st.plus = saved
		return '(' .. re .. im .. 'i)'
	elseif k == 'string' then
		if verb == 'v' then
			if st.sharpV then
				return strconv.Quote(v)
			end
			return fmtString(st, v, 's')
		elseif verb == 's' or verb == 'q' or verb == 'x' or verb == 'X' then
			return fmtString(st, v, verb)
		end
		return badVerb(st, T, v, verb)
	elseif k == 'ptr' or k == 'unsafeptr' then
		if verb == 'p' then
			if v == nil then
				return pad(st, '0x0')
			end
			return pad(st, addrOf(v))
		end
		if v == nil then
			if st.sharpV then
				return '(' .. T.str .. ')(nil)'
			end
			if verb == 'v' or verb == 's' then
				return pad(st, '<nil>')
			end
			return fmtInteger(st, 0, 'uintptr', verb == 'd' and 'd' or 'x')
		end
		if depth == 0 and T.elem then
			local ek = T.elem.kind
			if ek == 'struct' or ek == 'array' or ek == 'slice' or ek == 'map' then
				local inner = v
				if ek ~= 'struct' and ek ~= 'array' then
					inner = v.v
				end
				return '&' .. printValue(st, T.elem, inner, verb, depth + 1)
			end
		end
		if verb == 'd' then
			return fmtInteger(st, 0, 'uintptr', 'd')
		end
		return pad(st, addrOf(v))
	elseif k == 'func' or k == 'chan' then
		if v == nil then
			if verb == 'v' or verb == 'p' then
				return pad(st, k == 'func' and (st.sharpV and '(' .. T.str .. ')(nil)' or '<nil>') or '<nil>')
			end
		end
		return pad(st, addrOf(v))
	elseif k == 'slice' or k == 'array' then
		local E = T.elem
		local n, get
		if k == 'slice' then
			if v == nil then
				n = 0
			else
				n = v[3]
			end
			get = function(i)
				return v[1][v[2] + i]
			end
		else
			n = T.len
			get = function(i)
				return v[i + 1]
			end
		end
		if verb == 'p' and k == 'slice' then
			return pad(st, addrOf(v))
		end
		if E.kind == 'uint8' and (verb == 's' or verb == 'q' or verb == 'x' or verb == 'X') then
			local bytes = {}
			for i = 0, n - 1 do
				bytes[i + 1] = string.char(get(i))
			end
			return fmtString(st, tconcat(bytes), verb)
		end
		if st.sharpV then
			if k == 'slice' and v == nil then
				return T.str .. '(nil)'
			end
			local parts = {}
			for i = 0, n - 1 do
				parts[#parts + 1] = printValue(st, E, get(i), verb, depth + 1)
			end
			return T.str .. '{' .. tconcat(parts, ', ') .. '}'
		end
		local parts = {}
		for i = 0, n - 1 do
			parts[#parts + 1] = printValue(st, E, get(i), verb, depth + 1)
		end
		return '[' .. tconcat(parts, ' ') .. ']'
	elseif k == 'map' then
		if st.sharpV and v == nil then
			return T.str .. '(nil)'
		end
		local entries = v and sortedMapEntries(T, v) or {}
		local parts = {}
		for _, e in ipairs(entries) do
			parts[#parts + 1] = printValue(st, T.key, e[1], verb, depth + 1) .. ':' .. printValue(st, T.elem, e[2], verb, depth + 1)
		end
		if st.sharpV then
			return T.str .. '{' .. tconcat(parts, ', ') .. '}'
		end
		return 'map[' .. tconcat(parts, ' ') .. ']'
	elseif k == 'struct' then
		local parts = {}
		for _, f in ipairs(structFields(T)) do
			local key, FT, name, flags = f[1], f[2], f[3], f[4]
			local fv
			if flags == 2 or f[6] then
				-- A flattened struct field shares the outer table.
				fv = v
			else
				fv = v[key]
			end
			local s = printValue(st, FT, fv, verb, depth + 1)
			if st.sharpV or st.plusV then
				parts[#parts + 1] = name .. ':' .. s
			else
				parts[#parts + 1] = s
			end
		end
		if st.sharpV then
			return T.str .. '{' .. tconcat(parts, ', ') .. '}'
		end
		return '{' .. tconcat(parts, ' ') .. '}'
	end
	return '?'
end

local function printArg(st, arg, verb)
	local T = typeof(arg)
	if verb == 'T' then
		return pad(st, T and T.str or '<nil>')
	end
	if arg == nil then
		if verb == 'v' or verb == 's' then
			return pad(st, '<nil>')
		end
		return '%!' .. verb .. '(<nil>)'
	end
	if verb == 'p' then
		local k = T.kind
		if k == 'ptr' or k == 'slice' or k == 'map' or k == 'func' or k == 'chan' or k == 'unsafeptr' then
			local v = unbox(arg)
			if v == nil then
				return pad(st, '0x0')
			end
			return pad(st, addrOf(v))
		end
		return '%!p(' .. T.str .. '=' .. printValue({}, T, unbox(arg), 'v', 0) .. ')'
	end
	return printValue(st, T, unbox(arg), verb, 0)
end

---------------------------------------------------------------------------
-- Printf

local function newState()
	return {}
end

local function sliceArgs(args)
	if args == nil then
		return {}, 0
	end
	local list = {}
	for i = 0, args[3] - 1 do
		list[i + 1] = args[1][args[2] + i]
	end
	return list, args[3]
end

local function doPrintf(format, args)
	local list, nargs = sliceArgs(args)
	local out = {}
	local argi = 1
	local i = 1
	local n = #format
	local reordered = false
	while i <= n do
		local j = format:find('%', i, true)
		if not j then
			out[#out + 1] = ssub(format, i)
			break
		end
		if j > i then
			out[#out + 1] = ssub(format, i, j - 1)
		end
		i = j + 1
		local st = newState()
		-- flags
		while i <= n do
			local c = ssub(format, i, i)
			if c == '#' then
				st.sharp = true
			elseif c == '0' then
				st.zero = true
			elseif c == '+' then
				st.plus = true
			elseif c == '-' then
				st.minus = true
			elseif c == ' ' then
				st.space = true
			else
				break
			end
			i = i + 1
		end
		-- argument index [n]
		local function argIndex()
			if ssub(format, i, i) == '[' then
				local close = format:find(']', i, true)
				if close then
					local idx = tonumber(ssub(format, i + 1, close - 1))
					if idx then
						argi = idx
						reordered = true
					end
					i = close + 1
				end
			end
		end
		argIndex()
		-- width
		if ssub(format, i, i) == '*' then
			i = i + 1
			local a = list[argi]
			argi = argi + 1
			local av = unbox(a)
			if type(av) == 'number' then
				st.width = av
				if st.width < 0 then
					st.minus = true
					st.width = -st.width
				end
			else
				out[#out + 1] = '%!(BADWIDTH)'
			end
		else
			local s, e = format:find('^%d+', i)
			if s then
				st.width = tonumber(ssub(format, s, e))
				i = e + 1
			end
		end
		-- precision
		if ssub(format, i, i) == '.' then
			i = i + 1
			argIndex()
			if ssub(format, i, i) == '*' then
				i = i + 1
				local a = list[argi]
				argi = argi + 1
				local av = unbox(a)
				if type(av) == 'number' then
					st.prec = av
					if st.prec < 0 then
						st.prec = nil
					end
				else
					out[#out + 1] = '%!(BADPREC)'
				end
			else
				local s, e = format:find('^%d+', i)
				if s then
					st.prec = tonumber(ssub(format, s, e))
					i = e + 1
				else
					st.prec = 0
				end
			end
		end
		argIndex()
		if i > n then
			out[#out + 1] = '%!(NOVERB)'
			break
		end
		-- verb (a full rune)
		local r, w = rt.decodeRune(format, i)
		local verb = rt.runestr(r)
		i = i + w
		if verb == '%' then
			out[#out + 1] = '%'
		elseif argi > nargs then
			out[#out + 1] = '%!' .. verb .. '(MISSING)'
		else
			local arg = list[argi]
			argi = argi + 1
			if verb == 'v' then
				if st.sharp then
					st.sharpV = true
					st.sharp = false
				end
				if st.plus then
					st.plusV = true
					st.plus = false
				end
			end
			if verb == 'w' then
				verb = 'v'
			end
			out[#out + 1] = printArg(st, arg, verb)
		end
	end
	if not reordered and argi <= nargs then
		local extra = {}
		for k = argi, nargs do
			local a = list[k]
			local T = typeof(a)
			if T == nil then
				extra[#extra + 1] = '<nil>'
			else
				extra[#extra + 1] = T.str .. '=' .. printArg(newState(), a, 'v')
			end
		end
		out[#out + 1] = '%!(EXTRA ' .. tconcat(extra, ', ') .. ')'
	end
	return tconcat(out)
end

local function isString(a)
	return type(a) == 'string'
end

local function doPrint(args)
	local list, n = sliceArgs(args)
	local out = {}
	for i = 1, n do
		local a = list[i]
		if i > 1 and not isString(a) and not isString(list[i - 1]) then
			out[#out + 1] = ' '
		end
		out[#out + 1] = printArg(newState(), a, 'v')
	end
	return tconcat(out)
end

local function doPrintln(args)
	local list, n = sliceArgs(args)
	local out = {}
	for i = 1, n do
		out[i] = printArg(newState(), list[i], 'v')
	end
	return tconcat(out, ' ') .. '\n'
end

rt.fmtValue = function(v)
	return printArg(newState(), v, 'v')
end

local function write(w, s)
	if w == nil then
		rt.panic(rt.runtimeError('invalid memory address or nil pointer dereference'))
	end
	local T = typeof(w)
	if T and T.mset and T.mset._mWriteString then
		return w:_mWriteString(s)
	end
	return w:_mWrite(rt.strbytes(s))
end

function P.Sprintf(format, args)
	return doPrintf(format, args)
end

function P.Sprint(args)
	return doPrint(args)
end

function P.Sprintln(args)
	return doPrintln(args)
end

function P.Fprintf(w, format, args)
	return write(w, doPrintf(format, args))
end

function P.Fprint(w, args)
	return write(w, doPrint(args))
end

function P.Fprintln(w, args)
	return write(w, doPrintln(args))
end

function P.Printf(format, args)
	return write(os.Stdout, doPrintf(format, args))
end

function P.Print(args)
	return write(os.Stdout, doPrint(args))
end

function P.Println(args)
	return write(os.Stdout, doPrintln(args))
end

function P.Appendf(b, format, args)
	return rt.appendstr(b, doPrintf(format, args))
end

function P.Append(b, args)
	return rt.appendstr(b, doPrint(args))
end

function P.Appendln(b, args)
	return rt.appendstr(b, doPrintln(args))
end

-- Errorf -------------------------------------------------------------------

local wrapError = rt.named('fmt', 'fmt.wrapError', 'struct')
rt.defineStruct(wrapError, { { 'msg', rt.string, 'msg', 0 }, { 'err', rt.error, 'err', 0 } })
rt.methods(wrapError, {}, {
	_mError = function(e)
		return e.msg
	end,
	_mUnwrap = function(e)
		return e.err
	end,
})

local wrapErrors = rt.named('fmt', 'fmt.wrapErrors', 'struct')
rt.defineStruct(wrapErrors, { { 'msg', rt.string, 'msg', 0 }, { 'errs', rt.sliceOf(rt.error), 'errs', 0 } })
rt.methods(wrapErrors, {}, {
	_mError = function(e)
		return e.msg
	end,
	_mUnwrap = function(e)
		return e.errs
	end,
})

local errorsPkg = rt.import('go.errors')

function P.Errorf(format, args)
	local msg = doPrintf(format, args)
	-- Find %w verbs and their argument indexes.
	local wrapped = {}
	local list = sliceArgs(args)
	local argi = 1
	local i = 1
	local n = #format
	while i <= n do
		local j = format:find('%', i, true)
		if not j then
			break
		end
		i = j + 1
		while i <= n and ssub(format, i, i):match('[#0+%- %d%.%*%[%]]') do
			if ssub(format, i, i) == '*' then
				argi = argi + 1
			end
			i = i + 1
		end
		local verb = ssub(format, i, i)
		i = i + 1
		if verb ~= '%' then
			if verb == 'w' then
				local a = list[argi]
				if a ~= nil and rt.implements(typeof(a), rt.error) then
					wrapped[#wrapped + 1] = a
				end
			end
			argi = argi + 1
		end
	end
	if #wrapped == 0 then
		return errorsPkg.New(msg)
	elseif #wrapped == 1 then
		return setmetatable({ msg = msg, err = wrapped[1] }, wrapError.pmt)
	end
	local errs = nil
	for _, e in ipairs(wrapped) do
		errs = rt.append1(errs, e)
	end
	return setmetatable({ msg = msg, errs = errs }, wrapErrors.pmt)
end

-- Scanning (minimal) --------------------------------------------------------

function P.Sscanf(str, format, args)
	local list = sliceArgs(args)
	local count = 0
	local pos = 1
	local i = 1
	while i <= #format do
		local c = ssub(format, i, i)
		if c == '%' then
			local verb = ssub(format, i + 1, i + 1)
			i = i + 2
			local target = list[count + 1]
			if target == nil then
				break
			end
			local box = unbox(target)
			local s, e
			if verb == 'd' then
				s, e = str:find('^[-+]?%d+', pos)
				if not s then
					return count, errorsPkg.New('expected integer')
				end
				box.v = tonumber(ssub(str, s, e))
			elseif verb == 's' or verb == 'v' then
				s, e = str:find('^%S+', pos)
				if not s then
					return count, errorsPkg.New('unexpected EOF')
				end
				box.v = ssub(str, s, e)
			elseif verb == 'f' or verb == 'g' then
				s, e = str:find('^[-+]?[%d%.eE+-]+', pos)
				if not s then
					return count, errorsPkg.New('expected float')
				end
				box.v = tonumber(ssub(str, s, e)) + 0.0
			else
				return count, errorsPkg.New('unsupported scan verb')
			end
			pos = e + 1
			count = count + 1
		elseif c:match('%s') then
			local _, e = str:find('^%s*', pos)
			pos = e + 1
			i = i + 1
		else
			if ssub(str, pos, pos) ~= c then
				return count, errorsPkg.New('input does not match format')
			end
			pos = pos + 1
			i = i + 1
		end
	end
	return count, nil
end

function P.Sscan(str, args)
	local list = sliceArgs(args)
	local count = 0
	for word in str:gmatch('%S+') do
		local target = list[count + 1]
		if target == nil then
			break
		end
		local T = typeof(target).elem
		local box = unbox(target)
		if T.kind == 'string' then
			box.v = word
		elseif rt.isIntKind(T.kind) then
			box.v = tonumber(word)
		else
			box.v = tonumber(word)
		end
		count = count + 1
	end
	return count, nil
end

return P
