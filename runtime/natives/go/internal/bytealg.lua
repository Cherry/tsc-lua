-- Native replacement for internal/bytealg backed by Lua's string library.
local rt = require('rt')
local P = rt.package('internal/bytealg')

local sfind, sbyte, schar = string.find, string.byte, string.char

P.MaxLen = 64

local function bytesToString(s)
	return rt.bytestr(s)
end

function P.Cutover(n)
	return math.floor((n + 16) / 8)
end

function P.IndexByteString(s, c)
	local i = sfind(s, schar(c), 1, true)
	if i then
		return i - 1
	end
	return -1
end

function P.IndexByte(b, c)
	if b == nil then
		return -1
	end
	local a, o = b[1], b[2]
	for i = 0, b[3] - 1 do
		if a[o + i] == c then
			return i
		end
	end
	return -1
end

function P.LastIndexByteString(s, c)
	for i = #s, 1, -1 do
		if sbyte(s, i) == c then
			return i - 1
		end
	end
	return -1
end

function P.LastIndexByte(b, c)
	if b == nil then
		return -1
	end
	local a, o = b[1], b[2]
	for i = b[3] - 1, 0, -1 do
		if a[o + i] == c then
			return i
		end
	end
	return -1
end

function P.IndexString(a, b)
	local i = sfind(a, b, 1, true)
	if i then
		return i - 1
	end
	return -1
end

function P.Index(a, b)
	return P.IndexString(bytesToString(a), bytesToString(b))
end

function P.CountString(s, c)
	local n = 0
	local ch = schar(c)
	local i = 1
	while true do
		local j = sfind(s, ch, i, true)
		if not j then
			return n
		end
		n = n + 1
		i = j + 1
	end
end

function P.Count(b, c)
	local n = 0
	if b == nil then
		return 0
	end
	local a, o = b[1], b[2]
	for i = 0, b[3] - 1 do
		if a[o + i] == c then
			n = n + 1
		end
	end
	return n
end

function P.CompareString(a, b)
	if a == b then
		return 0
	elseif a < b then
		return -1
	end
	return 1
end

function P.Compare(a, b)
	return P.CompareString(bytesToString(a), bytesToString(b))
end

function P.Equal(a, b)
	return bytesToString(a) == bytesToString(b)
end

-- IndexRabinKarp and LastIndexRabinKarp are generic over string | []byte.
local function asString(T, v)
	if type(v) == 'string' then
		return v
	end
	return bytesToString(v)
end

function P.IndexRabinKarp(T, s, sep)
	return P.IndexString(asString(T, s), asString(T, sep))
end

function P.LastIndexRabinKarp(T, s, sep)
	local str, pat = asString(T, s), asString(T, sep)
	local n, m = #str, #pat
	for i = n - m + 1, 1, -1 do
		if str:sub(i, i + m - 1) == pat then
			return i - 1
		end
	end
	return -1
end

function P.HashStr()
	return 0, 0
end

function P.MakeNoZero(n)
	return rt.mkslice(n, n, 0)
end

return P
