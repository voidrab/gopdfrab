package pdfrab

import (
	"fmt"
	"testing"
)

func TestDebugLimits(t *testing.T) {
	files := []string{
		"test documents/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t02-fail-c.pdf",
		"test documents/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t03-fail-a.pdf",
		"test documents/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t08-fail-a.pdf",
		"test documents/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t09-fail-a.pdf",
	}
	for _, path := range files {
		t.Logf("=== %s ===", path[len(path)-20:])
		doc, err := Open(path)
		if err != nil {
			t.Logf("  open error: %v", err)
			continue
		}
		defer doc.Close()
		graph, _ := doc.ResolveGraph()

		// Look for large values
		visited := make(map[uintptr]bool)
		debugFindLargeValues(t, graph, visited)

		// Check content streams for q depth
		debugFindQDepth(t, graph, make(map[uintptr]bool))
	}
}

func debugFindLargeValues(t *testing.T, v PDFValue, visited map[uintptr]bool) {
	t.Helper()
	switch d := v.(type) {
	case PDFDict:
		if d.Entries == nil {
			return
		}
		ptr := pdfValuePointer(d.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		realCount := len(d.Entries)
		if _, has := d.Entries["_ref"]; has {
			realCount--
		}
		if realCount > 100 {
			t.Logf("  Large dict: %d entries type=%v", realCount, d.Entries["Type"])
		}
		for _, val := range d.Entries {
			debugFindLargeValues(t, val, visited)
		}
	case PDFArray:
		ptr := pdfValuePointer(d)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		if len(d) > 100 {
			t.Logf("  Large array: %d elements", len(d))
		}
		for _, item := range d {
			debugFindLargeValues(t, item, visited)
		}
	case PDFInteger:
		if d > 2_147_483_647 || d < -2_147_483_648 {
			t.Logf("  Out-of-range integer: %d", d)
		}
	case PDFReal:
		if float64(d) > 3.402e38 || float64(d) < -3.402e38 {
			t.Logf("  Out-of-range real: %g", d)
		}
	}
}

func debugFindQDepth(t *testing.T, v PDFValue, visited map[uintptr]bool) {
	t.Helper()
	switch d := v.(type) {
	case PDFDict:
		if d.Entries == nil {
			return
		}
		ptr := pdfValuePointer(d.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		if d.HasStream {
			typeName, _ := d.Entries["Type"].(PDFName)
			subtypeName, _ := d.Entries["Subtype"].(PDFName)
			if typeName.Value == "Page" || subtypeName.Value == "" {
				data, err := decodeStream(d)
				if err == nil {
					maxDepth, finalDepth := trackQDepth(data)
					if maxDepth > 0 {
						t.Logf("  Stream q depth: max=%d final=%d type=%s/%s", maxDepth, finalDepth, typeName.Value, subtypeName.Value)
					}
				}
			}
		}
		for _, val := range d.Entries {
			debugFindQDepth(t, val, visited)
		}
	case PDFArray:
		for _, item := range d {
			debugFindQDepth(t, item, visited)
		}
	}
}

func trackQDepth(data []byte) (maxDepth, finalDepth int) {
	depth := 0
	i := 0
	for i < len(data) {
		// skip whitespace
		for i < len(data) && isWhitespace(data[i]) {
			i++
		}
		if i >= len(data) {
			break
		}
		j := i
		for j < len(data) && !isWhitespace(data[j]) {
			j++
		}
		tok := string(data[i:j])
		switch tok {
		case "q":
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case "Q":
			depth--
		}
		i = j
	}
	return maxDepth, depth
}

var _ = fmt.Sprintf
