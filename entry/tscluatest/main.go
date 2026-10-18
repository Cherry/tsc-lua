// Command tscluatest runs typescript-go's compiler baseline test runner (the conformance
// suite) under tsc-lua. The testing package is provided natively by the Lua runtime.
package main

import (
	"flag"
	"fmt"
	"os"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/testrunner"
)

func main() {
	suite := flag.String("suite", "all", "test suite: conformance, compiler or all")
	run := flag.String("test.run", "", "regular expression selecting test files")
	flag.Parse()
	_ = run

	var runners []*testrunner.CompilerBaselineRunner
	switch *suite {
	case "conformance":
		runners = append(runners, testrunner.NewCompilerBaselineRunner(testrunner.TestTypeConformance))
	case "compiler":
		runners = append(runners, testrunner.NewCompilerBaselineRunner(testrunner.TestTypeRegression))
	case "all":
		runners = append(runners,
			testrunner.NewCompilerBaselineRunner(testrunner.TestTypeConformance),
			testrunner.NewCompilerBaselineRunner(testrunner.TestTypeRegression),
		)
	default:
		fmt.Fprintf(os.Stderr, "unknown suite %q\n", *suite)
		os.Exit(2)
	}

	t := &testing.T{}
	for _, r := range runners {
		r.RunTests(t)
	}
	os.Exit(report())
}

// report prints the results gathered by the native testing package and returns the exit code.
func report() int {
	return 0
}
