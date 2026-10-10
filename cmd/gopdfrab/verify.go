package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/voidrab/gopdfrab"
)

func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profileName := fs.String("profile", "pdfa1b", "conformance profile: pdfa1b, legacy1b, or pdf")
	password := fs.String("password", "", passwordFlagHelp)
	maxDecodedMB := fs.Int("max-decoded-mb", 0, "cap a single stream's decoded output in MB (0 = default 256)")
	maxResidentMB := fs.Int("max-resident-mb", 0, "cap a document's rebuildable caches in MB (0 = default 64)")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: gopdfrab verify [flags] <path-or-dir>...")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	applyLimits(*maxDecodedMB, *maxResidentMB)

	profile := profileByName(*profileName)
	if profile == nil {
		fmt.Fprintf(stderr, "gopdfrab: unknown profile %q (want pdfa1b, legacy1b, or pdf)\n", *profileName)
		return exitError
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return exitError
	}

	paths, err := collectPDFs(fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "gopdfrab: %v\n", err)
		return exitError
	}
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "gopdfrab: no PDF files found")
		return exitError
	}

	opts := gopdfrab.Options{Password: passwordBytes(*password)}
	results, err := gopdfrab.VerifyAllContext(ctx, paths, profile, opts)
	if err != nil {
		fmt.Fprintf(stderr, "gopdfrab: %v\n", err)
		return exitError
	}

	if *jsonOut {
		return printVerifyJSON(results, stdout, stderr)
	}
	return printVerifyText(results, *profileName, stdout)
}

// verifyFileJSON is the per-file wire shape for `verify --json`.
type verifyFileJSON struct {
	Path   string           `json:"path"`
	Error  string           `json:"error,omitempty"`
	Result *gopdfrab.Result `json:"result,omitempty"`
}

func printVerifyJSON(results []gopdfrab.FileResult[gopdfrab.Result], stdout, stderr io.Writer) int {
	out := make([]verifyFileJSON, 0, len(results))
	code := exitValid
	for _, r := range results {
		row := verifyFileJSON{Path: r.Path}
		switch {
		case r.Err != nil:
			row.Error = r.Err.Error()
			code = exitError
		default:
			res := r.Result
			row.Result = &res
			if !r.Result.Valid && code < exitInvalid {
				code = exitInvalid
			}
		}
		out = append(out, row)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(stderr, "gopdfrab: %v\n", err)
		return exitError
	}
	return code
}

// printVerifyText prints the profile once, then a line per file with the
// file's issues folded beneath it, then a summary. The layout is described at
// the top of report.go.
func printVerifyText(results []gopdfrab.FileResult[gopdfrab.Result], profileName string, stdout io.Writer) int {
	fmt.Fprintf(stdout, "profile: %s\n\n", profileName)
	pass, fail, errCount := 0, 0, 0
	code := exitValid
	for _, r := range results {
		switch {
		case r.Err != nil:
			fmt.Fprintf(stdout, "ERROR %s\n  error %s\n", r.Path, strconv.Quote(r.Err.Error()))
			errCount++
			code = exitError
		case r.Result.Valid:
			fmt.Fprintf(stdout, "PASS  %s\n", r.Path)
			pass++
		default:
			folded := foldIssues(r.Result.Issues)
			fmt.Fprintf(stdout, "FAIL  issues=%d distinct=%d  %s\n", len(r.Result.Issues), len(folded), r.Path)
			writeIssues(stdout, folded)
			fail++
			if code < exitInvalid {
				code = exitInvalid
			}
		}
	}
	fmt.Fprintf(stdout, "\nsummary: pass=%d fail=%d error=%d files=%d\n", pass, fail, errCount, len(results))
	return code
}
