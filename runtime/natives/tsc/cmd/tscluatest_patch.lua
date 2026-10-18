-- Reporting for the tscluatest entry point: prints failures as they happen and a summary,
-- and writes JSON results when TSCLUA_TEST_RESULTS names a file.
local rt = require('rt')

local function jsonString(s)
	return '"' .. s:gsub('[%c"\\]', function(c)
		return string.format('\\u%04x', c:byte())
	end) .. '"'
end

return function(P)
	local testing = rt.import('go.testing')
	testing.__testing = true
	local verbose = os.getenv('TSCLUA_TEST_VERBOSE') ~= nil
	local started = os.clock()
	testing.__onResult = function(result)
		if result.status == 'fail' then
			io.stdout:write('FAIL ', result.name, '\n')
			if verbose then
				for _, m in ipairs(result.messages) do
					io.stdout:write('    ', (m:gsub('\n', '\n    ')), '\n')
				end
			end
		elseif verbose then
			io.stdout:write(result.status == 'pass' and 'ok   ' or 'skip ', result.name, '\n')
		end
		io.stdout:flush()
	end

	P.report = function()
		local counts = { pass = 0, fail = 0, skip = 0 }
		for _, r in ipairs(testing.__results) do
			counts[r.status] = counts[r.status] + 1
		end
		local total = counts.pass + counts.fail
		local pct = total > 0 and (counts.pass * 100.0 / total) or 0.0
		io.stdout:write(
			string.format(
				'\n%d passed, %d failed, %d skipped (%.2f%% passing) in %.1fs\n',
				counts.pass,
				counts.fail,
				counts.skip,
				pct,
				os.clock() - started
			)
		)
		local out = os.getenv('TSCLUA_TEST_RESULTS')
		if out and out ~= '' then
			local f = assert(io.open(out, 'wb'))
			f:write('[\n')
			for i, r in ipairs(testing.__results) do
				f:write('\t{"name": ', jsonString(r.name), ', "status": "', r.status, '"}', i < #testing.__results and ',\n' or '\n')
			end
			f:write(']\n')
			f:close()
		end
		if counts.fail > 0 then
			return 1
		end
		return 0
	end
end
