-- Native encoding/json/v2: marshaling driven by the runtime type descriptors.
-- deps: encoding/json/jsontext encoding/json/internal/jsonopts errors io
local rt = require('rt')
local P = rt.package('encoding/json/v2')
local jt = rt.import('go.encoding.json.jsontext')
local errors = rt.import('go.errors')
local goio = rt.import('go.io')
local X = jt.__impl

local sbyte, ssub, sfind, schar, sgsub, slower = string.byte, string.sub, string.find, string.char, string.gsub, string.lower
local tconcat, tsort = table.concat, table.sort
local setmetatable, getmetatable, rawget, type, pairs, ipairs = setmetatable, getmetatable, rawget, type, pairs, ipairs
local typeof, unbox = rt.typeof, rt.unbox
local band, bor, lshift, rshift = bit.band, bit.bor, bit.lshift, bit.rshift
local LEN, NIL = rt.LEN, rt.NIL
local PKG = 'encoding/json/v2'

local throw, try = X.throw, X.try
local peek, readStr, readNumber, readLit, open, close = X.peek, X.readStr, X.readNumber, X.readLit, X.open, X.close
local readRaw, readString, nameRead, atName = X.readRaw, X.readString, X.nameRead, X.atName
local eput, epre, wstr, wscalar, wopen, wclose, eatName = X.eput, X.epre, X.wstr, X.wscalar, X.wopen, X.wclose, X.eatName
local fmtFloat, fmtUint, quote = X.fmtFloat, X.fmtUint, X.quote

---------------------------------------------------------------------------
-- Types

P.Options = jt.Options

local function iface(name, methods)
	local T = rt.named(PKG, name, 'interface')
	rt.define(T, rt.ifaceOf(methods))
	return T
end
P.Marshaler = iface('json.Marshaler', { 'MarshalJSON' })
P.MarshalerTo = iface('json.MarshalerTo', { 'MarshalJSONTo' })
P.Unmarshaler = iface('json.Unmarshaler', { 'UnmarshalJSON' })
P.UnmarshalerFrom = iface('json.UnmarshalerFrom', { 'UnmarshalJSONFrom' })

P.ErrUnknownName = errors.New('unknown object member name')
local errNonNilReference = errors.New('value must be passed as a non-nil pointer reference')
local errNilInterface = errors.New('cannot derive concrete type for nil interface with finite type set')
local errNoExportedFields = errors.New('Go struct has no exported fields')
local errOverflow = errors.New('value out of range')

P.SemanticError = rt.named(PKG, 'json.SemanticError', 'struct')
rt.defineStruct(P.SemanticError, {
	{ 'action', rt.string, 'action', 0 },
	{ 'ByteOffset', rt.int64, 'ByteOffset', 0 },
	{ 'JSONPointer', jt.Pointer, 'JSONPointer', 0 },
	{ 'JSONKind', jt.Kind, 'JSONKind', 0 },
	{ 'JSONValue', jt.Value, 'JSONValue', 0 },
	{ 'GoType', rt.any, 'GoType', 0 },
	{ 'Err', rt.error, 'Err', 0 },
})
local semMT = P.SemanticError.pmt

local kindPhrase = {
	[110] = ' JSON null',
	[102] = ' JSON boolean',
	[116] = ' JSON boolean',
	[34] = ' JSON string',
	[48] = ' JSON number',
	[123] = ' JSON object',
	[125] = ' JSON object',
	[91] = ' JSON array',
	[93] = ' JSON array',
}

local function semanticErrorString(e)
	local b = { 'json: cannot' }
	local prep
	if e.action == 'marshal' then
		b[#b + 1] = ' marshal'
		prep = ' from'
	elseif e.action == 'unmarshal' then
		b[#b + 1] = ' unmarshal'
		prep = ' into'
	else
		b[#b + 1] = ' handle'
		prep = ' with'
	end
	local kp = kindPhrase[e.JSONKind]
	if kp then
		b[#b + 1] = kp
	elseif e.action == '' then
		prep = ''
	end
	if e.goTypeStr then
		b[#b + 1] = prep .. ' Go ' .. e.goTypeStr
	end
	local err = e.Err
	if err == P.ErrUnknownName then
		local ptr = e.JSONPointer
		b[#b + 1] = ': ' .. err:_mError() .. ' ' .. jt.Pointer__LastToken(ptr)
		local parent = jt.Pointer__Parent(ptr)
		if parent ~= '' then
			b[#b + 1] = ' within ' .. string.format('%q', parent)
		end
		return tconcat(b)
	end
	if e.JSONPointer ~= '' then
		b[#b + 1] = ' within "' .. e.JSONPointer .. '"'
	elseif e.ByteOffset > 0 then
		b[#b + 1] = ' after offset ' .. e.ByteOffset
	end
	if err ~= nil then
		local s = err:_mError()
		if ssub(s, 1, 10) == 'jsontext: ' then
			s = ssub(s, 11)
		end
		b[#b + 1] = ': ' .. s
	end
	return tconcat(b)
end
P.SemanticError__Error = semanticErrorString
local function semanticErrorUnwrap(e)
	return e.Err
end
P.SemanticError__Unwrap = semanticErrorUnwrap
rt.methods(P.SemanticError, {}, { _mError = semanticErrorString, _mUnwrap = semanticErrorUnwrap })

local function semErr(d, action, T, kind, err)
	local ptr, off = '', 0
	if d then
		if d.s then
			ptr, off = X.decPointer(d), d.p - 1
		else
			off = d.written
		end
	end
	throw(setmetatable({
		action = action,
		ByteOffset = off,
		JSONPointer = ptr,
		JSONKind = kind or 0,
		JSONValue = nil,
		GoType = nil,
		Err = err,
		goTypeStr = T and T.str or nil,
	}, semMT))
end

local function unmarshalErr(d, T, kind, err)
	semErr(d, 'unmarshal', T, kind, err)
end

local function marshalErr(e, T, err)
	semErr(e, 'marshal', T, 0, err)
end

local function isPassThrough(err)
	local T = typeof(err)
	return T == rt.ptrTo(P.SemanticError) or T == rt.ptrTo(jt.SyntacticError) or err == goio.EOF
end

---------------------------------------------------------------------------
-- Options

local mkopt, setterKeys = X.mkopt, X.setterKeys
for _, name in ipairs({
	'StringifyNumbers',
	'Deterministic',
	'FormatNilSliceAsNull',
	'FormatNilMapAsNull',
	'OmitZeroStructFields',
	'MatchCaseInsensitiveNames',
	'RejectUnknownMembers',
}) do
	local f = function(v)
		return mkopt(name, v)
	end
	P[name] = f
	setterKeys[f] = name
end

function P.JoinOptions(srcs)
	local list = {}
	if srcs ~= nil then
		for i = 0, srcs[3] - 1 do
			list[#list + 1] = srcs[1][srcs[2] + i]
		end
	end
	return X.listopt(list)
end

function P.DefaultOptionsV2()
	return X.snapopt({
		AllowDuplicateNames = false,
		AllowInvalidUTF8 = false,
		Deterministic = false,
		FormatNilMapAsNull = false,
		FormatNilSliceAsNull = false,
		MatchCaseInsensitiveNames = false,
		OmitZeroStructFields = false,
		RejectUnknownMembers = false,
		StringifyNumbers = false,
		EscapeForHTML = false,
		EscapeForJS = false,
		Multiline = false,
		PreserveRawStrings = false,
		SpaceAfterColon = false,
		SpaceAfterComma = false,
	})
end

function P.GetOption(T, opts, setter)
	local key = setterKeys[setter]
	if key ~= nil and opts ~= nil then
		local o = {}
		X.applyOne(o, opts)
		local v = o[key]
		if v ~= nil then
			return v, true
		end
	end
	return T.zero(), false
end

function P.WithMarshalers(_)
	rt.panic('json.WithMarshalers is not supported by the Lua runtime')
end

function P.WithUnmarshalers(_)
	rt.panic('json.WithUnmarshalers is not supported by the Lua runtime')
end

---------------------------------------------------------------------------
-- Helpers

local function slotTarget(p)
	-- &x.f / &s[i] proxies expose the pointee as .v; plain struct pointers are the table itself.
	if rawget(p, '__tab') ~= nil and rawget(p, '__key') ~= nil then
		return p.v
	end
	-- Generic code takes &x of a type-parameter variable as a plain {v = x} box even for structs.
	if getmetatable(p) == nil and type(rawget(p, 'v')) == 'table' then
		return p.v
	end
	return p
end

local function methodFor(T, key)
	local k = T.kind
	if k == 'ptr' or k == 'interface' then
		return nil
	end
	local vs = T.mset
	if vs and vs[key] then
		return vs[key], false
	end
	local PT = T.ptr
	local ps = PT and PT.mset
	if ps and ps[key] then
		return ps[key], true
	end
	return nil
end

local function isExported(name)
	local c = sbyte(name, 1)
	if c and c >= 65 and c <= 90 then
		return true
	end
	if c and c >= 0x80 then
		local r = rt.decodeRune(name, 1)
		return rt.runestr(r) ~= slower(rt.runestr(r))
	end
	return false
end

-- lookupTag extracts the json tag value from a Go struct tag string.
local function lookupTag(tag)
	if tag == nil then
		return nil
	end
	local i = 1
	while true do
		local s, e, key = sfind(tag, '^%s*([^%s:"]+):"', i)
		if not s then
			return nil
		end
		local j = e + 1
		local buf = {}
		while true do
			local c = sbyte(tag, j)
			if c == nil then
				return nil
			elseif c == 92 then
				buf[#buf + 1] = ssub(tag, j + 1, j + 1)
				j = j + 2
			elseif c == 34 then
				break
			else
				buf[#buf + 1] = schar(c)
				j = j + 1
			end
		end
		if key == 'json' then
			return tconcat(buf)
		end
		i = j + 1
	end
end

local function parseFieldOptions(tag)
	local opt = {}
	if tag == nil or tag == '' then
		return opt
	end
	local name, rest = tag:match('^([^,]*)(.*)$')
	if name ~= '' then
		if sbyte(name, 1) == 39 and sbyte(name, #name) == 39 then
			name = ssub(name, 2, -2)
		end
		opt.name = name
	end
	for o in rest:gmatch(',([^,]*)') do
		if o == 'omitempty' then
			opt.omitempty = true
		elseif o == 'omitzero' then
			opt.omitzero = true
		elseif o == 'string' then
			opt.string = true
		elseif o == 'embed' or o == 'inline' then
			opt.embed = true
		elseif o == 'case:ignore' then
			opt.caseIgnore = true
		elseif o == 'case:strict' then
			opt.caseStrict = true
		elseif ssub(o, 1, 7) == 'format:' then
			opt.format = ssub(o, 8)
		end
	end
	return opt
end

local function foldName(s)
	return (sgsub(slower(s), '[-_]', ''))
end

---------------------------------------------------------------------------
-- Zero checks (reflect.Value.IsZero and IsZero methods)

local rzeroCache = setmetatable({}, { __mode = 'k' })
local function rzero(T)
	local f = rzeroCache[T]
	if f then
		return f
	end
	local k = T.kind
	if k == 'bool' then
		f = function(v)
			return v == false
		end
	elseif k == 'string' then
		f = function(v)
			return v == ''
		end
	elseif k == 'float32' or k == 'float64' then
		f = function(v)
			return v == 0 and 1 / v > 0
		end
	elseif rt.isIntKind(k) then
		f = function(v)
			return v == 0
		end
	elseif k == 'complex64' or k == 'complex128' then
		f = function(v)
			return v == nil or (v.re == 0 and v.im == 0)
		end
	elseif k == 'struct' then
		local leaves = rt.leaves(T)
		local fns = {}
		rzeroCache[T] = function(v)
			for i, leaf in ipairs(leaves) do
				local zf = fns[i]
				if not zf then
					zf = rzero(leaf[2])
					fns[i] = zf
				end
				if not zf(v[leaf[1]]) then
					return false
				end
			end
			return true
		end
		return rzeroCache[T]
	elseif k == 'array' then
		local n, E = T.len, T.elem
		f = function(v)
			local zf = rzero(E)
			for i = 1, n do
				if not zf(v[i]) then
					return false
				end
			end
			return true
		end
	else
		f = function(v)
			return v == nil
		end
	end
	rzeroCache[T] = f
	return f
end

local function zeroFn(T)
	local k = T.kind
	if k == 'interface' then
		for _, m in ipairs(T.methods) do
			if m == 'IsZero' then
				return function(v)
					if v == nil then
						return true
					end
					local DT = typeof(v)
					if DT and DT.kind == 'ptr' and unbox(v) == nil then
						return true
					end
					return v:_mIsZero()
				end
			end
		end
		return rzero(T)
	end
	if k == 'ptr' then
		local f = T.mset and T.mset._mIsZero
		if f then
			return function(v)
				return v == nil or f(v)
			end
		end
		return rzero(T)
	end
	local f, needPtr = methodFor(T, '_mIsZero')
	if f then
		if needPtr and k ~= 'struct' then
			return function(v)
				return f({ v = v })
			end
		end
		return f
	end
	return rzero(T)
end

local emptyKinds = { string = true, map = true, slice = true, array = true }

---------------------------------------------------------------------------
-- Struct field plans

local planCache = setmetatable({}, { __mode = 'k' })

local function structPlan(root)
	local plan = planCache[root]
	if plan then
		return plan
	end
	local all = {}
	local serr
	local queue = { { T = root, steps = {}, idx = {}, visit = true } }
	local seen = { [root] = true }
	local qi = 1
	while qi <= #queue do
		local qe = queue[qi]
		qi = qi + 1
		local hasAnyTag, hasAnyField = false, false
		local namesIndex = {}
		local fields = qe.T.fields or {}
		for i, f in ipairs(fields) do
			local key, FT, gname, flags, tag = f[1], f[2], f[3], f[4], f[5]
			local jtag = lookupTag(tag)
			if jtag ~= nil then
				hasAnyTag = true
			end
			local exported = isExported(gname)
			local anon = flags ~= 0
			if jtag ~= '-' and (exported or anon) and key ~= '_' then
				hasAnyField = true
				local opt = parseFieldOptions(jtag)
				local idx = { table.unpack(qe.idx) }
				idx[#idx + 1] = i
				local tf = FT
				if tf.kind == 'ptr' and not tf.named then
					tf = tf.elem
				end
				if (anon and opt.name == nil) or opt.embed then
					if tf.kind == 'struct' then
						local steps = qe.steps
						if flags ~= 2 then
							steps = { table.unpack(qe.steps) }
							steps[#steps + 1] = { key = key, T = FT }
						end
						if qe.visit then
							queue[#queue + 1] = { T = tf, steps = steps, idx = idx, visit = not seen[tf] }
						end
						seen[tf] = true
					elseif not serr then
						serr = {
							T = qe.T,
							err = errors.New(
								'embedded Go struct field ' .. gname .. ' of non-struct type must be explicitly given a JSON name'
							),
						}
					end
				elseif exported or tf.kind == 'struct' then
					local fname = opt.name or gname
					local prev = namesIndex[fname]
					if prev and not serr then
						serr = {
							T = qe.T,
							err = errors.New(
								'Go struct fields '
									.. prev
									.. ' and '
									.. gname
									.. ' conflict over JSON object name '
									.. string.format('%q', fname)
							),
						}
					end
					namesIndex[fname] = gname
					all[#all + 1] = {
						name = opt.name or gname,
						hasName = opt.name ~= nil,
						key = key,
						T = FT,
						flat = flags == 2 or f[6] == true,
						steps = qe.steps,
						idx = idx,
						omitzero = opt.omitzero,
						omitempty = opt.omitempty,
						caseIgnore = opt.caseIgnore,
						caseStrict = opt.caseStrict,
						order = #all + 1,
					}
				end
			end
		end
		if #fields > 0 and not hasAnyTag and not hasAnyField and not serr then
			serr = { T = qe.T, err = errNoExportedFields }
		end
	end

	-- Select the dominant field for each name (shallowest, then uniquely tagged).
	tsort(all, function(a, b)
		if a.name ~= b.name then
			return a.name < b.name
		end
		if #a.idx ~= #b.idx then
			return #a.idx < #b.idx
		end
		if a.hasName ~= b.hasName then
			return a.hasName
		end
		return a.order < b.order
	end)
	local list = {}
	local i = 1
	while i <= #all do
		local j = i
		while j < #all and all[j + 1].name == all[i].name do
			j = j + 1
		end
		local a, b = all[i], all[i + 1]
		if j == i or #a.idx ~= #b.idx or a.hasName ~= b.hasName then
			list[#list + 1] = a
		end
		i = j + 1
	end
	tsort(list, function(a, b)
		local ia, ib = a.idx, b.idx
		for k = 1, math.min(#ia, #ib) do
			if ia[k] ~= ib[k] then
				return ia[k] < ib[k]
			end
		end
		return #ia < #ib
	end)

	local byName, byFold = {}, {}
	local anyIgnore = false
	for _, f in ipairs(list) do
		f.qname = quote(f.name, { AllowInvalidUTF8 = true })
		byName[f.name] = f
		local fold = foldName(f.name)
		local l = byFold[fold]
		if not l then
			l = {}
			byFold[fold] = l
		end
		l[#l + 1] = f
		if f.caseIgnore then
			anyIgnore = true
		end
	end
	for _, l in pairs(byFold) do
		tsort(l, function(a, b)
			return a.order < b.order
		end)
	end
	plan = { list = list, byName = byName, byFold = byFold, anyIgnore = anyIgnore, err = serr }
	planCache[root] = plan
	return plan
end

---------------------------------------------------------------------------
-- Base64 for []byte

local b64chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
local b64enc, b64dec = {}, {}
for i = 1, 64 do
	b64enc[i - 1] = ssub(b64chars, i, i)
	b64dec[sbyte(b64chars, i)] = i - 1
end

local function base64Encode(s)
	local out = {}
	local n = #s
	for i = 1, n, 3 do
		local a, b, c = sbyte(s, i, i + 2)
		local v = a * 65536 + (b or 0) * 256 + (c or 0)
		out[#out + 1] = b64enc[band(rshift(v, 18), 63)]
			.. b64enc[band(rshift(v, 12), 63)]
			.. (b and b64enc[band(rshift(v, 6), 63)] or '=')
			.. (c and b64enc[band(v, 63)] or '=')
	end
	return tconcat(out)
end

local function base64Decode(s)
	s = sgsub(s, '[\r\n]', '')
	if #s % 4 ~= 0 then
		return nil
	end
	local out = {}
	for i = 1, #s, 4 do
		local c1, c2, c3, c4 = sbyte(s, i, i + 3)
		local v1, v2 = b64dec[c1], b64dec[c2]
		if not v1 or not v2 then
			return nil
		end
		if c4 == 61 then
			if i + 4 <= #s then
				return nil
			end
			if c3 == 61 then
				out[#out + 1] = schar(band(bor(lshift(v1, 2), rshift(v2, 4)), 255))
			else
				local v3 = b64dec[c3]
				if not v3 then
					return nil
				end
				out[#out + 1] = schar(band(bor(lshift(v1, 2), rshift(v2, 4)), 255), band(bor(lshift(v2, 4), rshift(v3, 2)), 255))
			end
		else
			local v3, v4 = b64dec[c3], b64dec[c4]
			if not v3 or not v4 then
				return nil
			end
			out[#out + 1] = schar(
				band(bor(lshift(v1, 2), rshift(v2, 4)), 255),
				band(bor(lshift(v2, 4), rshift(v3, 2)), 255),
				band(bor(lshift(v3, 6), v4), 255)
			)
		end
	end
	return tconcat(out)
end

---------------------------------------------------------------------------
-- Marshal

local mcache = setmetatable({}, { __mode = 'k' })
local buildM

local function getM(T)
	local f = mcache[T]
	if f then
		return f
	end
	local impl
	mcache[T] = function(e, v)
		return impl(e, v)
	end
	impl = buildM(T)
	mcache[T] = impl
	return impl
end

local tableKinds = { struct = true, array = true }

local function recvOf(T, v, needPtr)
	if needPtr and not tableKinds[T.kind] then
		return { v = v }
	end
	return v
end

local function methodError(e, T, err)
	if isPassThrough(err) then
		throw(err)
	end
	marshalErr(e, T, err)
end

local function wnumber(e, text)
	if eatName(e) then
		wstr(e, text)
	else
		wscalar(e, text)
	end
end

local unsignedKinds = { uint = true, uint64 = true, uintptr = true }

local function mapKeyName(K)
	local k = K.kind
	local f, needPtr = methodFor(K, '_mMarshalText')
	if f then
		return function(e, key)
			local b, err = f(recvOf(K, key, needPtr))
			if err ~= nil then
				methodError(e, K, err)
			end
			return rt.bytestr(b)
		end
	end
	if k == 'string' then
		return function(_, key)
			return key
		end
	elseif unsignedKinds[k] then
		return function(_, key)
			return fmtUint(key)
		end
	elseif rt.isIntKind(k) then
		if k == 'uint32' then
			return function(_, key)
				return rt.itoa(key % 4294967296)
			end
		end
		return function(_, key)
			return rt.itoa(key)
		end
	elseif k == 'float32' or k == 'float64' then
		local bits = k == 'float32' and 32 or 64
		return function(e, key)
			if key ~= key or key == math.huge or key == -math.huge then
				marshalErr(e, K, errors.New('unsupported value: ' .. tostring(key)))
			end
			return fmtFloat(key, bits)
		end
	end
	return function(e, _)
		marshalErr(e, K, jt.ErrNonStringName)
	end
end

local function marshalStruct(T)
	local plan
	return function(e, v)
		if not plan then
			plan = structPlan(T)
			for _, f in ipairs(plan.list) do
				f.m = getM(f.T)
				f.isZero = zeroFn(f.T)
				local hasMethods = f.T.kind ~= 'ptr'
					and f.T.kind ~= 'interface'
					and (methodFor(f.T, '_mMarshalJSONTo') or methodFor(f.T, '_mMarshalJSON') or methodFor(f.T, '_mMarshalText'))
				local k = f.T.kind
				if emptyKinds[k] and not hasMethods then
					f.emptyLen = true
				end
				if k == 'ptr' or k == 'interface' then
					f.emptyNil = true
				end
			end
		end
		if plan.err then
			marshalErr(e, plan.err.T, plan.err.err)
		end
		wopen(e, 123)
		local depth = e.depth
		local omitAllZero = e.o.OmitZeroStructFields
		local buf = e.buf
		for _, f in ipairs(plan.list) do
			local tbl = v
			for _, st in ipairs(f.steps) do
				tbl = tbl[st.key]
				if tbl == nil then
					break
				end
			end
			if tbl ~= nil then
				local fv
				if f.flat then
					fv = tbl
				else
					fv = tbl[f.key]
				end
				local skip = (f.omitzero or omitAllZero) and f.isZero(fv)
				local slow = false
				if not skip and f.omitempty then
					if f.emptyLen then
						local k = f.T.kind
						if k == 'string' then
							skip = fv == ''
						elseif k == 'array' then
							skip = f.T.len == 0
						elseif k == 'map' then
							skip = fv == nil or fv[LEN] == 0
						else
							skip = fv == nil or fv[3] == 0
						end
					elseif f.emptyNil and fv == nil then
						skip = true
					else
						slow = true
					end
				end
				if not skip then
					local mark, cnt = e.bn, e.fn[depth]
					if cnt > 0 then
						eput(e, e.comma)
					end
					if e.ml then
						eput(e, X.nlindent(e, depth))
					end
					eput(e, f.qname)
					X.ename(e, f.name, false)
					local vstart = e.bn + 2
					f.m(e, fv)
					if slow then
						buf = e.buf
						local s = tconcat(buf, '', vstart, e.bn)
						if s == 'null' or s == '""' or s == '{}' or s == '[]' then
							for i = mark + 1, e.bn do
								buf[i] = nil
							end
							e.bn = mark
							e.fn[depth] = cnt
						end
					end
				end
			end
		end
		wclose(e, 125)
	end
end

function buildM(T)
	local k = T.kind
	if k ~= 'ptr' and k ~= 'interface' then
		local f, needPtr = methodFor(T, '_mMarshalJSONTo')
		if f then
			return function(e, v)
				local depth = e.depth
				local err = f(recvOf(T, v, needPtr), e)
				if err ~= nil then
					methodError(e, T, err)
				end
				if e.depth ~= depth then
					marshalErr(e, T, errors.New('must write exactly one JSON value'))
				end
			end
		end
		f, needPtr = methodFor(T, '_mMarshalJSON')
		if f then
			return function(e, v)
				local b, err = f(recvOf(T, v, needPtr))
				if err ~= nil then
					methodError(e, T, err)
				end
				local raw = rt.bytestr(b)
				if raw == 'null' or raw == 'true' or raw == 'false' then
					wscalar(e, raw)
				else
					X.writeRaw(e, raw)
				end
			end
		end
		f, needPtr = methodFor(T, '_mAppendText')
		if f then
			return function(e, v)
				local b, err = f(recvOf(T, v, needPtr), nil)
				if err ~= nil then
					methodError(e, T, err)
				end
				wstr(e, rt.bytestr(b))
			end
		end
		f, needPtr = methodFor(T, '_mMarshalText')
		if f then
			return function(e, v)
				local b, err = f(recvOf(T, v, needPtr))
				if err ~= nil then
					methodError(e, T, err)
				end
				wstr(e, rt.bytestr(b))
			end
		end
	end
	if k == 'bool' then
		return function(e, v)
			if eatName(e) then
				marshalErr(e, T, jt.ErrNonStringName)
			end
			wscalar(e, v and 'true' or 'false')
		end
	elseif k == 'string' then
		return wstr
	elseif unsignedKinds[k] then
		return function(e, v)
			wnumber(e, fmtUint(v))
		end
	elseif rt.isIntKind(k) then
		if k == 'uint32' then
			-- uint32 values are held as their signed 32-bit pattern.
			return function(e, v)
				wnumber(e, rt.itoa(v % 4294967296))
			end
		end
		return function(e, v)
			wnumber(e, rt.itoa(v))
		end
	elseif k == 'float32' or k == 'float64' then
		local bits = k == 'float32' and 32 or 64
		return function(e, v)
			if v ~= v or v == math.huge or v == -math.huge then
				marshalErr(e, T, errors.New('unsupported value: ' .. (v ~= v and 'NaN' or (v > 0 and '+Inf' or '-Inf'))))
			end
			wnumber(e, fmtFloat(v, bits))
		end
	elseif k == 'slice' then
		local E = T.elem
		if E == rt.uint8 then
			return function(e, v)
				if v == nil and e.o.FormatNilSliceAsNull then
					wscalar(e, 'null')
					return
				end
				wstr(e, base64Encode(rt.bytestr(v)))
			end
		end
		local mE = getM(E)
		return function(e, v)
			if v == nil then
				if e.o.FormatNilSliceAsNull then
					wscalar(e, 'null')
					return
				end
				wopen(e, 91)
				wclose(e, 93)
				return
			end
			wopen(e, 91)
			local a, o = v[1], v[2]
			for i = o, o + v[3] - 1 do
				mE(e, a[i])
			end
			wclose(e, 93)
		end
	elseif k == 'array' then
		local E, n = T.elem, T.len
		if E == rt.uint8 then
			return function(e, v)
				wstr(e, base64Encode(schar(table.unpack(v, 1, n))))
			end
		end
		local mE = getM(E)
		return function(e, v)
			wopen(e, 91)
			for i = 1, n do
				mE(e, v[i])
			end
			wclose(e, 93)
		end
	elseif k == 'map' then
		local mV = getM(T.elem)
		local keyName = mapKeyName(T.key)
		return function(e, v)
			if v == nil and e.o.FormatNilMapAsNull then
				wscalar(e, 'null')
				return
			end
			wopen(e, 123)
			if v ~= nil and v[LEN] > 0 then
				local names, vals = {}, {}
				local order = {}
				local n = 0
				for key, val in rt.mrange(v, T) do
					n = n + 1
					local name = keyName(e, key)
					names[n] = name
					vals[name] = val
					order[n] = name
				end
				if n > 1 then
					tsort(order)
				end
				for i = 1, n do
					local name = order[i]
					local q, err = quote(name, e.o)
					if q == nil then
						X.encErr(e, err)
					end
					epre(e, true)
					eput(e, q)
					X.ename(e, name, false)
					if i > 1 and order[i - 1] == name and e.dup then
						X.encErr(e, jt.ErrDuplicateName)
					end
					mV(e, vals[name])
				end
			end
			wclose(e, 125)
		end
	elseif k == 'struct' then
		return marshalStruct(T)
	elseif k == 'ptr' then
		local E = T.elem
		local mE = getM(E)
		if tableKinds[E.kind] then
			return function(e, v)
				if v == nil then
					wscalar(e, 'null')
				else
					mE(e, slotTarget(v))
				end
			end
		end
		return function(e, v)
			if v == nil then
				wscalar(e, 'null')
			else
				mE(e, v.v)
			end
		end
	elseif k == 'interface' then
		return function(e, v)
			if v == nil then
				wscalar(e, 'null')
				return
			end
			getM(typeof(v))(e, unbox(v))
		end
	end
	return function(e, _)
		marshalErr(e, T, nil)
	end
end

local function marshalAny(e, v)
	if v == nil then
		wscalar(e, 'null')
		return
	end
	local T = typeof(v)
	getM(T)(e, unbox(v))
end

local function encOpts(opts)
	return X.applyOpts({}, opts)
end

function P.Marshal(inv, opts)
	local e = X.newEncoder(encOpts(opts), nil)
	e.omitNL = true
	local err = try(marshalAny, e, inv)
	local out = rt.strbytes(tconcat(e.buf, '', 1, e.bn))
	return out, err
end

function P.MarshalWrite(w, inv, opts)
	local e = X.newEncoder(encOpts(opts), w)
	e.omitNL = true
	return (try(marshalAny, e, inv))
end

local function withOptions(x, opts, cfg, f, ...)
	if opts == nil or opts[3] == 0 then
		return f(...)
	end
	local saved = x.o
	local o = {}
	for k, v in pairs(saved) do
		o[k] = v
	end
	x.o = X.applyOpts(o, opts)
	cfg(x)
	local r = { pcall(f, ...) }
	x.o = saved
	cfg(x)
	if not r[1] then
		error(r[2], 0)
	end
	return table.unpack(r, 2)
end

function P.MarshalEncode(enc, inv, opts)
	return (withOptions(enc, opts, X.econfig, try, marshalAny, enc, inv))
end

---------------------------------------------------------------------------
-- Unmarshal

local ucache = setmetatable({}, { __mode = 'k' })
local buildU

local function getU(T)
	local f = ucache[T]
	if f then
		return f
	end
	local impl
	ucache[T] = function(d, v)
		return impl(d, v)
	end
	impl = buildU(T)
	ucache[T] = impl
	return impl
end

local function readName(d)
	local p = d.p
	local name, np = readString(d, p)
	d.p = np
	d.pk = nil
	nameRead(d, name, p, false)
	return name, p
end

local function unmarshalMethodError(d, T, err)
	if isPassThrough(err) then
		throw(err)
	end
	unmarshalErr(d, T, 0, err)
end

local intRange = {
	int8 = { -128, 127 },
	int16 = { -32768, 32767 },
	int32 = { -2147483648, 2147483647 },
	uint8 = { 0, 255 },
	uint16 = { 0, 65535 },
	uint32 = { 0, 4294967295 },
}

local function parseIntText(d, T, text, kind)
	local neg = sbyte(text, 1) == 45
	local digits = neg and ssub(text, 2) or text
	if sfind(digits, '[^%d]') or digits == '' then
		unmarshalErr(d, T, kind, errors.New('cannot parse ' .. string.format('%q', text) .. ' as an integer'))
	end
	-- u is a uint64 FFI integer; the result takes T's representation.
	local u, over = X.parseUintDigits(digits)
	local k = T.kind
	if unsignedKinds[k] then
		if over or (neg and u ~= 0) then
			unmarshalErr(d, T, kind, errOverflow)
		end
		return rt.convnum(rt.uint64, T, u)
	end
	if over or u > (neg and 9223372036854775808ULL or 9223372036854775807ULL) then
		unmarshalErr(d, T, kind, errOverflow)
	end
	local v = rt.I64(u)
	if neg then
		v = -v
	end
	local r = intRange[k]
	if r and (v < r[1] or v > r[2]) then
		unmarshalErr(d, T, kind, errOverflow)
	end
	return rt.convnum(rt.int64, T, v)
end

local function validNumber(s)
	return sfind(s, '^-?%d+%.?%d*[eE]?[-+]?%d*$') ~= nil and sfind(s, '^-?0%d') == nil
end

local function numberText(d, T, allowString)
	local k = peek(d)
	if k == 48 then
		return readNumber(d), k
	elseif k == 110 then
		readLit(d, k)
		return nil, k
	elseif k == 34 and (allowString or atName(d) or d.o.StringifyNumbers) then
		local s = readStr(d)
		if not validNumber(s) then
			unmarshalErr(d, T, k, errors.New('cannot parse ' .. string.format('%q', s) .. ' as a number'))
		end
		return s, k
	end
	unmarshalErr(d, T, k, nil)
end

local function anyValue(d)
	local k = peek(d)
	if k == 34 then
		return readStr(d)
	elseif k == 48 then
		local f = tonumber(readNumber(d))
		if f == math.huge or f == -math.huge then
			unmarshalErr(d, rt.float64, k, errOverflow)
		end
		-- A raw number in an interface is an int; float64 values are boxed.
		return rt.box(rt.float64, f)
	elseif k == 116 then
		readLit(d, k)
		return true
	elseif k == 102 then
		readLit(d, k)
		return false
	elseif k == 110 then
		readLit(d, k)
		return nil
	elseif k == 123 then
		open(d, k)
		local m = rt.mkmap()
		local n = 0
		local check = d.dup
		while true do
			k = peek(d)
			if k == 125 then
				break
			end
			local p = d.p
			local name, np = readString(d, p)
			d.p = np
			d.pk = nil
			nameRead(d, name, p, false)
			local val = anyValue(d)
			if val == nil then
				val = NIL
			end
			if m[name] == nil then
				n = n + 1
			elseif check then
				X.dupName(d, p, name)
			end
			m[name] = val
		end
		m[LEN] = n
		close(d)
		return rt.mapOf(rt.string, rt.any).box(m)
	end
	open(d, k)
	local a, n = {}, 0
	while peek(d) ~= 93 do
		n = n + 1
		a[n] = anyValue(d)
	end
	close(d)
	return rt.sliceOf(rt.any).box({ a, 1, n, n })
end

local function mapKeyParser(K)
	local f, needPtr = methodFor(K, '_mUnmarshalText')
	if f then
		return function(d, name)
			local recv = K.kind == 'struct' and K.zero() or { v = K.zero() }
			if not needPtr and K.kind ~= 'struct' then
				recv = recv.v
			end
			local err = f(recv, rt.strbytes(name))
			if err ~= nil then
				unmarshalMethodError(d, K, err)
			end
			if K.kind == 'struct' then
				return recv
			end
			return recv.v
		end
	end
	local k = K.kind
	if k == 'string' then
		return function(_, name)
			return name
		end
	elseif rt.isIntKind(k) then
		return function(d, name)
			return parseIntText(d, K, name, 34)
		end
	elseif k == 'float32' or k == 'float64' then
		return function(d, name)
			if not validNumber(name) then
				unmarshalErr(d, K, 34, nil)
			end
			local f2 = tonumber(name) + 0.0
			if k == 'float32' then
				f2 = rt.f32(f2)
			end
			return f2
		end
	end
	return function(d, _)
		unmarshalErr(d, K, 34, nil)
	end
end

local mergeKinds = { struct = true, ptr = true, map = true, interface = true }

local function unmarshalStruct(T)
	local plan
	return function(d, v)
		if not plan then
			plan = structPlan(T)
			for _, f in ipairs(plan.list) do
				f.u = getU(f.T)
			end
		end
		local k = peek(d)
		if v == nil then
			v = T.zero()
		end
		if k == 110 then
			readLit(d, k)
			T.copy(v, T.zero())
			return v
		end
		if k ~= 123 then
			unmarshalErr(d, T, k, nil)
		end
		if plan.err then
			unmarshalErr(d, plan.err.T, k, plan.err.err)
		end
		open(d, k)
		local o = d.o
		local seen = d.dup and {} or nil
		local byName = plan.byName
		local fold = plan.anyIgnore or o.MatchCaseInsensitiveNames
		while true do
			k = peek(d)
			if k == 125 then
				break
			end
			local name, p = readName(d)
			local f = byName[name]
			if f == nil and fold then
				local cands = plan.byFold[foldName(name)]
				if cands then
					for _, c in ipairs(cands) do
						if c.caseIgnore or (o.MatchCaseInsensitiveNames and not c.caseStrict) then
							f = c
							break
						end
					end
				end
			end
			if f then
				if seen then
					if seen[f] then
						X.dupName(d, p, name)
					end
					seen[f] = true
				end
				local tbl = v
				for _, st in ipairs(f.steps) do
					local nxt = tbl[st.key]
					if nxt == nil then
						nxt = st.T.elem.zero()
						tbl[st.key] = nxt
					end
					tbl = nxt
				end
				if f.flat then
					f.u(d, tbl)
				else
					tbl[f.key] = f.u(d, tbl[f.key])
				end
			else
				if o.RejectUnknownMembers then
					unmarshalErr(d, T, 0, P.ErrUnknownName)
				end
				if seen then
					if seen[name] then
						X.dupName(d, p, name)
					end
					seen[name] = true
				end
				readRaw(d)
			end
		end
		close(d)
		return v
	end
end

function buildU(T)
	local k = T.kind
	if k ~= 'ptr' and k ~= 'interface' then
		local isStruct = tableKinds[k]
		local f, needPtr = methodFor(T, '_mUnmarshalJSONFrom')
		local kind = 'from'
		if not f then
			f, needPtr = methodFor(T, '_mUnmarshalJSON')
			kind = 'json'
		end
		if not f then
			f, needPtr = methodFor(T, '_mUnmarshalText')
			kind = 'text'
		end
		if f then
			return function(d, v)
				local recv, box
				if isStruct then
					recv = v or T.zero()
				elseif needPtr then
					box = { v = v }
					recv = box
				else
					recv = v
				end
				local err
				if kind == 'from' then
					err = f(recv, d)
				elseif kind == 'json' then
					err = f(recv, rt.strbytes(readRaw(d)))
				else
					local pk = peek(d)
					if pk == 110 then
						readLit(d, pk)
						if isStruct then
							T.copy(recv, T.zero())
							return recv
						end
						return T.zero()
					end
					if pk ~= 34 then
						unmarshalErr(d, T, pk, errors.New('JSON value must be string type'))
					end
					err = f(recv, rt.strbytes(readStr(d)))
				end
				if err ~= nil then
					unmarshalMethodError(d, T, err)
				end
				if isStruct then
					return recv
				elseif box then
					return box.v
				end
				return v
			end
		end
	end
	if k == 'bool' then
		return function(d, _)
			local pk = peek(d)
			if pk == 116 or pk == 102 or pk == 110 then
				readLit(d, pk)
				return pk == 116
			end
			unmarshalErr(d, T, pk, nil)
		end
	elseif k == 'string' then
		return function(d, _)
			local pk = peek(d)
			if pk == 34 then
				return readStr(d)
			elseif pk == 110 then
				readLit(d, pk)
				return ''
			end
			unmarshalErr(d, T, pk, nil)
		end
	elseif rt.isIntKind(k) then
		return function(d, _)
			local text, pk = numberText(d, T, false)
			if text == nil then
				return T.zero()
			end
			return parseIntText(d, T, text, pk)
		end
	elseif k == 'float32' or k == 'float64' then
		return function(d, _)
			local text, pk = numberText(d, T, false)
			if text == nil then
				return 0.0
			end
			local f = tonumber(text) + 0.0
			if k == 'float32' then
				f = rt.f32(f)
			end
			if f == math.huge or f == -math.huge then
				unmarshalErr(d, T, pk, errOverflow)
			end
			return f
		end
	elseif k == 'slice' then
		local E = T.elem
		if E == rt.uint8 then
			return function(d, _)
				local pk = peek(d)
				if pk == 110 then
					readLit(d, pk)
					return nil
				elseif pk ~= 34 then
					unmarshalErr(d, T, pk, nil)
				end
				local raw = base64Decode(readStr(d))
				if raw == nil then
					unmarshalErr(d, T, pk, errors.New('illegal base64 data'))
				end
				return rt.strbytes(raw)
			end
		end
		local uE, zero = getU(E), E.zero
		return function(d, _)
			local pk = peek(d)
			if pk == 110 then
				readLit(d, pk)
				return nil
			elseif pk ~= 91 then
				unmarshalErr(d, T, pk, nil)
			end
			open(d, pk)
			local a, n = {}, 0
			while peek(d) ~= 93 do
				n = n + 1
				a[n] = uE(d, zero())
			end
			close(d)
			return { a, 1, n, n }
		end
	elseif k == 'array' then
		local E, n = T.elem, T.len
		local uE = getU(E)
		return function(d, _)
			local pk = peek(d)
			local arr = T.zero()
			if pk == 110 then
				readLit(d, pk)
				return arr
			elseif E == rt.uint8 and pk == 34 then
				local raw = base64Decode(readStr(d))
				if raw == nil or #raw ~= n then
					unmarshalErr(d, T, pk, errors.New('decoded base64 length does not match array length'))
				end
				for i = 1, n do
					arr[i] = sbyte(raw, i)
				end
				return arr
			elseif pk ~= 91 then
				unmarshalErr(d, T, pk, nil)
			end
			open(d, pk)
			local i = 0
			while peek(d) ~= 93 do
				i = i + 1
				if i > n then
					unmarshalErr(d, T, 91, errors.New('too many array elements'))
				end
				arr[i] = uE(d, arr[i])
			end
			close(d)
			if i < n then
				unmarshalErr(d, T, 91, errors.New('too few array elements'))
			end
			return arr
		end
	elseif k == 'map' then
		local K, V = T.key, T.elem
		local uV = getU(V)
		local parseKey = mapKeyParser(K)
		local merge = mergeKinds[V.kind]
		local direct = K.kind == 'string' and not methodFor(K, '_mUnmarshalText')
		local venc = V.venc
		return function(d, v)
			local pk = peek(d)
			if pk == 110 then
				readLit(d, pk)
				return nil
			elseif pk ~= 123 then
				unmarshalErr(d, T, pk, nil)
			end
			open(d, pk)
			local m = v or rt.mkmap()
			while true do
				pk = peek(d)
				if pk == 125 then
					break
				end
				local p = d.p
				local name, np = readString(d, p)
				d.p = np
				d.pk = nil
				nameRead(d, name, p, true)
				if direct then
					local old = m[name]
					local cur
					if old == nil or not merge then
						cur = V.zero()
					elseif old == NIL or (old == false and venc == 'nilable') then
						cur = nil
					else
						cur = old
					end
					local nv = uV(d, cur)
					if old == nil then
						m[LEN] = m[LEN] + 1
					end
					if nv == nil then
						if venc == 'iface' then
							nv = NIL
						else
							nv = false
						end
					end
					m[name] = nv
				else
					local key = parseKey(d, name)
					local cur
					if merge then
						cur = rt.mget2(m, key, T)
					else
						cur = V.zero()
					end
					rt.msetp(m, key, uV(d, cur), T)
				end
			end
			close(d)
			return m
		end
	elseif k == 'struct' then
		return unmarshalStruct(T)
	elseif k == 'ptr' then
		local E = T.elem
		local uE = getU(E)
		if tableKinds[E.kind] then
			return function(d, v)
				local pk = peek(d)
				if pk == 110 then
					readLit(d, pk)
					return nil
				end
				if v == nil then
					return uE(d, E.zero())
				end
				local t = slotTarget(v)
				local r = uE(d, t)
				if r ~= t then
					E.copy(t, r)
				end
				return v
			end
		end
		return function(d, v)
			local pk = peek(d)
			if pk == 110 then
				readLit(d, pk)
				return nil
			end
			local b = v or { v = E.zero() }
			b.v = uE(d, b.v)
			return b
		end
	elseif k == 'interface' then
		local isAny = #T.methods == 0
		return function(d, v)
			local pk = peek(d)
			if pk == 110 then
				readLit(d, pk)
				return nil
			end
			if v == nil then
				if not isAny then
					unmarshalErr(d, T, pk, errNilInterface)
				end
				return anyValue(d)
			end
			local DT = typeof(v)
			local inner = unbox(v)
			if DT.kind == 'ptr' and inner ~= nil then
				getU(DT)(d, inner)
				return v
			end
			if DT.kind == 'struct' or DT.kind == 'array' then
				inner = DT.clone(inner)
			end
			return DT.box(getU(DT)(d, inner))
		end
	end
	return function(d, _)
		unmarshalErr(d, T, peek(d), nil)
	end
end

local function unmarshalInto(d, out)
	local T = typeof(out)
	local p = unbox(out)
	if T == nil or T.kind ~= 'ptr' or p == nil then
		semErr(nil, 'unmarshal', T, 0, errNonNilReference)
	end
	local E = T.elem
	local u = getU(E)
	if tableKinds[E.kind] then
		local t = slotTarget(p)
		local r = u(d, t)
		if r ~= t then
			E.copy(t, r)
		end
	else
		p.v = u(d, p.v)
	end
end

local function unmarshalAll(d, out)
	unmarshalInto(d, out)
	X.finish(d)
end

local function topLevel(d, out)
	local err = try(unmarshalAll, d, out)
	if err == goio.EOF then
		err = setmetatable({ ByteOffset = #d.s, JSONPointer = '', Err = goio.ErrUnexpectedEOF }, jt.SyntacticError.pmt)
	end
	return err
end

function P.Unmarshal(inb, out, opts)
	local d = X.newDecoder(rt.bytestr(inb), X.applyOpts({}, opts))
	return topLevel(d, out)
end

function P.UnmarshalRead(r, out, opts)
	local b, rerr = goio.ReadAll(r)
	if rerr ~= nil then
		return rerr
	end
	local d = X.newDecoder(rt.bytestr(b), X.applyOpts({}, opts))
	return topLevel(d, out)
end

function P.UnmarshalDecode(dec, out, opts)
	return (withOptions(dec, opts, X.dconfig, try, unmarshalInto, dec, out))
end

return P
