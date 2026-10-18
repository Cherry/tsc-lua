-- Packs Lua modules (as stripped bytecode) and data files into the archive format read by
-- native/host.c. Usage: luajit pack.lua <manifest> <output>
-- Manifest lines: "m<TAB>module.name<TAB>path" or "d<TAB>data/name<TAB>path".
local manifestPath, outPath = arg[1], arg[2]
if not manifestPath or not outPath then
	io.stderr:write('usage: pack.lua <manifest> <output>\n')
	os.exit(2)
end

local function readFile(path)
	local f = assert(io.open(path, 'rb'))
	local data = f:read('*a')
	f:close()
	return data
end

local entries = {}
for line in io.lines(manifestPath) do
	local kind, name, path = line:match('^(%a)\t([^\t]+)\t(.+)$')
	if kind then
		local data = readFile(path)
		if kind == 'm' then
			local fn, err = load(data, '@' .. name:gsub('%.', '/') .. '.lua')
			if not fn then
				error(err)
			end
			data = string.dump(fn, os.getenv('TSCLUA_STRIP') == '1')
			entries[#entries + 1] = { name = name, kind = 0, data = data }
		else
			entries[#entries + 1] = { name = name, kind = 1, data = data }
		end
	end
end

-- The host binary searches entries by (name bytes, kind).
table.sort(entries, function(a, b)
	if a.name ~= b.name then
		return a.name < b.name
	end
	return a.kind < b.kind
end)

local out = assert(io.open(outPath, 'wb'))
-- u32 encodes a number as 4 little endian bytes.
local function u32(n)
	return string.char(n % 256, math.floor(n / 256) % 256, math.floor(n / 65536) % 256, math.floor(n / 16777216) % 256)
end

out:write('TSCLUA01', u32(#entries))
local total = 0
for _, e in ipairs(entries) do
	out:write(u32(#e.name), e.name, u32(e.kind), u32(#e.data), e.data)
	total = total + #e.data
end
out:close()
io.stderr:write(string.format('packed %d entries (%.1f MB)\n', #entries, total / 1048576))
