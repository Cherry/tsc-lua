-- Native runtime/debug: GC tuning maps onto Lua's collector where meaningful.
local rt = require('rt')
local P = rt.package('runtime/debug')

local gcPercent = 100

function P.SetGCPercent(p)
	local old = gcPercent
	gcPercent = p
	if p < 0 then
		collectgarbage('stop')
	else
		collectgarbage('restart')
	end
	return old
end

function P.SetMemoryLimit()
	return 9223372036854775807
end

function P.SetMaxStack(n)
	return n
end

function P.SetMaxThreads(n)
	return n
end

function P.FreeOSMemory()
	collectgarbage('collect')
end

function P.Stack()
	return rt.strbytes(debug.traceback('', 2))
end

function P.PrintStack()
	io.stderr:write(debug.traceback('', 2), '\n')
end

function P.SetTraceback() end

P.BuildInfo = rt.named('runtime/debug', 'debug.BuildInfo', 'struct')
rt.defineStruct(P.BuildInfo, {
	{ 'GoVersion', rt.string, 'GoVersion', 0 },
	{ 'Path', rt.string, 'Path', 0 },
})

function P.ReadBuildInfo()
	return nil, false
end

return P
