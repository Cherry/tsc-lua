-- Cache keys without hashing. keyBuilder assembles a key's bytes and Go hashes them with
-- xxh3-128, which Lua can only do with emulated 64-bit multiplies. The keys are only compared
-- for equality, so interning the byte string gives each distinct key its own value instead,
-- which is also collision free. Writes store bytes directly rather than slicing the buffer.
-- The 192 byte inline buffer starts empty and grows as written, instead of being zero filled
-- for every key.
-- overrides: keyBuilder__writeUint32 keyBuilder__writeUint64 keyBuilder__writeString keyBuilder__hash
local rt = require('rt')
local schar, sbyte, tunpack = string.char, string.byte, table.unpack
local band, rshift, tobit = bit.band, bit.rshift, bit.tobit
local tnew = require('table.new')

-- Interned keys carry this in Hi, so they cannot equal the xxh3 hashes of the special keys.
local INTERNED = 0x7473636c7561ULL

return function(P)
	local spill = P.keyBuilder__spill
	local capacity = 192
	local keyBuilderMT = P.keyBuilder.pmt
	local LEN, OVERFLOW, BUFFER =
		rt.fieldkey(P.keyBuilder, 'inlineLength'), rt.fieldkey(P.keyBuilder, 'overflowBuffer'), rt.fieldkey(P.keyBuilder, 'inlineBuffer')
	local slots = math.max(LEN, OVERFLOW, BUFFER)

	-- Only the first inlineLength bytes are ever read, so the buffer need not be pre-filled.
	P.keyBuilder.zero = function()
		local b = setmetatable(tnew(slots, 0), keyBuilderMT)
		b[LEN] = 0
		b[BUFFER] = {}
		return b
	end

	P.keyBuilder__writeString = function(b, s)
		local n = #s
		if b[LEN] + n > capacity then
			spill(b)
			if n > capacity then
				b[OVERFLOW] = rt.appendstr(b[OVERFLOW], s)
				return
			end
		end
		local buf, i = b[BUFFER], b[LEN]
		for k = 1, n do
			buf[i + k] = sbyte(s, k)
		end
		b[LEN] = i + n
	end

	P.keyBuilder__writeUint32 = function(b, v)
		if b[LEN] + 4 > capacity then
			spill(b)
		end
		local buf, i = b[BUFFER], b[LEN]
		buf[i + 1] = band(v, 255)
		buf[i + 2] = band(rshift(v, 8), 255)
		buf[i + 3] = band(rshift(v, 16), 255)
		buf[i + 4] = rshift(v, 24)
		b[LEN] = i + 4
	end

	-- v is a uint64 FFI integer; its two 32-bit halves are written as plain numbers.
	P.keyBuilder__writeUint64 = function(b, v)
		if b[LEN] + 8 > capacity then
			spill(b)
		end
		local buf, i = b[BUFFER], b[LEN]
		local lo, hi = tobit(tonumber(band(v, 0xFFFFFFFFULL))), tobit(tonumber(rshift(v, 32)))
		buf[i + 1] = band(lo, 255)
		buf[i + 2] = band(rshift(lo, 8), 255)
		buf[i + 3] = band(rshift(lo, 16), 255)
		buf[i + 4] = rshift(lo, 24)
		buf[i + 5] = band(hi, 255)
		buf[i + 6] = band(rshift(hi, 8), 255)
		buf[i + 7] = band(rshift(hi, 16), 255)
		buf[i + 8] = rshift(hi, 24)
		b[LEN] = i + 8
	end

	local interned = {}
	local count = 0
	P.keyBuilder__hash = function(b)
		local n = b[LEN]
		local s = ''
		if n > 0 then
			s = schar(tunpack(b[BUFFER], 1, n))
		end
		if b[OVERFLOW] ~= nil then
			s = rt.bytestr(b[OVERFLOW]) .. s
		end
		local id = interned[s]
		if id == nil then
			count = count + 1
			id = count
			interned[s] = id
		end
		-- Lo stays a plain number: FFI integers compare equal to numbers, and wideKey maps both
		-- to the same table key.
		return setmetatable({ Hi = INTERNED, Lo = id }, P.CacheHashKey.pmt)
	end

	-- Maps key interned keys by their number instead of building a string from both halves.
	-- The struct's ops are built first, since building them sets mapkey.
	local K = P.CacheHashKey
	K.zero()
	local structKey = K.mapkey
	K.mapkey = function(k)
		if k.Hi == INTERNED then
			return k.Lo
		end
		return structKey(k)
	end
end
