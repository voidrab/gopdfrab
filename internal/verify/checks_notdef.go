package verify

import (
	"encoding/binary"
	"fmt"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// winAnsiBullets holds the WinAnsiEncoding codes that ISO 32000-1 leaves
// undefined and veraPDF maps to the bullet glyph. Showing them is not a
// reference to .notdef.
var winAnsiBullets = map[int]bool{127: true, 129: true, 141: true, 143: true, 144: true, 157: true}

// validateNotdefReferences reports a font whose shown codes select the .notdef
// glyph (6.2.11.8), in every rendering mode. It decides which glyph a code
// selects the way veraPDF does: a simple font by the glyph name its Encoding
// gives the code, a composite font by whether its embedded program has a
// glyph for the CID. A font whose usage is unknown is not checked.
func validateNotdefReferences(v pdf.PDFDict, subtype string, desc pdf.PDFDict, ctx *ValidationContext) {
	switch subtype {
	case "Type1", "MMType1", "TrueType", "Type3":
		codes, known := ctx.usedCodesFor(v)
		if !known {
			return
		}
		table := simpleNotdefCodes(v, subtype, desc)
		if code, found := lowestWhere(codes, func(c int) bool { return c >= 0 && c < 256 && table[c] }); found {
			ctx.Report(pdf.Checks.Font.NotdefGlyphReference, v, fmt.Sprintf("character code %d selects the .notdef glyph", code))
		}
	case "Type0":
		cid := DescendantCIDFont(v)
		cids, known := ctx.usedCIDsFor(cid)
		if !known {
			return
		}
		present, ok := cidGlyphPresence(cid, ctx)
		if !ok {
			return
		}
		if ctx.showsPartialCode(cid) {
			ctx.Report(pdf.Checks.Font.NotdefGlyphReference, v, "a shown string ends in the middle of a two-byte character code, which selects the .notdef glyph")
			return
		}
		if c, found := lowestWhere(cids, func(c int) bool { return !present(c) }); found {
			ctx.Report(pdf.Checks.Font.NotdefGlyphReference, v, fmt.Sprintf("CID %d selects the .notdef glyph", c))
		}
	}
}

// NotdefSelector returns how many bytes one character code of font takes
// and which codes select its .notdef glyph, as the 6.2.11.8 check decides it,
// so that a repair can find them in the strings a content stream shows. ok
// is false for a font it cannot judge code by code: a composite font other
// than a horizontal Identity-H one, or one whose program is missing or
// unreadable.
func NotdefSelector(font pdf.PDFDict, ctx *ValidationContext) (codeLen int, notdef func(code int) bool, ok bool) {
	subtype, _ := font.Entries.Get("Subtype").(pdf.PDFName)
	switch subtype.Value {
	case "Type1", "MMType1", "TrueType", "Type3":
		desc, _ := font.Entries.Get("FontDescriptor").(pdf.PDFDict)
		table := simpleNotdefCodes(font, subtype.Value, desc)
		return 1, func(code int) bool { return code >= 0 && code < 256 && table[code] }, true
	case "Type0":
		if (font.Entries.Get("Encoding") != pdf.PDFName{Value: "Identity-H"}) {
			return 0, nil, false
		}
		present, ok := cidGlyphPresence(DescendantCIDFont(font), ctx)
		if !ok {
			return 0, nil, false
		}
		return 2, func(cid int) bool { return !present(cid) }, true
	}
	return 0, nil, false
}

// lowestWhere returns the lowest member of set that pred holds for, so that
// the code reported is the same on every run.
func lowestWhere(set map[int]bool, pred func(int) bool) (int, bool) {
	lowest, found := 0, false
	for c := range set {
		if (!found || c < lowest) && pred(c) {
			lowest, found = c, true
		}
	}
	return lowest, found
}

// simpleNotdefCodes returns which codes select .notdef in a simple font. The
// glyph name comes from the Encoding: a Differences entry, or else the
// WinAnsiEncoding or MacRomanEncoding base table. A symbolic TrueType font's
// Encoding is ignored. When the Encoding names no glyph for a code, a
// TrueType code selects .notdef only if it is 0 and the font has no embedded
// program, as veraPDF trusts the program for every other code.
func simpleNotdefCodes(v pdf.PDFDict, subtype string, desc pdf.PDFDict) (notdef [256]bool) {
	trueType := subtype == "TrueType"
	enc := v.Entries.Get("Encoding")
	if trueType && desc.Entries != nil {
		if flags, _ := desc.Entries.Get("Flags").(pdf.PDFInteger); flags&4 != 0 {
			enc = nil
		}
	}
	names, named := encodingGlyphNames(enc)
	for code := range notdef {
		notdef[code] = named[code] && names[code] == ".notdef"
	}
	if !named[0] && trueType && !HasEmbeddedProgram(desc, "FontFile2", "FontFile3") {
		notdef[0] = true
	}
	return notdef
}

// encodingGlyphNames returns the glyph name a simple font's Encoding gives
// each code, and which codes it gives one. Only the WinAnsiEncoding and
// MacRomanEncoding base encodings name glyphs, as veraPDF's tables do; an
// Encoding of StandardEncoding, for example, gives none.
func encodingGlyphNames(enc pdf.PDFValue) (names [256]string, named [256]bool) {
	base := enc
	if dict, ok := enc.(pdf.PDFDict); ok {
		base = dict.Entries.Get("BaseEncoding")
	}
	baseName, _ := base.(pdf.PDFName)
	var table *[256]string
	switch baseName.Value {
	case "WinAnsiEncoding":
		table = &fontprog.WinAnsiGlyphName
	case "MacRomanEncoding":
		table = &fontprog.MacRomanGlyphName
	}
	if table != nil {
		for code, name := range table {
			if name == "" {
				name = ".notdef"
			}
			names[code], named[code] = name, true
		}
		if table == &fontprog.WinAnsiGlyphName {
			for code := range winAnsiBullets {
				names[code] = "bullet"
			}
		}
	}
	if dict, ok := enc.(pdf.PDFDict); ok {
		fontprog.EachDifference(dict, func(code int, glyph string) {
			if code >= 0 && code < 256 {
				names[code], named[code] = glyph, true
			}
		})
	}
	return names, named
}

// cidGlyphPresence returns a function reporting whether a CID has a glyph in
// a CIDFont's embedded program, as veraPDF decides it. It returns false when
// the program is missing or unreadable. CID 0 never has a glyph, as it is the
// .notdef glyph.
func cidGlyphPresence(cid pdf.PDFDict, ctx *ValidationContext) (func(int) bool, bool) {
	subtype, _ := cid.Entries.Get("Subtype").(pdf.PDFName)
	desc, _ := cid.Entries.Get("FontDescriptor").(pdf.PDFDict)
	switch subtype.Value {
	case "CIDFontType2":
		ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
		if !ok {
			return nil, false
		}
		data, err := ctx.decodeStreamCached(ff)
		if err != nil {
			return nil, false
		}
		tables, ok := fontprog.ParseSfnt(data)
		if !ok {
			return nil, false
		}
		gidOf, ok := cidToGID(cid.Entries.Get("CIDToGIDMap"), ctx)
		if !ok {
			return nil, false
		}
		numGlyphs := fontprog.TTNumGlyphs(tables)
		return func(c int) bool {
			gid, mapped := gidOf(c)
			return c != 0 && mapped && gid < numGlyphs
		}, true
	case "CIDFontType0":
		ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict)
		if !ok {
			return nil, false
		}
		data, err := ctx.decodeStreamCached(ff)
		if err != nil {
			return nil, false
		}
		td, ok := fontprog.ParseCFFTopDict(data)
		if !ok || td.CSOffset < 0 || td.CSOffset+2 > len(data) {
			return nil, false
		}
		numGlyphs := int(binary.BigEndian.Uint16(data[td.CSOffset : td.CSOffset+2]))
		if !td.IsCIDKeyed {
			return func(c int) bool { return c != 0 && c < numGlyphs }, true
		}
		cids := fontprog.ParseCFFCharsetCIDs(data, td.CharsetOffset, numGlyphs)
		if cids == nil {
			return nil, false
		}
		inCharset := make(map[int]bool, len(cids))
		for gid, c := range cids {
			if gid != 0 {
				inCharset[c] = true
			}
		}
		return func(c int) bool { return inCharset[c] }, true
	}
	return nil, false
}

// cidToGID returns the CID-to-glyph mapping that a CIDToGIDMap value gives:
// the identity for Identity or no entry, or the two-byte glyph indices of a
// stream. CIDs past the end of the stream are unmapped. ok is false for any
// other value.
func cidToGID(c2g pdf.PDFValue, ctx *ValidationContext) (gidOf func(int) (int, bool), ok bool) {
	if c2g == nil || c2g == (pdf.PDFName{Value: "Identity"}) {
		return func(c int) (int, bool) { return c, true }, true
	}
	stream, isDict := c2g.(pdf.PDFDict)
	if !isDict || !stream.IsStream() {
		return nil, false
	}
	data, err := ctx.decodeStreamCached(stream)
	if err != nil {
		return nil, false
	}
	return func(c int) (int, bool) {
		if c < 0 || 2*c+2 > len(data) {
			return 0, false
		}
		return int(binary.BigEndian.Uint16(data[2*c:])), true
	}, true
}
