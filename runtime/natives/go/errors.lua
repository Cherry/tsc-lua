-- Native errors package.
local rt = require('rt')
local P = rt.package('errors')

local errorString = rt.named('errors', 'errors.errorString', 'struct')
rt.defineStruct(errorString, { { 's', rt.string, 's', 0 } })
local function errorStringError(e)
	return e.s
end
P.errorString__Error = errorStringError
rt.methods(errorString, {}, { _mError = errorStringError })
P.errorString = errorString

function P.New(text)
	return setmetatable({ s = text }, errorString.pmt)
end

P.ErrUnsupported = P.New('unsupported operation')

local function hasMethod(err, key)
	local T = rt.typeof(err)
	return T ~= nil and T.mset ~= nil and T.mset[key] ~= nil
end

local function comparable(err)
	local T = rt.typeof(err)
	if T == nil then
		return true
	end
	local k = T.kind
	return k ~= 'slice' and k ~= 'map' and k ~= 'func'
end

-- unwrapList returns the wrapped errors of err (single or multiple).
local function unwrapList(err)
	if hasMethod(err, '_mUnwrap') then
		local r = err:_mUnwrap()
		if r == nil then
			return nil
		end
		if type(r) == 'table' and r[1] and r[3] and not rawget(r, '__t') and getmetatable(r) == nil then
			-- []error
			local res = {}
			for i = 0, r[3] - 1 do
				res[#res + 1] = r[1][r[2] + i]
			end
			return res
		end
		return { r }
	end
	return nil
end

function P.Unwrap(err)
	if err == nil then
		return nil
	end
	if hasMethod(err, '_mUnwrap') then
		local r = err:_mUnwrap()
		if type(r) == 'table' and r[1] and r[3] and not rawget(r, '__t') and getmetatable(r) == nil then
			return nil
		end
		return r
	end
	return nil
end

local function is(err, target, targetComparable)
	while true do
		if targetComparable and comparable(err) and rt.ieq(err, target) then
			return true
		end
		if hasMethod(err, '_mIs') and err:_mIs(target) then
			return true
		end
		local list = unwrapList(err)
		if list == nil then
			return false
		end
		if #list == 1 then
			err = list[1]
			if err == nil then
				return false
			end
		else
			for _, e in ipairs(list) do
				if e ~= nil and is(e, target, targetComparable) then
					return true
				end
			end
			return false
		end
	end
end

function P.Is(err, target)
	if err == nil or target == nil then
		return err == target
	end
	return is(err, target, comparable(target))
end

local function as(err, target, E)
	while true do
		local T = rt.typeof(err)
		if T == E then
			target.v = rt.unbox(err)
			return true
		end
		if E.kind == 'interface' and rt.implements(T, E) then
			target.v = err
			return true
		end
		if hasMethod(err, '_mAs') and err:_mAs(rt.box(rt.ptrTo(E), target)) then
			return true
		end
		local list = unwrapList(err)
		if list == nil then
			return false
		end
		if #list == 1 then
			err = list[1]
			if err == nil then
				return false
			end
		else
			for _, e in ipairs(list) do
				if e ~= nil and as(e, target, E) then
					return true
				end
			end
			return false
		end
	end
end

function P.As(err, target)
	if err == nil then
		return false
	end
	local TT = rt.typeof(target)
	if TT == nil or TT.kind ~= 'ptr' then
		rt.panic('errors: target must be a non-nil pointer')
	end
	local E = TT.elem
	local box = rt.unbox(target)
	if E.kind == 'struct' then
		-- Pointer to a struct target: the struct table itself is the storage.
		local T = rt.typeof(err)
		if T == E then
			E.copy(box, rt.unbox(err))
			return true
		end
		return false
	end
	return as(err, box, E)
end

-- AsType[E error](err) (E, bool): generic form of As, receiving E's descriptor first.
function P.AsType(E, err)
	if err == nil then
		return E.zero(), false
	end
	local box = { v = E.zero() }
	if as(err, box, E) then
		return box.v, true
	end
	return E.zero(), false
end

local joinError = rt.named('errors', 'errors.joinError', 'struct')
rt.defineStruct(joinError, { { 'errs', rt.sliceOf(rt.error), 'errs', 0 } })
local function joinErrorError(e)
	local parts = {}
	for i = 0, e.errs[3] - 1 do
		parts[#parts + 1] = e.errs[1][e.errs[2] + i]:_mError()
	end
	return table.concat(parts, '\n')
end
local function joinErrorUnwrap(e)
	return e.errs
end
rt.methods(joinError, {}, { _mError = joinErrorError, _mUnwrap = joinErrorUnwrap })

function P.Join(errs)
	local list = nil
	if errs ~= nil then
		for i = 0, errs[3] - 1 do
			local e = errs[1][errs[2] + i]
			if e ~= nil then
				list = rt.append1(list, e)
			end
		end
	end
	if list == nil then
		return nil
	end
	return setmetatable({ errs = list }, joinError.pmt)
end

return P
