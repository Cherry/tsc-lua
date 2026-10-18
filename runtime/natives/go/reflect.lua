-- Native reflect package built on the runtime type descriptors.
local rt = require('rt')
local P = rt.package('reflect')

local setmetatable, type, ipairs, pairs = setmetatable, type, ipairs, pairs
local sbyte, ssub, sfind, schar, tconcat = string.byte, string.sub, string.find, string.char, table.concat
local typeof, unbox = rt.typeof, rt.unbox

local function panic(msg)
	rt.panic(msg)
end

---------------------------------------------------------------------------
-- Kind

P.Kind = rt.named('reflect', 'reflect.Kind', 'uint')
rt.define(P.Kind, rt.uint)

local kindNum = {
	invalid = 0,
	bool = 1,
	int = 2,
	int8 = 3,
	int16 = 4,
	int32 = 5,
	int64 = 6,
	uint = 7,
	uint8 = 8,
	uint16 = 9,
	uint32 = 10,
	uint64 = 11,
	uintptr = 12,
	float32 = 13,
	float64 = 14,
	complex64 = 15,
	complex128 = 16,
	array = 17,
	chan = 18,
	func = 19,
	interface = 20,
	map = 21,
	ptr = 22,
	slice = 23,
	string = 24,
	struct = 25,
	unsafeptr = 26,
}
local kindNames = {}
for name, n in pairs(kindNum) do
	kindNames[n] = name
end
kindNames[26] = 'unsafe.Pointer'

for name, n in pairs(kindNum) do
	local exported = name == 'ptr' and 'Pointer' or name == 'unsafeptr' and 'UnsafePointer' or name:sub(1, 1):upper() .. name:sub(2)
	P[exported] = n
end
P.Ptr = 22

function P.Kind__String(k)
	local s = kindNames[k]
	if s then
		return s
	end
	return 'kind' .. tostring(k)
end
rt.methods(P.Kind, { _mString = P.Kind__String }, {
	_mString = function(p)
		return P.Kind__String(p.v)
	end,
})

local intKinds = { int = true, int8 = true, int16 = true, int32 = true, int64 = true }
local uintKinds = { uint = true, uint8 = true, uint16 = true, uint32 = true, uint64 = true, uintptr = true }
local nilableKinds = { chan = true, func = true, interface = true, map = true, ptr = true, slice = true, unsafeptr = true }

local function isAgg(T)
	local k = T.kind
	return k == 'struct' or k == 'array'
end

local function exported(name)
	local c = sbyte(name, 1)
	return c ~= nil and ((c >= 65 and c <= 90) or c >= 0x80)
end

P.ChanDir = rt.named('reflect', 'reflect.ChanDir', 'int')
rt.define(P.ChanDir, rt.int)
P.RecvDir, P.SendDir, P.BothDir = 1, 2, 3

function P.ChanDir__String(d)
	if d == 1 then
		return '<-chan'
	elseif d == 2 then
		return 'chan<-'
	elseif d == 3 then
		return 'chan'
	end
	return 'ChanDir' .. tostring(d)
end
rt.methods(P.ChanDir, { _mString = P.ChanDir__String }, {
	_mString = function(p)
		return P.ChanDir__String(p.v)
	end,
})

P.ValueError = rt.named('reflect', 'reflect.ValueError', 'struct')
rt.defineStruct(P.ValueError, { { 'Method', rt.string, 'Method', 0 }, { 'Kind', P.Kind, 'Kind', 0 } })

function P.ValueError__Error(e)
	if e.Kind == 0 then
		return 'reflect: call of ' .. e.Method .. ' on zero Value'
	end
	return 'reflect: call of ' .. e.Method .. ' on ' .. P.Kind__String(e.Kind) .. ' Value'
end
rt.methods(P.ValueError, {}, { _mError = P.ValueError__Error })

local function kindPanic(method, T)
	rt.panic(setmetatable({ Method = method, Kind = T and kindNum[T.kind] or 0 }, P.ValueError.pmt))
end

local function goslice(list)
	local n = #list
	return { list, 1, n, n }
end

---------------------------------------------------------------------------
-- Types

P.Type = rt.named('reflect', 'reflect.Type', 'interface')
rt.define(
	P.Type,
	rt.ifaceOf({
		'Align',
		'AssignableTo',
		'Bits',
		'ChanDir',
		'Comparable',
		'ConvertibleTo',
		'Elem',
		'Field',
		'FieldAlign',
		'FieldByIndex',
		'FieldByName',
		'Fields',
		'Implements',
		'In',
		'Ins',
		'IsVariadic',
		'Key',
		'Kind',
		'Len',
		'Name',
		'NumField',
		'NumIn',
		'NumMethod',
		'NumOut',
		'Out',
		'Outs',
		'PkgPath',
		'Size',
		'String',
	})
)

P.StructTag = rt.named('reflect', 'reflect.StructTag', 'string')
rt.define(P.StructTag, rt.string)

P.StructField = rt.named('reflect', 'reflect.StructField', 'struct')

local rtype = rt.named('reflect', 'reflect.rtype', 'struct')
rt.defineStruct(rtype, {})
P.rtype = rtype

-- One Type object per descriptor so Type values compare by identity.
local typeCache = {}
local function toType(T)
	if T == nil then
		return nil
	end
	local t = typeCache[T]
	if not t then
		t = setmetatable({ T = T }, rtype.pmt)
		typeCache[T] = t
	end
	return t
end
P.toType = toType

local function descOf(t)
	if t == nil then
		panic('reflect: nil type passed to reflect function')
	end
	return t.T
end

-- Local named types carry a "·line" suffix in the runtime spelling.
local function cleanName(s)
	return (s:gsub('\194\183%d+', ''))
end

local function typeString(T)
	return cleanName(T.str)
end

local function typeNameOf(T)
	if T.named then
		local n = T.name or ''
		local br = sfind(n, '[', 1, true)
		local head = br and ssub(n, 1, br - 1) or n
		local dot = head:match('.*()%.')
		if dot then
			n = ssub(n, dot + 1)
		end
		return cleanName(n)
	end
	if T.basic then
		if T.kind == 'unsafeptr' then
			return 'Pointer'
		end
		return T.kind
	end
	return ''
end

local function pkgPathOf(T)
	if not T.named or T == rt.error then
		return ''
	end
	local n = T.name or ''
	if ssub(n, 1, 5) == 'main.' then
		return 'main'
	end
	return T.pkg or ''
end

local sizeCache = {}
local function sizeAlign(T)
	local c = sizeCache[T]
	if c then
		return c[1], c[2], c[3]
	end
	local k = T.kind
	local size, align, offsets
	if k == 'bool' or k == 'int8' or k == 'uint8' then
		size, align = 1, 1
	elseif k == 'int16' or k == 'uint16' then
		size, align = 2, 2
	elseif k == 'int32' or k == 'uint32' or k == 'float32' then
		size, align = 4, 4
	elseif k == 'complex64' then
		size, align = 8, 4
	elseif k == 'complex128' or k == 'string' or k == 'interface' then
		size, align = 16, 8
	elseif k == 'slice' then
		size, align = 24, 8
	elseif k == 'array' then
		local es, ea = sizeAlign(T.elem)
		size, align = es * T.len, ea
	elseif k == 'struct' then
		offsets = {}
		local off, maxa = 0, 1
		for i, f in ipairs(T.fields) do
			local fs, fa = sizeAlign(f[2])
			off = math.floor((off + fa - 1) / fa) * fa
			offsets[i] = off
			off = off + fs
			if fa > maxa then
				maxa = fa
			end
		end
		size, align = math.floor((off + maxa - 1) / maxa) * maxa, maxa
	else
		size, align = 8, 8
	end
	sizeCache[T] = { size, align, offsets }
	return size, align, offsets
end

local function underlyingOf(T)
	return T.underlying or T
end

local function comparable(T)
	local k = T.kind
	if k == 'slice' or k == 'map' or k == 'func' then
		return false
	elseif k == 'array' then
		return comparable(T.elem)
	elseif k == 'struct' then
		for _, f in ipairs(T.fields) do
			if not comparable(f[2]) then
				return false
			end
		end
	end
	return true
end

local function implements(T, U)
	if U.kind ~= 'interface' then
		panic('reflect: non-interface type passed to Type.Implements')
	end
	return rt.implements(T, U)
end

local function assignable(V, T)
	if V == T then
		return true
	end
	if T.kind == 'interface' then
		return rt.implements(V, T)
	end
	if V.named and T.named then
		return false
	end
	return underlyingOf(V) == underlyingOf(T) and V.kind == T.kind
end

local numericKinds = {}
for k in pairs(intKinds) do
	numericKinds[k] = true
end
for k in pairs(uintKinds) do
	numericKinds[k] = true
end
numericKinds.float32 = true
numericKinds.float64 = true

local function convertible(V, T)
	if assignable(V, T) or underlyingOf(V) == underlyingOf(T) then
		return true
	end
	local vk, tk = V.kind, T.kind
	if numericKinds[vk] and numericKinds[tk] then
		return true
	end
	if tk == 'string' and (intKinds[vk] or uintKinds[vk]) then
		return true
	end
	if (tk == 'string' and vk == 'slice') or (vk == 'string' and tk == 'slice') then
		local E = (tk == 'slice' and T or V).elem.kind
		return E == 'uint8' or E == 'int32'
	end
	return false
end

-- fieldPath finds a (possibly promoted) field by name breadth-first, returning 0-based indices.
local function fieldPath(T, name)
	local level = { { T = T, path = {} } }
	local seen = {}
	while #level > 0 do
		local nextLevel, found, count = {}, nil, 0
		for _, e in ipairs(level) do
			if not seen[e.T] then
				seen[e.T] = true
				for i, f in ipairs(e.T.fields) do
					local path = { table.unpack(e.path) }
					path[#path + 1] = i - 1
					if f[3] == name then
						count = count + 1
						found = path
					elseif f[4] ~= 0 then
						local ft = f[2]
						if ft.kind == 'ptr' then
							ft = ft.elem
						end
						if ft.kind == 'struct' then
							nextLevel[#nextLevel + 1] = { T = ft, path = path }
						end
					end
				end
			end
		end
		if count == 1 then
			return found
		elseif count > 1 then
			return nil
		end
		level = nextLevel
	end
	return nil
end

local function structField(T, i, index)
	if T.kind ~= 'struct' then
		panic('reflect: Field of non-struct type ' .. typeString(T))
	end
	local f = T.fields[i + 1]
	if not f then
		panic('reflect: Field index out of bounds')
	end
	local _, _, offsets = sizeAlign(T)
	local name = f[3]
	local pkgPath = ''
	if not exported(name) then
		pkgPath = pkgPathOf(T)
	end
	return setmetatable({
		Name = name,
		PkgPath = pkgPath,
		Type = toType(f[2]),
		Tag = f[5] or '',
		Offset = offsets[i + 1],
		Index = index or goslice({ i }),
		Anonymous = f[4] ~= 0,
	}, P.StructField.pmt)
end

local function typeFieldByIndex(T, path)
	local f
	for j, i in ipairs(path) do
		if j > 1 then
			T = f[2]
			if T.kind == 'ptr' then
				T = T.elem
			end
		end
		f = T.fields[i + 1]
	end
	return structField(T, path[#path], goslice(path))
end

local function sliceToList(s)
	local list = {}
	if s ~= nil then
		for j = 0, s[3] - 1 do
			list[j + 1] = s[1][s[2] + j]
		end
	end
	return list
end

local function needKind(T, what, ...)
	local k = T.kind
	for i = 1, select('#', ...) do
		if k == select(i, ...) then
			return
		end
	end
	panic('reflect: ' .. what .. ' of non-' .. (...) .. ' type ' .. typeString(T))
end

local typeMethods = {
	_mString = function(t)
		return typeString(t.T)
	end,
	_mName = function(t)
		return typeNameOf(t.T)
	end,
	_mPkgPath = function(t)
		return pkgPathOf(t.T)
	end,
	_mKind = function(t)
		return kindNum[t.T.kind] or 0
	end,
	_mElem = function(t)
		local T = t.T
		local k = T.kind
		if k ~= 'ptr' and k ~= 'slice' and k ~= 'array' and k ~= 'map' and k ~= 'chan' then
			panic('reflect: Elem of invalid type ' .. typeString(T))
		end
		return toType(T.elem)
	end,
	_mKey = function(t)
		needKind(t.T, 'Key', 'map')
		return toType(t.T.key)
	end,
	_mLen = function(t)
		needKind(t.T, 'Len', 'array')
		return t.T.len
	end,
	_mNumField = function(t)
		needKind(t.T, 'NumField', 'struct')
		return #t.T.fields
	end,
	_mField = function(t, i)
		return structField(t.T, i)
	end,
	_mFieldByIndex = function(t, index)
		return typeFieldByIndex(t.T, sliceToList(index))
	end,
	_mFieldByName = function(t, name)
		needKind(t.T, 'FieldByName', 'struct')
		local path = fieldPath(t.T, name)
		if not path then
			return P.StructField.zero(), false
		end
		return typeFieldByIndex(t.T, path), true
	end,
	_mNumMethod = function(t)
		local T = t.T
		if T.kind == 'interface' then
			return #T.methods
		end
		local n = 0
		if T.mset then
			for k in pairs(T.mset) do
				if exported(ssub(k, 3)) then
					n = n + 1
				end
			end
		end
		return n
	end,
	_mImplements = function(t, u)
		return implements(t.T, descOf(u))
	end,
	_mAssignableTo = function(t, u)
		return assignable(t.T, descOf(u))
	end,
	_mConvertibleTo = function(t, u)
		return convertible(t.T, descOf(u))
	end,
	_mComparable = function(t)
		return comparable(t.T)
	end,
	_mSize = function(t)
		return (sizeAlign(t.T))
	end,
	_mAlign = function(t)
		local _, a = sizeAlign(t.T)
		return a
	end,
	_mFieldAlign = function(t)
		local _, a = sizeAlign(t.T)
		return a
	end,
	_mBits = function(t)
		local T = t.T
		if not (numericKinds[T.kind] or T.kind == 'complex64' or T.kind == 'complex128') then
			panic('reflect: Bits of non-arithmetic Type ' .. typeString(T))
		end
		return (sizeAlign(T)) * 8
	end,
	_mNumIn = function(t)
		needKind(t.T, 'NumIn', 'func')
		return #t.T.params
	end,
	_mIn = function(t, i)
		needKind(t.T, 'In', 'func')
		return toType(t.T.params[i + 1])
	end,
	_mNumOut = function(t)
		needKind(t.T, 'NumOut', 'func')
		return #t.T.results
	end,
	_mOut = function(t, i)
		needKind(t.T, 'Out', 'func')
		return toType(t.T.results[i + 1])
	end,
	_mIsVariadic = function(t)
		needKind(t.T, 'IsVariadic', 'func')
		return t.T.variadic and true or false
	end,
	_mFields = function(t)
		needKind(t.T, 'Fields', 'struct')
		return function(yield)
			for i = 0, #t.T.fields - 1 do
				if not yield(structField(t.T, i)) then
					return
				end
			end
		end
	end,
	_mIns = function(t)
		needKind(t.T, 'Ins', 'func')
		return function(yield)
			for _, PT in ipairs(t.T.params) do
				if not yield(toType(PT)) then
					return
				end
			end
		end
	end,
	_mOuts = function(t)
		needKind(t.T, 'Outs', 'func')
		return function(yield)
			for _, RT in ipairs(t.T.results) do
				if not yield(toType(RT)) then
					return
				end
			end
		end
	end,
	_mChanDir = function(t)
		needKind(t.T, 'ChanDir', 'chan')
		local d = t.T.dir
		if d == 1 then
			return 2
		elseif d == 2 then
			return 1
		end
		return 3
	end,
}
rt.methods(rtype, {}, typeMethods)

function P.TypeOf(i)
	return toType(typeof(i))
end

function P.TypeFor(T)
	return toType(T)
end

function P.PointerTo(t)
	return toType(rt.ptrTo(descOf(t)))
end
P.PtrTo = P.PointerTo

function P.SliceOf(t)
	return toType(rt.sliceOf(descOf(t)))
end

function P.MapOf(k, e)
	return toType(rt.mapOf(descOf(k), descOf(e)))
end

function P.ArrayOf(n, e)
	return toType(rt.arrayOf(descOf(e), n))
end

-- StructTag ---------------------------------------------------------------

local escapes = { n = '\n', t = '\t', r = '\r', a = '\a', b = '\b', f = '\f', v = '\v', ['\\'] = '\\', ['"'] = '"', ["'"] = "'" }

-- unquote handles the escapes that appear in struct tags; nil means malformed.
local function unquote(q)
	local body = ssub(q, 2, -2)
	if not sfind(body, '\\', 1, true) then
		return body
	end
	local out, i, n = {}, 1, #body
	while i <= n do
		local c = ssub(body, i, i)
		if c == '\\' then
			local e = ssub(body, i + 1, i + 1)
			if escapes[e] then
				out[#out + 1] = escapes[e]
				i = i + 2
			elseif e == 'x' then
				local h = tonumber(ssub(body, i + 2, i + 3), 16)
				if not h then
					return nil
				end
				out[#out + 1] = schar(h)
				i = i + 4
			else
				return nil
			end
		else
			out[#out + 1] = c
			i = i + 1
		end
	end
	return tconcat(out)
end

function P.StructTag__Lookup(tag, key)
	while tag ~= '' do
		local i = 1
		while i <= #tag and sbyte(tag, i) == 32 do
			i = i + 1
		end
		tag = ssub(tag, i)
		if tag == '' then
			break
		end
		i = 1
		while i <= #tag do
			local c = sbyte(tag, i)
			if c <= 32 or c == 58 or c == 34 or c == 0x7f then
				break
			end
			i = i + 1
		end
		if i == 1 or i + 1 > #tag or sbyte(tag, i) ~= 58 or sbyte(tag, i + 1) ~= 34 then
			break
		end
		local name = ssub(tag, 1, i - 1)
		tag = ssub(tag, i + 1)
		i = 2
		while i <= #tag and sbyte(tag, i) ~= 34 do
			if sbyte(tag, i) == 92 then
				i = i + 1
			end
			i = i + 1
		end
		if i > #tag then
			break
		end
		local qvalue = ssub(tag, 1, i)
		tag = ssub(tag, i + 1)
		if key == name then
			local v = unquote(qvalue)
			if v == nil then
				break
			end
			return v, true
		end
	end
	return '', false
end

function P.StructTag__Get(tag, key)
	return (P.StructTag__Lookup(tag, key))
end

rt.methods(P.StructTag, { _mGet = P.StructTag__Get, _mLookup = P.StructTag__Lookup }, {
	_mGet = function(p, key)
		return P.StructTag__Get(p.v, key)
	end,
	_mLookup = function(p, key)
		return P.StructTag__Lookup(p.v, key)
	end,
})

rt.defineStruct(P.StructField, {
	{ 'Name', rt.string, 'Name', 0 },
	{ 'PkgPath', rt.string, 'PkgPath', 0 },
	{ 'Type', P.Type, 'Type', 0 },
	{ 'Tag', P.StructTag, 'Tag', 0 },
	{ 'Offset', rt.uintptr, 'Offset', 0 },
	{ 'Index', rt.sliceOf(rt.int), 'Index', 0 },
	{ 'Anonymous', rt.bool, 'Anonymous', 0 },
})

function P.StructField__IsExported(f)
	return f.PkgPath == ''
end
rt.methods(P.StructField, { _mIsExported = P.StructField__IsExported }, { _mIsExported = P.StructField__IsExported })

---------------------------------------------------------------------------
-- Values

-- A Value holds its descriptor t and either the value v or a slot c[k] (non-aggregates
-- reached through a container, so Set writes through). Aggregates always live in v.
P.Value = rt.named('reflect', 'reflect.Value', 'struct')
rt.defineStruct(P.Value, {
	{ 't', rt.any, 't', 0 },
	{ 'v', rt.any, 'v', 0 },
	{ 'c', rt.any, 'c', 0 },
	{ 'k', rt.any, 'k', 0 },
	{ 'fl', rt.int, 'fl', 0 },
})
local Value = P.Value
local vpmt = Value.pmt

-- Flag bits: addressable, read-only (unexported field), read-only via embedded field, c is a pointer box.
local fAddr, fStickyRO, fEmbedRO, fBox = 1, 2, 4, 8
local band, bor = bit.band, bit.bor
local fRO = bor(fStickyRO, fEmbedRO)

local function mk(t, v, c, k, fl)
	return setmetatable({ t = t, v = v, c = c, k = k, fl = fl or 0 }, vpmt)
end

local function flags(x)
	return x.fl or 0
end

local function slot(t, c, k, fl)
	if isAgg(t) then
		return mk(t, c[k], nil, nil, fl)
	end
	return mk(t, nil, c, k, fl)
end

local function get(x)
	local c = x.c
	if c ~= nil then
		return c[x.k]
	end
	return x.v
end

local function mustValid(x, what)
	local T = x.t
	if T == nil then
		kindPanic('reflect.Value.' .. what, nil)
	end
	return T
end

local function mustKind(x, what, ...)
	local T = mustValid(x, what)
	local k = T.kind
	for i = 1, select('#', ...) do
		if k == select(i, ...) then
			return T
		end
	end
	kindPanic('reflect.Value.' .. what, T)
end

local function mustSettable(x, what)
	local fl = flags(x)
	if band(fl, fRO) ~= 0 then
		panic('reflect: reflect.Value.' .. what .. ' using value obtained using unexported field')
	end
	if band(fl, fAddr) == 0 then
		panic('reflect: reflect.Value.' .. what .. ' using unaddressable value')
	end
end

local function store(x, val)
	local T = x.t
	if isAgg(T) then
		T.copy(x.v, val)
	else
		x.c[x.k] = val
	end
end

-- ifaceOf converts a Value to the Go interface value holding it.
local function ifaceOf(x)
	local T = x.t
	local val = get(x)
	if T.kind == 'interface' then
		return val
	end
	return T.box(val)
end

-- assignValue converts the value of y for storage in a location of type T.
local function assignValue(y, T, what)
	local V = mustValid(y, what)
	local val = get(y)
	if not assignable(V, T) then
		panic('reflect.' .. what .. ': value of type ' .. typeString(V) .. ' is not assignable to type ' .. typeString(T))
	end
	if T.kind == 'interface' and V.kind ~= 'interface' then
		return V.box(val)
	end
	return val
end

local function valueOf(i)
	if i == nil then
		return mk(nil)
	end
	return mk(typeof(i), unbox(i))
end

function P.ValueOf(i)
	return valueOf(i)
end

function P.Value__IsValid(x)
	return x.t ~= nil
end

function P.Value__Kind(x)
	local T = x.t
	if T == nil then
		return 0
	end
	return kindNum[T.kind] or 0
end

function P.Value__Type(x)
	return toType(mustValid(x, 'Type'))
end

function P.Value__Interface(x)
	mustValid(x, 'Interface')
	return ifaceOf(x)
end

function P.Value__CanInterface(x)
	mustValid(x, 'CanInterface')
	return band(flags(x), fRO) == 0
end

function P.Value__CanAddr(x)
	return band(flags(x), fAddr) ~= 0
end

function P.Value__CanSet(x)
	return band(flags(x), bor(fAddr, fRO)) == fAddr
end

function P.Value__IsNil(x)
	local T = mustValid(x, 'IsNil')
	if not nilableKinds[T.kind] then
		kindPanic('reflect.Value.IsNil', T)
	end
	return get(x) == nil
end

local function isZero(T, v)
	local k = T.kind
	if k == 'bool' then
		return v == false
	elseif k == 'string' then
		return v == ''
	elseif k == 'float32' or k == 'float64' then
		return v == 0 and 1 / v > 0
	elseif intKinds[k] or uintKinds[k] then
		return v == 0
	elseif k == 'complex64' or k == 'complex128' then
		return v == nil or (v.re == 0 and 1 / v.re > 0 and v.im == 0 and 1 / v.im > 0)
	elseif k == 'array' then
		local E = T.elem
		for i = 1, T.len do
			if not isZero(E, v[i]) then
				return false
			end
		end
		return true
	elseif k == 'struct' then
		for _, leaf in ipairs(rt.leaves(T)) do
			if not isZero(leaf[2], v[leaf[1]]) then
				return false
			end
		end
		return true
	end
	return v == nil
end

function P.Value__IsZero(x)
	return isZero(mustValid(x, 'IsZero'), get(x))
end

function P.Value__Bool(x)
	mustKind(x, 'Bool', 'bool')
	return get(x)
end

function P.Value__Int(x)
	local T = mustValid(x, 'Int')
	if not intKinds[T.kind] then
		kindPanic('reflect.Value.Int', T)
	end
	-- Int and Uint return int64 and uint64 FFI integers whatever the value representation.
	return rt.convnum(T, rt.int64, get(x))
end

function P.Value__Uint(x)
	local T = mustValid(x, 'Uint')
	if not uintKinds[T.kind] then
		kindPanic('reflect.Value.Uint', T)
	end
	return rt.convnum(T, rt.uint64, get(x))
end

function P.Value__Float(x)
	local T = mustKind(x, 'Float', 'float32', 'float64')
	return rt.convnum(T, rt.float64, get(x))
end

function P.Value__Complex(x)
	mustKind(x, 'Complex', 'complex64', 'complex128')
	return get(x)
end

function P.Value__CanInt(x)
	return x.t ~= nil and intKinds[x.t.kind] == true
end

function P.Value__CanUint(x)
	return x.t ~= nil and uintKinds[x.t.kind] == true
end

function P.Value__CanFloat(x)
	return x.t ~= nil and (x.t.kind == 'float32' or x.t.kind == 'float64')
end

function P.Value__String(x)
	local T = x.t
	if T == nil then
		return '<invalid Value>'
	end
	if T.kind == 'string' then
		return get(x)
	end
	return '<' .. typeString(T) .. ' Value>'
end

function P.Value__Bytes(x)
	local T = mustKind(x, 'Bytes', 'slice', 'array')
	if T.elem.kind ~= 'uint8' then
		panic('reflect.Value.Bytes of non-byte slice')
	end
	if T.kind == 'array' then
		if band(flags(x), fAddr) == 0 then
			panic('reflect.Value.Bytes of unaddressable byte array')
		end
		return rt.arrslice(x.v, T.len)
	end
	return get(x)
end

function P.Value__Len(x)
	local T = mustValid(x, 'Len')
	local k = T.kind
	if k == 'slice' then
		local s = get(x)
		return s and s[3] or 0
	elseif k == 'array' then
		return T.len
	elseif k == 'string' then
		return #get(x)
	elseif k == 'map' then
		return rt.mlen(get(x))
	elseif k == 'chan' then
		return rt.chlen(get(x))
	elseif k == 'ptr' and T.elem.kind == 'array' then
		return T.elem.len
	end
	kindPanic('reflect.Value.Len', T)
end

function P.Value__Cap(x)
	local T = mustValid(x, 'Cap')
	local k = T.kind
	if k == 'slice' then
		local s = get(x)
		return s and s[4] or 0
	elseif k == 'array' then
		return T.len
	elseif k == 'chan' then
		return rt.chcap(get(x))
	elseif k == 'ptr' and T.elem.kind == 'array' then
		return T.elem.len
	end
	kindPanic('reflect.Value.Cap', T)
end

function P.Value__Index(x, i)
	local T = mustValid(x, 'Index')
	local k = T.kind
	local ro = band(flags(x), fStickyRO)
	if k == 'slice' then
		local s = get(x)
		if s == nil or i < 0 or i >= s[3] then
			panic('reflect: slice index out of range')
		end
		return slot(T.elem, s[1], s[2] + i, bor(fAddr, ro))
	elseif k == 'array' then
		if i < 0 or i >= T.len then
			panic('reflect: array index out of range')
		end
		return slot(T.elem, x.v, i + 1, band(flags(x), bor(fAddr, fStickyRO)))
	elseif k == 'string' then
		local s = get(x)
		if i < 0 or i >= #s then
			panic('reflect: string index out of range')
		end
		return mk(rt.uint8, sbyte(s, i + 1), nil, nil, ro)
	end
	kindPanic('reflect.Value.Index', T)
end

function P.Value__Elem(x)
	local T = mustValid(x, 'Elem')
	local k = T.kind
	local ro = band(flags(x), fStickyRO)
	if k == 'interface' then
		local iv = get(x)
		if iv == nil then
			return mk(nil)
		end
		local y = valueOf(iv)
		y.fl = ro
		return y
	elseif k == 'ptr' then
		local p = get(x)
		if p == nil then
			return mk(nil)
		end
		local E = T.elem
		if isAgg(E) then
			return mk(E, p, nil, nil, bor(fAddr, ro))
		end
		return mk(E, nil, p, 'v', bor(fAddr, fBox, ro))
	end
	kindPanic('reflect.Value.Elem', T)
end

function P.Indirect(x)
	if x.t ~= nil and x.t.kind == 'ptr' then
		return P.Value__Elem(x)
	end
	return Value.clone(x)
end

function P.Value__Addr(x)
	local T = mustValid(x, 'Addr')
	local fl = flags(x)
	if band(fl, fAddr) == 0 then
		panic('reflect.Value.Addr of unaddressable value')
	end
	local PT = rt.ptrTo(T)
	local ro = band(fl, fRO)
	if isAgg(T) then
		return mk(PT, x.v, nil, nil, ro)
	elseif band(fl, fBox) ~= 0 then
		return mk(PT, x.c, nil, nil, ro)
	end
	return mk(PT, rt.fieldptr(x.c, x.k), nil, nil, ro)
end

function P.Value__NumField(x)
	local T = mustKind(x, 'NumField', 'struct')
	return #T.fields
end

local function field(x, i)
	local T = mustKind(x, 'Field', 'struct')
	local f = T.fields[i + 1]
	if not f then
		panic('reflect: Field index out of range')
	end
	local tbl = get(x)
	local fl = band(flags(x), bor(fAddr, fStickyRO))
	if not exported(f[3]) then
		if f[4] ~= 0 then
			fl = bor(fl, fEmbedRO)
		else
			fl = bor(fl, fStickyRO)
		end
	end
	local ft = f[2]
	if f[4] == 2 or f[6] then
		return mk(ft, tbl, nil, nil, fl)
	end
	return slot(ft, tbl, f[1], fl)
end
P.Value__Field = field

local function fieldByPath(x, path)
	for j, i in ipairs(path) do
		if j > 1 and x.t.kind == 'ptr' and x.t.elem.kind == 'struct' then
			if get(x) == nil then
				panic('reflect: indirection through nil pointer to embedded struct')
			end
			x = P.Value__Elem(x)
		end
		x = field(x, i)
	end
	return x
end

function P.Value__FieldByIndex(x, index)
	return fieldByPath(x, sliceToList(index))
end

function P.Value__FieldByName(x, name)
	local T = mustKind(x, 'FieldByName', 'struct')
	local path = fieldPath(T, name)
	if not path then
		return mk(nil)
	end
	return fieldByPath(x, path)
end

local function convertKey(MT, key)
	return assignValue(key, MT.key, 'Value.MapIndex')
end

function P.Value__MapIndex(x, key)
	local MT = mustKind(x, 'MapIndex', 'map')
	local v, ok = rt.mget2(get(x), convertKey(MT, key), MT)
	if not ok then
		return mk(nil)
	end
	return mk(MT.elem, v, nil, nil, band(flags(x), fStickyRO))
end

function P.Value__MapKeys(x)
	local MT = mustKind(x, 'MapKeys', 'map')
	local ro = band(flags(x), fStickyRO)
	local list = {}
	for k in rt.mrange(get(x), MT) do
		list[#list + 1] = mk(MT.key, k, nil, nil, ro)
	end
	return goslice(list)
end

function P.Value__SetMapIndex(x, key, elem)
	local MT = mustKind(x, 'SetMapIndex', 'map')
	if band(flags(x), fRO) ~= 0 then
		panic('reflect: reflect.Value.SetMapIndex using value obtained using unexported field')
	end
	local m = get(x)
	local k = assignValue(key, MT.key, 'Value.SetMapIndex')
	if elem.t == nil then
		rt.mdelp(m, k, MT)
		return
	end
	if m == nil then
		panic('assignment to entry in nil map')
	end
	local v = assignValue(elem, MT.elem, 'Value.SetMapIndex')
	if isAgg(MT.elem) then
		v = MT.elem.clone(v)
	end
	rt.msetp(m, k, v, MT)
end

P.MapIter = rt.named('reflect', 'reflect.MapIter', 'struct')
rt.defineStruct(P.MapIter, {
	{ 'm', rt.any, 'm', 0 },
	{ 'it', rt.any, 'it', 0 },
	{ 'ck', rt.any, 'ck', 0 },
	{ 'cv', rt.any, 'cv', 0 },
	{ 'ok', rt.bool, 'ok', 0 },
})

function P.Value__MapRange(x)
	mustKind(x, 'MapRange', 'map')
	return setmetatable({ m = Value.clone(x), ok = false }, P.MapIter.pmt)
end

function P.MapIter__Next(it)
	local m = it.m
	if m == nil or m.t == nil then
		panic('reflect: MapIter.Next called on an iterator that does not have an associated map Value')
	end
	if it.it == nil then
		it.it = rt.mrange(get(m), m.t)
	end
	local k, v = it.it()
	if k == nil then
		it.ok = false
		return false
	end
	it.ck, it.cv, it.ok = k, v, true
	return true
end

function P.MapIter__Key(it)
	if not it.ok then
		panic('reflect: MapIter.Key called before Next')
	end
	return mk(it.m.t.key, it.ck)
end

function P.MapIter__Value(it)
	if not it.ok then
		panic('reflect: MapIter.Value called before Next')
	end
	return mk(it.m.t.elem, it.cv)
end

function P.MapIter__Reset(it, v)
	it.m = Value.clone(v)
	it.it, it.ck, it.cv, it.ok = nil, nil, nil, false
end

rt.methods(P.MapIter, {}, {
	_mNext = P.MapIter__Next,
	_mKey = P.MapIter__Key,
	_mValue = P.MapIter__Value,
	_mReset = P.MapIter__Reset,
})

function P.Value__Set(x, y)
	mustValid(x, 'Set')
	mustSettable(x, 'Set')
	store(x, assignValue(y, x.t, 'Set'))
end

function P.Value__SetZero(x)
	local T = mustValid(x, 'SetZero')
	mustSettable(x, 'SetZero')
	store(x, T.zero())
end

function P.Value__SetInt(x, n)
	local T = mustValid(x, 'SetInt')
	mustSettable(x, 'SetInt')
	if not intKinds[T.kind] then
		kindPanic('reflect.Value.SetInt', T)
	end
	store(x, rt.convnum(rt.int64, T, n))
end

function P.Value__SetUint(x, n)
	local T = mustValid(x, 'SetUint')
	mustSettable(x, 'SetUint')
	if not uintKinds[T.kind] then
		kindPanic('reflect.Value.SetUint', T)
	end
	store(x, rt.convnum(rt.uint64, T, n))
end

function P.Value__SetFloat(x, f)
	local T = mustKind(x, 'SetFloat', 'float32', 'float64')
	mustSettable(x, 'SetFloat')
	store(x, rt.convnum(rt.float64, T, f))
end

function P.Value__SetComplex(x, c)
	mustKind(x, 'SetComplex', 'complex64', 'complex128')
	mustSettable(x, 'SetComplex')
	store(x, c)
end

function P.Value__SetString(x, s)
	mustKind(x, 'SetString', 'string')
	mustSettable(x, 'SetString')
	store(x, s)
end

function P.Value__SetBool(x, b)
	mustKind(x, 'SetBool', 'bool')
	mustSettable(x, 'SetBool')
	store(x, b)
end

function P.Value__SetBytes(x, b)
	mustKind(x, 'SetBytes', 'slice')
	mustSettable(x, 'SetBytes')
	store(x, b)
end

function P.Value__SetLen(x, n)
	mustKind(x, 'SetLen', 'slice')
	mustSettable(x, 'SetLen')
	store(x, rt.slice(get(x), 0, n))
end

function P.Value__Slice(x, i, j)
	local T = mustValid(x, 'Slice')
	local k = T.kind
	if k == 'slice' then
		return mk(T, rt.slice(get(x), i, j), nil, nil, band(flags(x), fStickyRO))
	elseif k == 'string' then
		local s = get(x)
		if i < 0 or j < i or j > #s then
			panic('reflect.Value.Slice: string slice index out of bounds')
		end
		return mk(T, ssub(s, i + 1, j), nil, nil, band(flags(x), fStickyRO))
	elseif k == 'array' then
		if band(flags(x), fAddr) == 0 then
			panic('reflect.Value.Slice: slice of unaddressable array')
		end
		return mk(rt.sliceOf(T.elem), rt.slice(rt.arrslice(x.v, T.len), i, j), nil, nil, band(flags(x), fStickyRO))
	end
	kindPanic('reflect.Value.Slice', T)
end

-- Pointer identities: Lua tables have no addresses, so hand out stable fake ones.
local addrs = setmetatable({}, { __mode = 'k' })
local nextAddr = 0xc000010000
local function addrOf(t)
	if t == nil then
		return 0
	end
	if type(t) ~= 'table' and type(t) ~= 'function' then
		return 0
	end
	local a = addrs[t]
	if not a then
		a = nextAddr
		nextAddr = nextAddr + 16
		addrs[t] = a
	end
	return a
end

function P.Value__Pointer(x)
	local T = mustKind(x, 'Pointer', 'ptr', 'chan', 'map', 'unsafeptr', 'func', 'slice')
	local v = get(x)
	if T.kind == 'slice' and v ~= nil then
		return addrOf(v[1]) + (v[2] - 1) * 8
	end
	return addrOf(v)
end
P.Value__UnsafePointer = P.Value__Pointer

function P.Value__Convert(x, t)
	local V = mustValid(x, 'Convert')
	local T = descOf(t)
	if not convertible(V, T) then
		panic('reflect.Value.Convert: value of type ' .. typeString(V) .. ' cannot be converted to type ' .. typeString(T))
	end
	local val = get(x)
	if V.kind == 'interface' and T.kind == 'interface' then
		return mk(T, val)
	end
	return mk(T, rt.convert(V, T, val), nil, nil, band(flags(x), fStickyRO))
end

function P.Value__CanConvert(x, t)
	return convertible(mustValid(x, 'CanConvert'), descOf(t))
end

function P.Value__Comparable(x)
	local T = x.t
	if T == nil then
		return true
	end
	if T.kind == 'interface' then
		local iv = get(x)
		return iv == nil or comparable(typeof(iv))
	end
	return comparable(T)
end

function P.Value__Equal(x, y)
	if x.t == nil or y.t == nil then
		return x.t == nil and y.t == nil
	end
	return rt.ieq(ifaceOf(x), ifaceOf(y))
end

function P.Value__NumMethod(x)
	return typeMethods._mNumMethod(toType(mustValid(x, 'NumMethod')))
end

function P.Zero(t)
	local T = descOf(t)
	return mk(T, T.zero())
end

function P.New(t)
	local T = descOf(t)
	return mk(rt.ptrTo(T), T.new())
end

function P.MakeSlice(t, n, c)
	local T = descOf(t)
	if T.kind ~= 'slice' then
		panic('reflect.MakeSlice of non-slice type')
	end
	local E = T.elem
	local z
	if isAgg(E) then
		z = E.zero
	else
		z = E.zero()
	end
	return mk(T, rt.mkslice(n, c, z))
end

function P.MakeMap(t)
	local T = descOf(t)
	if T.kind ~= 'map' then
		panic('reflect.MakeMap of non-map type')
	end
	return mk(T, rt.mkmap())
end

function P.MakeMapWithSize(t, n)
	local T = descOf(t)
	if T.kind ~= 'map' then
		panic('reflect.MakeMapWithSize of non-map type')
	end
	return mk(T, rt.mkmap(n))
end

function P.Append(s, xs)
	local T = mustKind(s, 'Append', 'slice')
	local r = get(s)
	if xs ~= nil then
		for j = 0, xs[3] - 1 do
			local v = assignValue(xs[1][xs[2] + j], T.elem, 'Append')
			if isAgg(T.elem) then
				v = T.elem.clone(v)
			end
			r = rt.append1(r, v)
		end
	end
	return mk(T, r)
end

function P.TypeAssert(T, x)
	mustValid(x, 'TypeAssert')
	local iv = ifaceOf(x)
	if T.kind == 'interface' then
		if iv ~= nil and rt.implements(typeof(iv), T) then
			return iv, true
		end
		return nil, false
	end
	if typeof(iv) == T then
		return unbox(iv), true
	end
	return T.zero(), false
end

function P.Swapper(slice)
	local s = unbox(slice)
	if s == nil then
		return function() end
	end
	local a, o, n = s[1], s[2], s[3]
	return function(i, j)
		if i < 0 or j < 0 or i >= n or j >= n then
			panic('reflect: slice index out of range')
		end
		a[o + i], a[o + j] = a[o + j], a[o + i]
	end
end

function P.Value__Fields(x)
	local T = mustValid(x, 'Fields')
	if T.kind ~= 'struct' then
		panic('reflect: Fields of non-struct type ' .. typeString(T))
	end
	return function(yield)
		for i = 0, #T.fields - 1 do
			if not yield(structField(T, i), field(x, i)) then
				return
			end
		end
	end
end

function P.Value__Seq(x)
	local T = mustValid(x, 'Seq')
	local k = T.kind
	if intKinds[k] or uintKinds[k] then
		local n = get(x)
		return function(yield)
			for i = 0, n - 1 do
				if not yield(mk(T, i)) then
					return
				end
			end
		end
	elseif k == 'array' or k == 'slice' or k == 'string' then
		return function(yield)
			if k == 'string' then
				for i in rt.runes(get(x)) do
					if not yield(mk(rt.int, i)) then
						return
					end
				end
				return
			end
			for i = 0, P.Value__Len(x) - 1 do
				if not yield(mk(rt.int, i)) then
					return
				end
			end
		end
	elseif k == 'map' then
		return function(yield)
			for key in rt.mrange(get(x), T) do
				if not yield(mk(T.key, key)) then
					return
				end
			end
		end
	end
	panic('reflect: ' .. typeString(T) .. ' cannot produce iter.Seq[Value]')
end

function P.Value__Seq2(x)
	local T = mustValid(x, 'Seq2')
	local k = T.kind
	if k == 'array' or k == 'slice' then
		return function(yield)
			for i = 0, P.Value__Len(x) - 1 do
				if not yield(mk(rt.int, i), P.Value__Index(x, i)) then
					return
				end
			end
		end
	elseif k == 'string' then
		return function(yield)
			for i, r in rt.runes(get(x)) do
				if not yield(mk(rt.int, i), mk(rt.int32, r)) then
					return
				end
			end
		end
	elseif k == 'map' then
		return function(yield)
			for key, v in rt.mrange(get(x), T) do
				if not yield(mk(T.key, key), mk(T.elem, v)) then
					return
				end
			end
		end
	end
	panic('reflect: ' .. typeString(T) .. ' cannot produce iter.Seq2[Value, Value]')
end

function P.Value__Clear(x)
	local T = mustKind(x, 'Clear', 'map', 'slice')
	if T.kind == 'map' then
		rt.mclear(get(x))
	else
		local E = T.elem
		rt.sclear(get(x), isAgg(E) and E.zero or E.zero())
	end
end

-- Call converts arguments to the parameter types, packing trailing variadic arguments into a slice.
function P.Value__Call(x, args)
	local T = mustKind(x, 'Call', 'func')
	local f = get(x)
	if f == nil then
		panic('reflect: call of nil function')
	end
	local params, list = T.params, sliceToList(args)
	local np = #params
	local raw = {}
	local fixed = T.variadic and np - 1 or np
	for i = 1, fixed do
		raw[i] = assignValue(list[i], params[i], 'Value.Call')
	end
	if T.variadic then
		local VT = params[np]
		local rest = nil
		for i = np, #list do
			rest = rt.append1(rest, assignValue(list[i], VT.elem, 'Value.Call'))
		end
		raw[np] = rest
	end
	local res = table.pack(f(table.unpack(raw, 1, np)))
	local out = {}
	for i, RT in ipairs(T.results) do
		out[i] = mk(RT, res[i])
	end
	return goslice(out)
end

function P.Value__CanComplex(x)
	return x.t ~= nil and (x.t.kind == 'complex64' or x.t.kind == 'complex128')
end

function P.Value__OverflowInt(x, n)
	local T = mustValid(x, 'OverflowInt')
	if not intKinds[T.kind] then
		kindPanic('reflect.Value.OverflowInt', T)
	end
	return rt.convnum(T, rt.int64, rt.convnum(rt.int64, T, n)) ~= n
end

function P.Value__OverflowUint(x, n)
	local T = mustValid(x, 'OverflowUint')
	if not uintKinds[T.kind] then
		kindPanic('reflect.Value.OverflowUint', T)
	end
	return rt.convnum(T, rt.uint64, rt.convnum(rt.uint64, T, n)) ~= n
end

function P.Value__OverflowFloat(x, f)
	local T = mustKind(x, 'OverflowFloat', 'float32', 'float64')
	if T.kind == 'float32' then
		if f < 0 then
			f = -f
		end
		return f > 3.40282346638528859811704183484516925440e+38 and f ~= math.huge
	end
	return false
end

function P.Value__Grow(x, n)
	local T = mustKind(x, 'Grow', 'slice')
	mustSettable(x, 'Grow')
	if n < 0 then
		panic('reflect.Value.Grow: negative len')
	end
	local s = get(x)
	local len, cap = rt.len(s), rt.cap(s)
	if cap - len >= n then
		return
	end
	local nc = len + n
	local a = require('table.new')(nc, 0)
	if len > 0 then
		table.move(s[1], s[2], s[2] + len - 1, 1, a)
	end
	local E = T.elem
	for i = len + 1, nc do
		a[i] = E.zero()
	end
	store(x, { a, 1, len, nc })
end

function P.Value__SetCap(x, n)
	mustKind(x, 'SetCap', 'slice')
	mustSettable(x, 'SetCap')
	local s = get(x)
	store(x, rt.slice3(s, 0, rt.len(s), n))
end

function P.Value__SetIterKey(x, it)
	mustSettable(x, 'SetIterKey')
	store(x, assignValue(P.MapIter__Key(it), x.t, 'Value.SetIterKey'))
end

function P.Value__SetIterValue(x, it)
	mustSettable(x, 'SetIterValue')
	store(x, assignValue(P.MapIter__Value(it), x.t, 'Value.SetIterValue'))
end

function P.Value__UnsafeAddr(x)
	mustValid(x, 'UnsafeAddr')
	if band(flags(x), fAddr) == 0 then
		panic('reflect.Value.UnsafeAddr of unaddressable value')
	end
	return P.Value__Pointer(P.Value__Addr(x))
end

function P.Value__Send(x, y)
	local T = mustKind(x, 'Send', 'chan')
	rt.send(get(x), assignValue(y, T.elem, 'Value.Send'))
end

function P.Value__Recv(x)
	local T = mustKind(x, 'Recv', 'chan')
	local v, ok = rt.recv2(get(x), T.elem.zero())
	return mk(T.elem, v), ok
end

function P.Value__TrySend(x, y)
	local T = mustKind(x, 'TrySend', 'chan')
	local ch = get(x)
	return rt.select({ { ch, 'send', assignValue(y, T.elem, 'Value.TrySend') }, false }, true) == 1
end

function P.Value__TryRecv(x)
	local T = mustKind(x, 'TryRecv', 'chan')
	local idx, v, ok = rt.select({ { get(x), 'recv' }, false }, true)
	if idx ~= 1 then
		return mk(nil), false
	end
	if not ok then
		return mk(T.elem, T.elem.zero()), false
	end
	return mk(T.elem, v), true
end

function P.Value__Close(x)
	mustKind(x, 'Close', 'chan')
	rt.close(get(x))
end

local valueMethods = {}
for name, f in pairs(P) do
	local m = name:match('^Value__(.+)$')
	if m then
		valueMethods['_m' .. m] = f
	end
end
rt.methods(Value, valueMethods, valueMethods)

---------------------------------------------------------------------------
-- DeepEqual

local deepEqual

local function deepIface(a, b, visited)
	if a == nil or b == nil then
		return a == b
	end
	local T = typeof(a)
	if T ~= typeof(b) then
		return false
	end
	return deepEqual(T, unbox(a), unbox(b), visited)
end

-- seen records a pair of reference values so cyclic structures terminate.
local function seen(visited, a, b)
	local m = visited[a]
	if m == nil then
		m = {}
		visited[a] = m
	end
	if m[b] then
		return true
	end
	m[b] = true
	return false
end

deepEqual = function(T, a, b, visited)
	local k = T.kind
	if k == 'interface' then
		return deepIface(a, b, visited)
	elseif k == 'ptr' then
		if a == b then
			return true
		end
		if a == nil or b == nil then
			return false
		end
		if seen(visited, a, b) then
			return true
		end
		local E = T.elem
		if isAgg(E) then
			return deepEqual(E, a, b, visited)
		end
		return deepEqual(E, a.v, b.v, visited)
	elseif k == 'struct' then
		for _, leaf in ipairs(rt.leaves(T)) do
			local key = leaf[1]
			if not deepEqual(leaf[2], a[key], b[key], visited) then
				return false
			end
		end
		return true
	elseif k == 'array' then
		local E = T.elem
		for i = 1, T.len do
			if not deepEqual(E, a[i], b[i], visited) then
				return false
			end
		end
		return true
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
		if a[1] == b[1] and a[2] == b[2] then
			return true
		end
		local E = T.elem
		local aa, ao, ba, bo = a[1], a[2], b[1], b[2]
		for i = 0, a[3] - 1 do
			if not deepEqual(E, aa[ao + i], ba[bo + i], visited) then
				return false
			end
		end
		return true
	elseif k == 'map' then
		if (a == nil) ~= (b == nil) then
			return false
		end
		if a == b then
			return true
		end
		if rt.mlen(a) ~= rt.mlen(b) then
			return false
		end
		if seen(visited, a, b) then
			return true
		end
		local E = T.elem
		for key, av in rt.mrange(a, T) do
			local bv, ok = rt.mget2(b, key, T)
			if not ok or not deepEqual(E, av, bv, visited) then
				return false
			end
		end
		return true
	elseif k == 'func' then
		return a == nil and b == nil
	elseif k == 'complex64' or k == 'complex128' then
		return rt.ceq(a, b)
	end
	return a == b
end

function P.DeepEqual(x, y)
	if x == nil or y == nil then
		return x == y
	end
	local T = typeof(x)
	if T ~= typeof(y) then
		return false
	end
	return deepEqual(T, unbox(x), unbox(y), {})
end

return P
