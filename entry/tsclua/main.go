// Command tsclua is the tsc-lua entry point: the TypeScript 7 command line compiler
// without the language server and API modes. It is transpiled to Lua; terminal and
// process hooks below are replaced by the Lua runtime.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/execute"
	"github.com/microsoft/TypeScript/tsc/internal/execute/tsc"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
	"github.com/microsoft/TypeScript/tsc/internal/vfs"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/osvfs"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	args := os.Args[1:]
	result := execute.CommandLine(context.Background(), newSystem(), args, nil)
	return int(result.Status)
}

type osSys struct {
	writer             io.Writer
	fs                 vfs.FS
	defaultLibraryPath tspath.RootedDirectoryPath
	cwd                tspath.RootedDirectoryPath
	start              time.Time
}

func (s *osSys) SinceStart() time.Duration {
	return time.Since(s.start)
}

func (s *osSys) Now() time.Time {
	return time.Now()
}

func (s *osSys) FS() vfs.FS {
	return s.fs
}

func (s *osSys) DefaultLibraryPath() tspath.RootedDirectoryPath {
	return s.defaultLibraryPath
}

func (s *osSys) GetCurrentDirectory() tspath.RootedDirectoryPath {
	return s.cwd
}

func (s *osSys) Writer() io.Writer {
	return s.writer
}

func (s *osSys) ErrorWriter() io.Writer {
	return os.Stderr
}

func (s *osSys) WriteOutputIsTTY() bool {
	return isTerminal(1)
}

func (s *osSys) GetWidthOfTerminal() int {
	return terminalWidth()
}

func (s *osSys) GetEnvironmentVariable(name string) (string, bool) {
	return os.LookupEnv(name)
}

var errNoSpawn = errors.New("tsc-lua: spawning processes is not supported")

func (s *osSys) Spawn(command []string, dir string, stderr io.Writer) (io.ReadWriteCloser, error) {
	return nil, errNoSpawn
}

// isTerminal and terminalWidth are replaced by native implementations at runtime.
func isTerminal(fd int) bool {
	return false
}

func terminalWidth() int {
	return 0
}

func newSystem() *osSys {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current directory: %v\n", err)
		os.Exit(int(tsc.ExitStatusInvalidProject_OutputsSkipped))
	}

	return &osSys{
		cwd:                tspath.RootedDirectoryPathFromAbsolute(cwd),
		fs:                 bundled.WrapFS(osvfs.FS()),
		defaultLibraryPath: bundled.LibPath(),
		writer:             os.Stdout,
		start:              time.Now(),
	}
}
