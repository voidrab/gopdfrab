package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/voidrab/gopdfrab"
)

func runConvert(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profileName := fs.String("profile", "pdfa1b", "target profile: pdfa1b, legacy1b, or pdf")
	password := fs.String("password", "", "password for an encrypted input")
	dpi := fs.Int("dpi", 0, "raster fallback resolution in DPI (0 = default 150)")
	maxIter := fs.Int("max-iterations", 0, "verify/fix loop bound (0 = default 4)")
	outPath := fs.String("o", "", "output path (default: <input> with a .pdfa.pdf/.fixed.pdf suffix)")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: gopdfrab convert [flags] <input> [output]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitError
	}

	profile := profileByName(*profileName)
	if profile == nil {
		fmt.Fprintf(stderr, "gopdfrab: unknown profile %q (want pdfa1b, legacy1b, or pdf)\n", *profileName)
		return exitError
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return exitError
	}

	input := fs.Arg(0)
	output := *outPath
	if output == "" && fs.NArg() >= 2 {
		output = fs.Arg(1)
	}
	if output == "" {
		output = defaultOutput(input, *profileName)
	}

	opts := gopdfrab.Options{
		Password:      passwordBytes(*password),
		RasterDPI:     *dpi,
		MaxIterations: *maxIter,
	}
	cr, err := gopdfrab.ConvertContext(ctx, input, profile, opts)
	if err != nil {
		fmt.Fprintf(stderr, "gopdfrab: convert %s: %v\n", input, err)
		return exitError
	}
	if err := cr.Save(output); err != nil {
		fmt.Fprintf(stderr, "gopdfrab: write %s: %v\n", output, err)
		return exitError
	}

	if *jsonOut {
		return printConvertJSON(input, output, cr, stdout, stderr)
	}
	return printConvertText(input, output, cr, stdout)
}

// defaultOutput derives an output path from the input, tagging it by target.
func defaultOutput(input, profileName string) string {
	ext := filepath.Ext(input)
	base := strings.TrimSuffix(input, ext)
	if profileName == "pdf" {
		return base + ".fixed.pdf"
	}
	return base + ".pdfa.pdf"
}

// convertJSON is the wire shape for `convert --json`.
type convertJSON struct {
	Input      string              `json:"input"`
	Output     string              `json:"output"`
	Iterations int                 `json:"iterations"`
	Valid      bool                `json:"valid"`
	IssueCount int                 `json:"issueCount"`
	Residual   []gopdfrab.PDFError `json:"residual"`
}

func printConvertJSON(input, output string, cr gopdfrab.ConvertResult, stdout, stderr io.Writer) int {
	residual := cr.Residual()
	if residual == nil {
		residual = []gopdfrab.PDFError{}
	}
	row := convertJSON{
		Input:      input,
		Output:     output,
		Iterations: cr.Iterations,
		Valid:      cr.Result.Valid,
		IssueCount: len(residual),
		Residual:   residual,
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(row); err != nil {
		fmt.Fprintf(stderr, "gopdfrab: %v\n", err)
		return exitError
	}
	if cr.Result.Valid {
		return exitValid
	}
	return exitInvalid
}

func printConvertText(input, output string, cr gopdfrab.ConvertResult, stdout io.Writer) int {
	fmt.Fprintf(stdout, "%s -> %s (%d iteration(s))\n", input, output, cr.Iterations)
	if cr.Result.Valid {
		fmt.Fprintln(stdout, "result: conformant")
		return exitValid
	}
	residual := cr.Residual()
	fmt.Fprintf(stdout, "result: %d residual issue(s)\n", len(residual))
	for _, iss := range residual {
		fmt.Fprintf(stdout, "    %s\n", iss.String())
	}
	return exitInvalid
}
