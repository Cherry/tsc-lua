-- Terminal detection for the tsc-lua entry point.
local rt = require('rt')

return function(P)
	local os_ = rt.import('go.os')
	P.isTerminal = function(fd)
		local native = os_.__native
		if native then
			return native.isatty(fd)
		end
		return false
	end
	P.terminalWidth = function()
		local cols = tonumber(os.getenv('COLUMNS') or '')
		if cols then
			return math.floor(tonumber(cols) or 0)
		end
		if P.isTerminal(1) then
			return 80
		end
		return 0
	end
end
