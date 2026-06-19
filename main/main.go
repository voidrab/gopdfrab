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

	pass, fail, errCount := 0, 0, 0
	for _, path := range paths {
		doc, err := pdfrab.Open(path)
		if err != nil {
			fmt.Printf("[ERROR] %s: %v\n", path, err)
			errCount++
			continue
		}

		v, err := doc.Verify(pdfrab.A_1B)
		doc.Close()
		if err != nil {
			fmt.Printf("[ERROR] %s: %v\n", path, err)
			errCount++
			continue
		}

		if v.Valid {
			fmt.Printf("[PASS] %s\n", path)
			pass++
		} else {
			fmt.Printf("[FAIL] %s: %s\n", path, v.Summary())
			fail++
		}
	}

	fmt.Printf("\n--- Results: %d pass, %d fail, %d errors (total %d) ---\n", pass, fail, errCount, len(paths))
}
