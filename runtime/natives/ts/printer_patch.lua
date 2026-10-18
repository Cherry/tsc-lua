-- textWriter counts the lines of every string it writes by ranging over
-- core.ComputeECMALineStartsSeq, which makes two closures per write; LuaJIT cannot compile those,
-- and the checker writes a great deal when it prints types. This counts them directly.
-- overrides: textWriter__updateLineCountAndPosFor
local rt = require('rt')

return function(P)
	local core = rt.import('ts.core')
	local strings = rt.import('go.strings')
	local T = P.textWriter
	local LINE_START, LINE_COUNT, LINE_POS = rt.fieldkey(T, 'lineStart'), rt.fieldkey(T, 'lineCount'), rt.fieldkey(T, 'linePos')

	-- The builder is flattened into the writer, so the writer is its receiver.
	for _, f in ipairs(T.fields) do
		assert(f[3] ~= 'builder' or f[6], 'textWriter.builder is not flattened')
	end
	P.textWriter__updateLineCountAndPosFor = function(w, s)
		local count, lastLineStart = core.__countLineStarts(s)
		if count > 1 then
			w[LINE_COUNT] = w[LINE_COUNT] + count - 1
			local curLen = strings.Builder__Len(w)
			w[LINE_POS] = curLen - #s + lastLineStart
			w[LINE_START] = (w[LINE_POS] - curLen) == 0
			return
		end
		w[LINE_START] = false
	end
end
