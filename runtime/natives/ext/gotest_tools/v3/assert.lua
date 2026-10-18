-- Native gotest.tools/v3/assert: the assertion helpers used by typescript-go's test
-- runners, without the source parsing the real package does for messages.
-- deps: fmt
local rt = require('rt')
local P = rt.package('gotest.tools/v3/assert')

local function fmt()
	return rt.import('go.fmt')
end

P.TestingT = rt.named('gotest.tools/v3/assert', 'assert.TestingT', 'interface')
rt.define(P.TestingT, rt.ifaceOf({ 'FailNow', 'Fail', 'Log' }))

local function extra(msgAndArgs)
	if msgAndArgs == nil or msgAndArgs[3] == 0 then
		return ''
	end
	local first = msgAndArgs[1][msgAndArgs[2]]
	if type(first) == 'string' and msgAndArgs[3] > 1 then
		return ': ' .. fmt().Sprintf(first, rt.slice(msgAndArgs, 1, nil))
	end
	return ': ' .. fmt().Sprint(msgAndArgs)
end

local function fail(t, msg, msgAndArgs, now)
	local helper = rt.typeof(t)
	if helper and helper.mset and helper.mset._mHelper then
		t:_mHelper()
	end
	t:_mLog({ { 'assertion failed: ' .. msg .. extra(msgAndArgs) }, 1, 1, 1 })
	if now then
		t:_mFailNow()
	else
		t:_mFail()
	end
	return false
end

local function show(v)
	return fmt().Sprintf('%v', { { v }, 1, 1, 1 })
end

-- deepEqual compares two interface values structurally using the runtime descriptors.
local function deepEqual(T, a, b, seen)
	if T == nil then
		return a == b
	end
	local k = T.kind
	if k == 'interface' then
		if a == nil or b == nil then
			return a == b
		end
		local ta, tb = rt.typeof(a), rt.typeof(b)
		if ta ~= tb then
			return false
		end
		return deepEqual(ta, rt.unbox(a), rt.unbox(b), seen)
	elseif k == 'ptr' then
		if a == b then
			return true
		end
		if a == nil or b == nil then
			return false
		end
		if T.elem.kind == 'struct' or T.elem.kind == 'array' then
			return deepEqual(T.elem, a, b, seen)
		end
		return deepEqual(T.elem, a.v, b.v, seen)
	elseif k == 'slice' then
		if (a == nil) ~= (b == nil) then
			return false
		end
		if a == nil then
			return true
		end
		if a[3] ~= b[3] then
			return false
		end
		for i = 0, a[3] - 1 do
			if not deepEqual(T.elem, a[1][a[2] + i], b[1][b[2] + i], seen) then
				return false
			end
		end
		return true
	elseif k == 'array' then
		for i = 1, T.len do
			if not deepEqual(T.elem, a[i], b[i], seen) then
				return false
			end
		end
		return true
	elseif k == 'map' then
		if (a == nil) ~= (b == nil) then
			return false
		end
		if a == nil then
			return true
		end
		if rt.mlen(a) ~= rt.mlen(b) then
			return false
		end
		for key, va in rt.mrange(a, T) do
			local vb, ok = rt.mget2(b, key, T)
			if not ok or not deepEqual(T.elem, va, vb, seen) then
				return false
			end
		end
		return true
	elseif k == 'struct' then
		for _, leaf in ipairs(rt.leaves(T)) do
			if not deepEqual(leaf[2], a[leaf[1]], b[leaf[1]], seen) then
				return false
			end
		end
		return true
	elseif k == 'func' then
		return a == nil and b == nil
	end
	return a == b
end
P.__deepEqual = deepEqual

local function check(t, comparison, msgAndArgs, now)
	local T = rt.typeof(comparison)
	local v = rt.unbox(comparison)
	if T == nil then
		return true
	end
	if T.kind == 'bool' then
		if not v then
			return fail(t, 'expression is false', msgAndArgs, now)
		end
		return true
	end
	if rt.implements(T, rt.error) then
		return fail(t, 'error is not nil: ' .. comparison:_mError(), msgAndArgs, now)
	end
	if T.kind == 'func' then
		local result = v()
		if result == nil then
			return true
		end
		local ok, success = pcall(function()
			return result:_mSuccess()
		end)
		if ok and success then
			return true
		end
		local hasMessage, message = pcall(function()
			return result:_mFailureMessage()
		end)
		return fail(t, hasMessage and message or 'comparison failed', msgAndArgs, now)
	end
	return true
end

function P.Assert(t, comparison, msgAndArgs)
	check(t, comparison, msgAndArgs, true)
end

function P.Check(t, comparison, msgAndArgs)
	return check(t, comparison, msgAndArgs, false)
end

function P.NilError(t, err, msgAndArgs)
	if err ~= nil then
		fail(t, 'error is not nil: ' .. err:_mError(), msgAndArgs, true)
	end
end

function P.Equal(t, x, y, msgAndArgs)
	if not rt.ieq(x, y) then
		fail(t, show(x) .. ' (x) != ' .. show(y) .. ' (y)', msgAndArgs, true)
	end
end

function P.DeepEqual(t, x, y)
	local T = rt.typeof(x)
	if T ~= rt.typeof(y) or not deepEqual(T, rt.unbox(x), rt.unbox(y)) then
		fail(t, 'values are not deeply equal: ' .. show(x) .. ' != ' .. show(y), nil, true)
	end
end

function P.Error(t, err, expected, msgAndArgs)
	if err == nil then
		fail(t, 'expected an error, got nil', msgAndArgs, true)
	elseif err:_mError() ~= expected then
		fail(t, 'expected error "' .. expected .. '", got "' .. err:_mError() .. '"', msgAndArgs, true)
	end
end

function P.ErrorContains(t, err, substring, msgAndArgs)
	if err == nil then
		fail(t, 'expected an error, got nil', msgAndArgs, true)
	elseif not err:_mError():find(substring, 1, true) then
		fail(t, 'expected error to contain "' .. substring .. '", got "' .. err:_mError() .. '"', msgAndArgs, true)
	end
end

function P.ErrorIs(t, err, expected, msgAndArgs)
	if not rt.import('go.errors').Is(err, expected) then
		fail(t, 'error is not the expected error', msgAndArgs, true)
	end
end

return P
