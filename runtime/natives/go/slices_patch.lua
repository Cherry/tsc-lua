-- slices.overlaps compares element addresses with unsafe; slices overlap here when they share
-- a backing table and their index ranges intersect.
return function(P)
	P.overlaps = function(_, a, b)
		if a == nil or b == nil or a[3] == 0 or b[3] == 0 or a[1] ~= b[1] then
			return false
		end
		return a[2] < b[2] + b[3] and b[2] < a[2] + a[3]
	end
end
