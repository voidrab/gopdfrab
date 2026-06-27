package convert

import (
	_ "embed"
	"encoding/binary"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
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
	appearanceFontDict pdf.PDFDict
)

// appearanceFont returns the shared, PDF/A-1b-conformant simple TrueType
// font object used by synthesized appearance streams (fixups_appearance.go),
// building it once and reusing the same pdf.PDFDict value on every call so the
// writer's identity-based dedup (writer.go) coalesces every reference into a
// single embedded object. The font is embedded whole (no subset prefix on
// BaseFont) specifically to keep BaseFont outside the subset-tag pattern --
// SubsetGlyphCoverage (6.3.5) only applies to subset-tagged fonts, and
// Widths is built straight from hmtx, so AdvanceWidthMismatch (6.3.6) cannot
// fire either.
func appearanceFont() pdf.PDFDict {
	appearanceFontOnce.Do(func() {
		appearanceFontDict = buildAppearanceFont()
	})
	return appearanceFontDict
}

func buildAppearanceFont() pdf.PDFDict {
	tables, _ := verify.ParseSfnt(liberationSansTTF)
	cmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))

	const firstChar, lastChar = 32, 255
	widths := make(pdf.PDFArray, lastChar-firstChar+1)
	for cc := firstChar; cc <= lastChar; cc++ {
		w := 0
		if unicode := verify.WinAnsiToUnicode[cc]; unicode != 0 {
			if gid, ok := cmap[unicode]; ok {
				if aw := verify.TTAdvanceWidth(tables, int(gid)); aw >= 0 {
					w = aw
				}
			}
		}
		widths[cc-firstChar] = pdf.PDFInteger(w)
	}

	fontFile := pdf.NewPDFDict()
	fontFile.Entries["Length1"] = pdf.PDFInteger(len(liberationSansTTF))
	if err := writer.SetStreamFlate(&fontFile, liberationSansTTF); err != nil {
		return pdf.PDFDict{}
	}

	desc := pdf.NewPDFDict()
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["FontName"] = pdf.PDFName{Value: "LiberationSans"}
	desc.Entries["Flags"] = pdf.PDFInteger(32) // Nonsymbolic.
	desc.Entries["FontBBox"] = ttScaledBBox(tables)
	desc.Entries["ItalicAngle"] = pdf.PDFInteger(0)
	desc.Entries["Ascent"] = pdf.PDFInteger(liberationSansAscent)
	desc.Entries["Descent"] = pdf.PDFInteger(liberationSansDescent)
	desc.Entries["CapHeight"] = pdf.PDFInteger(liberationSansCapHeight(tables))
	desc.Entries["StemV"] = pdf.PDFInteger(liberationSansStemV)
	desc.Entries["MissingWidth"] = pdf.PDFInteger(0)
	desc.Entries["FontFile2"] = fontFile

	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "LiberationSans"}
	font.Entries["FirstChar"] = pdf.PDFInteger(firstChar)
	font.Entries["LastChar"] = pdf.PDFInteger(lastChar)
	font.Entries["Widths"] = widths
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	font.Entries["FontDescriptor"] = desc
	return font
}

// ttScaledBBox reads head's glyph bounding box and scales it to PDF's
// 1000-unit em, the same scaling ttAdvanceWidth applies to hmtx widths.
func ttScaledBBox(tables map[string][]byte) pdf.PDFArray {
	head := tables["head"]
	if len(head) < 44 {
		return pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0)}
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	scale := func(off int) pdf.PDFInteger {
		v := int(int16(binary.BigEndian.Uint16(head[off:])))
		return pdf.PDFInteger(v * 1000 / upm)
	}
	return pdf.PDFArray{scale(36), scale(38), scale(40), scale(42)}
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
