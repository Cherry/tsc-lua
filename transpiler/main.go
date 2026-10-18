// Command golua transpiles Go packages (and their dependencies) into Lua modules for LuaJIT.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
)

func main() {
	var (
		outDir     = flag.String("o", "build/lua", "output directory for generated Lua modules")
		dir        = flag.String("C", ".", "directory to load packages from")
		nativesDir = flag.String("natives", "runtime/natives", "directory containing native Lua overrides")
		onlyPkgs   = flag.String("only", "", "comma separated list of package path prefixes to (re)emit; others are only loaded")
		tags       = flag.String("tags", "", "extra build tags")
		verbose    = flag.Bool("v", false, "verbose output")
		mainMod    = flag.String("main", "", "package path whose main function is the program entry point")
		embedDir   = flag.String("embed", "build/embed", "output directory for //go:embed files")
	)
	flag.Parse()
	patterns := flag.Args()
	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "usage: golua [flags] packages...")
		os.Exit(2)
	}

	start := time.Now()
	buildFlags := []string{}
	if *tags != "" {
		buildFlags = append(buildFlags, "-tags="+*tags)
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports | packages.NeedEmbedFiles |
			packages.NeedEmbedPatterns | packages.NeedModule,
		Dir:        *dir,
		Env:        append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0"),
		BuildFlags: buildFlags,
	}
	roots, err := packages.Load(cfg, patterns...)
	if err != nil {
		fatalf("load: %v", err)
	}
	var all []*packages.Package
	seen := map[*packages.Package]bool{}
	var visit func(p *packages.Package)
	visit = func(p *packages.Package) {
		if seen[p] {
			return
		}
		seen[p] = true
		paths := make([]string, 0, len(p.Imports))
		for path := range p.Imports {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			visit(p.Imports[path])
		}
		all = append(all, p)
	}
	for _, p := range roots {
		visit(p)
	}
	hadErrors := false
	for _, p := range all {
		for _, e := range p.Errors {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p.PkgPath, e)
			hadErrors = true
		}
	}
	if hadErrors {
		fatalf("package loading failed")
	}
	if *verbose {
		fmt.Fprintf(os.Stderr, "loaded %d packages in %s\n", len(all), time.Since(start))
	}

	c := newCompiler(all, *nativesDir)
	c.verbose = *verbose
	c.mainPkg = *mainMod
	c.embedOut = *embedDir
	for _, r := range roots {
		if r.Module != nil {
			c.ownModules[r.Module.Path] = true
		}
	}
	all = c.reachable(roots)
	c.pkgs = all
	c.analyze()
	c.buildInlineTemplates()

	var only []string
	if *onlyPkgs != "" {
		only = strings.Split(*onlyPkgs, ",")
	}
	emitted := 0
	for _, p := range all {
		if len(only) > 0 {
			match := false
			for _, o := range only {
				if strings.HasPrefix(p.PkgPath, o) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		if c.isFullNative(p.PkgPath) {
			continue
		}
		if c.skipPackage(p.PkgPath) {
			continue
		}
		code := c.emitPackage(p)
		file := filepath.Join(*outDir, filepath.FromSlash(strings.ReplaceAll(luaModuleName(p.PkgPath), ".", "/"))+".lua")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			fatalf("%v", err)
		}
		if err := writeIfChanged(file, code); err != nil {
			fatalf("%v", err)
		}
		emitted++
	}
	if *mainMod != "" {
		writeIfChanged(filepath.Join(*outDir, "main_module.lua"), "return '"+luaModuleName(*mainMod)+"'\n")
	}
	for _, w := range c.warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	fmt.Fprintf(os.Stderr, "emitted %d packages (%d warnings) in %s\n", emitted, len(c.warnings), time.Since(start).Round(time.Millisecond))
}

func writeIfChanged(file, content string) error {
	if old, err := os.ReadFile(file); err == nil && string(old) == content {
		return nil
	}
	return os.WriteFile(file, []byte(content), 0o644)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "golua: "+format+"\n", args...)
	os.Exit(1)
}
