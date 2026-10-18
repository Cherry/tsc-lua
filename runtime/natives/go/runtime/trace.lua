-- Native stub for runtime/trace: execution tracing is a no-op in the Lua runtime.
local rt = require('rt')
local P = rt.package('runtime/trace')

P.Task = rt.named('runtime/trace', 'trace.Task', 'struct')
rt.defineStruct(P.Task, {})
P.Region = rt.named('runtime/trace', 'trace.Region', 'struct')
rt.defineStruct(P.Region, {})

local function noop() end

function P.IsEnabled()
	return false
end

P.Log = noop
P.Logf = noop
P.Stop = noop

function P.Start()
	return nil
end

function P.NewTask(ctx)
	return ctx, P.Task.zero()
end

function P.StartRegion()
	return P.Region.zero()
end

function P.WithRegion(ctx, typ, f)
	f()
end

P.Task__End = noop
P.Region__End = noop
rt.methods(P.Task, {}, { _mEnd = noop })
rt.methods(P.Region, {}, { _mEnd = noop })

return P
