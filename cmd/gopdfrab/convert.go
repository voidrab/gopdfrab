package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab"
)

func runConvert(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profileName := fs.String("profile", "pdfa1b", "target profile: pdfa1b, legacy1b, or pdf")
	password := fs.String("password", "", passwordFlagHelp)
	dpi := fs.Int("dpi", 0, "raster fallback resolution in DPI (0 = default 150)")
	maxIter := fs.Int("max-iterations", 0, "verify/fix loop bound (0 = default 4)")
	maxDecodedMB := fs.Int("max-decoded-mb", 0, "cap a single stream's decoded output in MB (0 = default 256)")
	maxResidentMB := fs.Int("max-resident-mb", 0, "cap a document's rebuildable caches in MB (0 = default 64)")
	outPath := fs.String("o", "", "output path (default: <input> with a .pdfa.pdf/.fixed.pdf suffix)")
	jsonOut := fs.Bool("json", false, "emit machine-readable JSON")
	detail := fs.Bool("detail", false, "list every page and object an issue was found on")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: gopdfrab convert [flags] <input> [output]")
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
	if fs.NArg() < 1 {
		fs.Usage()
		return exitError
	}

	if fs.NArg() > 2 || (fs.NArg() == 2 && *outPath != "") {
		fmt.Fprintln(stderr, "gopdfrab: give the output path once, as -o or as the second argument")
		return exitError
	}
	input := fs.Arg(0)
	output := *outPath
	if output == "" && fs.NArg() == 2 {
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
	defer cr.Close()
	if err := cr.Save(output); err != nil {
		fmt.Fprintf(stderr, "gopdfrab: write %s: %v\n", output, err)
		return exitError
	}

	if *jsonOut {
		return printConvertJSON(input, output, cr, stdout, stderr)
	}
	return printConvertText(input, output, *profileName, *detail, cr, stdout)
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
	// RasterizedPages are the pages rebuilt as a flat image, and RasterDrops
	// what the rasterizer could not draw on them.
	RasterizedPages []int            `json:"rasterizedPages,omitempty"`
	RasterDrops     []rasterDropJSON `json:"rasterDrops,omitempty"`
	// Lost is content the conversion could not carry over. It is not a
	// conformance failure -- the output can be valid and still have lost
	// something -- so it is reported on its own.
	Lost []gopdfrab.PDFError `json:"lost,omitempty"`
}

// rasterDropJSON is one page's dropped features, keyed as the wasm build
// reports them.
type rasterDropJSON struct {
	Page     int      `json:"page"`
	Features []string `json:"features"`
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
		Lost:       cr.LostObjects,

		RasterizedPages: cr.RasterizedPages,
	}
	for _, d := range cr.RasterDrops {
		row.RasterDrops = append(row.RasterDrops, rasterDropJSON{Page: d.Page, Features: d.Features})
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

// printConvertText prints the conversion as "key: value" lines, with any lost
// content and residual issues beneath their lines, grouped or per location as
// detail says, in the layout described at the top of report.go.
func printConvertText(input, output, profileName string, detail bool, cr gopdfrab.ConvertResult, stdout io.Writer) int {
	fmt.Fprintf(stdout, "profile: %s\n\n", profileName)
	fmt.Fprintf(stdout, "input: %s\noutput: %s\niterations: %d\n", input, output, cr.Iterations)
	// Rasterization and loss are reported whatever the verdict: a conformant
	// output can still have stopped being text, or be missing something the
	// input had.
	if len(cr.RasterizedPages) > 0 {
		fmt.Fprintf(stdout, "rasterized: pages=%s\n", pageRanges(cr.RasterizedPages))
	}
	writeRasterDrops(stdout, cr.RasterDrops)
	if len(cr.LostObjects) > 0 {
		writeIssueList(stdout, cr.LostObjects, detail, func(distinct int) string {
			return fmt.Sprintf("lost: objects=%d distinct=%d", len(cr.LostObjects), distinct)
		})
	}
	if cr.Result.Valid {
		fmt.Fprintln(stdout, "result: conformant")
		return exitValid
	}
	residual := cr.Residual()
	writeIssueList(stdout, residual, detail, func(distinct int) string {
		return fmt.Sprintf("result: residual issues=%d distinct=%d", len(residual), distinct)
	})
	return exitInvalid
}

// writeRasterDrops prints what the rasterizer could not draw: a header with
// the pages and the number of distinct features, then one line per feature
// with the pages it was dropped from, sorted by feature.
func writeRasterDrops(w io.Writer, drops []gopdfrab.RasterDrop) {
	if len(drops) == 0 {
		return
	}
	pagesOf := map[string][]int{}
	var all []int
	for _, d := range drops {
		all = append(all, d.Page)
		for _, f := range d.Features {
			pagesOf[f] = append(pagesOf[f], d.Page)
		}
	}
	features := slices.Sorted(maps.Keys(pagesOf))
	fmt.Fprintf(w, "dropped: pages=%s features=%d\n", pageRanges(sortedUnique(all)), len(features))
	rows := make([][]string, 0, len(features))
	for _, f := range features {
		rows = append(rows, []string{"pages=" + pageRanges(sortedUnique(pagesOf[f])), strconv.Quote(f)})
	}
	writeColumns(w, rows)
}

// sortedUnique returns pages ascending with duplicates removed.
func sortedUnique(pages []int) []int {
	out := slices.Clone(pages)
	slices.Sort(out)
	return slices.Compact(out)
}
