// Command gopdfrab verifies and converts PDFs for PDF/A-1b conformance from
// the command line. It is a thin wrapper over the public API: the verify
// subcommand and the shared reporting use gopdfrab/verify, and only convert
// uses gopdfrab.
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

	"github.com/voidrab/gopdfrab/verify"
)

const (
	exitValid   = 0 // conformant / all files pass
	exitInvalid = 1 // non-conformant / at least one file fails
	exitError   = 2 // usage, open, or I/O error
)

var exit = os.Exit

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
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
	return versionOf(debug.ReadBuildInfo())
}

// versionOf is version's decision, split from the build-info read so the
// module-cache case can be tested: a test binary is always a "(devel)" build.
func versionOf(bi *debug.BuildInfo, ok bool) string {
	if ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// profileNames lists the ready-made profiles' names for flag help and errors,
// as in "pdfa1b, legacy1b, or pdf".
func profileNames() string {
	var names []string
	for _, p := range verify.Profiles() {
		names = append(names, p.Name())
	}
	return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
}

// applyLimits sets the process-wide resource caps from the --max-decoded-mb
// and --max-resident-mb flag values; 0 leaves that cap at its built-in default.
// Both are set in one call because SetLimits applies a whole Limits value.
func applyLimits(decodedMB, residentMB int) {
	if decodedMB <= 0 && residentMB <= 0 {
		return
	}
	verify.SetLimits(verify.Limits{
		MaxDecodedStreamBytes: int64(decodedMB) << 20,
		MaxResidentBytes:      int64(residentMB) << 20,
	})
}

// passwordEnv names the environment variable read when --password is not
// given. A command-line password is visible to every user through ps and
// /proc; the environment of another user's process is not.
const passwordEnv = "GOPDFRAB_PASSWORD"

// passwordFlagHelp is the --password usage text, shared by both subcommands.
const passwordFlagHelp = "password for an encrypted input (visible in ps; prefer $" + passwordEnv + ")"

// passwordBytes returns the password to use: the flag value when given, else
// $GOPDFRAB_PASSWORD, else nil (the empty-password default).
func passwordBytes(s string) []byte {
	if s == "" {
		s = os.Getenv(passwordEnv)
	}
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
