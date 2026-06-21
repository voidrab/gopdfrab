package pdfrab

// This file registers a Fixer for the one purely-structural composite-font
// violation classified as "easy" in the converter plan: a missing
// CIDFontType2 /CIDToGIDMap (6.3.3.2). It deliberately does not touch the
// TrueType encoding checks (6.3.7) -- normalizing Encoding/cmap subtables
// can change glyph mapping and therefore rendered appearance, unlike adding
// /Identity, which is already the PDF spec default when the key is absent.

func init() {
	registerFixer(fontDictFixer{})
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

func (fontDictFixer) Applies(c Check) bool {
	return c == Checks.Font.CIDToGIDMapMissing
}

func (fontDictFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		if (d.Entries["Type"] != PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries["Subtype"] != PDFName{Value: "CIDFontType2"}) {
			return
		}
		if d.Entries["CIDToGIDMap"] == nil {
			d.Entries["CIDToGIDMap"] = PDFName{Value: "Identity"}
			changed = true
		}
	})
	return changed, nil
}
