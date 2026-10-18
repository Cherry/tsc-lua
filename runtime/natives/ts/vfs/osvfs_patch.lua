-- Fast paths for the OS file system: read files straight into Lua strings and stat
-- natively, instead of going through io/fs byte slices.
-- deps: os
local rt = require('rt')

return function(P)
	local os_ = rt.import('go.os')
	local internal = rt.import('ts.vfs.internal')
	local nativepath = rt.import('ts.nativepath')

	local function isNetwork(p)
		return nativepath.IsNetworkPath(p)
	end

	P.osFS__ReadFile = function(_, path)
		if isNetwork(path) then
			return '', false
		end
		local data = os_.__readFileString(path)
		if data == nil then
			return '', false
		end
		return internal.decodeBytes(data)
	end

	local function stat(path)
		local st = os_.__rawStat(path, true)
		return st
	end

	P.osFS__FileExists = function(_, path)
		if isNetwork(path) then
			return false
		end
		local st = stat(path)
		return st ~= nil and not st.isdir
	end

	P.osFS__DirectoryExists = function(_, path)
		if isNetwork(path) then
			return false
		end
		local st = stat(path)
		return st ~= nil and st.isdir
	end

	P.osFS__Stat = function(_, path)
		if isNetwork(path) then
			return nil
		end
		local info, err = os_.Stat(path)
		if err ~= nil then
			return nil
		end
		return info
	end
end
