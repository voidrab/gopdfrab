package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/voidrab/gopdfrab/verify"
)

func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profileName := fs.String("profile", "pdfa1b", "conformance profile: "+profileNames())
	password := fs.String("password", "", passwordFlagHelp)
	maxDecodedMB := fs.Int("max-decoded-mb", 0, "cap a single stream's decoded output in MB (0 = default 256)")
	maxResidentMB := fs.Int("max-resident-mb", 0, "cap a document's rebuildable caches in MB (0 = default 64)")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	detail := fs.Bool("detail", false, "list every page and object an issue was found on")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: gopdfrab verify [flags] <path-or-dir>...")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	applyLimits(*maxDecodedMB, *maxResidentMB)

	profile, ok := verify.ProfileByName(*profileName)
	if !ok {
		fmt.Fprintf(stderr, "gopdfrab: unknown profile %q (want %s)\n", *profileName, profileNames())
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

	opts := verify.Options{Password: passwordBytes(*password)}
	// VerifyAllContext records every failure, cancellation included, in its
	// file's result; its error return exists for API uniformity and is nil.
	results, _ := verify.VerifyAllContext(ctx, paths, profile, opts)

	if *jsonOut {
		return printVerifyJSON(results, stdout, stderr)
	}
	return printVerifyText(results, *profileName, *detail, stdout)
}

// verifyFileJSON is the per-file wire shape for `verify --json`.
type verifyFileJSON struct {
	Path   string         `json:"path"`
	Error  string         `json:"error,omitempty"`
	Result *verify.Result `json:"result,omitempty"`
}

func printVerifyJSON(results []verify.FileResult[verify.Result], stdout, stderr io.Writer) int {
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
// file's issues beneath it, grouped or per location as detail says, then a
// summary. The layout is described at the top of report.go.
func printVerifyText(results []verify.FileResult[verify.Result], profileName string, detail bool, stdout io.Writer) int {
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
			writeIssueList(stdout, r.Result.Issues, detail, func(distinct int) string {
				return fmt.Sprintf("FAIL  issues=%d distinct=%d  %s", len(r.Result.Issues), distinct, r.Path)
			})
			fail++
			if code < exitInvalid {
				code = exitInvalid
			}
		}
	}
	fmt.Fprintf(stdout, "\nsummary: pass=%d fail=%d error=%d files=%d\n", pass, fail, errCount, len(results))
	return code
}
