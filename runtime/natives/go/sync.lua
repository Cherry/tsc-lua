-- Native sync for the cooperative scheduler: goroutines only switch when they block,
-- so locks are uncontended unless a holder parks while holding them.
local rt = require('rt')
local P = rt.package('sync')

local function waitOn(q)
	q[#q + 1] = rt.current()
	rt.park()
end

local function wakeAll(q)
	for i = 1, #q do
		rt.ready(q[i])
		q[i] = nil
	end
end

P.Locker = rt.named('sync', 'sync.Locker', 'interface')
rt.define(P.Locker, rt.ifaceOf({ 'Lock', 'Unlock' }))

-- Mutex ---------------------------------------------------------------------
P.Mutex = rt.named('sync', 'sync.Mutex', 'struct')
rt.defineStruct(P.Mutex, { { 'locked', rt.bool, 'locked', 0 } })

function P.Mutex__Lock(m)
	while m.locked do
		m.waiters = m.waiters or {}
		waitOn(m.waiters)
	end
	m.locked = true
end

function P.Mutex__TryLock(m)
	if m.locked then
		return false
	end
	m.locked = true
	return true
end

function P.Mutex__Unlock(m)
	m.locked = false
	if m.waiters and #m.waiters > 0 then
		wakeAll(m.waiters)
	end
end

rt.methods(P.Mutex, {}, { _mLock = P.Mutex__Lock, _mUnlock = P.Mutex__Unlock, _mTryLock = P.Mutex__TryLock })

-- RWMutex -------------------------------------------------------------------
P.RWMutex = rt.named('sync', 'sync.RWMutex', 'struct')
rt.defineStruct(P.RWMutex, { { 'w', rt.bool, 'w', 0 }, { 'r', rt.int, 'r', 0 } })

function P.RWMutex__Lock(m)
	while m.w or m.r > 0 do
		m.waiters = m.waiters or {}
		waitOn(m.waiters)
	end
	m.w = true
end

function P.RWMutex__Unlock(m)
	m.w = false
	if m.waiters and #m.waiters > 0 then
		wakeAll(m.waiters)
	end
end

function P.RWMutex__RLock(m)
	while m.w do
		m.waiters = m.waiters or {}
		waitOn(m.waiters)
	end
	m.r = m.r + 1
end

function P.RWMutex__RUnlock(m)
	m.r = m.r - 1
	if m.r == 0 and m.waiters and #m.waiters > 0 then
		wakeAll(m.waiters)
	end
end

function P.RWMutex__TryLock(m)
	if m.w or m.r > 0 then
		return false
	end
	m.w = true
	return true
end

function P.RWMutex__TryRLock(m)
	if m.w then
		return false
	end
	m.r = m.r + 1
	return true
end

local RLockerMT = {
	__index = {
		_mLock = function(l)
			P.RWMutex__RLock(l.m)
		end,
		_mUnlock = function(l)
			P.RWMutex__RUnlock(l.m)
		end,
	},
}

function P.RWMutex__RLocker(m)
	return setmetatable({ m = m }, RLockerMT)
end

rt.methods(P.RWMutex, {}, {
	_mLock = P.RWMutex__Lock,
	_mUnlock = P.RWMutex__Unlock,
	_mRLock = P.RWMutex__RLock,
	_mRUnlock = P.RWMutex__RUnlock,
	_mTryLock = P.RWMutex__TryLock,
	_mTryRLock = P.RWMutex__TryRLock,
	_mRLocker = P.RWMutex__RLocker,
})

-- WaitGroup -----------------------------------------------------------------
P.WaitGroup = rt.named('sync', 'sync.WaitGroup', 'struct')
rt.defineStruct(P.WaitGroup, { { 'n', rt.int, 'n', 0 } })

function P.WaitGroup__Add(wg, d)
	wg.n = wg.n + d
	if wg.n < 0 then
		rt.panic('sync: negative WaitGroup counter')
	end
	if wg.n == 0 and wg.waiters then
		wakeAll(wg.waiters)
	end
end

function P.WaitGroup__Done(wg)
	P.WaitGroup__Add(wg, -1)
end

function P.WaitGroup__Wait(wg)
	while wg.n > 0 do
		wg.waiters = wg.waiters or {}
		waitOn(wg.waiters)
	end
end

-- runDone runs a WaitGroup.Go function; it is module level so that Go creates no closure, which
-- would stop the JIT from compiling the caller's trace.
local function runDone(wg, f)
	f()
	P.WaitGroup__Done(wg)
end

function P.WaitGroup__Go(wg, f)
	P.WaitGroup__Add(wg, 1)
	rt.go(runDone, wg, f)
end

rt.methods(P.WaitGroup, {}, {
	_mAdd = P.WaitGroup__Add,
	_mDone = P.WaitGroup__Done,
	_mWait = P.WaitGroup__Wait,
	_mGo = P.WaitGroup__Go,
})

-- Once ----------------------------------------------------------------------
P.Once = rt.named('sync', 'sync.Once', 'struct')
rt.defineStruct(P.Once, { { 'done', rt.bool, 'done', 0 } })

function P.Once__Do(o, f)
	if o.done then
		return
	end
	if o.running then
		while not o.done do
			o.waiters = o.waiters or {}
			waitOn(o.waiters)
		end
		return
	end
	o.running = true
	local ok, err = pcall(f)
	o.done = true
	o.running = nil
	if o.waiters then
		wakeAll(o.waiters)
	end
	if not ok then
		error(err, 0)
	end
end

rt.methods(P.Once, {}, { _mDo = P.Once__Do })

function P.OnceFunc(f)
	local done, ok, err = false, true, nil
	return function()
		if not done then
			done = true
			ok, err = pcall(f)
		end
		if not ok then
			error(err, 0)
		end
	end
end

function P.OnceValue(_, f)
	local done, ok, v = false, true, nil
	return function()
		if not done then
			done = true
			ok, v = pcall(f)
		end
		if not ok then
			error(v, 0)
		end
		return v
	end
end

function P.OnceValues(_, _, f)
	local done, ok, a, b = false, true, nil, nil
	return function()
		if not done then
			done = true
			ok, a, b = pcall(f)
		end
		if not ok then
			error(a, 0)
		end
		return a, b
	end
end

-- Cond ----------------------------------------------------------------------
P.Cond = rt.named('sync', 'sync.Cond', 'struct')
rt.defineStruct(P.Cond, { { 'L', P.Locker, 'L', 0 } })

function P.NewCond(l)
	local c = P.Cond.zero()
	c.L = l
	return c
end

function P.Cond__Wait(c)
	c.waiters = c.waiters or {}
	c.L:_mUnlock()
	waitOn(c.waiters)
	c.L:_mLock()
end

function P.Cond__Signal(c)
	if c.waiters and #c.waiters > 0 then
		rt.ready(table.remove(c.waiters, 1))
	end
end

function P.Cond__Broadcast(c)
	if c.waiters then
		wakeAll(c.waiters)
	end
end

rt.methods(P.Cond, {}, { _mWait = P.Cond__Wait, _mSignal = P.Cond__Signal, _mBroadcast = P.Cond__Broadcast })

-- Map -----------------------------------------------------------------------
P.Map = rt.named('sync', 'sync.Map', 'struct')
rt.defineStruct(P.Map, {})

local function smap(m)
	local d = rawget(m, 'data')
	if not d then
		d = {}
		rawset(m, 'data', d)
		rawset(m, 'keys', {})
	end
	return d, rawget(m, 'keys')
end

function P.Map__Load(m, k)
	local d = smap(m)
	local v = d[rt.ikey(k)]
	if v == nil then
		return nil, false
	end
	return rt.nn(v), true
end

function P.Map__Store(m, k, v)
	local d, keys = smap(m)
	local ks = rt.ikey(k)
	d[ks] = rt.nilv(v)
	keys[ks] = k
end

function P.Map__LoadOrStore(m, k, v)
	local d, keys = smap(m)
	local ks = rt.ikey(k)
	local cur = d[ks]
	if cur ~= nil then
		return rt.nn(cur), true
	end
	d[ks] = rt.nilv(v)
	keys[ks] = k
	return v, false
end

function P.Map__LoadAndDelete(m, k)
	local d, keys = smap(m)
	local ks = rt.ikey(k)
	local cur = d[ks]
	if cur == nil then
		return nil, false
	end
	d[ks] = nil
	keys[ks] = nil
	return rt.nn(cur), true
end

function P.Map__Delete(m, k)
	P.Map__LoadAndDelete(m, k)
end

function P.Map__Swap(m, k, v)
	local prev, loaded = P.Map__Load(m, k)
	P.Map__Store(m, k, v)
	return prev, loaded
end

function P.Map__CompareAndSwap(m, k, old, new)
	local cur, ok = P.Map__Load(m, k)
	if ok and rt.ieq(cur, old) then
		P.Map__Store(m, k, new)
		return true
	end
	return false
end

function P.Map__CompareAndDelete(m, k, old)
	local cur, ok = P.Map__Load(m, k)
	if ok and rt.ieq(cur, old) then
		P.Map__Delete(m, k)
		return true
	end
	return false
end

function P.Map__Range(m, f)
	local d, keys = smap(m)
	local snapshot = {}
	for ks, v in pairs(d) do
		snapshot[#snapshot + 1] = { keys[ks], v }
	end
	for _, e in ipairs(snapshot) do
		if not f(e[1], rt.nn(e[2])) then
			return
		end
	end
end

function P.Map__Clear(m)
	rawset(m, 'data', {})
	rawset(m, 'keys', {})
end

rt.methods(P.Map, {}, {
	_mLoad = P.Map__Load,
	_mStore = P.Map__Store,
	_mLoadOrStore = P.Map__LoadOrStore,
	_mLoadAndDelete = P.Map__LoadAndDelete,
	_mDelete = P.Map__Delete,
	_mSwap = P.Map__Swap,
	_mCompareAndSwap = P.Map__CompareAndSwap,
	_mCompareAndDelete = P.Map__CompareAndDelete,
	_mRange = P.Map__Range,
	_mClear = P.Map__Clear,
})

-- Pool ----------------------------------------------------------------------
P.Pool = rt.named('sync', 'sync.Pool', 'struct')
rt.defineStruct(P.Pool, { { 'New', rt.funcOf({}, { rt.any }, false), 'New', 0 } })

function P.Pool__Get(p)
	local items = rawget(p, 'items')
	if items and #items > 0 then
		return table.remove(items)
	end
	if p.New then
		return p.New()
	end
	return nil
end

function P.Pool__Put(p, x)
	local items = rawget(p, 'items')
	if not items then
		items = {}
		rawset(p, 'items', items)
	end
	if #items < 64 then
		items[#items + 1] = x
	end
end

rt.methods(P.Pool, {}, { _mGet = P.Pool__Get, _mPut = P.Pool__Put })

return P
