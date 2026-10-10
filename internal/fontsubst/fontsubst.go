// Package fontsubst bundles the substitute fonts gopdfrab embeds in place of
// fonts a document references but does not embed: the Liberation Sans, Serif
// and Mono families (metric-compatible with Helvetica, Times and Courier) and
// two Noto symbol faces. The converter embeds them when it substitutes a font,
// and the rasterizer draws with them. All are under the SIL Open Font License
// 1.1; see fonts/OFL.txt and the repository NOTICE.
package fontsubst

import (
	_ "embed"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// LiberationSansRegular is the Liberation Sans regular face, metric-compatible
// with Helvetica/Arial.
//
//go:embed fonts/LiberationSans-Regular.ttf
var LiberationSansRegular []byte

//go:embed fonts/LiberationSans-Bold.ttf
var libSansBold []byte

//go:embed fonts/LiberationSans-Italic.ttf
var libSansItalic []byte

//go:embed fonts/LiberationSans-BoldItalic.ttf
var libSansBoldItalic []byte

//go:embed fonts/LiberationSerif-Regular.ttf
var libSerifRegular []byte

//go:embed fonts/LiberationSerif-Bold.ttf
var libSerifBold []byte

//go:embed fonts/LiberationSerif-Italic.ttf
var libSerifItalic []byte

//go:embed fonts/LiberationSerif-BoldItalic.ttf
var libSerifBoldItalic []byte

//go:embed fonts/LiberationMono-Regular.ttf
var libMonoRegular []byte

//go:embed fonts/LiberationMono-Bold.ttf
var libMonoBold []byte

//go:embed fonts/LiberationMono-Italic.ttf
var libMonoItalic []byte

//go:embed fonts/LiberationMono-BoldItalic.ttf
var libMonoBoldItalic []byte

// NotoSymbols2 and NotoSymbols are fallback faces for symbol glyphs the
// Liberation faces do not cover.
//
//go:embed fonts/NotoSansSymbols2-Regular.ttf
var NotoSymbols2 []byte

//go:embed fonts/NotoSansSymbols-VariableFont_wght.ttf
var NotoSymbols []byte

// Face is a bundled Liberation face and the style it was picked for.
type Face struct {
	Data                            []byte
	Serif, FixedPitch, Italic, Bold bool
}

// Pick chooses a bundled Liberation style to substitute for a
// non-conformant font, from its FontDescriptor flags/weight and BaseFont
// name (descriptors are sometimes incomplete, so the name is a fallback).
func Pick(desc pdf.PDFDict, baseFont string) Face {
	flags := 0
	if f, ok := desc.Entries.Get("Flags").(pdf.PDFInteger); ok {
		flags = int(f)
	}
	lower := strings.ToLower(baseFont)
	serif := flags&0x2 != 0 || strings.Contains(lower, "times") || strings.Contains(lower, "serif") ||
		strings.Contains(lower, "georgia") || strings.Contains(lower, "garamond") || strings.Contains(lower, "minion")
	fixedPitch := flags&0x1 != 0 || strings.Contains(lower, "courier") || strings.Contains(lower, "mono") ||
		strings.Contains(lower, "consol")
	italic := flags&0x40 != 0 || strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
	bold := flags&0x40000 != 0 || strings.Contains(lower, "bold")
	if fw, ok := desc.Entries.Get("FontWeight").(pdf.PDFInteger); ok && fw >= 600 {
		bold = true
	}

	pick := func(regular, b, i, bi []byte) []byte {
		switch {
		case bold && italic:
			return bi
		case bold:
			return b
		case italic:
			return i
		default:
			return regular
		}
	}
	var data []byte
	switch {
	case fixedPitch:
		data = pick(libMonoRegular, libMonoBold, libMonoItalic, libMonoBoldItalic)
	case serif:
		data = pick(libSerifRegular, libSerifBold, libSerifItalic, libSerifBoldItalic)
	default:
		data = pick(LiberationSansRegular, libSansBold, libSansItalic, libSansBoldItalic)
	}
	return Face{Data: data, Serif: serif, FixedPitch: fixedPitch, Italic: italic, Bold: bold}
}
