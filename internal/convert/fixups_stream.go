package convert

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/writer"
)

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
// lost, when non-nil, collects what the second case discards, so a caller can
// report the glyphs that were removed.
type undecodableStreamFixer struct {
	lost *[]pdf.PDFError
}

func (undecodableStreamFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.StreamUndecodable
}

func (f undecodableStreamFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if !d.IsStream() || len(d.Stream()) != 0 || d.Entries.Get("Filter") == nil {
			return false
		}
		dropFilters(&d)
		changed = true
		return true
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
			if !ok || !proc.IsStream() {
				continue
			}
			if _, err := pdf.DecodeStream(proc); err == nil {
				continue
			}
			// WriteOp fails only on an operand type it cannot serialize; the
			// operands here are two PDFReals, which it always writes (a
			// non-finite advance is written as 0), into an in-memory buffer.
			var cw writer.ContentStreamWriter
			_ = cw.WriteOp("d0", []pdf.PDFValue{pdf.PDFReal(widths[name]), pdf.PDFReal(0)})
			dropFilters(&proc)
			proc.SetStream(cw.Bytes())
			procs.Entries.Set(name, proc)
			changed = true
			if f.lost != nil {
				ref, _ := proc.Ref()
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
	widths, _ := font.Entries.Get("Widths").(pdf.PDFArray)
	first := pdf.DictInt(font, "FirstChar", 0)
	fontprog.EachDifference(enc, func(code int, name string) {
		if i := code - first; i >= 0 && i < len(widths) {
			if w, ok := pdf.PDFNumberToFloat(widths[i]); ok {
				out[name] = w
			}
		}
	})
	return out
}

// dropFilters removes a stream's filter chain, leaving its bytes as they are.
func dropFilters(d *pdf.PDFDict) {
	d.Entries.Del("Filter")
	d.Entries.Del("DecodeParms")
	d.Entries.Del("DL")
}

// streamFilterFixer repairs the streams whose filter chain the level does not
// allow: one using LZW, and, for StreamFilterNotAllowed, one naming a filter
// outside the level's set or a Crypt filter other than the Identity one. A
// filter written in its inline-image abbreviation is renamed, which keeps the
// bytes. A chain that names any other disallowed filter, or uses LZW, is
// decoded and re-encoded as Flate. A chain that cannot be decoded to bytes,
// such as one ending in an image codec, is left as it is. Each repair runs
// only for its reported check.
type streamFilterFixer struct{ spec *pdfa.Spec }

func (streamFilterFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.StreamLZWFilter || c == pdf.Checks.Structure.StreamFilterNotAllowed
}

func (f streamFilterFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	reported := reportedChecks(issues)
	lzw := reported[pdf.Checks.Structure.StreamLZWFilter]
	others := reported[pdf.Checks.Structure.StreamFilterNotAllowed] && f.spec.StreamFilters.Len() > 0
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if !d.IsStream() {
			return false
		}
		renamed := others && renameAbbreviatedFilters(d)
		changed = changed || renamed
		if !(lzw && pdf.HasFilter(d.Entries.Get("Filter"), pdf.FilterLZW)) && !(others && f.disallowed(d)) {
			return renamed
		}
		plaintext, err := pdf.DecodeStream(d)
		if err != nil {
			return renamed
		}
		writer.SetStreamFlate(&d, plaintext)
		changed = true
		return true
	})
	return changed, nil
}

// disallowed reports whether a stream names a filter outside the level's set,
// or a Crypt filter other than the Identity one.
func (f streamFilterFixer) disallowed(d pdf.PDFDict) bool {
	names := pdf.FilterNames(d.Entries.Get("Filter"))
	for i, name := range names {
		if !f.spec.StreamFilters.Has(name) {
			return true
		}
		if n := pdf.FilterDecodeParms(d, i, len(names)).Entries.Get("Name"); name == "Crypt" && n != nil && n != (pdf.PDFName{Value: "Identity"}) {
			return true
		}
	}
	return false
}

// renameAbbreviatedFilters replaces each filter of a stream written in its
// inline-image abbreviation with the filter's full name.
func renameAbbreviatedFilters(d pdf.PDFDict) bool {
	full := func(v pdf.PDFValue) (pdf.PDFName, bool) {
		n, ok := v.(pdf.PDFName)
		if !ok {
			return n, false
		}
		info, ok := pdf.LookupFilter(n.Value)
		return pdf.PDFName{Value: info.Name}, ok && info.Name != n.Value
	}
	switch filter := d.Entries.Get("Filter").(type) {
	case pdf.PDFName:
		if n, ok := full(filter); ok {
			d.Entries.Set("Filter", n)
			return true
		}
	case pdf.PDFArray:
		renamed := false
		for i, v := range filter {
			if n, ok := full(v); ok {
				filter[i], renamed = n, true
			}
		}
		return renamed
	}
	return false
}

// walkStreamDicts calls fix for every pdf.PDFDict found within v's dictionary
// entries or array elements, using cycle protection. fix edits the dict in
// place and reports whether it changed it. A stream that fix has changed is
// not passed to fix again through its other slots.
func walkStreamDicts(v pdf.PDFValue, visited map[uintptr]bool, fix func(pdf.PDFDict) bool) {
	fixed := map[uintptr]bool{}
	walkGraph(v, visited, nil, func(child pdf.PDFValue) (pdf.PDFValue, bool) {
		cd, ok := child.(pdf.PDFDict)
		if !ok {
			return nil, false
		}
		ptr, isStream := pdf.ValuePointer(cd.Entries), cd.IsStream()
		if isStream && fixed[ptr] {
			return nil, false
		}
		if fix(cd) && isStream {
			fixed[ptr] = true
		}
		return nil, false
	})
}
