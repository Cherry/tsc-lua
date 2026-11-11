-- Native hash/maphash: Go's version hashes through the runtime's map internals. This covers
-- MakeSeed and String with seeded 32-bit FNV-1a in plain numbers, which LuaJIT compiles
-- without boxing; callers only need a well spread value, not Go's exact hash.
local rt = require('rt')
local P = rt.package('hash/maphash')

local sbyte = string.byte
local bxor, lshift, rshift, tobit = bit.bxor, bit.lshift, bit.rshift, bit.tobit
local U64 = rt.U64

P.Seed = rt.named('hash/maphash', 'maphash.Seed', 'struct')
rt.defineStruct(P.Seed, { { 's', rt.uint64, 's', 0 } })

-- A fixed seed keeps runs reproducible; nothing observable depends on its value.
function P.MakeSeed()
	local seed = P.Seed.zero()
	seed.s = U64(0x811c9dc5)
	return seed
end

function P.String(seed, s)
	local h = tobit(tonumber(seed.s % 0x100000000))
	for i = 1, #s do
		h = bxor(h, sbyte(s, i))
		-- h * 16777619 split as h * 403 + h << 24, so the product stays exact in a double.
		h = tobit(h * 403 + lshift(h, 24))
	end
	-- FNV's high bits mix better than its low bits, and callers mask off the low ones.
	h = bxor(h, rshift(h, 16))
	return U64(h % 0x100000000)
end

return P
