-- Exact-result math functions mapped onto Lua's C math. Log/Exp/Pow stay transpiled
-- because libm results may differ from Go's in the last ulp.

local floor, ceil, sqrt, fmod, huge = math.floor, math.ceil, math.sqrt, math.fmod, math.huge

return function(P)
	local function special(x)
		return x == 0 or x ~= x or x == huge or x == -huge
	end

	local function mfloor(x)
		if special(x) then
			return x
		end
		local r = floor(x) + 0.0
		if r == 0 and x < 0 then
			return -0.0
		end
		return r
	end

	local function mceil(x)
		if special(x) then
			return x
		end
		local r = ceil(x) + 0.0
		if r == 0 and x < 0 then
			return -0.0
		end
		return r
	end

	local function mtrunc(x)
		if special(x) then
			return x
		end
		if x < 0 then
			return mceil(x)
		end
		return mfloor(x)
	end

	P.archFloor = mfloor
	P.archCeil = mceil
	P.archTrunc = mtrunc
	P.Floor = mfloor
	P.Ceil = mceil
	P.Trunc = mtrunc
	P.floor = mfloor
	P.ceil = mceil
	P.trunc = mtrunc

	P.Sqrt = function(x)
		return sqrt(x)
	end
	P.sqrt = P.Sqrt

	P.Mod = function(x, y)
		if y == 0 or x == huge or x == -huge or x ~= x or y ~= y then
			return 0.0 / 0.0
		end
		return fmod(x, y)
	end

	-- A union reinterprets float bits; the results are uint64/uint32 values in their Lua representations.
	local ffi = require('ffi')
	local bits = ffi.new('union { double d; uint64_t u; float f; int32_t i; }')
	P.Float64bits = function(f)
		bits.d = f
		return bits.u
	end
	P.Float64frombits = function(b)
		bits.u = b
		return bits.d
	end
	P.Float32bits = function(f)
		bits.f = f
		return bits.i
	end
	P.Float32frombits = function(b)
		bits.i = b
		return bits.f
	end

	P.Inf = function(sign)
		if sign >= 0 then
			return huge
		end
		return -huge
	end
	P.NaN = function()
		return 0.0 / 0.0
	end
	P.IsNaN = function(f)
		return f ~= f
	end
	P.IsInf = function(f, sign)
		return (sign >= 0 and f == huge) or (sign <= 0 and f == -huge)
	end
	P.Abs = function(x)
		return math.abs(x)
	end
	P.Signbit = function(x)
		return x < 0 or (x == 0 and 1 / x < 0)
	end
end
