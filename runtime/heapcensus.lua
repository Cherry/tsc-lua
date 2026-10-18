-- Development aid: counts reachable Lua objects by Go type and estimates their memory, to see
-- which structures dominate the heap. Sizes are estimates from LuaJIT's (GC64) object layout.
local getmetatable, rawget, next, type = getmetatable, rawget, next, type

local TABLE, NODE, ARRAY_SLOT, STRING, CLOSURE, UPVALUE = 64, 24, 8, 24, 40, 32

local function hashSlots(n)
	if n == 0 then
		return 0
	end
	local slots = 1
	while slots < n do
		slots = slots * 2
	end
	return slots
end

local function label(t)
	local mt = getmetatable(t)
	if type(mt) == 'table' then
		local s = rawget(mt, '__struct')
		if s then
			return s.str or '?struct'
		end
	end
	if type(rawget(t, 1)) == 'table' and type(rawget(t, 3)) == 'number' and type(rawget(t, 4)) == 'number' and rawget(t, 5) == nil then
		return '(slice header)'
	end
	if rawget(t, '__t') ~= nil and next(t, next(t, next(t))) == nil then
		return '(interface box)'
	end
	if #t > 0 then
		return '(array table)'
	end
	if type(mt) == 'table' and mt.__mode then
		return '(weak table)'
	end
	if rawget(t, require('rt').LEN) ~= nil then
		return '(map)'
	end
	-- Otherwise name the table by its first few string keys, e.g. pointer boxes {v} or slot proxies.
	local keys = {}
	for k in next, t do
		if type(k) == 'string' then
			keys[#keys + 1] = k
			if #keys == 4 then
				break
			end
		end
	end
	table.sort(keys)
	return '(table {' .. table.concat(keys, ',') .. '})'
end

local function closureLabel(f)
	local info = debug.getinfo(f, 'S')
	return '(closure ' .. info.short_src:gsub('^.*[/\\]', '') .. ':' .. info.linedefined .. ')'
end

return function(roots, out, top)
	local seen = {}
	local stats = {}
	local stack = {}
	local function push(v)
		local tv = type(v)
		if (tv == 'table' or tv == 'function' or tv == 'string' or tv == 'thread' or tv == 'userdata') and not seen[v] then
			seen[v] = true
			stack[#stack + 1] = v
		end
	end
	local function add(name, bytes)
		local s = stats[name]
		if not s then
			s = { 0, 0 }
			stats[name] = s
		end
		s[1] = s[1] + 1
		s[2] = s[2] + bytes
	end
	for _, r in ipairs(roots) do
		push(r)
	end
	push(seen)
	seen[seen] = true
	seen[stats] = true
	seen[stack] = true
	while #stack > 0 do
		local v = stack[#stack]
		stack[#stack] = nil
		local tv = type(v)
		if tv == 'string' then
			add('(string)', STRING + #v)
		elseif tv == 'table' then
			local arr = #v
			local hash = 0
			for k, x in next, v do
				if not (type(k) == 'number' and k >= 1 and k <= arr and k % 1 == 0) then
					hash = hash + 1
				end
				push(k)
				push(x)
			end
			push(getmetatable(v))
			add(label(v), TABLE + arr * ARRAY_SLOT + hashSlots(hash) * NODE)
		elseif tv == 'function' then
			local i, bytes = 1, CLOSURE
			while true do
				local name, uv = debug.getupvalue(v, i)
				if name == nil then
					break
				end
				bytes = bytes + UPVALUE
				push(uv)
				i = i + 1
			end
			add(closureLabel(v), bytes)
		elseif tv == 'thread' then
			local level = 0
			while debug.getinfo(v, level, 'l') do
				local i = 1
				while true do
					local name, x = debug.getlocal(v, level, i)
					if name == nil then
						break
					end
					push(x)
					i = i + 1
				end
				level = level + 1
			end
			add('(coroutine)', 1024)
		end
	end
	local rows = {}
	local total = 0
	for name, s in pairs(stats) do
		rows[#rows + 1] = { name, s[1], s[2] }
		total = total + s[2]
	end
	table.sort(rows, function(a, b)
		return a[3] > b[3]
	end)
	out:write(
		string.format(
			'heap census: %.1f MB estimated reachable (%.1f MB reported by Lua)\n',
			total / 1048576,
			collectgarbage('count') / 1024
		)
	)
	for i = 1, math.min(top, #rows) do
		local r = rows[i]
		out:write(string.format('%10.1f MB %10d  %s\n', r[3] / 1048576, r[2], r[1]))
	end
	out:flush()
end
