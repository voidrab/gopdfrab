// Command gopdfrab verifies and converts PDFs for PDF/A-1b conformance from
// the command line. It is a thin wrapper over the gopdfrab package, using only
// its public API.
//
// Exit codes: 0 conformant, 1 non-conformant, 2 error.
package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/voidrab/gopdfrab"
)

const (
	exitValid   = 0 // conformant / all files pass
	exitInvalid = 1 // non-conformant / at least one file fails
	exitError   = 2 // usage, open, or I/O error
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point: it dispatches a subcommand and returns the
// process exit code instead of calling os.Exit.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitError
	}
	switch args[0] {
	case "verify":
		return runVerify(ctx, args[1:], stdout, stderr)
	case "convert":
		return runConvert(ctx, args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, version())
		return exitValid
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return exitValid
	default:
		fmt.Fprintf(stderr, "gopdfrab: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitError
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `gopdfrab -- verify and convert PDFs for PDF/A-1b conformance

usage:
  gopdfrab verify  [flags] <path-or-dir>...   verify conformance (dirs walked recursively)
  gopdfrab convert [flags] <input> [output]   rewrite a PDF towards conformance
  gopdfrab version                            print the version
  gopdfrab help                               show this help

exit codes: 0 conformant, 1 non-conformant, 2 error

Run "gopdfrab verify -h" or "gopdfrab convert -h" for command flags.
`)
}

// version reports the module version when built with the module cache (e.g.
// "go install ...@v1.2.3"), or "dev" for a local build.
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// profileByName maps a --profile flag value to a profile, or nil if unknown.
func profileByName(name string) *gopdfrab.Profile {
	switch name {
	case "pdfa1b":
		return gopdfrab.PDFA1B
	case "legacy1b":
		return gopdfrab.Legacy1B
	case "pdf":
		return gopdfrab.PDF
	}
	return nil
}

// passwordBytes returns nil for the empty string (the empty-password default)
// and the raw bytes otherwise.
func passwordBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return []byte(s)
}

// collectPDFs expands each argument (a file or a directory walked recursively)
// into a flat list of .pdf paths, preserving order.
func collectPDFs(args []string) ([]string, error) {
	var paths []string
	for _, root := range args {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			paths = append(paths, root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".pdf") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return paths, nil
}
