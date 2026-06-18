package pdfrab

import (
	"fmt"
	"testing"
)

func TestT03PassA(t *testing.T) {
	doc, err := Open("test documents/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t03-pass-a.pdf")
	if err != nil {
		t.Fatal("open error:", err)
	}
	defer doc.Close()
	graph, _ := doc.ResolveGraph()
	var findLongStrings func(v PDFValue, path string, visited map[uintptr]bool)
	findLongStrings = func(v PDFValue, path string, visited map[uintptr]bool) {
		switch d := v.(type) {
		case PDFDict:
			if d.Entries == nil { return }
			ptr := pdfValuePointer(d.Entries)
			if visited[ptr] { return }
			visited[ptr] = true
			for k, val := range d.Entries {
				findLongStrings(val, path+"/"+k, visited)
			}
		case PDFArray:
			ptr := pdfValuePointer(d)
			if visited[ptr] { return }
			visited[ptr] = true
			for i, item := range d {
				findLongStrings(item, fmt.Sprintf("%s[%d]", path, i), visited)
			}
		case PDFString:
			if len(d.Value) > 65000 {
				fmt.Printf("Long string at %s: %d bytes, first bytes: %x\n", path, len(d.Value), []byte(d.Value[:min2(20, len(d.Value))]))
			}
		}
	}
	findLongStrings(graph, "root", make(map[uintptr]bool))
}

func min2(a, b int) int {
	if a < b { return a }
	return b
}
