-- Native internal/godebug: settings come from the GODEBUG environment variable only.
local rt = require('rt')
local P = rt.package('internal/godebug')

P.Setting = rt.named('internal/godebug', 'godebug.Setting', 'struct')
rt.defineStruct(P.Setting, { { 'name', rt.string, 'name', 0 } })

local env = os.getenv('GODEBUG') or ''

local function lookup(name)
	for kv in env:gmatch('[^,]+') do
		local k, v = kv:match('^%s*([^=]+)=(.*)$')
		if k == name then
			return v
		end
	end
	return ''
end

function P.New(name)
	local s = P.Setting.zero()
	s.name = name:gsub('^#', '')
	return s
end

function P.Setting__Name(s)
	return s.name
end

function P.Setting__Value(s)
	return lookup(s.name)
end

function P.Setting__String(s)
	return s.name .. '=' .. lookup(s.name)
end

function P.Setting__IncNonDefault() end

function P.Setting__Undocumented()
	return false
end

rt.methods(P.Setting, {}, {
	_mName = P.Setting__Name,
	_mValue = P.Setting__Value,
	_mString = P.Setting__String,
	_mIncNonDefault = P.Setting__IncNonDefault,
	_mUndocumented = P.Setting__Undocumented,
})

return P
