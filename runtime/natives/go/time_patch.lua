-- Runtime hooks for the transpiled time package (normally provided by the Go runtime). int64
-- values (seconds, nanoseconds, durations) are FFI integers.
local rt = require('rt')

local floor = math.floor

local function nanotime()
	return rt.I64(floor(rt.clock() * 1e9))
end

local function wallnow()
	if rt.wallclock then
		local sec, nsec = rt.wallclock()
		return sec, nsec
	end
	return os.time(), 0
end

return function(P)
	P.now = function()
		local sec, nsec = wallnow()
		return rt.I64(sec), nsec, nanotime()
	end
	-- wasip1 time has no local zone; use the host's current UTC offset (DST changes mid-run are ignored).
	P.initLocal = function()
		local now = os.time()
		local localFields = os.date('*t', now)
		local utcFields = os.date('!*t', now)
		utcFields.isdst = localFields.isdst
		local offset = os.time(localFields) - os.time(utcFields)
		-- Windows reports long names such as "GMT Summer Time"; keep only short abbreviations.
		local name = os.date('%Z', now)
		if not name:match('^%a%a%a?%a?%a?$') then
			name = ''
		end
		local zone = P.FixedZone(name, offset)
		for key, value in pairs(zone) do
			P.localLoc[key] = value
		end
		P.localLoc.name = 'Local'
	end
	P.runtimeNow = P.now
	P.runtimeNano = nanotime
	P.runtimeIsBubbled = function()
		return false
	end
	P.Sleep = function(d)
		if d <= 0 then
			return
		end
		if rt.current() then
			rt.sleepUntil(rt.clock() + tonumber(d) / 1e9)
		elseif rt.sleepNative then
			rt.sleepNative(tonumber(d) / 1e9)
		end
	end
	-- Timers fire by running f(arg, seq, delta) from a goroutine once `when` is reached.
	P.newTimer = function(when, period, f, arg, cp)
		local t = P.Timer.zero()
		t.initTimer = true
		local state = { when = when, period = period, active = true, gen = 0 }
		rawset(t, '__timer', state)
		local function arm()
			state.gen = state.gen + 1
			local gen = state.gen
			rt.go(function()
				while true do
					rt.sleepUntil(tonumber(state.when) / 1e9)
					if not state.active or state.gen ~= gen then
						return
					end
					f(arg, 0, 0)
					if state.period > 0 then
						state.when = state.when + state.period
					else
						state.active = false
						return
					end
				end
			end)
		end
		rawset(t, '__arm', arm)
		if when > 0 then
			arm()
		end
		if cp ~= nil then
			t.C = cp
		end
		return t
	end
	P.stopTimer = function(t)
		local state = rawget(t, '__timer')
		if not state then
			return false
		end
		local was = state.active
		state.active = false
		return was
	end
	P.resetTimer = function(t, when, period)
		local state = rawget(t, '__timer')
		if not state then
			return false
		end
		local was = state.active
		state.when = when
		state.period = period
		state.active = true
		rawget(t, '__arm')()
		return was
	end
end
