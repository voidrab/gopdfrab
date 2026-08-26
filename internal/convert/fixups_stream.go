package convert

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

func init() {
	registerFixer(lzwStreamFixer{})
	registerFixer(undecodableStreamFixer{})
}

// undecodableStreamFixer remediates Structure.StreamUndecodable where the
// stream's own bytes say what it should have been:
//
//   - a stream whose body is empty but which declares a filter decodes to
//     nothing whichever way it is read, so dropping the filter loses nothing
//     and makes it readable. Real files carry these by the dozen as
//     placeholder content-stream parts.
//   - a Type 3 glyph procedure that cannot be decoded is gone either way, but
//     its advance is not: /Widths states it, and a reader that cannot get a
//     width out of the program is entitled to disagree with the dictionary
//     (6.3.6). Replacing it with that advance and no marks keeps the text
//     around it spaced as the document intended.
//
// Anything else stays a residual: a stream nobody can decode is content this
// converter has no honest way to reconstruct.
//
// lost, when non-nil, collects what the second case gave up, so a caller sees
// the glyph it no longer has. The registry prototype leaves it nil.
type undecodableStreamFixer struct {
	lost *[]pdf.PDFError
}

func (undecodableStreamFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.StreamUndecodable
}

func (f undecodableStreamFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		if !d.HasStream || len(d.RawStream) != 0 || d.Entries.Get("Filter") == nil {
			return d, false
		}
		dropFilters(&d)
		changed = true
		return d, true
	})
	if f.fixType3CharProcs(*trailer) {
		changed = true
	}
	return changed, nil
}

// fixType3CharProcs replaces every undecodable glyph procedure of every Type 3
// font with the advance its /Widths entry declares.
func (f undecodableStreamFixer) fixType3CharProcs(trailer pdf.PDFDict) bool {
	changed := false
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Type3"}) {
			return
		}
		procs, ok := d.Entries.Get("CharProcs").(pdf.PDFDict)
		if !ok {
			return
		}
		widths := type3GlyphWidths(d)
		// Sorted, not map order: what the fixer reports has to be the same on
		// every run.
		for _, name := range sortedKeys(procs.Entries) {
			proc, ok := procs.Entries.Get(name).(pdf.PDFDict)
			if !ok || !proc.HasStream {
				continue
			}
			if _, err := pdf.DecodeStream(proc); err == nil {
				continue
			}
			content, err := writer.WriteContentStream([]writer.ContentOp{{
				Op:       "d0",
				Operands: []pdf.PDFValue{pdf.PDFReal(widths[name]), pdf.PDFReal(0)},
			}})
			if err != nil {
				continue
			}
			dropFilters(&proc)
			proc.RawStream = content
			procs.Entries.Set(name, proc)
			changed = true
			if f.lost != nil {
				ref, _ := proc.Entries.Get("_ref").(pdf.PDFRef)
				*f.lost = append(*f.lost, pdf.NewError(
					pdf.Checks.Structure.StreamUndecodable,
					[]error{fmt.Errorf("Type 3 glyph /%s could not be decoded; replaced by its declared advance", name)},
					0,
					&ref,
				))
			}
		}
	})
	return changed
}

// type3GlyphWidths maps each glyph name a Type 3 font's /Encoding names to the
// advance /Widths gives its code, in glyph space. A name with no width maps to
// zero, which is what a reader computes for a glyph procedure that draws
// nothing and says nothing.
func type3GlyphWidths(font pdf.PDFDict) map[string]float64 {
	out := map[string]float64{}
	enc, ok := font.Entries.Get("Encoding").(pdf.PDFDict)
	if !ok {
		return out
	}
	diffs, ok := enc.Entries.Get("Differences").(pdf.PDFArray)
	if !ok {
		return out
	}
	widths, _ := font.Entries.Get("Widths").(pdf.PDFArray)
	first := pdf.DictInt(font, "FirstChar", 0)

	code := 0
	for _, item := range diffs {
		switch v := item.(type) {
		case pdf.PDFInteger:
			code = int(v)
		case pdf.PDFName:
			if i := code - first; i >= 0 && i < len(widths) {
				if w, ok := pdf.PDFNumberToFloat(widths[i]); ok {
					out[v.Value] = w
				}
			}
			code++
		}
	}
	return out
}

// dropFilters removes a stream's filter chain, leaving its bytes as they are.
func dropFilters(d *pdf.PDFDict) {
	d.Entries.Del("Filter")
	d.Entries.Del("DecodeParms")
	d.Entries.Del("DL")
}

type lzwStreamFixer struct{}

func (lzwStreamFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.StreamLZWFilter
}

func (lzwStreamFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		if !d.HasStream || !pdf.HasFilter(d.Entries.Get("Filter"), pdf.FilterLZW) {
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
		for k, child := range val.Entries.All() {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			if cd, ok := child.(pdf.PDFDict); ok {
				if updated, ok := fix(cd); ok {
					val.Entries.Set(k, updated)
					child = updated
				}
			}
			walkStreamDicts(child, visited, fix)
		}

	case pdf.PDFArray:
		ptr := pdf.ArrayPointer(val)
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
