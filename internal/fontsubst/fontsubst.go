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

// Files lists every bundled font file, by the name a Source is asked for: the
// gzip-compressed TrueType programs of the fonts directory.
var Files []string

// LiberationSansRegular returns the Liberation Sans regular face,
// metric-compatible with Helvetica/Arial.
var LiberationSansRegular = bundled("LiberationSans-Regular.ttf.gz")

var (
	libSansBold        = bundled("LiberationSans-Bold.ttf.gz")
	libSansItalic      = bundled("LiberationSans-Italic.ttf.gz")
	libSansBoldItalic  = bundled("LiberationSans-BoldItalic.ttf.gz")
	libSerifRegular    = bundled("LiberationSerif-Regular.ttf.gz")
	libSerifBold       = bundled("LiberationSerif-Bold.ttf.gz")
	libSerifItalic     = bundled("LiberationSerif-Italic.ttf.gz")
	libSerifBoldItalic = bundled("LiberationSerif-BoldItalic.ttf.gz")
	libMonoRegular     = bundled("LiberationMono-Regular.ttf.gz")
	libMonoBold        = bundled("LiberationMono-Bold.ttf.gz")
	libMonoItalic      = bundled("LiberationMono-Italic.ttf.gz")
	libMonoBoldItalic  = bundled("LiberationMono-BoldItalic.ttf.gz")
)

// NotoSymbols2 and NotoSymbols return fallback faces for symbol glyphs the
// Liberation faces do not cover.
var (
	NotoSymbols2 = bundled("NotoSansSymbols2-Regular.ttf.gz")
	NotoSymbols  = bundled("NotoSansSymbols-VariableFont_wght.ttf.gz")
)

// Source returns the gzip-compressed bytes of the bundled font file name, one
// of Files. It may be slow, for example when it fetches over a network. It is
// called at most once for each file after each SetSource, and again for a file
// after a failure.
type Source func(name string) ([]byte, error)

var (
	mu         sync.Mutex // guards source and generation
	source     Source     // nil for the built-in fonts
	generation int        // counts the sources set, so that fonts of an earlier one are dropped
	fonts      = map[string]*font{}
)

// font is one bundled font file, as loaded from the source of a generation.
// Its own lock lets a slow fetch of one font leave the others free.
type font struct {
	name string
	mu   sync.Mutex
	gen  int
	z    []byte // fetched and not yet inflated
	data []byte // inflated
}

// SetSource makes src the source of the fonts, in place of those built in, and
// forgets the fonts loaded so far; a nil src restores the built-in ones. It is
// meant to be called once, before any font is used: a render or conversion
// running meanwhile may find fonts missing.
func SetSource(src Source) {
	mu.Lock()
	defer mu.Unlock()
	source = src
	generation++
}

// current returns the source in use, or the built-in source when none is set,
// and the generation it belongs to.
func current() (Source, int) {
	mu.Lock()
	defer mu.Unlock()
	if source == nil {
		return builtin, generation
	}
	return source, generation
}

// fetch returns the compressed bytes of the font from src, fetching them only
// if none are held for generation gen. f.mu must be held.
func (f *font) fetch(src Source, gen int) ([]byte, error) {
	if f.gen != gen {
		f.gen, f.z, f.data = gen, nil, nil
	}
	if f.z == nil {
		z, err := src(f.name)
		if err != nil {
			return nil, fmt.Errorf("fontsubst: loading %s: %w", f.name, err)
		}
		f.z = z
	}
	return f.z, nil
}

// load returns the inflated font, loading it on its first use. A failure is
// not remembered, so that the next use tries again.
func (f *font) load() ([]byte, error) {
	src, gen := current()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gen == gen && f.data != nil {
		return f.data, nil
	}
	z, err := f.fetch(src, gen)
	if err != nil {
		return nil, err
	}
	b, err := inflate(z)
	if err != nil {
		f.z = nil
		return nil, fmt.Errorf("fontsubst: %s: %w", f.name, err)
	}
	f.z, f.data = nil, b
	return b, nil
}

// bundled registers the font file name and returns a function that gives the
// font's bytes, loading them on the first call. The function returns nil while
// the font cannot be loaded, and a nil face covers no glyphs. Callers must not
// modify the bytes.
func bundled(name string) func() []byte {
	f := &font{name: name}
	fonts[name] = f
	Files = append(Files, name)
	return func() []byte {
		b, _ := f.load()
		return b
	}
}

// Preload fetches every bundled font from the source set, all at once, and
// returns the errors. Inflating each font is left to its first use. With the
// built-in fonts it does nothing.
func Preload() error {
	mu.Lock()
	custom := source != nil
	mu.Unlock()
	if builtIn && !custom {
		return nil
	}
	src, gen := current()
	errs := make([]error, len(Files))
	var wg sync.WaitGroup
	for i, name := range Files {
		f := fonts[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.gen != gen || f.data == nil {
				_, errs[i] = f.fetch(src, gen)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
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
