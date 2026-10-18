-- SourceFile.ECMALineMap guards its lazily computed line map with a RWMutex and a deferred
-- unlock; tsc-lua runs on one thread, so the lock and the defer frame are pure overhead on
-- every position-to-line lookup.
-- overrides: SourceFile__ECMALineMap
local rt = require('rt')

return function(P)
	local core = rt.import('ts.core')
	local LINE_MAP, TEXT = rt.fieldkey(P.SourceFile, 'ecmaLineMap'), rt.fieldkey(P.SourceFile, 'text')
	P.SourceFile__ECMALineMap = function(node)
		local lineMap = node[LINE_MAP]
		if lineMap == nil then
			lineMap = core.ComputeECMALineStarts(node[TEXT])
			node[LINE_MAP] = lineMap
		end
		return lineMap
	end
end
