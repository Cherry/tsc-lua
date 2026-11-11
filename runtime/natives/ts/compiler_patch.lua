-- Shares type checking with copies of the process (see ts/checkcopies.lua): the functions
-- below gather checker diagnostics, which a copy finds for the files it checks.
-- overrides: Program__collectCheckerDiagnosticsFromFiles checkerPool__GetGlobalDiagnostics Program__getDeclarationDiagnosticsForFile
-- overrides: Program__SymbolCount Program__TypeCount Program__InstantiationCount
return function(P)
	require('ts.checkcopies').install(P)
end
