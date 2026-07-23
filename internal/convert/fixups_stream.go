package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

func init() {
	registerFixer(lzwStreamFixer{})
}

type lzwStreamFixer struct{}

func (lzwStreamFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.StreamLZWFilter
}

func (lzwStreamFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		if !d.HasStream || !pdf.HasFilter(d.Entries["Filter"], pdf.FilterLZW) {
			return d, false
		}
		plaintext, err := pdf.DecodeStream(d)
		if err != nil {
			return d, false
		}
		if err := writer.SetStreamFlate(&d, plaintext); err != nil {
			return d, false
		}
		changed = true
		return d, true
	})
	return changed, nil
}

// walkStreamDicts calls fix for every pdf.PDFDict found within v's dictionary entries or array elements,
// using cycle protection. Unlike walkDicts, it writes modified dictionaries back to the parent structure
// so that changes to stream fields take effect.
func walkStreamDicts(v pdf.PDFValue, visited map[uintptr]bool, fix func(pdf.PDFDict) (pdf.PDFDict, bool)) {
	switch val := v.(type) {
	case pdf.PDFDict:
		ptr := pdf.ValuePointer(val.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for k, child := range val.Entries {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			if cd, ok := child.(pdf.PDFDict); ok {
				if updated, ok := fix(cd); ok {
					val.Entries[k] = updated
					child = updated
				}
			}
			walkStreamDicts(child, visited, fix)
		}

	case pdf.PDFArray:
		ptr := pdf.ValuePointer(val)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for i, child := range val {
			if cd, ok := child.(pdf.PDFDict); ok {
				if updated, ok := fix(cd); ok {
					val[i] = updated
					child = updated
				}
			}
			walkStreamDicts(child, visited, fix)
		}
	}
}
