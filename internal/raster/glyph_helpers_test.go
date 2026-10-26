package raster

// glyphOutlineFromCFF parses a bare CFF program and returns gid's outline.
// The renderer parses a program once per font instead; tests use this to
// drive one glyph.
func glyphOutlineFromCFF(cff []byte, gid int) (GlyphPath, bool) {
	prog, ok := parseCFFProgram(cff)
	if !ok {
		return GlyphPath{}, false
	}
	return prog.outline(gid)
}

// glyphOutlineFromType1 parses a Type1 program and returns the named
// glyph's outline, the Type1 counterpart of glyphOutlineFromCFF.
func glyphOutlineFromType1(fontData []byte, glyphName string) (GlyphPath, bool) {
	prog, ok := parseType1Program(fontData)
	if !ok {
		return GlyphPath{}, false
	}
	return prog.outline(glyphName)
}
