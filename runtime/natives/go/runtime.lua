-- Native runtime package: process information and scheduler hooks.
local rt = require('rt')
local P = rt.package('runtime')

-- Code reads GOOS/GOARCH at run time (the transpiler does not fold them), so report the real host.
local ok, native = pcall(require, 'tscnative')
if ok then
	P.GOOS = native.os
	P.GOARCH = native.arch or 'amd64'
elseif package.config:sub(1, 1) == '\\' then
	P.GOOS = 'windows'
	P.GOARCH = 'amd64'
else
	P.GOOS = 'linux'
	P.GOARCH = 'amd64'
end
P.Compiler = 'gc'

P.Error = rt.named('runtime', 'runtime.Error', 'interface')
rt.define(P.Error, rt.ifaceOf({ 'Error', 'RuntimeError' }))

function P.Gosched()
	if rt.current() then
		rt.gosched()
	end
end

function P.GC()
	collectgarbage('collect')
end

function P.NumCPU()
	return 1
end

function P.GOMAXPROCS()
	return 1
end

function P.NumGoroutine()
	return 1
end

function P.Version()
	return 'go1.27-lua'
end

function P.GOROOT()
	return ''
end

function P.KeepAlive() end

function P.SetFinalizer() end

function P.AddCleanup() end

function P.LockOSThread() end

function P.UnlockOSThread() end

function P.Caller(skip)
	local info = debug.getinfo(skip + 2, 'Sl')
	if not info then
		return 0, '', 0, false
	end
	return 0, info.short_src, info.currentline or 0, true
end

function P.Callers()
	return 0
end

function P.Stack()
	return 0
end

P.MemStats = rt.named('runtime', 'runtime.MemStats', 'struct')
rt.defineStruct(P.MemStats, {
	{ 'Alloc', rt.uint64, 'Alloc', 0 },
	{ 'TotalAlloc', rt.uint64, 'TotalAlloc', 0 },
	{ 'Sys', rt.uint64, 'Sys', 0 },
	{ 'Mallocs', rt.uint64, 'Mallocs', 0 },
	{ 'Frees', rt.uint64, 'Frees', 0 },
	{ 'HeapAlloc', rt.uint64, 'HeapAlloc', 0 },
	{ 'HeapSys', rt.uint64, 'HeapSys', 0 },
	{ 'HeapInuse', rt.uint64, 'HeapInuse', 0 },
	{ 'HeapObjects', rt.uint64, 'HeapObjects', 0 },
	{ 'NumGC', rt.uint32, 'NumGC', 0 },
	{ 'PauseTotalNs', rt.uint64, 'PauseTotalNs', 0 },
})

function P.ReadMemStats(m)
	local bytes = rt.U64(math.floor(collectgarbage('count') * 1024))
	m.Alloc = bytes
	m.HeapAlloc = bytes
	m.Sys = bytes
	m.HeapSys = bytes
	m.HeapInuse = bytes
	m.TotalAlloc = bytes
end

return P
