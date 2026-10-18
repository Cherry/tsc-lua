-- Native weak: Pointer[T] holds its target through a weak table.
local rt = require('rt')
local P = rt.package('weak')

local weakMT = { __mode = 'v' }

P.Pointer = rt.generic('weak', 'weak.Pointer', 1, function(T)
	rt.defineStruct(T, { { 'ref', rt.any, 'ref', 0 } })
end)

function P.Make(E, p)
	local w = P.Pointer(E).zero()
	if p ~= nil then
		w.ref = setmetatable({ p }, weakMT)
	end
	return w
end

function P.Pointer__Value(_, w)
	local ref = w.ref
	if ref == nil then
		return nil
	end
	return ref[1]
end

return P
