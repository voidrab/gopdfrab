package fontprog

import (
	_ "embed"
	"strings"
	"sync"
)

// glyphList is the Adobe Glyph List, version 2.0.
//
//go:embed assets/glyphlist.txt
var glyphList string

// glyphListNames is the set of glyph names glyphList defines, built on first
// use.
var glyphListNames = sync.OnceValue(func() map[string]bool {
	names := make(map[string]bool, 4300)
	for line := range strings.Lines(glyphList) {
		if name, _, ok := strings.Cut(line, ";"); ok && !strings.HasPrefix(name, "#") {
			names[name] = true
		}
	}
	return names
})

// InAdobeGlyphList reports whether name is a glyph name the Adobe Glyph List
// defines. The list itself omits .notdef; veraPDF's copy of it adds the name,
// so it counts here too.
func InAdobeGlyphList(name string) bool {
	return name == ".notdef" || glyphListNames()[name]
}
