// Package fontsubst bundles the substitute fonts gopdfrab embeds in place of
// fonts a document references but does not embed: the Liberation Sans, Serif
// and Mono families (metric-compatible with Helvetica, Times and Courier) and
// two Noto symbol faces. The converter embeds them when it substitutes a font,
// and the rasterizer draws with them. All are under the SIL Open Font License
// 1.1; see fonts/OFL.txt and the repository NOTICE.
//
// The fonts are stored gzip-compressed, which halves what they add to every
// binary, and each is inflated the first time it is used. A program that
// never needs a substitute font never inflates one.
package fontsubst

import (
	"bytes"
	_ "embed"
	"io"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

var (
	//go:embed fonts/LiberationSans-Regular.ttf.gz
	libSansRegularGz []byte
	//go:embed fonts/LiberationSans-Bold.ttf.gz
	libSansBoldGz []byte
	//go:embed fonts/LiberationSans-Italic.ttf.gz
	libSansItalicGz []byte
	//go:embed fonts/LiberationSans-BoldItalic.ttf.gz
	libSansBoldItalicGz []byte
	//go:embed fonts/LiberationSerif-Regular.ttf.gz
	libSerifRegularGz []byte
	//go:embed fonts/LiberationSerif-Bold.ttf.gz
	libSerifBoldGz []byte
	//go:embed fonts/LiberationSerif-Italic.ttf.gz
	libSerifItalicGz []byte
	//go:embed fonts/LiberationSerif-BoldItalic.ttf.gz
	libSerifBoldItalicGz []byte
	//go:embed fonts/LiberationMono-Regular.ttf.gz
	libMonoRegularGz []byte
	//go:embed fonts/LiberationMono-Bold.ttf.gz
	libMonoBoldGz []byte
	//go:embed fonts/LiberationMono-Italic.ttf.gz
	libMonoItalicGz []byte
	//go:embed fonts/LiberationMono-BoldItalic.ttf.gz
	libMonoBoldItalicGz []byte
	//go:embed fonts/NotoSansSymbols2-Regular.ttf.gz
	notoSymbols2Gz []byte
	//go:embed fonts/NotoSansSymbols-VariableFont_wght.ttf.gz
	notoSymbolsGz []byte
)

// LiberationSansRegular returns the Liberation Sans regular face,
// metric-compatible with Helvetica/Arial.
var LiberationSansRegular = bundled(libSansRegularGz)

var (
	libSansBold        = bundled(libSansBoldGz)
	libSansItalic      = bundled(libSansItalicGz)
	libSansBoldItalic  = bundled(libSansBoldItalicGz)
	libSerifRegular    = bundled(libSerifRegularGz)
	libSerifBold       = bundled(libSerifBoldGz)
	libSerifItalic     = bundled(libSerifItalicGz)
	libSerifBoldItalic = bundled(libSerifBoldItalicGz)
	libMonoRegular     = bundled(libMonoRegularGz)
	libMonoBold        = bundled(libMonoBoldGz)
	libMonoItalic      = bundled(libMonoItalicGz)
	libMonoBoldItalic  = bundled(libMonoBoldItalicGz)
)

// NotoSymbols2 and NotoSymbols return fallback faces for symbol glyphs the
// Liberation faces do not cover.
var (
	NotoSymbols2 = bundled(notoSymbols2Gz)
	NotoSymbols  = bundled(notoSymbolsGz)
)

// bundled returns a function that inflates the gzip data z on its first call
// and returns those same bytes on every call. Callers must not modify them.
func bundled(z []byte) func() []byte {
	return sync.OnceValue(func() []byte { return inflate(z) })
}

// inflate decompresses the gzip data z. The data is embedded at build time
// and every bundled font is inflated by the tests, so a failure here is a
// broken build, not bad input, and panics.
func inflate(z []byte) []byte {
	r, err := gzip.NewReader(bytes.NewReader(z))
	if err == nil {
		var b []byte
		if b, err = io.ReadAll(r); err == nil {
			return b
		}
	}
	panic("fontsubst: corrupt bundled font: " + err.Error())
}

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

	pick := func(regular, b, i, bi func() []byte) []byte {
		switch {
		case bold && italic:
			return bi()
		case bold:
			return b()
		case italic:
			return i()
		default:
			return regular()
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
