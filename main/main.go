package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "convert":
		runConvert(os.Args[2:])
	case "verify":
		runVerify(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  go run main.go convert <input.pdf> [output.pdf]   convert towards PDF/A-1b conformance
  go run main.go verify <path-or-dir>...            verify PDF/A-1b conformance`)
}

// runConvert converts a single PDF and reports the outcome: how many
// verify/fixup passes it took and whether the result is fully conformant.
func runConvert(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}
	input := args[0]
	output := input + ".pdfa.pdf"
	if len(args) >= 2 {
		output = args[1]
	}

	start := time.Now()
	cr, err := gopdfrab.Convert(input, pdf.PDFA_1B)
	if err != nil {
		fmt.Fprintf(os.Stderr, "convert %s: %v\n", input, err)
		os.Exit(1)
	}
	end := time.Now()
	fmt.Printf("convert time: %v\n", end.Sub(start))

	if err := cr.Save(output); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", output, err)
		os.Exit(1)
	}

	fmt.Printf("%s -> %s\n", input, output)
	fmt.Printf("iterations: %d\n", cr.Iterations)

	if cr.Result.Valid {
		fmt.Println("result: fully PDF/A-1b conformant")
		return
	}

	residual := cr.Residual()
	fmt.Printf("result: %d residual issue(s)\n", len(residual))
	for _, iss := range residual {
		check := iss.Check()
		line := fmt.Sprintf("  [%s/%d %s]", check.Clause(), check.Subclause(), check.Name())
		fmt.Println(line)
		for _, msg := range iss.Messages() {
			fmt.Printf("    %s\n", msg)
		}
	}
	os.Exit(1)
}

// runVerify verifies every PDF found at or under each given path (a single
// file or a directory walked recursively) and prints a pass/fail summary.
func runVerify(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(1)
	}

	var paths []string
	for _, root := range args {
		info, err := os.Stat(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", root, err)
			os.Exit(1)
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
			fmt.Fprintf(os.Stderr, "%s: %v\n", root, err)
			os.Exit(1)
		}
	}

	results, err := gopdfrab.VerifyAll(paths, gopdfrab.PDFA_1B)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify: %v\n", err)
		os.Exit(1)
	}

	pass, fail, errCount := 0, 0, 0
	for _, r := range results {
		switch {
		case r.Err != nil:
			fmt.Printf("[ERROR] %s: %v\n", r.Path, r.Err)
			errCount++
		case r.Result.Valid:
			fmt.Printf("[PASS] %s\n", r.Path)
			pass++
		default:
			fmt.Printf("[FAIL] %s: %s\n", r.Path, r.Result.Summary())
			fail++
		}
	}

	fmt.Printf("\n--- PDF/A Results: %d pass, %d fail, %d errors (total %d) ---\n", pass, fail, errCount, len(paths))

	results, err = gopdfrab.VerifyAll(paths, gopdfrab.PDF)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify: %v\n", err)
		os.Exit(1)
	}

	pass, fail, errCount = 0, 0, 0
	for _, r := range results {
		switch {
		case r.Err != nil:
			fmt.Printf("[ERROR] %s: %v\n", r.Path, r.Err)
			errCount++
		case r.Result.Valid:
			fmt.Printf("[PASS] %s\n", r.Path)
			pass++
		default:
			fmt.Printf("[FAIL] %s: %s\n", r.Path, r.Result.Summary())
			fail++
		}
	}

	fmt.Printf("\n--- PDF Results: %d pass, %d fail, %d errors (total %d) ---\n", pass, fail, errCount, len(paths))
}
