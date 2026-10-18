-- Native encoding/json/internal/jsonopts: only the Options interface, which json.Options and
-- jsontext.Options alias (the transpiler resolves aliases to this package).
local rt = require('rt')
local P = rt.package('encoding/json/internal/jsonopts')

P.Options = rt.named('encoding/json/internal/jsonopts', 'jsonopts.Options', 'interface')
rt.define(P.Options, rt.ifaceOf({ 'JSONOptions' }))

return P
