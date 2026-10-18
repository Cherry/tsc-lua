-- strings.Builder backed by a table of Lua string parts instead of a []byte (a table of
-- integers), joined once when String is called. The state lives in the declared buf field
-- (as {parts, count, length}) so that struct copies of a Builder keep their contents.
-- IndexAny with an ASCII set uses a Lua character class: ASCII bytes never occur inside a
-- multibyte UTF-8 sequence, so the first matching byte is the first matching rune.
-- Every Builder method is listed so none of the []byte-based originals is inlined.
-- overrides: Builder__WriteString Builder__WriteByte Builder__WriteRune Builder__Write Builder__String Builder__Len Builder__Reset Builder__Grow Builder__Cap Builder__grow Builder__copyCheck IndexAny genericReplacer__lookup genericReplacer__WriteString
local rt = require('rt')

local tconcat, schar, sfind = table.concat, string.char, string.find

return function(P)
	local function state(b)
		local st = b.buf
		if st == nil then
			st = { {}, 0, 0 }
			b.buf = st
		end
		return st
	end

	local function add(b, s)
		local st = state(b)
		local n = st[2] + 1
		st[1][n] = s
		st[2] = n
		st[3] = st[3] + #s
	end

	P.Builder__WriteString = function(b, s)
		add(b, s)
		return #s, nil
	end

	P.Builder__WriteByte = function(b, c)
		add(b, schar(c))
		return nil
	end

	P.Builder__WriteRune = function(b, r)
		local s = rt.runestr(r)
		add(b, s)
		return #s, nil
	end

	P.Builder__Write = function(b, bytes)
		local s = rt.bytestr(bytes)
		add(b, s)
		return #s, nil
	end

	P.Builder__String = function(b)
		local st = b.buf
		if st == nil or st[2] == 0 then
			return ''
		end
		local parts, n = st[1], st[2]
		if n > 1 then
			local s = tconcat(parts, '', 1, n)
			for i = n, 2, -1 do
				parts[i] = nil
			end
			parts[1] = s
			st[2] = 1
			return s
		end
		return parts[1]
	end

	P.Builder__Len = function(b)
		local st = b.buf
		return st and st[3] or 0
	end

	P.Builder__Cap = P.Builder__Len

	P.Builder__Reset = function(b)
		b.buf = nil
		b.addr = nil
	end

	P.Builder__Grow = function(_, n)
		if n < 0 then
			rt.panic('strings.Builder.Grow: negative count')
		end
	end

	local indexAny = P.IndexAny
	P.IndexAny = function(s, chars)
		local class = rt.anyClass(chars)
		if not class then
			return indexAny(s, chars)
		end
		local i = sfind(s, class)
		if i == nil then
			return -1
		end
		return i - 1
	end

	-- The generic Replacer looks up a key at every position of the input; Go passes s[i:], which
	-- copies the rest of the string here (and LuaJIT hashes every string it makes), so these
	-- versions walk the trie from an offset instead. The root trie node is flattened into r.
	local sbyte, ssub = string.byte, string.sub
	local function lookupAt(r, s, i, ignoreRoot)
		local val, keylen, found = '', 0, false
		local bestPriority = 0
		local node = r
		local n = 0
		local len = #s
		while node ~= nil do
			if node.priority > bestPriority and not (ignoreRoot and node == r) then
				bestPriority = node.priority
				val = node.value
				keylen = n
				found = true
			end
			if i >= len then
				break
			end
			local tbl = node._Etable
			local prefix = node.prefix
			if tbl ~= nil then
				local index = r.mapping[sbyte(s, i + 1) + 1]
				if index == r.tableSize then
					break
				end
				node = tbl[1][tbl[2] + index]
				i = i + 1
				n = n + 1
			elseif prefix ~= '' and ssub(s, i + 1, i + #prefix) == prefix then
				n = n + #prefix
				i = i + #prefix
				node = node._Enext
			else
				break
			end
		end
		return val, keylen, found
	end

	P.genericReplacer__lookup = function(r, s, ignoreRoot)
		return lookupAt(r, s, 0, ignoreRoot)
	end

	P.genericReplacer__WriteString = function(r, w, s)
		local sw = P.getStringWriter(w)
		local n, last, i = 0, 0, 0
		local len = #s
		local prevMatchEmpty = false
		local rootTable, mapping, tableSize = r._Etable, r.mapping, r.tableSize
		while i <= len do
			local skip = false
			-- Fast path: s[i] is not a prefix of any pattern.
			if i ~= len and r.priority == 0 then
				local index = mapping[sbyte(s, i + 1) + 1]
				if index == tableSize or rootTable[1][rootTable[2] + index] == nil then
					i = i + 1
					skip = true
				end
			end
			if not skip then
				-- Ignore the empty match iff the previous loop found the empty match.
				local val, keylen, match = lookupAt(r, s, i, prevMatchEmpty)
				prevMatchEmpty = match and keylen == 0
				if match then
					local wn, err = sw:_mWriteString(ssub(s, last + 1, i))
					n = n + wn
					if err ~= nil then
						return n, err
					end
					wn, err = sw:_mWriteString(val)
					n = n + wn
					if err ~= nil then
						return n, err
					end
					i = i + keylen
					last = i
				else
					i = i + 1
				end
			end
		end
		if last ~= len then
			local wn, err = sw:_mWriteString(ssub(s, last + 1))
			n = n + wn
			return n, err
		end
		return n, nil
	end
end
