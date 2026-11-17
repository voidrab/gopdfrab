package gopdfrab

import (
	"slices"

	"github.com/voidrab/gopdfrab/internal/fontsubst"
)

// SetFontSource makes src the source of the substitute fonts that gopdfrab
// draws and embeds for fonts a document uses without embedding them, in place
// of the built-in fonts; a nil src restores those. The src function returns
// the gzip-compressed bytes of the named font file, one of FontFiles, as they
// are in gopdfrab's internal/fontsubst/fonts directory. It may be called
// concurrently for different fonts, and again for a font after a failure.
//
// A build with the gopdfrab_nofonts tag leaves the built-in fonts out, for a
// program, such as a WebAssembly build, that fetches them only when a document
// needs one. Call SetFontSource before rendering or converting: the fonts
// loaded so far are forgotten.
//
// A page rendered while a font cannot be loaded leaves out the glyphs it would
// draw. A conversion first fetches every font, as PreloadFonts does, and fails
// if the source returns an error for one.
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
