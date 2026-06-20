package main

import (
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	pdfrab "github.com/voidrab/gopdfrab"
)

func main() {
	root := "test documents/Isartor testsuite"
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".pdf") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	results := pdfrab.VerifyAll(paths, pdfrab.A_1B)

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

	fmt.Printf("\n--- Results: %d pass, %d fail, %d errors (total %d) ---\n", pass, fail, errCount, len(paths))
}
