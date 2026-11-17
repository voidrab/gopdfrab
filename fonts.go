package gopdfrab

import (
	"slices"

	"github.com/voidrab/gopdfrab/internal/fontsubst"
)

// SetFontSource makes src the source of the substitute fonts, which gopdfrab
// draws and embeds for the fonts a document uses without embedding them, in
// place of the built-in ones; a nil src restores those. A build with the
// gopdfrab_nofonts tag leaves the fonts out, for a program, such as a
// WebAssembly build, that fetches them only when a document needs one. Call
// it before rendering or converting: the fonts loaded so far are forgotten. src returns the gzip-compressed bytes of the font file name,
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

// PreloadFonts fetches every substitute font from the source set with
// SetFontSource, all at once, so that none is missing later, and returns the
// errors. With the built-in fonts it does nothing.
func PreloadFonts() error { return fontsubst.Preload() }
