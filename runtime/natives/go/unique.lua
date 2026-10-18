-- Native unique: Handle[T] canonicalizes comparable values; the handle is the value itself
-- for directly keyed types and an interned table otherwise.
local rt = require('rt')
local P = rt.package('unique')

P.Handle = rt.generic('unique', 'unique.Handle', 1, function(T, E)
	rt.defineStruct(T, { { 'value', E, 'value', 0 } })
	rt.methods(T, {
		_mValue = function(h)
			return h.value
		end,
	}, {
		_mValue = function(h)
			return h.value
		end,
	})
end)

local interned = setmetatable({}, { __mode = 'k' })

function P.Make(E, v)
	local cache = interned[E]
	if not cache then
		cache = setmetatable({}, { __mode = 'v' })
		interned[E] = cache
	end
	local key = E.mapkey(v)
	local h = cache[key]
	if not h then
		h = setmetatable({ value = v }, P.Handle(E).pmt)
		cache[key] = h
	end
	return h
end

function P.Handle__Value(_, h)
	return h.value
end

return P
