package raster

import "github.com/voidrab/gopdfrab/internal/fontprog"

// glyphOutlineFromCFF parses a bare CFF program and returns gid's outline.
// The renderer parses a program once per font instead; tests use this to
// drive one glyph.
func glyphOutlineFromCFF(cff []byte, gid int) (GlyphPath, bool) {
	prog, ok := fontprog.ParseCFFCharstrings(cff)
	if !ok {
		return GlyphPath{}, false
	}
	return cffOutline(prog, gid)
}

// glyphOutlineFromType1 parses a Type1 program and returns the named
// glyph's outline, the Type1 counterpart of glyphOutlineFromCFF.
func glyphOutlineFromType1(fontData []byte, glyphName string) (GlyphPath, bool) {
	prog, ok := fontprog.ParseType1Charstrings(fontData)
	if !ok {
		return GlyphPath{}, false
	}
	return type1Outline(prog, glyphName)
}

// b139 encodes a small charstring operand (-107..107).
func b139(v int) byte { return byte(v + 139) }
