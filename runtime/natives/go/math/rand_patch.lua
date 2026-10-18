-- Seedless runtime randomness for math/rand, from Lua's generator (all 64 bits).
return function(P)
	P.runtime_rand = function()
		return math.random(0)
	end
end
