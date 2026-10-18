-- maps.clone is provided by the Go runtime; here it is a shallow table copy (which also
-- copies the entry count and any nil sentinels or key/value pairs as-is).
local rt = require('rt')

return function(P)
	P.clone = function(m)
		local T = rt.typeof(m)
		local src = rt.unbox(m)
		if src == nil then
			return m
		end
		local dst = require('table.new')(0, src[rt.LEN] + 1)
		local storesPairs = T and T.key and not T.key.direct
		for k, v in next, src do
			if storesPairs and k ~= rt.LEN then
				dst[k] = { v[1], v[2] }
			else
				dst[k] = v
			end
		end
		return rt.box(T, dst)
	end
end
