-- Native line start computation: string.find skips between line terminators in C instead of
-- visiting every byte in Lua. U+2028 and U+2029 are E2 80 A8/A9 in UTF-8, and E2 is never a
-- continuation byte, so this matches Go's per-rune decoding even for invalid UTF-8.
-- Arena.New extends its slice in place instead of making a new slice header per allocation:
-- no Arena method hands a.data itself out (NewSlice and Clone make their own headers). So the
-- slots of a chunk are only filled as New and NewSlice hand them out, where slices.Grow would
-- make every zero value up front and then copy it.
-- overrides: ComputeECMALineStartsSeq ComputeECMALineStarts Arena__New Arena__NewSlice
local rt = require('rt')
local sbyte, sfind = string.byte, string.find
local tnew = require('table.new')

-- nextLineStart finds the first line terminator at or after byte init (1-based) and returns the
-- 0-based position after it with the byte to continue from, or nil when there is none.
local function nextLineStart(text, init)
	while true do
		local i = sfind(text, '[\r\n\226]', init)
		if i == nil then
			return nil
		end
		local b = sbyte(text, i)
		if b == 10 then
			return i, i + 1
		elseif b == 13 then
			if sbyte(text, i + 1) == 10 then
				return i + 1, i + 2
			end
			return i, i + 1
		end
		local b2, b3 = sbyte(text, i + 1, i + 2)
		if b2 == 0x80 and (b3 == 0xA8 or b3 == 0xA9) then
			return i + 2, i + 3
		end
		init = i + 1
	end
end

return function(P)
	local DATA = rt.fieldkey(P.Arena(rt.int), 'data')

	local function chunk(n)
		return { tnew(n, 0), 1, 0, n }
	end

	P.Arena__New = function(T, a)
		local data = a[DATA]
		if data == nil or data[3] == data[4] then
			local size = 0
			if data ~= nil then
				size = data[3]
			end
			data = chunk(P.nextArenaSize(T, size))
			a[DATA] = data
		end
		local index = data[3]
		data[3] = index + 1
		data[1][data[2] + index] = T.zero()
		return rt.elemptrT(T, data, index)
	end

	P.Arena__NewSlice = function(T, a, size)
		if size == 0 then
			return nil
		end
		local data = a[DATA]
		local len = data and data[3] or 0
		if len + size > (data and data[4] or 0) then
			local nextSize = P.nextArenaSize(T, len)
			if size > nextSize then
				return rt.mkslice(size, nil, T.zero)
			end
			data = chunk(nextSize)
			a[DATA] = data
			len = 0
		end
		local arr, base, zero = data[1], data[2] + len, T.zero
		for i = 0, size - 1 do
			arr[base + i] = zero()
		end
		data[3] = len + size
		return { arr, base, size, size }
	end

	P.ComputeECMALineStartsSeq = function(text)
		return function(yield)
			local lineStart, init = 0, 1
			while lineStart do
				if not yield(lineStart) then
					return
				end
				lineStart, init = nextLineStart(text, init)
			end
		end
	end

	-- Without the iterator: ranging over it makes two closures, which LuaJIT cannot compile.
	P.ComputeECMALineStarts = function(text)
		local starts, n = { 0 }, 1
		local lineStart, init = nextLineStart(text, 1)
		while lineStart do
			n = n + 1
			starts[n] = lineStart
			lineStart, init = nextLineStart(text, init)
		end
		return { starts, 1, n, n }
	end

	-- __countLineStarts returns how many lines text has and where its last one starts.
	P.__countLineStarts = function(text)
		local count, last = 1, 0
		local lineStart, init = nextLineStart(text, 1)
		while lineStart do
			count, last = count + 1, lineStart
			lineStart, init = nextLineStart(text, init)
		end
		return count, last
	end
end
