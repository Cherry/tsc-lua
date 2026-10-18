-- The upstream repo root is located through Go source file paths, which do not exist in the
-- Lua build; tools set TSCLUA_REPO_ROOT to upstream/TypeScript/tsc instead.
return function(P)
	local function root()
		local dir = os.getenv('TSCLUA_REPO_ROOT')
		if not dir or dir == '' then
			error('TSCLUA_REPO_ROOT must point at upstream/TypeScript/tsc')
		end
		return (dir:gsub('\\', '/'):gsub('/$', ''))
	end
	P.RootPath = root
	P.TestDataPath = function()
		return root() .. '/testdata'
	end
end
