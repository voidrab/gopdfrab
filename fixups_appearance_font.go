package pdfrab

import (
	_ "embed"
	"encoding/binary"
	"sync"
)

// liberationSansTTF is the Liberation Sans (SIL OFL 1.1) regular face,
// metric-compatible with Helvetica/Arial, used to render text into
// synthesized form-field appearance streams (fixups_appearance.go). See
// assets/fonts/LICENSE.
//
//go:embed assets/fonts/LiberationSans-Regular.ttf
var liberationSansTTF []byte

// Liberation Sans FontDescriptor metrics with no programmatic source (head/
// OS2/post cover FontBBox/ItalicAngle/CapHeight; these don't map to any
// table field) -- read once from the font and hardcoded, since the bundled
// face never changes. StemV has no source at all; 80 is the typical value
// for a Regular weight.
const (
	liberationSansAscent  = 728
	liberationSansDescent = -211
	liberationSansStemV   = 80
)

var (
	appearanceFontOnce sync.Once
	appearanceFontDict PDFDict
)

// appearanceFont returns the shared, PDF/A-1b-conformant simple TrueType
// font object used by synthesized appearance streams (fixups_appearance.go),
// building it once and reusing the same PDFDict value on every call so the
// writer's identity-based dedup (writer.go) coalesces every reference into a
// single embedded object. The font is embedded whole (no subset prefix on
// BaseFont) specifically to keep BaseFont outside the subset-tag pattern --
// SubsetGlyphCoverage (6.3.5) only applies to subset-tagged fonts, and
// Widths is built straight from hmtx, so AdvanceWidthMismatch (6.3.6) cannot
// fire either.
func appearanceFont() PDFDict {
	appearanceFontOnce.Do(func() {
		appearanceFontDict = buildAppearanceFont()
	})
	return appearanceFontDict
}

func buildAppearanceFont() PDFDict {
	tables, _ := parseSfnt(liberationSansTTF)
	cmap := parseCmapFormat4(ttWindowsBMPCmap(tables))

	const firstChar, lastChar = 32, 255
	widths := make(PDFArray, lastChar-firstChar+1)
	for cc := firstChar; cc <= lastChar; cc++ {
		w := 0
		if unicode := winAnsiToUnicode[cc]; unicode != 0 {
			if gid, ok := cmap[unicode]; ok {
				if aw := ttAdvanceWidth(tables, int(gid)); aw >= 0 {
					w = aw
				}
			}
		}
		widths[cc-firstChar] = PDFInteger(w)
	}

	fontFile := NewPDFDict()
	fontFile.Entries["Length1"] = PDFInteger(len(liberationSansTTF))
	fontFile.HasStream = true
	fontFile.RawStream = liberationSansTTF
	MarkStreamDirty(&fontFile)

	desc := NewPDFDict()
	desc.Entries["Type"] = PDFName{Value: "FontDescriptor"}
	desc.Entries["FontName"] = PDFName{Value: "LiberationSans"}
	desc.Entries["Flags"] = PDFInteger(32) // Nonsymbolic.
	desc.Entries["FontBBox"] = ttScaledBBox(tables)
	desc.Entries["ItalicAngle"] = PDFInteger(0)
	desc.Entries["Ascent"] = PDFInteger(liberationSansAscent)
	desc.Entries["Descent"] = PDFInteger(liberationSansDescent)
	desc.Entries["CapHeight"] = PDFInteger(liberationSansCapHeight(tables))
	desc.Entries["StemV"] = PDFInteger(liberationSansStemV)
	desc.Entries["MissingWidth"] = PDFInteger(0)
	desc.Entries["FontFile2"] = fontFile

	font := NewPDFDict()
	font.Entries["Type"] = PDFName{Value: "Font"}
	font.Entries["Subtype"] = PDFName{Value: "TrueType"}
	font.Entries["BaseFont"] = PDFName{Value: "LiberationSans"}
	font.Entries["FirstChar"] = PDFInteger(firstChar)
	font.Entries["LastChar"] = PDFInteger(lastChar)
	font.Entries["Widths"] = widths
	font.Entries["Encoding"] = PDFName{Value: "WinAnsiEncoding"}
	font.Entries["FontDescriptor"] = desc
	return font
}

// ttScaledBBox reads head's glyph bounding box and scales it to PDF's
// 1000-unit em, the same scaling ttAdvanceWidth applies to hmtx widths.
func ttScaledBBox(tables map[string][]byte) PDFArray {
	head := tables["head"]
	if len(head) < 44 {
		return PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(0), PDFInteger(0)}
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	scale := func(off int) PDFInteger {
		v := int(int16(binary.BigEndian.Uint16(head[off:])))
		return PDFInteger(v * 1000 / upm)
	}
	return PDFArray{scale(36), scale(38), scale(40), scale(42)}
}

// liberationSansCapHeight reads OS/2's sCapHeight (present from OS/2 version
// 2), scaled to PDF's 1000-unit em.
func liberationSansCapHeight(tables map[string][]byte) int {
	os2 := tables["OS/2"]
	head := tables["head"]
	if len(os2) < 90 || len(head) < 20 {
		return liberationSansAscent // reasonable fallback, never hit for the bundled face
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	capHeight := int(int16(binary.BigEndian.Uint16(os2[88:90])))
	return capHeight * 1000 / upm
}
