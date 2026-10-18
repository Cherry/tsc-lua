-- Native testing package: a sequential implementation of testing.T sufficient to drive
-- typescript-go's baseline test runners, recording a result per (sub)test.
-- deps: fmt context os flag regexp
local rt = require('rt')
local P = rt.package('testing')

local function lazy(mod)
	return function()
		return rt.import(mod)
	end
end
local fmt = lazy('go.fmt')
local context = lazy('go.context')
local os_ = lazy('go.os')

-- Set by tools that run tests (the tscluatest entry point) to make Testing() true.
P.__testing = false

function P.Testing()
	return P.__testing
end

function P.Short()
	return false
end

function P.Verbose()
	return os.getenv('TSCLUA_TEST_VERBOSE') ~= nil
end

P.__results = {}
P.__onResult = nil

local commonFields = {
	{ 'name', rt.string, 'name', 0 },
	{ 'failed', rt.bool, 'failed', 0 },
	{ 'skipped', rt.bool, 'skipped', 0 },
}
for _, name in ipairs({ 'T', 'B', 'F', 'M', 'PB' }) do
	P[name] = rt.named('testing', 'testing.' .. name, 'struct')
	rt.defineStruct(P[name], commonFields)
end
P.TB = rt.named('testing', 'testing.TB', 'interface')
rt.define(
	P.TB,
	rt.ifaceOf({
		'Cleanup',
		'Error',
		'Errorf',
		'Fail',
		'FailNow',
		'Failed',
		'Fatal',
		'Fatalf',
		'Helper',
		'Log',
		'Logf',
		'Name',
		'Skip',
		'SkipNow',
		'Skipf',
		'Skipped',
		'TempDir',
	})
)

local function sprintln(args)
	local s = fmt().Sprintln(args)
	return (s:gsub('\n$', ''))
end

local function messages(t)
	local m = rawget(t, 'messages')
	if not m then
		m = {}
		rawset(t, 'messages', m)
	end
	return m
end

local function log(t, msg)
	local m = messages(t)
	m[#m + 1] = msg
end

function P.T__Name(t)
	return t.name
end

function P.T__Log(t, args)
	log(t, sprintln(args))
end

function P.T__Logf(t, format, args)
	log(t, fmt().Sprintf(format, args))
end

function P.T__Fail(t)
	t.failed = true
	local parent = rawget(t, 'parent')
	while parent do
		parent.failed = true
		parent = rawget(parent, 'parent')
	end
end

function P.T__Failed(t)
	return t.failed
end

function P.T__FailNow(t)
	P.T__Fail(t)
	rt.goexit()
end

function P.T__Error(t, args)
	log(t, sprintln(args))
	P.T__Fail(t)
end

function P.T__Errorf(t, format, args)
	log(t, fmt().Sprintf(format, args))
	P.T__Fail(t)
end

function P.T__Fatal(t, args)
	log(t, sprintln(args))
	P.T__FailNow(t)
end

function P.T__Fatalf(t, format, args)
	log(t, fmt().Sprintf(format, args))
	P.T__FailNow(t)
end

function P.T__SkipNow(t)
	t.skipped = true
	rt.goexit()
end

function P.T__Skip(t, args)
	log(t, sprintln(args))
	P.T__SkipNow(t)
end

function P.T__Skipf(t, format, args)
	log(t, fmt().Sprintf(format, args))
	P.T__SkipNow(t)
end

function P.T__Skipped(t)
	return t.skipped
end

function P.T__Helper() end

function P.T__Parallel() end

function P.T__Setenv() end

function P.T__Chdir() end

function P.T__Cleanup(t, f)
	local list = rawget(t, 'cleanups')
	if not list then
		list = {}
		rawset(t, 'cleanups', list)
	end
	list[#list + 1] = f
end

function P.T__Context()
	return context().Background()
end

function P.T__Deadline()
	return nil, false
end

local tempCount = 0
function P.T__TempDir(t)
	tempCount = tempCount + 1
	local dir, err = os_().MkdirTemp('', 'tsclua-test')
	if err ~= nil then
		P.T__Fatalf(t, 'TempDir: %v', { { err }, 1, 1, 1 })
	end
	P.T__Cleanup(t, function()
		os_().RemoveAll(dir)
	end)
	return dir
end

local function runCleanups(t)
	local list = rawget(t, 'cleanups')
	if not list then
		return
	end
	for i = #list, 1, -1 do
		local ok, err = pcall(list[i])
		if not ok and not rt.isgoexit(err) then
			log(t, 'cleanup panicked: ' .. rt.panicString(rt.panicValue(err)))
			P.T__Fail(t)
		end
	end
end

local function record(t)
	local status = 'pass'
	if t.failed then
		status = 'fail'
	elseif t.skipped then
		status = 'skip'
	end
	local result = { name = t.name, status = status, messages = rawget(t, 'messages') or {} }
	P.__results[#P.__results + 1] = result
	if P.__onResult then
		P.__onResult(result)
	end
end

-- Subtests are selected like go test -run: the pattern is split on '/', its first element
-- names the (absent) top-level test function, and each further element filters one level.
local runPatterns = nil

local function loadRunPatterns()
	if runPatterns ~= nil then
		return runPatterns
	end
	runPatterns = {}
	local flag = rt.import('go.flag')
	local f = flag.Lookup('test.run')
	local pattern = f and f.Value:_mString() or ''
	if pattern == '' then
		return runPatterns
	end
	local regexp = rt.import('go.regexp')
	local i = 0
	for part in (pattern .. '/'):gmatch('(.-)/') do
		if i > 0 then
			runPatterns[i] = regexp.MustCompile(part)
		end
		i = i + 1
	end
	return runPatterns
end

-- rewriteName mirrors how go test names subtests (spaces become underscores).
local function rewriteName(name)
	return (name:gsub('%s', '_'))
end

-- TSCLUA_SHARD=i/n runs the top-level subtests whose name hashes to shard i of n, to split a
-- suite across processes. Hashing the name, not counting, keeps the split the same in every
-- process even where tests are created in map iteration order, which differs between processes.
local shardIndex, shardCount = (os.getenv('TSCLUA_SHARD') or ''):match('^(%d+)/(%d+)$')
shardIndex, shardCount = tonumber(shardIndex), tonumber(shardCount)

local function shardOf(name)
	-- 32-bit FNV-1a.
	local h = 2166136261
	for i = 1, #name do
		h = rt.imul(bit.bxor(h, name:byte(i)), 16777619) % 4294967296
	end
	return h % shardCount
end

function P.T__Run(t, name, f)
	name = rewriteName(name)
	local depth = rawget(t, 'depth') or 0
	local re = loadRunPatterns()[depth + 1]
	if re and not rt.import('go.regexp').Regexp__MatchString(re, name) then
		return true
	end
	if depth == 0 and shardCount and shardOf(name) ~= shardIndex then
		return true
	end
	if depth == 0 and P.Verbose() then
		io.stdout:write('=== RUN ', name, '\n')
		io.stdout:flush()
	end
	local child = P.T.zero()
	rawset(child, 'depth', depth + 1)
	if t.name == '' then
		child.name = name
	else
		child.name = t.name .. '/' .. name
	end
	rawset(child, 'parent', t)
	local ok, err = pcall(f, child)
	if not ok and not rt.isgoexit(err) then
		log(child, 'panic: ' .. rt.panicString(rt.panicValue(err)))
		P.T__Fail(child)
	end
	runCleanups(child)
	if not rawget(child, 'hasChildren') or #(rawget(child, 'messages') or {}) > 0 then
		record(child)
	end
	rawset(t, 'hasChildren', true)
	return not child.failed
end

local tMethods = {}
for k, v in pairs(P) do
	local method = type(k) == 'string' and k:match('^T__(.+)$')
	if method then
		tMethods['_m' .. method] = v
	end
end
rt.methods(P.T, {}, tMethods)
rt.methods(P.B, {}, tMethods)
rt.methods(P.F, {}, tMethods)

function P.AllocsPerRun()
	return 0.0
end

return P
