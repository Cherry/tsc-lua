-- Native os package backed by Lua io and the tscnative C module.
-- deps: io/fs internal/oserror time io errors
local rt = require('rt')
local P = rt.package('os')
local fs = rt.import('go.io.fs')
local oserror = rt.import('go.internal.oserror')
local time = rt.import('go.time')
local io_ = rt.import('go.io')
local errors = rt.import('go.errors')

local ok, native = pcall(require, 'tscnative')
if not ok then
	native = nil
end
P.__native = native

if native then
	-- Go writes bytes verbatim; avoid CRLF translation on Windows.
	native.setbinary()
	rt.clock = native.monotime
	rt.sleepNative = native.sleep
	rt.wallclock = native.walltime
end
-- On Windows, io.open takes ANSI code page paths and fails past MAX_PATH; tscnative.open does not.
local openFile = native and native.open or io.open

local isWindows = package.config:sub(1, 1) == '\\'
P.PathSeparator = isWindows and 92 or 47
P.PathListSeparator = isWindows and 59 or 58
P.DevNull = isWindows and 'NUL' or '/dev/null'

function P.IsPathSeparator(c)
	if isWindows then
		return c == 92 or c == 47
	end
	return c == 47
end

---------------------------------------------------------------------------
-- Errors

-- syscallError carries an errno-like code and maps to the oserror sentinels via Is.
local errnoType = rt.named('syscall', 'syscall.Errno', 'struct')
rt.defineStruct(errnoType, { { 'msg', rt.string, 'msg', 0 }, { 'code', rt.string, 'code', 0 } })
local codeTargets = {
	ENOENT = function()
		return oserror.ErrNotExist
	end,
	EEXIST = function()
		return oserror.ErrExist
	end,
	EACCES = function()
		return oserror.ErrPermission
	end,
	ENOTEMPTY = function()
		return oserror.ErrExist
	end,
}
local function errnoError(e)
	return e.msg
end
local function errnoIs(e, target)
	local f = codeTargets[e.code]
	return f ~= nil and rt.ieq(f(), target)
end
rt.methods(errnoType, {}, { _mError = errnoError, _mIs = errnoIs })

local function errno(msg, code)
	return setmetatable({ msg = msg or 'error', code = code or 'EIO' }, errnoType.pmt)
end
P.__errno = errno

local function pathError(op, path, msg, code)
	local e = fs.PathError.zero()
	e.Op = op
	e.Path = path
	e.Err = errno(msg, code)
	return e
end
P.__pathError = pathError

P.ErrInvalid = fs.ErrInvalid
P.ErrPermission = fs.ErrPermission
P.ErrExist = fs.ErrExist
P.ErrNotExist = fs.ErrNotExist
P.ErrClosed = fs.ErrClosed
P.ErrNoDeadline = errors.New('file type does not support deadline')
P.ErrDeadlineExceeded = errors.New('i/o timeout')
P.ErrProcessDone = errors.New('os: process already finished')

local function underlying(err)
	local T = rt.typeof(err)
	if T and T.kind == 'ptr' and T.elem == fs.PathError then
		return rt.unbox(err).Err
	end
	if getmetatable(err) == fs.PathError.pmt then
		return err.Err
	end
	return err
end

local function underlyingIs(err, target)
	err = underlying(err)
	if rt.ieq(err, target) then
		return true
	end
	local T = rt.typeof(err)
	if T == errnoType.ptr or getmetatable(err) == errnoType.pmt then
		return errnoIs(err, target)
	end
	return false
end

function P.IsNotExist(err)
	return underlyingIs(err, oserror.ErrNotExist)
end

function P.IsExist(err)
	return underlyingIs(err, oserror.ErrExist)
end

function P.IsPermission(err)
	return underlyingIs(err, oserror.ErrPermission)
end

function P.IsTimeout()
	return false
end

---------------------------------------------------------------------------
-- File info

local fileStat = rt.named('os', 'os.fileStat', 'struct')
rt.defineStruct(fileStat, {
	{ 'name', rt.string, 'name', 0 },
	{ 'size', rt.int64, 'size', 0 },
	{ 'mode', fs.FileMode, 'mode', 0 },
	{ 'mtime', rt.int64, 'mtime', 0 },
	{ 'mtimensec', rt.int64, 'mtimensec', 0 },
})
P.fileStat = fileStat

local band, bor = bit.band, bit.bor
-- FileMode is a uint32, held as its signed 32-bit pattern.
local MODE_DIR = bit.tobit(0x80000000)
local MODE_SYMLINK = 0x08000000

local fileStatMethods = {
	_mName = function(s)
		return s.name
	end,
	_mSize = function(s)
		return s.size
	end,
	_mMode = function(s)
		return s.mode
	end,
	_mModTime = function(s)
		return time.Unix(s.mtime, s.mtimensec)
	end,
	_mIsDir = function(s)
		return band(s.mode, MODE_DIR) ~= 0
	end,
	_mSys = function()
		return nil
	end,
}
rt.methods(fileStat, {}, fileStatMethods)

local function basename(path)
	local name = path:match('([^/\\]+)[/\\]*$')
	return name or path
end

local function makeStat(path, st)
	local mode = st.perm or 0x1A4
	if st.isdir then
		mode = bor(mode, MODE_DIR)
	end
	if st.islink then
		mode = bor(mode, MODE_SYMLINK)
	end
	return setmetatable({
		name = basename(path),
		size = rt.I64(st.size or 0),
		mode = mode,
		mtime = rt.I64(st.mtime or 0),
		mtimensec = rt.I64(st.mtimensec or 0),
	}, fileStat.pmt)
end

local function fallbackStat(path)
	local f = openFile(path, 'rb')
	if f then
		local size = f:seek('end') or 0
		f:close()
		return { isdir = false, size = size, mtime = 0 }
	end
	-- Directories cannot be opened with io.open on every platform; probe for an entry.
	local d = openFile(path .. '/.', 'rb')
	if d then
		d:close()
		return { isdir = true, size = 0, mtime = 0 }
	end
	return nil, 'no such file or directory', 'ENOENT'
end

local function rawStat(path, follow)
	if native then
		if follow then
			return native.stat(path)
		end
		return native.lstat(path)
	end
	return fallbackStat(path)
end
P.__rawStat = rawStat

function P.Stat(name)
	local st, msg, code = rawStat(name, true)
	if not st then
		return nil, pathError('stat', name, msg, code)
	end
	return makeStat(name, st), nil
end

function P.Lstat(name)
	local st, msg, code = rawStat(name, false)
	if not st then
		return nil, pathError('lstat', name, msg, code)
	end
	return makeStat(name, st), nil
end

function P.SameFile(a, b)
	return a ~= nil and b ~= nil and a.name == b.name and a.size == b.size and a.mtime == b.mtime
end

-- Directory entries ----------------------------------------------------------

local dirEntry = rt.named('os', 'os.dirEntry', 'struct')
rt.defineStruct(dirEntry, {
	{ 'dir', rt.string, 'dir', 0 },
	{ 'name', rt.string, 'name', 0 },
	{ 'typ', fs.FileMode, 'typ', 0 },
})
rt.methods(dirEntry, {}, {
	_mName = function(e)
		return e.name
	end,
	_mIsDir = function(e)
		return band(e.typ, MODE_DIR) ~= 0
	end,
	_mType = function(e)
		return e.typ
	end,
	_mInfo = function(e)
		return P.Lstat(e.dir .. '/' .. e.name)
	end,
	_mString = function(e)
		return fs.FormatDirEntry(e)
	end,
})

local MODE_IRREGULAR = 0x00080000

local function readDirRaw(name)
	if not native then
		return nil, 'directory listing requires the tscnative module', 'EIO'
	end
	return native.readdir(name)
end
P.__readDirRaw = readDirRaw

local function listDir(name)
	local list, msg, code = readDirRaw(name)
	if not list then
		return nil, pathError('open', name, msg, code)
	end
	table.sort(list, function(a, b)
		return a[1] < b[1]
	end)
	local res = nil
	for _, e in ipairs(list) do
		local typ = 0
		if e[2] == 'd' then
			typ = MODE_DIR
		elseif e[2] == 'l' then
			if isWindows then
				-- Windows reparse points are reported as irregular, like Go does for junctions.
				typ = MODE_IRREGULAR
			else
				typ = MODE_SYMLINK
			end
		elseif e[2] == '?' then
			local st = native.lstat(name .. '/' .. e[1])
			if st then
				if st.islink then
					typ = MODE_SYMLINK
				elseif st.isdir then
					typ = MODE_DIR
				end
			end
		end
		res = rt.append1(res, setmetatable({ dir = name, name = e[1], typ = typ }, dirEntry.pmt))
	end
	return res, nil
end

function P.ReadDir(name)
	return listDir(name)
end

---------------------------------------------------------------------------
-- Files

P.File = rt.named('os', 'os.File', 'struct')
rt.defineStruct(P.File, { { 'name', rt.string, 'name', 0 } })

local function fileFrom(fh, name, isdir)
	local f = P.File.zero()
	f.name = name
	rawset(f, 'fh', fh)
	rawset(f, 'isdir', isdir)
	return f
end

function P.NewFile(fd, name)
	if fd == 0 then
		return fileFrom(io.stdin, name)
	elseif fd == 1 then
		return fileFrom(io.stdout, name)
	elseif fd == 2 then
		return fileFrom(io.stderr, name)
	end
	return nil
end

P.Stdin = fileFrom(io.stdin, '/dev/stdin')
P.Stdout = fileFrom(io.stdout, '/dev/stdout')
P.Stderr = fileFrom(io.stderr, '/dev/stderr')
rawset(P.Stdin, 'fd', 0)
rawset(P.Stdout, 'fd', 1)
rawset(P.Stderr, 'fd', 2)

P.O_RDONLY = 0
P.O_WRONLY = 1
P.O_RDWR = 2
P.O_APPEND = 0x400
P.O_CREATE = 0x40
P.O_EXCL = 0x80
P.O_SYNC = 0x101000
P.O_TRUNC = 0x200

function P.OpenFile(name, flag)
	local acc = band(flag, 3)
	local mode
	if acc == 0 then
		local st = rawStat(name, true)
		if st and st.isdir then
			return fileFrom(nil, name, true), nil
		end
		mode = 'rb'
	elseif band(flag, 0x400) ~= 0 then
		mode = acc == 2 and 'a+b' or 'ab'
	elseif band(flag, 0x200) ~= 0 or band(flag, 0x40) ~= 0 then
		if band(flag, 0x80) ~= 0 and rawStat(name, false) then
			return nil, pathError('open', name, 'file exists', 'EEXIST')
		end
		if band(flag, 0x200) ~= 0 then
			mode = acc == 2 and 'w+b' or 'wb'
		else
			-- Create without truncation.
			local existing = openFile(name, 'rb')
			if existing then
				existing:close()
				mode = 'r+b'
			else
				mode = acc == 2 and 'w+b' or 'wb'
			end
		end
	else
		mode = 'r+b'
	end
	local fh, msg = openFile(name, mode)
	if not fh then
		local code = 'EIO'
		if msg and (msg:find('No such file') or msg:find('cannot find')) then
			code = 'ENOENT'
		elseif msg and msg:find('ermission') then
			code = 'EACCES'
		elseif msg and msg:find('s a directory') then
			code = 'EISDIR'
		end
		local reason = msg and msg:gsub('^.-: ', '') or 'error'
		return nil, pathError('open', name, reason, code)
	end
	return fileFrom(fh, name, false), nil
end

function P.Open(name)
	return P.OpenFile(name, 0, 0)
end

function P.Create(name)
	return P.OpenFile(name, bor(2, 0x40, 0x200), 0x1B6)
end

local function fileWriteString(f, s)
	local fh = rawget(f, 'fh')
	if not fh then
		return 0, P.ErrClosed
	end
	local okw, msg = fh:write(s)
	if not okw then
		return 0, pathError('write', f.name, msg, 'EIO')
	end
	return #s, nil
end

function P.File__WriteString(f, s)
	return fileWriteString(f, s)
end

function P.File__Write(f, b)
	return fileWriteString(f, rt.bytestr(b))
end

function P.File__Read(f, b)
	local fh = rawget(f, 'fh')
	if not fh then
		return 0, P.ErrClosed
	end
	if b == nil or b[3] == 0 then
		return 0, nil
	end
	local data = fh:read(b[3])
	if data == nil or #data == 0 then
		return 0, io_.EOF
	end
	local a, o = b[1], b[2]
	for i = 1, #data do
		a[o + i - 1] = data:byte(i)
	end
	return #data, nil
end

function P.File__ReadAt(f, b, off)
	local fh = rawget(f, 'fh')
	local cur = fh:seek()
	fh:seek('set', off)
	local n, err = P.File__Read(f, b)
	fh:seek('set', cur)
	return n, err
end

function P.File__Seek(f, offset, whence)
	local fh = rawget(f, 'fh')
	local w = whence == 0 and 'set' or whence == 1 and 'cur' or 'end'
	local pos, msg = fh:seek(w, offset)
	if not pos then
		return 0, pathError('seek', f.name, msg, 'EIO')
	end
	return pos, nil
end

function P.File__Close(f)
	local fh = rawget(f, 'fh')
	if fh and fh ~= io.stdout and fh ~= io.stderr and fh ~= io.stdin then
		fh:close()
	end
	rawset(f, 'fh', nil)
	return nil
end

function P.File__Sync(f)
	local fh = rawget(f, 'fh')
	if fh then
		fh:flush()
	end
	return nil
end

function P.File__Name(f)
	return f.name
end

function P.File__Fd(f)
	return rawget(f, 'fd') or 3
end

function P.File__Stat(f)
	if rawget(f, 'fd') then
		return setmetatable(
			{ name = f.name, size = rt.I64(0), mode = bor(0x04000000, 0x1B6), mtime = rt.I64(0), mtimensec = rt.I64(0) },
			fileStat.pmt
		),
			nil
	end
	return P.Stat(f.name)
end

function P.File__ReadDir(f, n)
	local entries, err = listDir(f.name)
	if err then
		return nil, err
	end
	local pos = rawget(f, 'dirpos') or 0
	local total = entries and entries[3] or 0
	if n <= 0 then
		rawset(f, 'dirpos', total)
		return rt.slice(entries, pos, total), nil
	end
	if pos >= total then
		return nil, io_.EOF
	end
	local hi = math.min(pos + n, total)
	rawset(f, 'dirpos', hi)
	return rt.slice(entries, pos, hi), nil
end

function P.File__Readdirnames(f, n)
	local entries, err = P.File__ReadDir(f, n)
	if err then
		return nil, err
	end
	local res = nil
	if entries then
		for i = 0, entries[3] - 1 do
			res = rt.append1(res, entries[1][entries[2] + i].name)
		end
	end
	return res, nil
end

function P.File__Truncate()
	return nil
end

function P.File__Chmod()
	return nil
end

rt.methods(P.File, {}, {
	_mWriteString = P.File__WriteString,
	_mWrite = P.File__Write,
	_mRead = P.File__Read,
	_mReadAt = P.File__ReadAt,
	_mSeek = P.File__Seek,
	_mClose = P.File__Close,
	_mSync = P.File__Sync,
	_mName = P.File__Name,
	_mFd = P.File__Fd,
	_mStat = P.File__Stat,
	_mReadDir = P.File__ReadDir,
	_mReaddirnames = P.File__Readdirnames,
	_mTruncate = P.File__Truncate,
	_mChmod = P.File__Chmod,
})

---------------------------------------------------------------------------
-- Whole-file helpers

-- readFileString returns the file contents as a Lua string (fast path for patches).
function P.__readFileString(name)
	local fh, msg = openFile(name, 'rb')
	if not fh then
		local st = rawStat(name, true)
		if st and st.isdir then
			return nil, pathError('read', name, 'is a directory', 'EISDIR')
		end
		local code = 'ENOENT'
		if msg and msg:find('ermission') then
			code = 'EACCES'
		end
		return nil, pathError('open', name, msg and msg:gsub('^.-: ', '') or 'error', code)
	end
	local data = fh:read('*a')
	fh:close()
	if data == nil then
		return nil, pathError('read', name, 'is a directory', 'EISDIR')
	end
	return data, nil
end

function P.ReadFile(name)
	local data, err = P.__readFileString(name)
	if err then
		return nil, err
	end
	return rt.strbytes(data), nil
end

function P.__writeFileString(name, data, append)
	local fh, msg = openFile(name, append and 'ab' or 'wb')
	if not fh then
		local code = 'ENOENT'
		if msg and msg:find('ermission') then
			code = 'EACCES'
		end
		return pathError('open', name, msg and msg:gsub('^.-: ', '') or 'error', code)
	end
	fh:write(data)
	fh:close()
	return nil
end

function P.WriteFile(name, data)
	return P.__writeFileString(name, rt.bytestr(data), false)
end

function P.Mkdir(name, perm)
	if not native then
		return pathError('mkdir', name, 'not supported', 'EIO')
	end
	local okm, msg, code = native.mkdir(name, perm)
	if not okm then
		return pathError('mkdir', name, msg, code)
	end
	return nil
end

function P.MkdirAll(path, perm)
	local st = rawStat(path, true)
	if st then
		if st.isdir then
			return nil
		end
		return pathError('mkdir', path, 'not a directory', 'ENOTDIR')
	end
	local parent = path:match('^(.*)[/\\][^/\\]+[/\\]*$')
	if parent and parent ~= '' and not parent:match('^%a:$') then
		local err = P.MkdirAll(parent, perm)
		if err ~= nil then
			return err
		end
	end
	local err = P.Mkdir(path, perm)
	if err ~= nil then
		local st2 = rawStat(path, false)
		if st2 and st2.isdir then
			return nil
		end
		return err
	end
	return nil
end

function P.Remove(name)
	if not native then
		local okr, msg = os.remove(name)
		if not okr then
			return pathError('remove', name, msg, 'ENOENT')
		end
		return nil
	end
	local st = native.lstat(name)
	if not st then
		return pathError('remove', name, 'no such file or directory', 'ENOENT')
	end
	local okr, msg, code
	if st.isdir and not st.islink then
		okr, msg, code = native.rmdir(name)
	else
		okr, msg, code = native.unlink(name)
		if not okr and st.isdir then
			okr, msg, code = native.rmdir(name)
		end
	end
	if not okr then
		return pathError('remove', name, msg, code)
	end
	return nil
end

function P.RemoveAll(path)
	if path == '' then
		return nil
	end
	local st = rawStat(path, false)
	if not st then
		return nil
	end
	if st.isdir and not st.islink then
		local list = readDirRaw(path)
		if list then
			for _, e in ipairs(list) do
				local err = P.RemoveAll(path .. '/' .. e[1])
				if err ~= nil then
					return err
				end
			end
		end
	end
	return P.Remove(path)
end

function P.Rename(from, to)
	if native then
		local okr, msg, code = native.rename(from, to)
		if not okr then
			local e = P.LinkError.zero()
			e.Op = 'rename'
			e.Old = from
			e.New = to
			e.Err = errno(msg, code)
			return e
		end
		return nil
	end
	local okr, msg = os.rename(from, to)
	if not okr then
		return pathError('rename', from, msg, 'EIO')
	end
	return nil
end

P.LinkError = rt.named('os', 'os.LinkError', 'struct')
rt.defineStruct(P.LinkError, {
	{ 'Op', rt.string, 'Op', 0 },
	{ 'Old', rt.string, 'Old', 0 },
	{ 'New', rt.string, 'New', 0 },
	{ 'Err', rt.error, 'Err', 0 },
})
rt.methods(P.LinkError, {}, {
	_mError = function(e)
		return e.Op .. ' ' .. e.Old .. ' ' .. e.New .. ': ' .. e.Err:_mError()
	end,
	_mUnwrap = function(e)
		return e.Err
	end,
})

function P.Symlink(old, new)
	if not native then
		return pathError('symlink', new, 'not supported', 'EIO')
	end
	local oks, msg, code = native.symlink(old, new)
	if not oks then
		return pathError('symlink', new, msg, code)
	end
	return nil
end

function P.Link(old, new)
	return pathError('link', new, 'not supported', 'EIO')
end

function P.Readlink(name)
	if not native then
		return '', pathError('readlink', name, 'not supported', 'EIO')
	end
	local target, msg, code = native.readlink(name)
	if not target then
		return '', pathError('readlink', name, msg, code)
	end
	return target, nil
end

function P.Chmod()
	return nil
end

function P.Chtimes(name, atime, mtime)
	if native then
		local sec = time.Time__Unix(mtime)
		local nsec = time.Time__Nanosecond(mtime)
		local okc, msg, code = native.utimes(name, tonumber(sec), nsec)
		if not okc then
			return pathError('chtimes', name, msg, code)
		end
	end
	return nil
end

function P.Truncate(name, size)
	if size == 0 then
		return P.__writeFileString(name, '', false)
	end
	return nil
end

---------------------------------------------------------------------------
-- Process and environment

P.Args = nil

function P.__setArgs(list)
	local s = nil
	for _, a in ipairs(list) do
		s = rt.append1(s, a)
	end
	P.Args = s
end

function P.Exit(code)
	io.stdout:flush()
	io.stderr:flush()
	rt.exit(code)
end

function P.Getenv(key)
	return os.getenv(key) or ''
end

function P.LookupEnv(key)
	local v = os.getenv(key)
	if v == nil then
		return '', false
	end
	return v, true
end

local envOverrides = {}
function P.Setenv(key, value)
	envOverrides[key] = value
	return nil
end

function P.Environ()
	return nil
end

function P.Getwd()
	if native then
		local cwd, msg, code = native.getcwd()
		if not cwd then
			return '', pathError('getwd', '.', msg, code)
		end
		return cwd, nil
	end
	local pwd = os.getenv('PWD') or os.getenv('CD')
	if pwd then
		return pwd, nil
	end
	return '.', nil
end

function P.Chdir(dir)
	if native then
		local okc, msg, code = native.chdir(dir)
		if not okc then
			return pathError('chdir', dir, msg, code)
		end
	end
	return nil
end

function P.Executable()
	if native then
		local p = native.exepath()
		if p then
			return p, nil
		end
	end
	return rt.executablePath or (arg and arg[0]) or '', nil
end

function P.Getpid()
	return 1
end

function P.Getppid()
	return 0
end

function P.Geteuid()
	return 0
end

function P.Getuid()
	return 0
end

function P.Hostname()
	return os.getenv('COMPUTERNAME') or os.getenv('HOSTNAME') or 'localhost', nil
end

function P.TempDir()
	if isWindows then
		return os.getenv('TEMP') or os.getenv('TMP') or 'C:\\Windows\\Temp'
	end
	return os.getenv('TMPDIR') or '/tmp'
end

function P.UserHomeDir()
	local h = os.getenv('HOME') or os.getenv('USERPROFILE')
	if h then
		return h, nil
	end
	return '', errors.New('$HOME is not defined')
end

function P.UserCacheDir()
	if isWindows then
		local d = os.getenv('LocalAppData')
		if d then
			return d, nil
		end
	else
		local d = os.getenv('XDG_CACHE_HOME')
		if d then
			return d, nil
		end
		local h = os.getenv('HOME')
		if h then
			return h .. '/.cache', nil
		end
	end
	return '', errors.New('cache dir not found')
end

local tempCounter = 0
function P.MkdirTemp(dir, pattern)
	if dir == '' then
		dir = P.TempDir()
	end
	for _ = 1, 100 do
		tempCounter = tempCounter + 1
		local name = dir .. '/' .. pattern:gsub('%*', '') .. tostring(os.time()) .. tostring(tempCounter)
		if P.Mkdir(name, 0x1C0) == nil then
			return name, nil
		end
	end
	return '', errors.New('could not create temp dir')
end

function P.CreateTemp(dir, pattern)
	if dir == '' then
		dir = P.TempDir()
	end
	tempCounter = tempCounter + 1
	local name = dir .. '/' .. pattern:gsub('%*', '') .. tostring(os.time()) .. tostring(tempCounter)
	return P.Create(name)
end

-- Signals are only used for interrupt handling in watch mode.
P.Signal = rt.named('os', 'os.Signal', 'interface')
rt.define(P.Signal, rt.ifaceOf({ 'Signal', 'String' }))
local sigType = rt.named('os', 'os.sig', 'int')
rt.define(sigType, rt.int)
rt.methods(sigType, {
	_mSignal = function() end,
	_mString = function(s)
		return s == 2 and 'interrupt' or 'killed'
	end,
}, {})
P.Interrupt = rt.box(sigType, 2)
P.Kill = rt.box(sigType, 9)

P.Process = rt.named('os', 'os.Process', 'struct')
rt.defineStruct(P.Process, { { 'Pid', rt.int, 'Pid', 0 } })
function P.FindProcess(pid)
	local p = P.Process.zero()
	p.Pid = pid
	return p, nil
end
function P.Process__Signal()
	return P.ErrProcessDone
end
function P.Process__Kill()
	return P.ErrProcessDone
end
rt.methods(P.Process, {}, { _mSignal = P.Process__Signal, _mKill = P.Process__Kill })

---------------------------------------------------------------------------
-- DirFS

local dirFS = rt.named('os', 'os.dirFS', 'string')
rt.define(dirFS, rt.string)

local function join(dir, name)
	if name == '.' then
		return dir
	end
	if dir:sub(-1) == '/' or dir:sub(-1) == '\\' then
		return dir .. name
	end
	return dir .. '/' .. name
end

local function checkName(op, name)
	if not fs.ValidPath(name) then
		return pathError(op, name, 'invalid argument', 'EINVAL')
	end
	return nil
end

function P.dirFS__Open(dir, name)
	local err = checkName('open', name)
	if err then
		return nil, err
	end
	local f, oerr = P.Open(join(dir, name))
	if oerr ~= nil then
		oerr.Path = name
		return nil, oerr
	end
	return f, nil
end

function P.dirFS__ReadFile(dir, name)
	local err = checkName('readfile', name)
	if err then
		return nil, err
	end
	local data, rerr = P.__readFileString(join(dir, name))
	if rerr ~= nil then
		rerr.Path = name
		return nil, rerr
	end
	return rt.strbytes(data), nil
end

function P.dirFS__ReadDir(dir, name)
	local err = checkName('readdir', name)
	if err then
		return nil, err
	end
	return listDir(join(dir, name))
end

function P.dirFS__Stat(dir, name)
	local err = checkName('stat', name)
	if err then
		return nil, err
	end
	local st, serr = P.Stat(join(dir, name))
	if serr ~= nil then
		serr.Path = name
		return nil, serr
	end
	return st, nil
end

function P.dirFS__Lstat(dir, name)
	local st, serr = P.Lstat(join(dir, name))
	if serr ~= nil then
		serr.Path = name
		return nil, serr
	end
	return st, nil
end

rt.methods(dirFS, {
	_mOpen = P.dirFS__Open,
	_mReadFile = P.dirFS__ReadFile,
	_mReadDir = P.dirFS__ReadDir,
	_mStat = P.dirFS__Stat,
	_mLstat = P.dirFS__Lstat,
}, {})

function P.DirFS(dir)
	return rt.box(dirFS, dir)
end

-- File modes are io/fs FileMode values.
P.ModeDir = MODE_DIR
P.ModeSymlink = MODE_SYMLINK
P.ModePerm = 0x1FF

return P
