-- Minimal native syscall: just enough for time's zoneinfo loading and env lookup.
local rt = require('rt')
local P = rt.package('syscall')

P.Errno = rt.named('syscall', 'syscall.Errno', 'uintptr')
rt.define(P.Errno, rt.uintptr)

local messages = {
	[2] = 'no such file or directory',
	[13] = 'permission denied',
	[17] = 'file exists',
	[22] = 'invalid argument',
	[44] = 'no such file or directory',
}

function P.Errno__Error(e)
	return messages[e] or ('errno ' .. tostring(e))
end

function P.Errno__Is()
	return false
end

function P.Errno__Temporary()
	return false
end

function P.Errno__Timeout()
	return false
end

rt.methods(P.Errno, {
	_mError = P.Errno__Error,
	_mIs = P.Errno__Is,
	_mTemporary = P.Errno__Temporary,
	_mTimeout = P.Errno__Timeout,
}, {
	_mError = function(p)
		return P.Errno__Error(p.v)
	end,
})

-- wasip1 errno values, matching the constants the transpiler inlines.
P.ENOENT = 44
P.EINVAL = 28

function P.Getenv(key)
	local v = os.getenv(key)
	if v == nil then
		return '', false
	end
	return v, true
end

function P.Open()
	return -1, rt.box(P.Errno, P.ENOENT)
end

function P.Read()
	return 0, rt.box(P.Errno, P.EINVAL)
end

function P.Seek()
	return 0, rt.box(P.Errno, P.EINVAL)
end

function P.Close()
	return nil
end

function P.Getpagesize()
	return 65536
end

function P.Exit(code)
	rt.exit(code)
end

return P
