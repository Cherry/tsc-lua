-- Minimal native internal/reflectlite (used by sort.Slice, errors and context).
local rt = require('rt')
local P = rt.package('internal/reflectlite')

-- Type wraps a runtime descriptor.
P.Type = rt.named('internal/reflectlite', 'reflectlite.Type', 'interface')
rt.define(P.Type, rt.ifaceOf({ 'String', 'Kind', 'Elem', 'Implements', 'AssignableTo', 'Comparable', 'Name', 'PkgPath', 'Size' }))

local rtype = rt.named('internal/reflectlite', 'reflectlite.rtype', 'struct')
rt.defineStruct(rtype, {})
rt.methods(rtype, {}, {
	_mString = function(t)
		return t.T.str
	end,
	_mName = function(t)
		return t.T.name or ''
	end,
	_mPkgPath = function(t)
		return t.T.pkg or ''
	end,
	_mKind = function(t)
		return t.T.kind
	end,
	_mElem = function(t)
		return P.toType(t.T.elem)
	end,
	_mComparable = function(t)
		local k = t.T.kind
		return k ~= 'slice' and k ~= 'map' and k ~= 'func'
	end,
	_mImplements = function(t, u)
		return rt.implements(t.T, u.T)
	end,
	_mAssignableTo = function(t, u)
		return t.T == u.T or (u.T.kind == 'interface' and rt.implements(t.T, u.T))
	end,
	_mSize = function()
		return 8
	end,
})

function P.toType(T)
	if T == nil then
		return nil
	end
	return setmetatable({ T = T }, rtype.pmt)
end

function P.TypeOf(i)
	return P.toType(rt.typeof(i))
end

P.Value = rt.named('internal/reflectlite', 'reflectlite.Value', 'struct')
rt.defineStruct(P.Value, {})

function P.ValueOf(i)
	local v = P.Value.zero()
	rawset(v, 'iface', i)
	return v
end

function P.Value__Type(v)
	return P.TypeOf(rawget(v, 'iface'))
end

function P.Value__IsNil(v)
	return rt.unbox(rawget(v, 'iface')) == nil
end

function P.Value__Kind(v)
	local T = rt.typeof(rawget(v, 'iface'))
	return T and T.kind or 'invalid'
end

function P.Value__Elem(v)
	local inner = rt.unbox(rawget(v, 'iface'))
	return P.ValueOf(inner)
end

function P.Value__Interface(v)
	return rawget(v, 'iface')
end

function P.Value__Len(v)
	local x = rt.unbox(rawget(v, 'iface'))
	if type(x) == 'string' then
		return #x
	end
	return rt.len(x)
end

rt.methods(P.Value, {}, {
	_mType = P.Value__Type,
	_mIsNil = P.Value__IsNil,
	_mKind = P.Value__Kind,
	_mElem = P.Value__Elem,
	_mInterface = P.Value__Interface,
})

-- Kinds compare as strings in this runtime.
P.Ptr = 'ptr'
P.Pointer = 'ptr'
P.Interface = 'interface'
P.Slice = 'slice'
P.String = 'string'

function P.Swapper(slice)
	local s = rt.unbox(slice)
	if s == nil then
		return function() end
	end
	local a, o = s[1], s[2]
	return function(i, j)
		a[o + i], a[o + j] = a[o + j], a[o + i]
	end
end

return P
