package gopdfrab

import (
	"slices"

	"github.com/voidrab/gopdfrab/internal/fontsubst"
)

// gopdfrab draws and embeds substitute fonts (Liberation Sans, Serif and Mono,
// and two Noto symbol faces) for the fonts a document uses without embedding
// them. They are built in, gzip-compressed, unless the program is built with
// the gopdfrab_nofonts tag, which leaves them out of the binary: a WebAssembly
// build, which is downloaded before it runs, then fetches them only when a
// document needs one. Such a program supplies them with SetFontSource.

// SetFontSource makes src the source of the substitute fonts in place of the
// built-in ones, and forgets those loaded so far; a nil src restores the
// built-in ones. src returns the gzip-compressed bytes of the font file name,
// one of FontFiles, as they are in gopdfrab's internal/fontsubst/fonts
// directory. It is called when a font is first needed, from the goroutine
// that renders or converts, and again after a failure.
//
// A page rendered while a font cannot be loaded leaves out the glyphs it
// would draw. A conversion loads every font first, as PreloadFonts does, and
// fails if one cannot be loaded.
func SetFontSource(src func(name string) ([]byte, error)) {
	fontsubst.SetSource(src)
}

// FontFiles returns the names of the substitute font files a source set with
// SetFontSource serves.
func FontFiles() []string { return slices.Clone(fontsubst.Files) }

// PreloadFonts loads every substitute font from the source set with
// SetFontSource, so that none is missing later, and returns the first error.
// With the built-in fonts it does nothing.
func PreloadFonts() error { return fontsubst.Preload() }
