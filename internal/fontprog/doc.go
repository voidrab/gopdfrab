// Package fontprog reads the font programs embedded in PDF files -- TrueType
// (sfnt) tables, CFF (Type1C / CIDFontType0C) and Type 1 -- together with the
// glyph-name and encoding tables needed to interpret them.
//
// It is shared by the verifier, which uses it to check subset completeness
// (6.3.5) and width consistency (6.3.6), and by the converter, whose font
// rewriting lives one layer up in fontedit. PackSfnt and BuildCmapFormat4Table
// are the only writers here: the sfnt container codec both layers build on.
// Every parser is defensive: malformed input yields a zero value or ok=false,
// never a panic.
package fontprog
