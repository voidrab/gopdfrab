package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// A Type 3 font's glyphs are content streams (ISO 32000-1 9.6.5), not
// outlines: each code maps through /Encoding's Differences to a /CharProcs
// entry, which is executed with the glyph's own /FontMatrix in place of the
// 1000-unit em every other font type uses. Everything else -- the graphics
// state, resources, the operator loop -- is the renderer's ordinary content
// path, so a Type 3 glyph can itself draw images, shadings and Form XObjects.

// buildType3FontInfo resolves a Type 3 font's CharProcs by code.
func buildType3FontInfo(font pdf.PDFDict) *fontInfo {
	fi := &fontInfo{bytesPerCode: 1, widths: map[int]float64{}, charProcs: map[int]pdf.PDFDict{}}

	// A Type 3 /FontMatrix is required and arbitrary; 1/1000 only matches the
	// common case of a font emulating the usual glyph space.
	fi.fontMatrix = Matrix{A: 0.001, D: 0.001}
	if m, err := pdf.FloatArray(font.Entries.Get("FontMatrix")); err == nil && len(m) == 6 {
		fi.fontMatrix = Matrix{A: m[0], B: m[1], C: m[2], D: m[3], E: m[4], F: m[5]}
	}

	firstChar, ok := font.Entries.Get("FirstChar").(pdf.PDFInteger)
	if !ok {
		firstChar = 0
	}
	if widths, ok := font.Entries.Get("Widths").(pdf.PDFArray); ok {
		for i, w := range widths {
			if v, ok := pdf.PDFNumberToFloat(w); ok {
				fi.widths[int(firstChar)+i] = v
			}
		}
	}

	fi.t3Resources, _ = font.Entries.Get("Resources").(pdf.PDFDict)
	procs, _ := font.Entries.Get("CharProcs").(pdf.PDFDict)
	names := resolveSimpleEncoding(font.Entries.Get("Encoding"))
	for code, name := range names {
		if name == "" {
			continue
		}
		if proc, ok := procs.Entries.Get(name).(pdf.PDFDict); ok && proc.HasStream {
			fi.charProcs[code] = proc
		}
	}
	return fi
}

// type3WordSpace returns the word spacing applied to a Type 3 code, which
// like any single-byte font applies only to code 32.
func type3WordSpace(code int, gs *renderState) float64 {
	if code == 32 {
		return gs.wordSpace
	}
	return 0
}

// drawType3Glyph executes one glyph's CharProc with the CTM set to map glyph
// space through the font matrix, the text state and the CTM in force. A code
// with no CharProc is reported, since the glyph is then simply missing.
// pageResources stands in when the font declares no /Resources of its own,
// per ISO 32000-1 9.6.5.
func (r *renderer) drawType3Glyph(fi *fontInfo, code int, pageResources pdf.PDFDict, gs *renderState) {
	proc, ok := fi.charProcs[code]
	if !ok {
		r.drop(dropType3)
		return
	}
	// A CharProc may Do a Form or show text again; share the Form recursion
	// guard rather than adding a second one.
	if r.depth > 12 {
		return
	}
	data, err := pdf.DecodeStream(proc)
	if err != nil {
		r.drop(dropType3)
		return
	}

	resources := fi.t3Resources
	if resources.Entries == nil {
		resources = pageResources
	}
	child := *gs
	// Glyph space -> text space (the font matrix) -> user space (font size,
	// horizontal scale and rise, then the text matrix) -> device.
	child.ctm = fi.fontMatrix.
		Mul(Matrix{A: gs.fontSize * gs.hScale, D: gs.fontSize, F: gs.rise}).
		Mul(gs.tm).
		Mul(gs.ctm)
	// The glyph procedure starts its own text object if it shows text at all.
	child.tm, child.tlm = IdentityMatrix, IdentityMatrix
	child.font = pdf.PDFDict{}

	r.depth++
	defer func() { r.depth-- }()
	r.execContent(data, resources, child)
}
