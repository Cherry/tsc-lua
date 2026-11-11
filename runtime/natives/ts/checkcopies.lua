-- Type checking in copies of the process. Go checks a program's files on several threads; Lua
-- has one. So for a plain `--noEmit` compilation the program is built and bound once, with a
-- single checker, and then the process is copied (fork, or its Windows equivalent): the copies
-- share everything built so far and each checks a share of the files, carrying on from the
-- same checker. A copy writes the diagnostics it found to a file and exits, and the original
-- reports them. A file is checked by one checker either way, as `tsc --singleThreaded` does.
-- A copy that fails for any reason only costs time: the original checks its files itself.
-- The memory is shared for as long as nothing writes to it, so the compilation collects only
-- young objects (collectgarbage('young'), added to the LuaJIT in vendor/): what the program
-- was built from stays untouched, and each process still frees its own garbage. See
-- "Checking in copies of the process" in docs/RUNTIME.md.
local rt = require('rt')
local native = require('tscnative')
local sformat, sbyte, tconcat = string.format, string.byte, table.concat

local W = {}

-- The compilation in progress when it may check in copies:
--   count              how many processes may check, the original included
--   copies[j]          { handle, out } for copy j
--   program, checked   set once the program's files have been checked
local session

local megabyte = 2 ^ 20
-- Megabytes a process allocates between collections of its young objects (TSCLUA_GC_YOUNG).
local nursery = tonumber(os.getenv('TSCLUA_GC_YOUNG') or '') or 64
local trace = os.getenv('TSCLUA_CHECK_DEBUG') and function(...)
	io.stderr:write('tsc-lua check: ', sformat(...), '\n')
end or function() end

local function tempDir()
	return os.getenv('TEMP') or os.getenv('TMP') or os.getenv('TMPDIR') or '/tmp'
end

-- A Lua string literal on one line: control and non-ASCII bytes are written as escapes.
local function quote(s)
	return '"' .. s:gsub('[%c"\\\127-\255]', function(c)
		return sformat('\\%03d', sbyte(c))
	end) .. '"'
end

function W.install(P)
	local ast, checker, diagnostics = package.loaded['ts.ast'], package.loaded['ts.checker'], package.loaded['ts.diagnostics']
	local core = package.loaded['ts.core']
	local D = ast.Diagnostic
	local key = rt.fieldkey
	local POOL, FILES = key(P.Program, 'compilerCheckerPool'), key(P.Program, 'files')
	local CHECKERS = key(P.checkerPool, 'checkers')
	local IS_DECLARATION = key(ast.SourceFile, 'IsDeclarationFile')
	local COUNTS = {
		s = key(checker.Checker, 'SymbolCount'),
		t = key(checker.Checker, 'TypeCount'),
		i = key(checker.Checker, 'TotalInstantiationCount'),
	}
	local D_FILE, D_POS, D_END, D_CODE, D_CATEGORY = key(D, 'file'), key(D, 'pos'), key(D, 'end'), key(D, 'code'), key(D, 'category')
	local D_SOURCE, D_MESSAGE, D_TEXT, D_KEY = key(D, 'source'), key(D, 'message'), key(D, 'messageText'), key(D, 'messageKey')
	local D_ARGS, D_CHAIN, D_RELATED = key(D, 'messageArgs'), key(D, 'messageChain'), key(D, 'relatedInformation')
	local D_UNNECESSARY, D_DEPRECATED, D_SKIPPED = key(D, 'reportsUnnecessary'), key(D, 'reportsDeprecated'), key(D, 'skippedOnNoEmit')

	-- A diagnostic field this file does not know about would be lost between processes, so
	-- copies are only used while the struct has exactly the fields written below.
	local known = {
		file = true,
		loc = true,
		code = true,
		category = true,
		source = true,
		message = true,
		messageText = true,
		messageKey = true,
		messageArgs = true,
		messageChain = true,
		relatedInformation = true,
		reportsUnnecessary = true,
		reportsDeprecated = true,
		skippedOnNoEmit = true,
		-- Only incremental builds read this, and they do not check in copies.
		repopulateInfo = true,
	}
	W.supported = true
	for _, field in ipairs(D.fields) do
		if not known[field[3]] then
			W.supported = false
		end
		known[field[3]] = nil
	end
	if next(known) ~= nil then
		W.supported = false
	end

	local writeDiagnostics

	local function writeDiagnostic(out, d)
		out[#out + 1] = sformat('{p=%d,e=%d,c=%d,g=%d,', d[D_POS], d[D_END], d[D_CODE], d[D_CATEGORY])
		if d[D_FILE] ~= nil then
			out[#out + 1] = 'f=' .. quote(ast.SourceFile__FileName(d[D_FILE])) .. ','
		end
		if d[D_SOURCE] ~= '' then
			out[#out + 1] = 's=' .. quote(d[D_SOURCE]) .. ','
		end
		local message = d[D_MESSAGE]
		if message == nil then
			out[#out + 1] = 'n=true,'
		elseif d[D_CODE] == -1 then
			-- An ad hoc message exists only in the process that made it.
			out[#out + 1] = 'm=' .. quote(diagnostics.Message__String(message)) .. ','
		end
		if d[D_TEXT] ~= '' then
			out[#out + 1] = 't=' .. quote(d[D_TEXT]) .. ','
		end
		if d[D_KEY] ~= '' then
			out[#out + 1] = 'k=' .. quote(d[D_KEY]) .. ','
		end
		local args = d[D_ARGS]
		if args ~= nil then
			out[#out + 1] = 'a={'
			for i = 0, args[3] - 1 do
				out[#out + 1] = quote(args[1][args[2] + i]) .. ','
			end
			out[#out + 1] = '},'
		end
		if d[D_CHAIN] ~= nil then
			out[#out + 1] = 'h='
			writeDiagnostics(out, d[D_CHAIN])
			out[#out + 1] = ','
		end
		if d[D_RELATED] ~= nil then
			out[#out + 1] = 'r='
			writeDiagnostics(out, d[D_RELATED])
			out[#out + 1] = ','
		end
		if d[D_UNNECESSARY] then
			out[#out + 1] = 'u=true,'
		end
		if d[D_DEPRECATED] then
			out[#out + 1] = 'x=true,'
		end
		if d[D_SKIPPED] then
			out[#out + 1] = 'o=true,'
		end
		out[#out + 1] = '}'
	end

	-- Writes a slice of diagnostics (or nil) as a Lua list.
	function writeDiagnostics(out, list)
		out[#out + 1] = '{'
		if list ~= nil then
			for i = 0, list[3] - 1 do
				writeDiagnostic(out, list[1][list[2] + i])
				out[#out + 1] = ','
			end
		end
		out[#out + 1] = '}'
	end

	local readDiagnostics

	local function strings(list)
		if list == nil then
			return nil
		end
		return { list, 1, #list, #list }
	end

	local function readDiagnostic(s, t)
		local d = D.zero()
		if t.f ~= nil then
			d[D_FILE] = assert(s.fileByName[t.f], 'diagnostic in an unknown file')
		end
		d[D_POS], d[D_END], d[D_CODE], d[D_CATEGORY] = t.p, t.e, t.c, t.g
		d[D_SOURCE], d[D_TEXT], d[D_KEY] = t.s or '', t.t or '', t.k or ''
		if t.m ~= nil then
			d[D_MESSAGE] = diagnostics.NewAdHocMessage(t.m)
		elseif not t.n then
			d[D_MESSAGE] = assert(diagnostics.keyToMessage(d[D_KEY]), 'unknown diagnostic message')
		end
		d[D_ARGS] = strings(t.a)
		if t.h ~= nil then
			d[D_CHAIN] = readDiagnostics(s, t.h)
		end
		if t.r ~= nil then
			d[D_RELATED] = readDiagnostics(s, t.r)
		end
		d[D_UNNECESSARY], d[D_DEPRECATED], d[D_SKIPPED] = t.u == true, t.x == true, t.o == true
		return d
	end

	-- Turns a list written by writeDiagnostics back into a slice of diagnostics.
	function readDiagnostics(s, list)
		local n = #list
		local a = {}
		for i = 1, n do
			a[i] = readDiagnostic(s, list[i])
		end
		return { a, 1, n, n }
	end

	-- Writes what a copy found: a line per file (S: its diagnostics, D: its declaration
	-- diagnostics), the checker's global diagnostics (G), its counters (C) and timing (T).
	-- Each line is a Lua literal, so one huge result cannot outgrow a single chunk's constants.
	local function writeResult(path, fileCount, semantic, globals, declaration, c, seconds, megabytes)
		local out = {}
		local function section(kind, lists)
			for index, list in pairs(lists) do
				out[#out + 1] = kind .. ' ' .. index .. ' '
				writeDiagnostics(out, list or nil)
				out[#out + 1] = '\n'
			end
		end
		section('S', semantic)
		section('G', { [0] = globals or false })
		section('D', declaration)
		out[#out + 1] = sformat('C 0 {s=%d,t=%d,i=%d}\n', c[COUNTS.s], c[COUNTS.t], c[COUNTS.i])
		out[#out + 1] = sformat('T 0 {t=%.3f,m=%.0f}\n', seconds, megabytes)
		-- A result without this line was cut short.
		out[#out + 1] = 'end ' .. fileCount .. '\n'
		local f = assert(io.open(path, 'wb'))
		f:write(tconcat(out))
		f:close()
	end

	-- Waits for copy j and returns what it wrote, or nil when it failed.
	local function readResult(s, j)
		local copy = s.copies[j]
		local code = native.wait(copy.handle)
		copy.handle = nil
		local result
		local f = code == 0 and io.open(copy.out, 'rb')
		if f then
			local data = f:read('*a')
			f:close()
			local parsed = { S = {}, G = {}, D = {}, C = {}, T = {} }
			local complete = false
			local fileCount = 0
			-- Every diagnostic is read here, so a damaged result is rejected as a whole.
			local ok = pcall(function()
				for line in data:gmatch('[^\n]+') do
					local kind, index, body = line:match('^(%u) (%d+) (.*)$')
					if kind ~= nil then
						local value = assert(load('return ' .. body, '=copy', 't', {}))()
						if kind == 'C' or kind == 'T' then
							parsed[kind][tonumber(index)] = value
						else
							parsed[kind][tonumber(index)] = readDiagnostics(s, value)
						end
						if kind == 'S' then
							fileCount = fileCount + 1
						end
					elseif line == 'end ' .. s.files[3] then
						complete = true
					end
				end
			end)
			if ok and complete then
				result = parsed
				trace('copy %d checked %d files in %.2fs and allocated %d MB', j, fileCount, parsed.T[0].t, parsed.T[0].m)
			end
		end
		if result == nil then
			trace('copy %d exited with %s and no usable result', j, tostring(code))
		end
		os.remove(copy.out)
		return result
	end

	local declarationDiagnostics = P.Program__getDeclarationDiagnosticsForFile

	-- Divides the files into at most count runs of consecutive files of about equal size.
	-- Files that import each other tend to be neighbours, and a process that checks
	-- neighbours repeats less of what the other processes work out.
	local function runsOf(s, p, count)
		local files = s.files
		local fa, fo, n = files[1], files[2], files[3]
		local weight, total = {}, 0
		for i = 0, n - 1 do
			-- A file that is not checked costs next to nothing.
			local w = 0
			if not P.Program__SkipTypeChecking(p, fa[fo + i], false) then
				w = #ast.SourceFile__Text(fa[fo + i]) + 1
			end
			weight[i] = w
			total = total + w
		end
		local runs = {}
		local low, size = 0, 0
		for i = 0, n - 1 do
			size = size + weight[i]
			local full = #runs < count - 1 and size >= total * (#runs + 1) / count
			if i == n - 1 or (full and size < total) then
				runs[#runs + 1] = { low, i }
				low = i + 1
			end
		end
		return runs
	end

	-- Whether the machine still has memory for this process to keep what it works out.
	local function memoryToSpare(s)
		local available, _, commit = native.memory()
		if available == nil then
			return true
		end
		return available > s.floor and (commit == nil or commit > s.floor)
	end

	-- What a copy does: its run of files, then what the original would ask its checker.
	local function runCopy(s, p, out, run)
		local fa, fo = s.files[1], s.files[2]
		local c = s.checker
		local started, heap = native.monotime(), collectgarbage('count')
		local semantic, clean = {}, true
		for i = run[1], run[2] do
			-- What a copy leaves is checked by the original once the copies are gone.
			if not memoryToSpare(s) then
				break
			end
			local found = s.collect(s.ctx, c, fa[fo + i])
			semantic[i] = found or false
			if found ~= nil and found[3] > 0 then
				clean = false
			end
		end
		local globals = checker.Checker__GetGlobalDiagnostics(c)
		-- Declaration diagnostics are only asked for when nothing else was reported.
		local declaration = {}
		if clean and s.declarations then
			for i in pairs(semantic) do
				if not fa[fo + i][IS_DECLARATION] then
					declaration[i] = declarationDiagnostics(p, s.ctx, fa[fo + i]) or false
				end
			end
		end
		local allocated = (collectgarbage('count') - heap) / 1024
		writeResult(out, s.files[3], semantic, globals, declaration, c, native.monotime() - started, allocated)
	end

	-- How many copies of this process there is memory for.
	local function copyCount(s)
		local copies = s.count - 1
		local available, total, commit = native.memory()
		if available == nil then
			return copies
		end
		-- Below this much free memory a copy takes no more files, so the machine stays usable.
		s.floor = math.max(2048 * megabyte, total / 8)
		local heap = collectgarbage('count') * 1024
		-- A copy shares this heap but writes to part of it (up to a quarter, on the benchmark
		-- apps), and has young objects and what it works out besides.
		copies = math.min(copies, math.floor((available - s.floor) / (heap / 4 + (nursery + 64) * megabyte)))
		if commit ~= nil then
			-- Windows commits the whole heap again for each copy, shared or not.
			copies = math.min(copies, math.floor((commit - s.floor) / (heap * 1.2)))
		end
		return math.max(copies, 0)
	end

	-- Checks every file, sharing the work with copies of this process.
	local function forkCheck(s, p)
		local files = s.files
		local fa, fo, n = files[1], files[2], files[3]
		local c = s.checker
		local found = {}
		local function check(i)
			found[i] = s.collect(s.ctx, c, fa[fo + i]) or false
		end
		-- Everything made so far becomes old, garbage included (see W.begin), and each process
		-- collects what it makes from here on.
		collectgarbage('young', nursery * 1024)
		local runs = runsOf(s, p, copyCount(s) + 1)
		s.declarations = core.CompilerOptions__GetEmitDeclarations(P.Program__Options(p))
		-- The counters a copy reports include what its checker counted before the copy was made.
		s.base = { s = c[COUNTS.s], t = c[COUNTS.t], i = c[COUNTS.i] }
		if #runs > 1 then
			io.stdout:flush()
			io.stderr:flush()
			local pid = native.getpid()
			for j = 1, #runs - 1 do
				local out = sformat('%s/tsc-lua-%d-%d.txt', tempDir(), pid, j)
				local handle = native.fork()
				if handle == 0 then
					if native.os ~= 'windows' then
						native.watchparent(pid)
					end
					-- An error is the original's to report: it meets it checking this copy's files.
					local ok = pcall(runCopy, s, p, out, runs[j + 1])
					native.exit(ok and 0 or 1)
				end
				if handle == nil then
					trace('copy %d could not be made', j)
					break
				end
				s.copies[j] = { handle = handle, out = out }
			end
			-- The original takes the first run, which has the least to work out.
			local started = native.monotime()
			for i = runs[1][1], runs[1][2] do
				check(i)
			end
			trace('the original checked %d files in %.2fs, with %d copies', runs[1][2] + 1, native.monotime() - started, #s.copies)
			for j = 1, #s.copies do
				local result = readResult(s, j)
				if result ~= nil then
					for i, list in pairs(result.S) do
						found[i] = list
						s.declaration[i] = result.D[i]
						s.remote[i] = true
					end
					s.globals[#s.globals + 1] = result.G[0]
					s.counts[#s.counts + 1] = result.C[0]
				end
			end
		end
		-- Whatever no process reported (a copy failed or stopped, or there were none) is checked here.
		for i = 0, n - 1 do
			if found[i] == nil then
				check(i)
			end
		end
		return found
	end

	local collectFromFiles = P.Program__collectCheckerDiagnosticsFromFiles
	P.Program__collectCheckerDiagnosticsFromFiles = function(p, ctx, sourceFiles, collect)
		local s = session
		local files = p[FILES]
		local whole = sourceFiles ~= nil and files ~= nil and sourceFiles[1] == files[1] and sourceFiles[3] == files[3]
		if s == nil or s.program ~= nil then
			return collectFromFiles(p, ctx, sourceFiles, collect)
		end
		local pool = p[POOL]
		local list
		if pool ~= nil and whole then
			P.checkerPool__createCheckers(pool)
			list = pool[CHECKERS]
		end
		if list == nil or list[3] ~= 1 then
			-- Not the compilation this was set up for: it checks as usual.
			collectgarbage('young', nursery * 1024)
			return collectFromFiles(p, ctx, sourceFiles, collect)
		end
		s.program, s.ctx, s.collect, s.files, s.checker = p, ctx, collect, sourceFiles, list[1][list[2]]
		s.fileIndex, s.fileByName = {}, {}
		local n = sourceFiles[3]
		for i = 0, n - 1 do
			local file = sourceFiles[1][sourceFiles[2] + i]
			s.fileIndex[file] = i
			s.fileByName[ast.SourceFile__FileName(file)] = file
		end
		local found = forkCheck(s, p)
		s.checked = true
		local diagnostics = rt.mkslice(n)
		local da, base = diagnostics[1], diagnostics[2]
		for i = 0, n - 1 do
			da[base + i] = found[i] or nil
		end
		return diagnostics
	end

	local globalDiagnostics = P.checkerPool__GetGlobalDiagnostics
	P.checkerPool__GetGlobalDiagnostics = function(pool)
		local s = session
		if s == nil or not s.checked or s.program[POOL] ~= pool then
			return globalDiagnostics(pool)
		end
		-- The copies' checkers carried on from this one, so each has global diagnostics of its own.
		local all = {}
		local function add(found)
			if found ~= nil then
				for i = 0, found[3] - 1 do
					all[#all + 1] = found[1][found[2] + i]
				end
			end
		end
		add(checker.Checker__GetGlobalDiagnostics(s.checker))
		for _, theirs in ipairs(s.globals) do
			add(theirs)
		end
		return P.SortAndDeduplicateDiagnostics({ all, 1, #all, #all })
	end

	P.Program__getDeclarationDiagnosticsForFile = function(p, ctx, sourceFile)
		local s = session
		local i = s ~= nil and s.checked and s.program == p and s.fileIndex[sourceFile]
		if not i or not s.remote[i] then
			return declarationDiagnostics(p, ctx, sourceFile)
		end
		local theirs = s.declaration[i]
		if theirs ~= nil then
			return theirs
		end
		-- This checker has to check the file before it can describe its declarations.
		s.remote[i] = nil
		s.collect(s.ctx, s.checker, sourceFile)
		return declarationDiagnostics(p, ctx, sourceFile)
	end

	-- The statistics add up the checker's counters, and each copy's checker went on counting.
	local function counted(name, k)
		local original = P[name]
		P[name] = function(p)
			local s = session
			local total = original(p)
			if s ~= nil and s.checked and s.program == p then
				for _, theirs in ipairs(s.counts) do
					total = total + theirs[k] - s.base[k]
				end
			end
			return total
		end
	end
	counted('Program__SymbolCount', 's')
	counted('Program__TypeCount', 't')
	counted('Program__InstantiationCount', 'i')
end

-- How many processes may check a compilation with this configuration; 1 means no copies.
local function processCount(config, testing)
	local limit = tonumber(os.getenv('TSCLUA_CHECK_PROCESSES') or '')
	if testing ~= nil or not W.supported or (limit ~= nil and limit < 2) then
		return 1
	end
	-- A copy has only the thread that made it, and these all run a timer thread.
	if os.getenv('TSCLUA_PROFILE') or os.getenv('TSCLUA_MEMLOG') or os.getenv('TSCLUA_HEAPCENSUS') then
		return 1
	end
	local core, tsoptions = package.loaded['ts.core'], package.loaded['ts.tsoptions']
	local options = tsoptions.ParsedCommandLine__CompilerOptions(config)
	local function option(name)
		return options[rt.fieldkey(core.CompilerOptions, name)]
	end
	local yes = 2 -- core.TSTrue
	-- Emit needs every file's checker in one process, and the other modes have their own output.
	if option('NoEmit') ~= yes or option('NoCheck') == yes or option('ListFilesOnly') == yes or option('SingleThreaded') == yes then
		return 1
	end
	if option('GenerateTrace') ~= '' or rt.len(tsoptions.ParsedCommandLine__ContentMappers(config)) > 0 then
		return 1
	end
	-- An explicit --checkers asks for that many independent checkers, as Go would run them.
	if option('Checkers') ~= nil then
		return 1
	end
	return math.min(native.cpucount(), 8, limit or math.huge)
end

-- Whether a copy of this process can run here: one is made that allocates, writes a file and
-- exits. Copying is cheap while the heap is small, and a system where a copy fails or hangs
-- (the Windows call is undocumented) is found out before anything depends on one.
local function copiesWork()
	local out = sformat('%s/tsc-lua-%d-probe.txt', tempDir(), native.getpid())
	io.stdout:flush()
	io.stderr:flush()
	local handle = native.fork()
	if handle == 0 then
		local ok = pcall(function()
			local filler = {}
			for i = 1, 1000 do
				filler[i] = { i }
			end
			local f = assert(io.open(out, 'wb'))
			f:write('copy ', #filler)
			f:close()
		end)
		native.exit(ok and 0 or 1)
	end
	if handle == nil then
		trace('this system cannot copy the process')
		return false
	end
	local code = native.wait(handle, 10000)
	if code == nil then
		native.kill(handle)
		native.wait(handle)
	end
	local f = io.open(out, 'rb')
	local wrote = f ~= nil and f:read('*a')
	if f ~= nil then
		f:close()
		os.remove(out)
	end
	if code ~= 0 or wrote ~= 'copy 1000' then
		trace('a copy of the process did not work (exit %s)', tostring(code))
		return false
	end
	return true
end

-- Called before a compilation that the process exits after. Nearly everything such a
-- compilation keeps it keeps until the end, so it collects only young objects, which is faster
-- and peaks lower than collecting everything. Returns whether it does: tests compile many
-- programs in one process, and a LuaJIT other than the one in vendor/ cannot.
function W.collectYoung(testing)
	return testing == nil and pcall(collectgarbage, 'young', nursery * 1024)
end

-- Called before a plain (not incremental, not watching) compilation builds its program.
function W.begin(config, testing)
	session = nil
	-- Copies share memory only while collections leave old objects alone.
	if not W.collectYoung(testing) then
		return
	end
	local count = processCount(config, testing)
	if count < 2 or not copiesWork() then
		return
	end
	-- One checker, which the copies of this process carry on from.
	local core, tsoptions = package.loaded['ts.core'], package.loaded['ts.tsoptions']
	local options = tsoptions.ParsedCommandLine__CompilerOptions(config)
	options[rt.fieldkey(core.CompilerOptions, 'Checkers')] = { v = 1 }
	session = { count = count, copies = {}, declaration = {}, remote = {}, globals = {}, counts = {} }
	-- Garbage freed before the copies are made leaves holes all over the heap, which every
	-- copy then fills, unsharing those pages. So nothing is collected while the program is
	-- built, unless the heap outgrows a quarter of the memory that was free.
	local available = native.memory()
	if available ~= nil then
		collectgarbage('young', math.max(nursery * 1024, available / 4 / 1024))
	end
end

-- Called when the compilation is over: copies whose results were never read are stopped.
function W.finish()
	local s = session
	session = nil
	if s == nil then
		return
	end
	for _, copy in pairs(s.copies) do
		if copy.handle ~= nil then
			native.kill(copy.handle)
			native.wait(copy.handle)
			os.remove(copy.out)
		end
	end
end

return W
