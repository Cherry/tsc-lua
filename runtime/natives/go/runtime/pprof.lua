-- Native stub for runtime/pprof: profiling is not available in the Lua runtime.
-- deps: errors
local rt = require('rt')
local P = rt.package('runtime/pprof')
local errors = rt.import('go.errors')

local unsupported = errors.New('tsc-lua: profiling is not supported')

P.Profile = rt.named('runtime/pprof', 'pprof.Profile', 'struct')
rt.defineStruct(P.Profile, {})

function P.StartCPUProfile()
	return unsupported
end

function P.StopCPUProfile() end

function P.WriteHeapProfile()
	return unsupported
end

function P.Lookup()
	return nil
end

function P.Profile__WriteTo()
	return unsupported
end

function P.Do(ctx, labels, f)
	f(ctx)
end

function P.Labels()
	return nil
end

rt.methods(P.Profile, {}, { _mWriteTo = P.Profile__WriteTo })

return P
