-- scanASCIIWhile calls a predicate for every byte. Predicates that capture nothing are created
-- once per module, so the same closure comes back on every call: after seeing it twice, its
-- ASCII class is recorded as a Lua pattern and the scan runs in string.find.
-- overrides: Scanner__scanASCIIWhile
local rt = require('rt')
local sfind, schar, tconcat = string.find, string.char, table.concat

return function(P)
	-- The scanner's fields, its flattened ScannerState's included, are numbered slots.
	local TEXT, POS, END = rt.fieldkey(P.Scanner, 'text'), rt.fieldkey(P.Scanner, 'pos'), rt.fieldkey(P.Scanner, 'end')
	local patterns = setmetatable({}, { __mode = 'k' })
	local seen = setmetatable({}, { __mode = 'k' })

	-- A class item for byte b: %x is a literal x for any non-alphanumeric x, and %z is NUL,
	-- which would end a LuaJIT pattern.
	local function item(b)
		if b == 0 then
			return '%z'
		elseif (b >= 48 and b <= 57) or (b >= 65 and b <= 90) or (b >= 97 and b <= 122) then
			return schar(b)
		end
		return '%' .. schar(b)
	end

	-- Class items for the bytes in set, with runs written as ranges: matching walks the class
	-- for every byte, so a short class matters.
	local function classItems(set)
		local parts = {}
		local b = 0
		while b <= 127 do
			if set[b] then
				local e = b
				while e < 127 and set[e + 1] do
					e = e + 1
				end
				-- Range endpoints are raw bytes, so they must not be pattern specials or NUL.
				local function plain(c)
					return c ~= 0 and c ~= 37 and c ~= 45 and c ~= 93 and c ~= 94
				end
				if e - b >= 2 and plain(b) and plain(e) then
					parts[#parts + 1] = schar(b) .. '-' .. schar(e)
				else
					for c = b, e do
						parts[#parts + 1] = item(c)
					end
				end
				b = e + 1
			else
				b = b + 1
			end
		end
		return tconcat(parts)
	end

	local function classPattern(pred)
		local yes, no = {}, {}
		local any = false
		for b = 0, 127 do
			if pred(b) then
				yes[b] = true
				any = true
			else
				no[b] = true
			end
		end
		if not any then
			return false
		end
		local positive = '^[' .. classItems(yes) .. ']*'
		-- Bytes from 128 up always stop the scan.
		local negative = '^[^' .. classItems(no) .. '\128-\255]*'
		if #negative < #positive then
			return negative
		end
		return positive
	end

	P.Scanner__scanASCIIWhile = function(s, pred)
		local pattern = patterns[pred]
		if pattern == nil then
			if seen[pred] then
				pattern = classPattern(pred)
				patterns[pred] = pattern
			else
				seen[pred] = true
			end
		end
		if pattern == false then
			return
		end
		local text, pos, stop = s[TEXT], s[POS], s[END]
		if pattern then
			local _, last = sfind(text, pattern, pos + 1)
			if last > stop then
				last = stop
			end
			s[POS] = last
			return
		end
		local i = pos
		while i < stop do
			local b = text:byte(i + 1)
			if b >= 128 or not pred(b) then
				break
			end
			i = i + 1
		end
		s[POS] = i
	end
end
