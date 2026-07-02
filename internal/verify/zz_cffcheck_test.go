package verify

import (
	"fmt"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestCFFWidthsCrossCheck(t *testing.T) {
	doc, err := pdf.Open("../../tests/regression/150911-LF-Energieeffizienz-in-RZ.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatal(err)
	}
	trailer := graph.(pdf.PDFDict)
	seen := map[uintptr]bool{}
	count := 0
	var walk func(v pdf.PDFValue)
	walk = func(v pdf.PDFValue) {
		switch val := v.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(val.Entries)
			if seen[ptr] {
				return
			}
			seen[ptr] = true
			if (val.Entries["Type"] == pdf.PDFName{Value: "FontDescriptor"}) {
				if ff, ok := val.Entries["FontFile3"].(pdf.PDFDict); ok && count < 2 {
					data, err := pdf.DecodeStream(ff)
					if err == nil {
						name, _ := val.Entries["FontName"].(pdf.PDFName)
						td, ok := ParseCFFTopDict(data)
						fmt.Printf("FONT %s: td=%+v ok=%v\n", name.Value, td, ok)
						names := CFFGlyphNames(data)
						cs, _ := ParseCFFIndex(data, td.CSOffset)
						fmt.Printf("  names=%d charstrings=%d\n", len(names), len(cs))
						dw, nw, ls, pok := cffPrivateInfo(data, td.PrivateOffset, td.PrivateSize)
						fmt.Printf("  defaultWidthX=%v nominalWidthX=%v lsubrs=%d pok=%v gsubrs=%d\n", dw, nw, len(ls), pok, len(cffGlobalSubrs(data)))
						w := CFFAdvanceWidths(data)
						fmt.Printf("  widths=%d\n", len(w))
						for _, g := range []string{"a", "e", "space", "odieresis", "q", "one"} {
							if v, ok := w[g]; ok {
								fmt.Printf("  /%s = %d\n", g, v)
							}
						}
						count++
					}
				}
			}
			for k, c := range val.Entries {
				if k == "_ref" {
					continue
				}
				walk(c)
			}
		case pdf.PDFArray:
			for _, c := range val {
				walk(c)
			}
		}
	}
	walk(trailer)
}
