// Package fontedit rewrites embedded font programs for the converter: it
// subsets TrueType programs, adds or trims cmap subtables, promotes empty
// glyphs to explicit zero-contour ones, inlines Type 1 width subroutines and
// rounds fractional CFF width defaults.
//
// It parses through fontprog and is imported only by convert, which keeps
// verification -- and so the light gopdfrab/verify package -- free of
// font-writing code.
package fontedit
