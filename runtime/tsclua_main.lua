-- Entry point inside the tsc-lua executable: modules and embedded files come from the
-- archive linked into the binary (exposed by the host as the global tsclua_archive).
local rt = require('rt')
local archive = tsclua_archive

rt.embedReader = function(mod, name)
	return archive.read('embed/' .. mod .. '/' .. name)
end

local args = { arg[0] or 'tsc-lua' }
for i = 1, #arg do
	args[#args + 1] = arg[i]
end

-- TSCLUA_PROFILE=<milliseconds> prints a sampling profile to stderr, as with runtime/run.lua.
local report
if os.getenv('TSCLUA_PROFILE') then
	report = rt.startProfile(tonumber(os.getenv('TSCLUA_PROFILE')) or 1)
	rt.onExit = function()
		report(io.stderr, tonumber(os.getenv('TSCLUA_PROFILE_TOP') or '') or 60)
		report = nil
	end
end

local main = rt.import('tsc.cmd.tsclua')
package.loaded['go.os'].__setArgs(args)
rt.runmain(main.main)
io.stdout:flush()
if report then
	report(io.stderr, tonumber(os.getenv('TSCLUA_PROFILE_TOP') or '') or 60)
end
os.exit(0)
