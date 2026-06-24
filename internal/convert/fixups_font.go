package convert

import (
	"strconv"

	"github.com/voidrab/gopdfrab/internal/pdf"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file registers Fixers for the purely-structural composite-font
// violations classified as "easy" in the converter plan: a missing
// CIDFontType2 /CIDToGIDMap (6.3.3.2), and a Type0 font's CMap
// CIDSystemInfo/WMode disagreeing with its descendant CIDFont/CMap stream
// (6.3.3.1/6.3.3.3). They deliberately do not touch the TrueType encoding
// checks (6.3.7) -- normalizing Encoding/cmap subtables can change glyph
// mapping and therefore rendered appearance, unlike adding /Identity (the
// spec default) or reconciling metadata that already describes the same
// embedded data two different ways.

func init() {
	registerFixer(fontDictFixer{})
	registerFixer(type0FontFixer{})
}

// fontDictFixer remediates Checks.Font.CIDToGIDMapMissing by adding the
// spec-default /CIDToGIDMap /Identity to any CIDFontType2 descendant font
// that lacks it, mirroring the detection in validateFontDict
// (checks_font.go). Adding /Identity is always valid -- it IS the PDF
// default applied when the key is absent -- so it never changes rendered
// appearance and never breaks a conformant file, which is why it's safe to
// apply unconditionally rather than only for non-invisible-only fonts like
// the check itself.
type fontDictFixer struct{}

func (fontDictFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Font.CIDToGIDMapMissing
}

func (f fontDictFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (fontDictFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries["Subtype"] != pdf.PDFName{Value: "CIDFontType2"}) {
			return
		}
		if d.Entries["CIDToGIDMap"] == nil {
			d.Entries["CIDToGIDMap"] = pdf.PDFName{Value: "Identity"}
			*changed = true
		}
	}, true
}

// type0FontFixer remediates Type0 font CIDSystemInfo and CMap WMode
// mismatches, mirroring validateType0Font/validateCMapWMode
// (checks_font.go/checks_font_program.go). The descendant CIDFont's
// CIDSystemInfo is authoritative -- it describes the glyph data actually
// embedded -- so a mismatched CMap CIDSystemInfo is overwritten to match it;
// a mismatched dictionary /WMode is overwritten to match the value the CMap
// stream itself declares.
type type0FontFixer struct{}

func (type0FontFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Font.CIDSystemInfoMismatch, pdf.Checks.Font.CMapWModeInconsistent:
		return true
	}
	return false
}

func (f type0FontFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (type0FontFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries["Subtype"] != pdf.PDFName{Value: "Type0"}) {
			return
		}
		cmap, ok := d.Entries["Encoding"].(pdf.PDFDict)
		if !ok {
			return
		}

		if cid := verify.DescendantCIDFont(d); cid.Entries != nil {
			cmapCSI, hasCmapCSI := cmap.Entries["CIDSystemInfo"].(pdf.PDFDict)
			cidCSI, hasCidCSI := cid.Entries["CIDSystemInfo"].(pdf.PDFDict)
			if hasCmapCSI && hasCidCSI && !verify.SameCIDSystemInfo(cmapCSI, cidCSI) {
				cmap.Entries["CIDSystemInfo"] = cid.Entries["CIDSystemInfo"]
				*changed = true
			}
		}

		if !cmap.HasStream {
			return
		}
		dictWMode, ok := cmap.Entries["WMode"].(pdf.PDFInteger)
		if !ok {
			return
		}
		data, err := pdf.DecodeStream(cmap)
		if err != nil {
			return
		}
		m := verify.WmodeRe.FindSubmatch(data)
		if m == nil {
			return
		}
		if streamWMode, err := strconv.Atoi(string(m[1])); err == nil && int(dictWMode) != streamWMode {
			cmap.Entries["WMode"] = pdf.PDFInteger(streamWMode)
			*changed = true
		}
	}, true
}
