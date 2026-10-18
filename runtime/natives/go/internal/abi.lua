-- Native stub for internal/abi: only the pieces reachable from transpiled stdlib code.
local rt = require('rt')
local P = rt.package('internal/abi')

function P.NoEscape(p)
	return p
end

function P.Escape(T, x)
	return x
end

function P.FuncPCABI0()
	return 0
end

function P.FuncPCABIInternal()
	return 0
end

return P
