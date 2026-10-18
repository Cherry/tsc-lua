-- Native stub for internal/race: the Lua runtime has no race detector.
local rt = require('rt')
local P = rt.package('internal/race')

local function noop() end

P.Enabled = false
P.Acquire = noop
P.Release = noop
P.ReleaseMerge = noop
P.Disable = noop
P.Enable = noop
P.Read = noop
P.Write = noop
P.ReadRange = noop
P.WriteRange = noop
P.ReadPC = noop
P.WritePC = noop
P.ReadObjectPC = noop
P.WriteObjectPC = noop
P.Errors = function()
	return 0
end

return P
