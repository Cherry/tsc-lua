-- Native sync/atomic. Execution is single threaded, so atomics are plain loads and stores.
-- Atomic types are structs with a single field v; pointers to scalars are {v=...} boxes.
-- Struct fields of the integer types hold the plain value instead, with their common methods
-- compiled inline (rt.aadd and friends) and the rest called on a field pointer, whose v is
-- the field.
local rt = require('rt')
local P = rt.package('sync/atomic')

local function defineScalar(name, T, wrap, andOp, orOp)
	local A = rt.named('sync/atomic', 'atomic.' .. name, 'struct')
	rt.defineStruct(A, { { 'v', T, 'v', 0 } })
	P[name] = A
	local m = {}
	m.Load = function(x)
		return x.v
	end
	m.Store = function(x, v)
		x.v = v
	end
	m.Swap = function(x, v)
		local old = x.v
		x.v = v
		return old
	end
	m.CompareAndSwap = function(x, old, new)
		if x.v == old then
			x.v = new
			return true
		end
		return false
	end
	if wrap then
		m.Add = function(x, d)
			local v = wrap(x.v + d)
			x.v = v
			return v
		end
		m.And = function(x, mask)
			local old = x.v
			x.v = andOp(old, mask)
			return old
		end
		m.Or = function(x, mask)
			local old = x.v
			x.v = orOp(old, mask)
			return old
		end
	end
	local pset = {}
	for k, f in pairs(m) do
		P[name .. '__' .. k] = f
		pset['_m' .. k] = f
	end
	rt.methods(A, {}, pset)
	return A
end

local function id(x)
	return x
end

defineScalar('Bool', rt.bool, nil)
defineScalar('Int32', rt.int32, rt.int32.wrap, bit.band, bit.bor)
defineScalar('Int64', rt.int64, id, bit.band, bit.bor)
defineScalar('Uint32', rt.uint32, rt.uint32.wrap, bit.band, bit.bor)
defineScalar('Uint64', rt.uint64, id, bit.band, bit.bor)
defineScalar('Uintptr', rt.uintptr, id, bit.band, bit.bor)

-- Value holds an interface value.
P.Value = rt.named('sync/atomic', 'atomic.Value', 'struct')
rt.defineStruct(P.Value, { { 'v', rt.any, 'v', 0 } })
function P.Value__Load(x)
	return x.v
end
function P.Value__Store(x, v)
	x.v = v
end
function P.Value__Swap(x, v)
	local old = x.v
	x.v = v
	return old
end
function P.Value__CompareAndSwap(x, old, new)
	if rt.ieq(x.v, old) then
		x.v = new
		return true
	end
	return false
end
rt.methods(P.Value, {}, {
	_mLoad = P.Value__Load,
	_mStore = P.Value__Store,
	_mSwap = P.Value__Swap,
	_mCompareAndSwap = P.Value__CompareAndSwap,
})

-- Pointer[T] holds a *T.
P.Pointer = rt.generic('sync/atomic', 'atomic.Pointer', 1, function(T, E)
	rt.defineStruct(T, { { 'v', rt.ptrTo(E), 'v', 0 } })
	rt.methods(T, {}, {
		_mLoad = P.Value__Load,
		_mStore = P.Value__Store,
		_mSwap = P.Value__Swap,
		_mCompareAndSwap = function(x, old, new)
			if x.v == old then
				x.v = new
				return true
			end
			return false
		end,
	})
end)
-- Methods of generic types receive the type argument first.
function P.Pointer__Load(_, x)
	return x.v
end
function P.Pointer__Store(_, x, v)
	x.v = v
end
function P.Pointer__Swap(_, x, v)
	local old = x.v
	x.v = v
	return old
end
function P.Pointer__CompareAndSwap(_, x, old, new)
	if x.v == old then
		x.v = new
		return true
	end
	return false
end

-- Functions on pointers to scalars.
local function addFn(wrap)
	return function(p, d)
		local v = wrap(p.v + d)
		p.v = v
		return v
	end
end
local function load(p)
	return p.v
end
local function store(p, v)
	p.v = v
end
local function swap(p, v)
	local old = p.v
	p.v = v
	return old
end
local function cas(p, old, new)
	if p.v == old then
		p.v = new
		return true
	end
	return false
end
local function bitFn(op)
	return function(p, m)
		local old = p.v
		p.v = op(old, m)
		return old
	end
end

for _, n in ipairs({ 'Int32', 'Int64', 'Uint32', 'Uint64', 'Uintptr', 'Pointer' }) do
	P['Load' .. n] = load
	P['Store' .. n] = store
	P['Swap' .. n] = swap
	P['CompareAndSwap' .. n] = cas
	P['And' .. n] = bitFn(bit.band)
	P['Or' .. n] = bitFn(bit.bor)
end
P.AddInt32 = addFn(rt.int32.wrap)
P.AddUint32 = addFn(rt.uint32.wrap)
P.AddInt64 = addFn(id)
P.AddUint64 = addFn(id)
P.AddUintptr = addFn(id)

return P
