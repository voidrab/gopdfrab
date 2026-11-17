// Package fontsubst bundles the substitute fonts gopdfrab embeds in place of
// fonts a document references but does not embed: the Liberation Sans, Serif
// and Mono families (metric-compatible with Helvetica, Times and Courier) and
// two Noto symbol faces. The converter embeds them when it substitutes a font,
// and the rasterizer draws with them. All are under the SIL Open Font License
// 1.1; see fonts/OFL.txt and the repository NOTICE.
//
// The fonts are stored gzip-compressed and each is inflated the first time
// it is used, so a program that never needs a substitute font never inflates
// one. A build with the gopdfrab_nofonts tag leaves them out of the binary,
// for programs that fetch them only when needed, as a WebAssembly build does
// over the network; such a program supplies them through SetSource.
package fontsubst

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// The bundled font files, by the names a Source serves them under.
const (
	libSansRegularFile     = "LiberationSans-Regular.ttf.gz"
	libSansBoldFile        = "LiberationSans-Bold.ttf.gz"
	libSansItalicFile      = "LiberationSans-Italic.ttf.gz"
	libSansBoldItalicFile  = "LiberationSans-BoldItalic.ttf.gz"
	libSerifRegularFile    = "LiberationSerif-Regular.ttf.gz"
	libSerifBoldFile       = "LiberationSerif-Bold.ttf.gz"
	libSerifItalicFile     = "LiberationSerif-Italic.ttf.gz"
	libSerifBoldItalicFile = "LiberationSerif-BoldItalic.ttf.gz"
	libMonoRegularFile     = "LiberationMono-Regular.ttf.gz"
	libMonoBoldFile        = "LiberationMono-Bold.ttf.gz"
	libMonoItalicFile      = "LiberationMono-Italic.ttf.gz"
	libMonoBoldItalicFile  = "LiberationMono-BoldItalic.ttf.gz"
	notoSymbols2File       = "NotoSansSymbols2-Regular.ttf.gz"
	notoSymbolsFile        = "NotoSansSymbols-VariableFont_wght.ttf.gz"
)

// Files lists every bundled font file, by the name a Source is asked for. The
// files are those of the fonts directory, gzip-compressed TrueType programs.
var Files = []string{
	libSansRegularFile, libSansBoldFile, libSansItalicFile, libSansBoldItalicFile,
	libSerifRegularFile, libSerifBoldFile, libSerifItalicFile, libSerifBoldItalicFile,
	libMonoRegularFile, libMonoBoldFile, libMonoItalicFile, libMonoBoldItalicFile,
	notoSymbols2File, notoSymbolsFile,
}

// LiberationSansRegular returns the Liberation Sans regular face,
// metric-compatible with Helvetica/Arial.
var LiberationSansRegular = bundled(libSansRegularFile)

var (
	libSansBold        = bundled(libSansBoldFile)
	libSansItalic      = bundled(libSansItalicFile)
	libSansBoldItalic  = bundled(libSansBoldItalicFile)
	libSerifRegular    = bundled(libSerifRegularFile)
	libSerifBold       = bundled(libSerifBoldFile)
	libSerifItalic     = bundled(libSerifItalicFile)
	libSerifBoldItalic = bundled(libSerifBoldItalicFile)
	libMonoRegular     = bundled(libMonoRegularFile)
	libMonoBold        = bundled(libMonoBoldFile)
	libMonoItalic      = bundled(libMonoItalicFile)
	libMonoBoldItalic  = bundled(libMonoBoldItalicFile)
)

// NotoSymbols2 and NotoSymbols return fallback faces for symbol glyphs the
// Liberation faces do not cover.
var (
	NotoSymbols2 = bundled(notoSymbols2File)
	NotoSymbols  = bundled(notoSymbolsFile)
)

// Source returns the gzip-compressed bytes of the bundled font file name, one
// of Files. It may be slow, as a fetch over the network is, and is called at
// most once for each file until a call fails.
type Source func(name string) ([]byte, error)

// ErrNoSource is returned for a font of a build that leaves the fonts out,
// with the gopdfrab_nofonts tag, before SetSource has been called.
var ErrNoSource = errors.New("fontsubst: the fonts are not built in and no source is set")

var (
	mu     sync.Mutex
	source Source                // nil for the embedded fonts
	loaded = map[string][]byte{} // inflated fonts, by file name
)

// SetSource makes src the source of the fonts, in place of those built in, and
// forgets the fonts loaded so far. A nil src restores the built-in fonts.
func SetSource(src Source) {
	mu.Lock()
	defer mu.Unlock()
	source = src
	loaded = map[string][]byte{}
}

// load returns the inflated font file name, loading it on its first use. A
// failure is not remembered, so that the next use tries again.
func load(name string) ([]byte, error) {
	mu.Lock()
	defer mu.Unlock()
	if b, ok := loaded[name]; ok {
		return b, nil
	}
	var z []byte
	if source == nil {
		var ok bool
		if z, ok = embedded[name]; !ok {
			return nil, ErrNoSource
		}
	} else {
		var err error
		if z, err = source(name); err != nil {
			return nil, fmt.Errorf("fontsubst: loading %s: %w", name, err)
		}
	}
	b, err := inflate(z)
	if err != nil {
		return nil, fmt.Errorf("fontsubst: %s: %w", name, err)
	}
	loaded[name] = b
	return b, nil
}

// bundled returns a function that returns the font file name, loading it on
// its first call and returning those same bytes on every call after. It
// returns nil while the font cannot be loaded, which the faces' users treat
// as a face that covers nothing. Callers must not modify the bytes.
func bundled(name string) func() []byte {
	return func() []byte {
		b, _ := load(name)
		return b
	}
}

// Preload loads every bundled font, so that no later use can fail, and returns
// the first error. With the built-in fonts it costs nothing: they are loaded
// on first use as ever.
func Preload() error {
	mu.Lock()
	builtIn := source == nil && embedded != nil
	mu.Unlock()
	if builtIn {
		return nil
	}
	for _, name := range Files {
		if _, err := load(name); err != nil {
			return err
		}
	}
	return nil
}

// inflate decompresses the gzip data z.
func inflate(z []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
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
