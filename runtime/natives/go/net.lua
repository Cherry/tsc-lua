-- Native net: only in-memory pipes (used by the test harness's fake content mapper
-- processes). Real networking belongs to the language server and API modes, which
-- tsc-lua does not include.
-- deps: errors io
local rt = require('rt')
local P = rt.package('net')
local errors = rt.import('go.errors')
local io_ = rt.import('go.io')

local unsupported = errors.New('tsc-lua: networking is not supported')

P.ErrClosed = errors.New('use of closed network connection')

P.Conn = rt.named('net', 'net.Conn', 'interface')
rt.define(P.Conn, rt.ifaceOf({ 'Close', 'LocalAddr', 'Read', 'RemoteAddr', 'SetDeadline', 'SetReadDeadline', 'SetWriteDeadline', 'Write' }))
P.Listener = rt.named('net', 'net.Listener', 'interface')
rt.define(P.Listener, rt.ifaceOf({ 'Accept', 'Addr', 'Close' }))
P.Addr = rt.named('net', 'net.Addr', 'interface')
rt.define(P.Addr, rt.ifaceOf({ 'Network', 'String' }))

local pipeAddr = rt.named('net', 'net.pipeAddr', 'struct')
rt.defineStruct(pipeAddr, {})
rt.methods(pipeAddr, {
	_mNetwork = function()
		return 'pipe'
	end,
	_mString = function()
		return 'pipe'
	end,
}, {
	_mNetwork = function()
		return 'pipe'
	end,
	_mString = function()
		return 'pipe'
	end,
})

-- A pipe end reads from its own inbox and writes to its peer's inbox. State lives in a
-- declared field so struct copies share it.
local pipe = rt.named('net', 'net.pipe', 'struct')
rt.defineStruct(pipe, { { 'state', rt.any, 'state', 0 } })

local function wakeReaders(inbox)
	local waiters = inbox.waiters
	inbox.waiters = {}
	for _, g in ipairs(waiters) do
		rt.ready(g)
	end
end

local function pipeRead(p, b)
	local st = p.state
	local inbox = st.inbox
	while #inbox.chunks == 0 do
		if st.closed then
			return 0, P.ErrClosed
		end
		if inbox.eof then
			return 0, io_.EOF
		end
		inbox.waiters[#inbox.waiters + 1] = rt.current()
		rt.park()
	end
	if b == nil or b[3] == 0 then
		return 0, nil
	end
	local chunk = inbox.chunks[1]
	local n = math.min(#chunk, b[3])
	local a, o = b[1], b[2]
	for i = 1, n do
		a[o + i - 1] = string.byte(chunk, i)
	end
	if n == #chunk then
		table.remove(inbox.chunks, 1)
	else
		inbox.chunks[1] = chunk:sub(n + 1)
	end
	return n, nil
end

local function pipeWrite(p, b)
	local st = p.state
	if st.closed or st.outbox.eof then
		return 0, io_.ErrClosedPipe
	end
	local s = rt.bytestr(b)
	if #s > 0 then
		local outbox = st.outbox
		outbox.chunks[#outbox.chunks + 1] = s
		wakeReaders(outbox)
	end
	return #s, nil
end

local function pipeClose(p)
	local st = p.state
	if st.closed then
		return nil
	end
	st.closed = true
	-- Readers on either side see EOF/closed.
	st.outbox.eof = true
	wakeReaders(st.outbox)
	wakeReaders(st.inbox)
	return nil
end

local function noDeadline()
	return nil
end

local function addr()
	return setmetatable({}, pipeAddr.pmt)
end

P.pipe__Read = pipeRead
P.pipe__Write = pipeWrite
P.pipe__Close = pipeClose
rt.methods(pipe, {}, {
	_mRead = pipeRead,
	_mWrite = pipeWrite,
	_mClose = pipeClose,
	_mLocalAddr = addr,
	_mRemoteAddr = addr,
	_mSetDeadline = noDeadline,
	_mSetReadDeadline = noDeadline,
	_mSetWriteDeadline = noDeadline,
})

function P.Pipe()
	local a = { chunks = {}, waiters = {}, eof = false }
	local b = { chunks = {}, waiters = {}, eof = false }
	local left = setmetatable({ state = { inbox = a, outbox = b, closed = false } }, pipe.pmt)
	local right = setmetatable({ state = { inbox = b, outbox = a, closed = false } }, pipe.pmt)
	return left, right
end

function P.Listen()
	return nil, unsupported
end

function P.Dial()
	return nil, unsupported
end

function P.DialTimeout()
	return nil, unsupported
end

return P
