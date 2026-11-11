-- A plain compilation is where type checking can be shared with copies of the process (see
-- ts/checkcopies.lua), which has to be decided before the program and its checkers are made.
-- An incremental one only gets the collector that suits a compilation the process exits after.
-- overrides: performCompilation performIncrementalCompilation
return function(P)
	local copies = require('ts.checkcopies')
	local performCompilation = P.performCompilation
	P.performCompilation = function(ctx, sys, config, reportDiagnostic, reportErrorSummary, extendedConfigCache, compileTimes, testing)
		copies.begin(config, testing)
		local result =
			performCompilation(ctx, sys, config, reportDiagnostic, reportErrorSummary, extendedConfigCache, compileTimes, testing)
		copies.finish()
		return result
	end
	local performIncrementalCompilation = P.performIncrementalCompilation
	P.performIncrementalCompilation = function(...)
		-- It takes the same parameters as performCompilation: testing is the eighth.
		local testing = select(8, ...)
		copies.collectYoung(testing)
		return performIncrementalCompilation(...)
	end
end
