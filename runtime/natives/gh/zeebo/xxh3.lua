-- Native port of github.com/zeebo/xxh3 (XXH3 64 and 128-bit, default secret). The Go
-- package reads memory through unsafe pointers, so it is reimplemented over Lua strings.
-- 64-bit values are uint64_t FFI integers, whose arithmetic wraps like Go's.
local rt = require('rt')
local ffi = require('ffi')
local P = rt.package('github.com/zeebo/xxh3')

local sbyte = string.byte
local band, bor, bxor, bnot, lshift, rshift = bit.band, bit.bor, bit.bxor, bit.bnot, bit.lshift, bit.rshift
local U64 = rt.U64

local PRIME32_1 = 0x9E3779B1ULL
local PRIME32_2 = 0x85EBCA77ULL
local PRIME32_3 = 0xC2B2AE3DULL
local PRIME64_1 = 0x9E3779B185EBCA87ULL
local PRIME64_2 = 0xC2B2AE3D27D4EB4FULL
local PRIME64_3 = 0x165667B19E3779F9ULL
local PRIME64_4 = 0x85EBCA77C2B2AE63ULL
local PRIME64_5 = 0x27D4EB2F165667C5ULL
local MASK32 = 0xFFFFFFFFULL

local KEY = string.char(
	0xb8,
	0xfe,
	0x6c,
	0x39,
	0x23,
	0xa4,
	0x4b,
	0xbe,
	0x7c,
	0x01,
	0x81,
	0x2c,
	0xf7,
	0x21,
	0xad,
	0x1c,
	0xde,
	0xd4,
	0x6d,
	0xe9,
	0x83,
	0x90,
	0x97,
	0xdb,
	0x72,
	0x40,
	0xa4,
	0xa4,
	0xb7,
	0xb3,
	0x67,
	0x1f,
	0xcb,
	0x79,
	0xe6,
	0x4e,
	0xcc,
	0xc0,
	0xe5,
	0x78,
	0x82,
	0x5a,
	0xd0,
	0x7d,
	0xcc,
	0xff,
	0x72,
	0x21,
	0xb8,
	0x08,
	0x46,
	0x74,
	0xf7,
	0x43,
	0x24,
	0x8e,
	0xe0,
	0x35,
	0x90,
	0xe6,
	0x81,
	0x3a,
	0x26,
	0x4c,
	0x3c,
	0x28,
	0x52,
	0xbb,
	0x91,
	0xc3,
	0x00,
	0xcb,
	0x88,
	0xd0,
	0x65,
	0x8b,
	0x1b,
	0x53,
	0x2e,
	0xa3,
	0x71,
	0x64,
	0x48,
	0x97,
	0xa2,
	0x0d,
	0xf9,
	0x4e,
	0x38,
	0x19,
	0xef,
	0x46,
	0xa9,
	0xde,
	0xac,
	0xd8,
	0xa8,
	0xfa,
	0x76,
	0x3f,
	0xe3,
	0x9c,
	0x34,
	0x3f,
	0xf9,
	0xdc,
	0xbb,
	0xc7,
	0xc7,
	0x0b,
	0x4f,
	0x1d,
	0x8a,
	0x51,
	0xe0,
	0x4b,
	0xcd,
	0xb4,
	0x59,
	0x31,
	0xc8,
	0x9f,
	0x7e,
	0xc9,
	0xd9,
	0x78,
	0x73,
	0x64,
	0xea,
	0xc5,
	0xac,
	0x83,
	0x34,
	0xd3,
	0xeb,
	0xc3,
	0xc5,
	0x81,
	0xa0,
	0xff,
	0xfa,
	0x13,
	0x63,
	0xeb,
	0x17,
	0x0d,
	0xdd,
	0x51,
	0xb7,
	0xf0,
	0xda,
	0x49,
	0xd3,
	0x16,
	0x55,
	0x26,
	0x29,
	0xd4,
	0x68,
	0x9e,
	0x2b,
	0x16,
	0xbe,
	0x58,
	0x7d,
	0x47,
	0xa1,
	0xfc,
	0x8f,
	0xf8,
	0xb8,
	0xd1,
	0x7a,
	0xd0,
	0x31,
	0xce,
	0x45,
	0xcb,
	0x3a,
	0x8f,
	0x95,
	0x16,
	0x04,
	0x28,
	0xaf,
	0xd7,
	0xfb,
	0xca,
	0xbb,
	0x4b,
	0x40,
	0x7e
)

-- Reads are 0-based offsets, like the Go code, and little endian like every supported target.
local u8p, u32p, u64p = ffi.typeof('const uint8_t *'), ffi.typeof('const uint32_t *'), ffi.typeof('const uint64_t *')

local function r64(s, o)
	return ffi.cast(u64p, ffi.cast(u8p, s) + o)[0]
end

local function r32(s, o)
	return U64(ffi.cast(u32p, ffi.cast(u8p, s) + o)[0])
end

-- The secret's words, by byte offset. Reading them through a pointer into the KEY string
-- miscompiles: LuaJIT's JIT drops the constant base address from the load.
local K64, K32 = {}, {}
for o = 0, #KEY - 4 do
	local w = U64(0)
	for i = 3, 0, -1 do
		w = w * 256 + sbyte(KEY, o + i + 1)
	end
	K32[o] = w
end
for o = 0, #KEY - 8 do
	K64[o] = K32[o] + K32[o + 4] * 4294967296ULL
end

local function key64(o)
	return K64[o]
end

local function key32(o)
	return K32[o]
end

-- mul64 returns the high and low 64 bits of the unsigned 128-bit product a*b.
local function mul64(a, b)
	local a0, a1 = band(a, MASK32), rshift(a, 32)
	local b0, b1 = band(b, MASK32), rshift(b, 32)
	local w0 = a0 * b0
	local t = a1 * b0 + rshift(w0, 32)
	local w1 = band(t, MASK32) + a0 * b1
	local hi = a1 * b1 + rshift(t, 32) + rshift(w1, 32)
	return hi, a * b
end

local function mulFold64(x, y)
	local hi, lo = mul64(x, y)
	return bxor(hi, lo)
end

local function avalanche(x)
	x = bxor(x, rshift(x, 37))
	x = x * 0x165667919E3779F9ULL
	return bxor(x, rshift(x, 32))
end

local function avalancheSmall(x)
	x = x * PRIME64_2
	x = bxor(x, rshift(x, 29))
	x = x * PRIME64_3
	return bxor(x, rshift(x, 32))
end

local function bswap64(x)
	return bit.bswap(x)
end

local function bswap32(x)
	return band(rshift(bit.bswap(x), 32), MASK32)
end

local function rotl32(x, n)
	return band(bor(lshift(x, n), rshift(x, 32 - n)), MASK32)
end

local function rotl64(x, n)
	return bit.rol(x, n)
end

-- One 64-byte stripe: data at offset p, secret at key offset k.
local function stripe(accs, s, p, k)
	for lane = 0, 7 do
		local dv = r64(s, p + 8 * lane)
		local dk = bxor(dv, key64(k + 8 * lane))
		local j = lane + 1
		local other = bxor(lane, 1) + 1
		accs[other] = accs[other] + dv
		accs[j] = accs[j] + band(dk, MASK32) * rshift(dk, 32)
	end
end

local function scramble(accs)
	for lane = 0, 7 do
		local a = accs[lane + 1]
		a = bxor(a, rshift(a, 47))
		a = bxor(a, key64(128 + 8 * lane))
		accs[lane + 1] = a * PRIME32_1
	end
end

local function accumulate(s, l)
	local accs = { PRIME32_3, PRIME64_1, PRIME64_2, PRIME64_3, PRIME64_4, PRIME32_2, PRIME64_5, PRIME32_1 }
	local p = 0
	while l > 1024 do
		local k = 0
		for _ = 1, 16 do
			stripe(accs, s, p, k)
			l = l - 64
			if l > 0 then
				p = p + 64
				k = k + 8
			end
		end
		scramble(accs)
	end
	if l > 0 then
		local t = math.floor((l - 1) / 64)
		local k = 0
		for _ = 1, t do
			stripe(accs, s, p, k)
			l = l - 64
			if l > 0 then
				p = p + 64
				k = k + 8
			end
		end
		if l > 0 then
			p = p - (64 - l)
			stripe(accs, s, p, 121)
		end
	end
	return accs
end

local function hash128(s)
	local l = #s
	local ul = U64(l)
	local hi, lo
	if l <= 16 then
		if l > 8 then
			local bitflipl = bxor(key64(32), key64(40))
			local bitfliph = bxor(key64(48), key64(56))
			local inLo = r64(s, 0)
			local inHi = r64(s, l - 8)
			local mh, ml = mul64(bxor(inLo, inHi, bitflipl), PRIME64_1)
			ml = ml + lshift(ul - 1, 54)
			inHi = bxor(inHi, bitfliph)
			mh = mh + inHi + band(inHi, MASK32) * (PRIME32_2 - 1)
			ml = bxor(ml, bswap64(mh))
			hi, lo = mul64(ml, PRIME64_2)
			hi = hi + mh * PRIME64_2
			return avalanche(hi), avalanche(lo)
		elseif l > 3 then
			local bitflip = bxor(key64(16), key64(24))
			local input64 = r32(s, 0) + lshift(r32(s, l - 4), 32)
			local keyed = bxor(input64, bitflip)
			hi, lo = mul64(keyed, PRIME64_1 + lshift(ul, 2))
			hi = hi + lshift(lo, 1)
			lo = bxor(lo, rshift(hi, 3))
			lo = bxor(lo, rshift(lo, 35))
			lo = lo * 0x9FB21C651E98DF25ULL
			lo = bxor(lo, rshift(lo, 28))
			return avalanche(hi), lo
		elseif l == 3 then
			local c12 = sbyte(s, 1) + sbyte(s, 2) * 256
			lo = U64(c12 * 65536 + sbyte(s, 3) + 3 * 256)
		elseif l == 2 then
			local c12 = sbyte(s, 1) + sbyte(s, 2) * 256
			lo = rshift(U64(c12) * (16777216 + 1), 8) + 2 * 256
		elseif l == 1 then
			lo = U64(sbyte(s, 1) * (16777216 + 65536 + 1) + 256)
		else
			return 0x99AA06D3014798D8ULL, 0x6001C324468D497FULL
		end
		hi = rotl32(bswap32(band(lo, MASK32)), 13)
		lo = bxor(lo, bxor(key32(0), key32(4)))
		hi = bxor(hi, bxor(key32(8), key32(12)))
		return avalancheSmall(hi), avalancheSmall(lo)
	elseif l <= 128 then
		lo = ul * PRIME64_1
		hi = U64(0)
		local function round(fromEnd, fromStart, kHi, kLo)
			local inA, inB = r64(s, l - fromEnd), r64(s, l - fromEnd + 8)
			local iA, iB = r64(s, fromStart), r64(s, fromStart + 8)
			hi = hi + mulFold64(bxor(inA, key64(kHi)), bxor(inB, key64(kHi + 8)))
			hi = bxor(hi, iA + iB)
			lo = lo + mulFold64(bxor(iA, key64(kLo)), bxor(iB, key64(kLo + 8)))
			lo = bxor(lo, inA + inB)
		end
		if l > 32 then
			if l > 64 then
				if l > 96 then
					round(64, 48, 112, 96)
				end
				round(48, 32, 80, 64)
			end
			round(32, 16, 48, 32)
		end
		round(16, 0, 16, 0)
		hi, lo = (lo * PRIME64_1) + (hi * PRIME64_4) + (ul * PRIME64_2), hi + lo
		return -avalanche(hi), avalanche(lo)
	elseif l <= 240 then
		lo = ul * PRIME64_1
		hi = U64(0)
		for g = 0, 3 do
			local base = g * 32
			local i0, i1, i2, i3 = r64(s, base), r64(s, base + 8), r64(s, base + 16), r64(s, base + 24)
			hi = hi + mulFold64(bxor(i2, key64(base + 16)), bxor(i3, key64(base + 24)))
			hi = bxor(hi, i0 + i1)
			lo = lo + mulFold64(bxor(i0, key64(base)), bxor(i1, key64(base + 8)))
			lo = bxor(lo, i2 + i3)
		end
		hi = avalanche(hi)
		lo = avalanche(lo)
		local top = l - l % 32
		for i = 128, top - 1, 32 do
			local i0, i1, i2, i3 = r64(s, i), r64(s, i + 8), r64(s, i + 16), r64(s, i + 24)
			local k0, k1, k2, k3 = key64(i - 125), key64(i - 117), key64(i - 109), key64(i - 101)
			hi = hi + mulFold64(bxor(i2, k2), bxor(i3, k3))
			hi = bxor(hi, i0 + i1)
			lo = lo + mulFold64(bxor(i0, k0), bxor(i1, k1))
			lo = bxor(lo, i2 + i3)
		end
		local i0, i1, i2, i3 = r64(s, l - 32), r64(s, l - 24), r64(s, l - 16), r64(s, l - 8)
		hi = hi + mulFold64(bxor(i0, key64(119)), bxor(i1, key64(127)))
		hi = bxor(hi, i2 + i3)
		lo = lo + mulFold64(bxor(i2, key64(103)), bxor(i3, key64(111)))
		lo = bxor(lo, i0 + i1)
		hi, lo = (lo * PRIME64_1) + (hi * PRIME64_4) + (ul * PRIME64_2), hi + lo
		return -avalanche(hi), avalanche(lo)
	end
	lo = ul * PRIME64_1
	hi = bnot(ul * PRIME64_2)
	local accs = accumulate(s, l)
	for g = 0, 3 do
		lo = lo + mulFold64(bxor(accs[2 * g + 1], key64(11 + 16 * g)), bxor(accs[2 * g + 2], key64(19 + 16 * g)))
		hi = hi + mulFold64(bxor(accs[2 * g + 1], key64(117 + 16 * g)), bxor(accs[2 * g + 2], key64(125 + 16 * g)))
	end
	return avalanche(hi), avalanche(lo)
end

local function rrmxmx(h64, len)
	h64 = bxor(h64, bxor(rotl64(h64, 49), rotl64(h64, 24)))
	h64 = h64 * 0x9FB21C651E98DF25ULL
	h64 = bxor(h64, rshift(h64, 35) + len)
	h64 = h64 * 0x9FB21C651E98DF25ULL
	return bxor(h64, rshift(h64, 28))
end

local function hash64(s)
	local l = #s
	local ul = U64(l)
	if l <= 16 then
		if l > 8 then
			local inLo = bxor(r64(s, 0), bxor(key64(24), key64(32)))
			local inHi = bxor(r64(s, l - 8), bxor(key64(40), key64(48)))
			local folded = mulFold64(inLo, inHi)
			return avalanche(ul + bswap64(inLo) + inHi + folded)
		elseif l > 3 then
			local input = r32(s, l - 4) + lshift(r32(s, 0), 32)
			return rrmxmx(bxor(input, bxor(key64(8), key64(16))), ul)
		elseif l > 0 then
			local c1, c2, c3 = sbyte(s, 1), sbyte(s, math.floor(l / 2) + 1), sbyte(s, l)
			local combined = U64(c1 * 65536 + c2 * 16777216 + c3 + l * 256)
			local x = bxor(combined, bxor(key32(0), key32(4)))
			x = bxor(x, rshift(x, 33))
			return avalancheSmall(x)
		end
		return 0x2D06800538D394C2ULL
	elseif l <= 128 then
		local acc = ul * PRIME64_1
		local function round(o, k)
			acc = acc + mulFold64(bxor(r64(s, o), key64(k)), bxor(r64(s, o + 8), key64(k + 8)))
			acc = acc + mulFold64(bxor(r64(s, l - o - 16), key64(k + 16)), bxor(r64(s, l - o - 8), key64(k + 24)))
		end
		if l > 32 then
			if l > 64 then
				if l > 96 then
					round(48, 96)
				end
				round(32, 64)
			end
			round(16, 32)
		end
		round(0, 0)
		return avalanche(acc)
	elseif l <= 240 then
		local acc = ul * PRIME64_1
		for i = 0, 7 do
			acc = acc + mulFold64(bxor(r64(s, 16 * i), key64(16 * i)), bxor(r64(s, 16 * i + 8), key64(16 * i + 8)))
		end
		acc = avalanche(acc)
		local rounds = math.floor(l / 16)
		for i = 8, rounds - 1 do
			acc = acc + mulFold64(bxor(r64(s, 16 * i), key64(16 * i - 125)), bxor(r64(s, 16 * i + 8), key64(16 * i - 117)))
		end
		acc = acc + mulFold64(bxor(r64(s, l - 16), key64(119)), bxor(r64(s, l - 8), key64(127)))
		return avalanche(acc)
	end
	local acc = ul * PRIME64_1
	local accs = accumulate(s, l)
	for g = 0, 3 do
		acc = acc + mulFold64(bxor(accs[2 * g + 1], key64(11 + 16 * g)), bxor(accs[2 * g + 2], key64(19 + 16 * g)))
	end
	return avalanche(acc)
end

P.Uint128 = rt.named('github.com/zeebo/xxh3', 'xxh3.Uint128', 'struct')
rt.defineStruct(P.Uint128, { { 'Hi', rt.uint64, 'Hi', 0 }, { 'Lo', rt.uint64, 'Lo', 0 } })
local U128MT = P.Uint128.pmt

local function u128(hi, lo)
	return setmetatable({ Hi = hi, Lo = lo }, U128MT)
end

function P.HashString128(s)
	return u128(hash128(s))
end

function P.Hash128(b)
	return u128(hash128(rt.bytestr(b)))
end

function P.HashString(s)
	return hash64(s)
end

function P.Hash(b)
	return hash64(rt.bytestr(b))
end

-- Bytes is the big-endian encoding of Hi then Lo.
function P.Uint128__Bytes(u)
	local out = {}
	for i = 0, 7 do
		out[i + 1] = tonumber(band(rshift(u.Hi, 56 - 8 * i), 0xFF))
		out[i + 9] = tonumber(band(rshift(u.Lo, 56 - 8 * i), 0xFF))
	end
	return out
end

rt.methods(P.Uint128, { _mBytes = P.Uint128__Bytes }, { _mBytes = P.Uint128__Bytes })

-- Exposed for tests and fast paths in natives.
P.__hash128 = hash128
P.__hash64 = hash64

return P
