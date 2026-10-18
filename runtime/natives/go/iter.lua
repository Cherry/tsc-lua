-- Native iter: Seq/Seq2 are plain functions; Pull is implemented with coroutines.
local rt = require('rt')
local P = rt.package('iter')

P.Seq = rt.generic('iter', 'iter.Seq', 1, function(T, V)
	rt.define(T, rt.funcOf({ rt.funcOf({ V }, { rt.bool }, false) }, {}, false))
end)

P.Seq2 = rt.generic('iter', 'iter.Seq2', 2, function(T, K, V)
	rt.define(T, rt.funcOf({ rt.funcOf({ K, V }, { rt.bool }, false) }, {}, false))
end)

local function pull(K, V, seq, arity)
	local co = coroutine.create(function()
		seq(function(...)
			coroutine.yield(true, ...)
			return true
		end)
		return false
	end)
	local done = false
	local function finished()
		if arity == 2 then
			return K.zero(), V.zero(), false
		end
		return K.zero(), false
	end
	local function nextFn()
		if done then
			return finished()
		end
		local ok, more, a, b = coroutine.resume(co)
		if not ok then
			done = true
			error(more, 0)
		end
		if not more then
			done = true
			return finished()
		end
		if arity == 2 then
			return a, b, true
		end
		return a, true
	end
	local function stop()
		done = true
	end
	return nextFn, stop
end

function P.Pull(V, seq)
	return pull(V, nil, seq, 1)
end

function P.Pull2(K, V, seq)
	return pull(K, V, seq, 2)
end

return P
