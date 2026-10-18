-- Native path/filepath. The Lua build is OS independent, so Windows volume semantics are
-- decided at runtime; paths are produced with '/' separators, which Windows also accepts.
-- deps: path io/fs errors os
local rt = require('rt')
local P = rt.package('path/filepath')
local path = rt.import('go.path')
local fs = rt.import('go.io.fs')
local errors = rt.import('go.errors')
local os_ = rt.import('go.os')

local isWindows = package.config:sub(1, 1) == '\\'

P.Separator = 47
P.ListSeparator = isWindows and 59 or 58
P.SkipDir = fs.SkipDir
P.SkipAll = fs.SkipAll
P.ErrBadPattern = path.ErrBadPattern

local function toSlash(p)
	if isWindows then
		return (p:gsub('\\', '/'))
	end
	return p
end

-- volumeLen returns the length of a Windows volume prefix ("C:" or "//server/share").
local function volumeLen(p)
	if not isWindows then
		return 0
	end
	if #p >= 2 and p:sub(2, 2) == ':' and p:sub(1, 1):match('%a') then
		return 2
	end
	local s = toSlash(p)
	if s:sub(1, 2) == '//' and s:sub(3, 3) ~= '/' then
		local server = s:find('/', 3, true)
		if server then
			local share = s:find('/', server + 1, true)
			return share and share - 1 or #s
		end
	end
	return 0
end

function P.VolumeName(p)
	return p:sub(1, volumeLen(p))
end

function P.ToSlash(p)
	return toSlash(p)
end

function P.FromSlash(p)
	return p
end

function P.IsAbs(p)
	if isWindows then
		local v = volumeLen(p)
		if v == 0 then
			return false
		end
		if v > 2 then
			return true
		end
		local c = p:sub(3, 3)
		return c == '/' or c == '\\'
	end
	return p:sub(1, 1) == '/'
end

function P.Clean(p)
	local v = volumeLen(p)
	local vol = p:sub(1, v)
	local rest = toSlash(p:sub(v + 1))
	if rest == '' then
		if v > 2 then
			return toSlash(vol) .. '/'
		end
		return vol .. '.'
	end
	local cleaned = path.Clean(rest)
	if v > 2 then
		vol = toSlash(vol)
	end
	return vol .. cleaned
end

function P.Join(elems)
	local parts = {}
	if elems ~= nil then
		for i = 0, elems[3] - 1 do
			local e = elems[1][elems[2] + i]
			if e ~= '' then
				parts[#parts + 1] = e
			end
		end
	end
	if #parts == 0 then
		return ''
	end
	return P.Clean(table.concat(parts, '/'))
end

function P.Split(p)
	local v = volumeLen(p)
	local i = #p
	while i > v and p:sub(i, i) ~= '/' and not (isWindows and p:sub(i, i) == '\\') do
		i = i - 1
	end
	return p:sub(1, i), p:sub(i + 1)
end

function P.Base(p)
	if p == '' then
		return '.'
	end
	p = toSlash(p)
	while #p > 1 and p:sub(-1) == '/' do
		p = p:sub(1, -2)
	end
	p = p:sub(volumeLen(p) + 1)
	local base = p:match('([^/]*)$')
	if base == '' then
		return '/'
	end
	return base
end

function P.Dir(p)
	local vol = P.VolumeName(p)
	local i = #p
	while i > #vol and p:sub(i, i) ~= '/' and not (isWindows and p:sub(i, i) == '\\') do
		i = i - 1
	end
	local dir = P.Clean(p:sub(#vol + 1, i))
	if dir == '.' and #vol > 2 then
		return vol
	end
	return vol .. dir
end

function P.Ext(p)
	for i = #p, 1, -1 do
		local c = p:sub(i, i)
		if c == '/' or (isWindows and c == '\\') then
			break
		end
		if c == '.' then
			return p:sub(i)
		end
	end
	return ''
end

function P.Abs(p)
	if P.IsAbs(p) then
		return P.Clean(p), nil
	end
	local wd, err = os_.Getwd()
	if err ~= nil then
		return '', err
	end
	if isWindows and volumeLen(p) == 2 then
		-- Drive relative path like "C:foo": resolve against that drive's root.
		return P.Clean(p:sub(1, 2) .. '/' .. p:sub(3)), nil
	end
	return P.Clean(toSlash(wd) .. '/' .. p), nil
end

function P.Rel(basepath, targpath)
	local baseVol, targVol = P.VolumeName(basepath), P.VolumeName(targpath)
	local base = P.Clean(basepath)
	local targ = P.Clean(targpath)
	if isWindows then
		if base:lower() == targ:lower() then
			return '.', nil
		end
	elseif targ == base then
		return '.', nil
	end
	base = base:sub(#baseVol + 1)
	targ = targ:sub(#targVol + 1)
	if base == '.' then
		base = ''
	end
	local baseSlashed = base:sub(1, 1) == '/'
	local targSlashed = targ:sub(1, 1) == '/'
	if baseSlashed ~= targSlashed or (isWindows and baseVol:lower() ~= targVol:lower()) or (not isWindows and baseVol ~= targVol) then
		return '', errors.New("Rel: can't make " .. targpath .. ' relative to ' .. basepath)
	end
	local bparts, tparts = {}, {}
	for s in base:gmatch('[^/]+') do
		bparts[#bparts + 1] = s
	end
	for s in targ:gmatch('[^/]+') do
		tparts[#tparts + 1] = s
	end
	local i = 1
	local function same(a, b)
		if isWindows then
			return a:lower() == b:lower()
		end
		return a == b
	end
	while i <= #bparts and i <= #tparts and same(bparts[i], tparts[i]) do
		i = i + 1
	end
	local out = {}
	for _ = i, #bparts do
		if bparts[_] == '..' then
			return '', errors.New("Rel: can't make " .. targpath .. ' relative to ' .. basepath)
		end
		out[#out + 1] = '..'
	end
	for j = i, #tparts do
		out[#out + 1] = tparts[j]
	end
	if #out == 0 then
		return '.', nil
	end
	return table.concat(out, '/'), nil
end

function P.EvalSymlinks(p)
	local native = os_.__native
	if native then
		local real, msg, code = native.realpath(p)
		if not real then
			return '', os_.__pathError('lstat', p, msg, code)
		end
		return toSlash(real), nil
	end
	return P.Clean(p), nil
end

function P.SplitList(p)
	local res = nil
	if p == '' then
		return nil
	end
	local sep = isWindows and ';' or ':'
	for part in (p .. sep):gmatch('(.-)' .. sep) do
		res = rt.append1(res, part)
	end
	return res
end

function P.Match(pattern, name)
	return path.Match(toSlash(pattern), toSlash(name))
end

function P.Glob(pattern)
	local dir, file = P.Split(pattern)
	if dir == '' then
		dir = '.'
	end
	local entries = os_.ReadDir(dir)
	local res = nil
	if entries then
		for i = 0, entries[3] - 1 do
			local name = entries[1][entries[2] + i]:_mName()
			local ok = path.Match(file, name)
			if ok then
				res = rt.append1(res, P.Join({ { dir, name }, 1, 2, 2 }))
			end
		end
	end
	return res, nil
end

local function walkDir(root, d, fn)
	local err = fn(root, d, nil)
	if err ~= nil or not d:_mIsDir() then
		if err ~= nil and d:_mIsDir() and rt.ieq(err, fs.SkipDir) then
			return nil
		end
		return err
	end
	local entries, rerr = os_.ReadDir(root)
	if rerr ~= nil then
		err = fn(root, d, rerr)
		if err ~= nil then
			if rt.ieq(err, fs.SkipDir) then
				return nil
			end
			return err
		end
	end
	if entries then
		for i = 0, entries[3] - 1 do
			local e = entries[1][entries[2] + i]
			local sub = root .. '/' .. e:_mName()
			local werr = walkDir(sub, e, fn)
			if werr ~= nil then
				if rt.ieq(werr, fs.SkipDir) then
					break
				end
				return werr
			end
		end
	end
	return nil
end

function P.WalkDir(root, fn)
	local info, err = os_.Lstat(root)
	if err ~= nil then
		err = fn(root, nil, err)
	else
		err = walkDir(root, fs.FileInfoToDirEntry(info), fn)
	end
	if err ~= nil and (rt.ieq(err, fs.SkipDir) or rt.ieq(err, fs.SkipAll)) then
		return nil
	end
	return err
end

function P.Walk(root, fn)
	return P.WalkDir(root, function(p, d, err)
		if err ~= nil then
			return fn(p, nil, err)
		end
		local info, ierr = d:_mInfo()
		return fn(p, info, ierr)
	end)
end

function P.IsLocal(p)
	return not P.IsAbs(p) and not toSlash(P.Clean(p)):match('^%.%.') and p ~= ''
end

function P.Localize(p)
	return p, nil
end

return P
