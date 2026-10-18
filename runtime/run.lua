-- Entry point for running a transpiled Go program with LuaJIT:
--   luajit runtime/run.lua <build dir> <main module> [args...]
local sep = package.config:sub(1, 1)
local script = arg and arg[0] or 'runtime/run.lua'
local runtimeDir = script:match('^(.*)[/\\][^/\\]+$') or '.'
local buildDir = arg[1]
local mainModule = arg[2]
if not buildDir or not mainModule then
	io.stderr:write('usage: run.lua <build dir> <main module> [args...]\n')
	os.exit(2)
end

package.path = table.concat({
	buildDir .. '/?.lua',
	runtimeDir .. '/?.lua',
	runtimeDir .. '/natives/?.lua',
}, ';')
-- tscnative must match the interpreter: look next to it (build/tools), or in TSCLUA_NATIVE_DIR.
-- The interpreter is the lowest arg index, below any interpreter options such as -joff.
local ext = sep == '\\' and '.dll' or '.so'
local first = -1
while arg[first - 1] do
	first = first - 1
end
local interpreterDir = arg[first] and arg[first]:match('^(.*)[/\\][^/\\]+$')
local nativeDir = os.getenv('TSCLUA_NATIVE_DIR') or (runtimeDir .. '/../build/tools')
package.cpath = (interpreterDir and (interpreterDir .. '/?' .. ext .. ';') or '') .. nativeDir .. '/?' .. ext .. ';' .. package.cpath

local rt = require('rt')
rt.embedRoot = os.getenv('TSCLUA_EMBED_DIR') or (buildDir .. '/../embed')

local args = { 'main' }
for i = 3, #arg do
	args[#args + 1] = arg[i]
end

-- TSCLUA_MEMLIMIT_MB=<n> makes allocations past n MB fail, which raises "not enough memory" in
-- the running goroutine, so a runaway test fails instead of exhausting the machine's memory.
-- TSCLUA_MEMLOG=<seconds> prints the Lua heap size periodically, to find leaks and GC lag.
-- TSCLUA_HEAPCENSUS=<n> prints which Go types fill the heap the first time it passes n MB.
local memLimit = tonumber(os.getenv('TSCLUA_MEMLIMIT_MB') or '')
if memLimit then
	require('tscnative').setmemlimit(memLimit * 1048576)
end
local memlogInterval = tonumber(os.getenv('TSCLUA_MEMLOG') or '')
local censusAt = tonumber(os.getenv('TSCLUA_HEAPCENSUS') or '')
if (memlogInterval or censusAt) and not os.getenv('TSCLUA_PROFILE') then
	local started, nextAt = os.clock(), memlogInterval
	-- LuaJIT's profiler timer interrupts compiled code too, unlike a count hook.
	require('jit.profile').start('i100', function(thread)
		local heap = collectgarbage('count')
		if censusAt and heap > censusAt * 1024 then
			censusAt = nil
			collectgarbage('collect')
			require('heapcensus')({ debug.getregistry(), thread }, io.stderr, 40)
		end
		if memlogInterval then
			local now = os.clock() - started
			if now >= nextAt then
				nextAt = now + memlogInterval
				io.stderr:write(string.format('memlog %8.1fs %10.1f MB\n', now, heap / 1024))
				-- stderr is block buffered when redirected to a file.
				io.stderr:flush()
			end
		end
	end)
end

local report
-- TSCLUA_PROFILE_TOP sets how many functions and lines the profile lists.
local profileTop = tonumber(os.getenv('TSCLUA_PROFILE_TOP') or '') or 60
if os.getenv('TSCLUA_PROFILE') then
	report = rt.startProfile(tonumber(os.getenv('TSCLUA_PROFILE')) or 1)
	-- os.Exit ends the process directly, so report from an exit hook as well.
	rt.onExit = function()
		report(io.stderr, profileTop)
		report = nil
	end
end

local main = rt.import(mainModule)
local osmod = package.loaded['go.os']
if osmod then
	osmod.__setArgs(args)
end
rt.runmain(main.main)
io.stdout:flush()
if report then
	report(io.stderr, profileTop)
end
os.exit(0, rt.closeOnExit)
