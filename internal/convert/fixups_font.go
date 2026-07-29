package convert

import (
	"encoding/binary"
	"regexp"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file registers Fixers for the purely-structural composite-font
// violations classified as "easy" in the converter plan: a missing
// CIDFontType2 /CIDToGIDMap (6.3.3.2), and a Type0 font's CMap
// CIDSystemInfo/WMode disagreeing with its descendant CIDFont/CMap stream
// (6.3.3.1/6.3.3.3). They deliberately do not touch the TrueType encoding
// checks (6.3.7) -- normalizing Encoding/cmap subtables can change glyph
// mapping and therefore rendered appearance, unlike adding /Identity (the
// spec default) or reconciling metadata that already describes the same
// embedded data two different ways.

func init() {
	registerFixer(fontDictFixer{})
	registerFixer(type0FontFixer{})
	registerFixer(baseFontFixer{})
}

// fontDictFixer remediates Checks.Font.CIDToGIDMapMissing by adding the
// spec-default /CIDToGIDMap /Identity to any CIDFontType2 descendant font
// that lacks it, mirroring the detection in validateFontDict
// (checks_font.go). Adding /Identity is always valid -- it IS the PDF
// default applied when the key is absent -- so it never changes rendered
// appearance and never breaks a conformant file, which is why it's safe to
// apply unconditionally rather than only for non-invisible-only fonts like
// the check itself.
type fontDictFixer struct{}

func (fontDictFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Font.CIDToGIDMapMissing
}

func (f fontDictFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (fontDictFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "CIDFontType2"}) {
			return
		}
		if d.Entries.Get("CIDToGIDMap") == nil {
			d.Entries.Set("CIDToGIDMap", pdf.PDFName{Value: "Identity"})
			*changed = true
		}
	}, true
}

// type0FontFixer remediates Type0 font CIDSystemInfo and CMap WMode
// mismatches, mirroring validateType0Font/validateCMapWMode
// (checks_font.go/checks_font_program.go). The descendant CIDFont's
// CIDSystemInfo is authoritative -- it describes the glyph data actually
// embedded -- so a mismatched CMap CIDSystemInfo is overwritten to match it;
// a mismatched dictionary /WMode is overwritten to match the value the CMap
// stream itself declares.
type type0FontFixer struct{}

func (type0FontFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Font.CIDSystemInfoMismatch, pdf.Checks.Font.CMapWModeInconsistent:
		return true
	}
	return false
}

func (f type0FontFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (type0FontFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Type0"}) {
			return
		}
		cmap, ok := d.Entries.Get("Encoding").(pdf.PDFDict)
		if !ok {
			return
		}

		if cid := verify.DescendantCIDFont(d); cid.Entries != nil {
			cmapCSI, hasCmapCSI := cmap.Entries.Get("CIDSystemInfo").(pdf.PDFDict)
			cidCSI, hasCidCSI := cid.Entries.Get("CIDSystemInfo").(pdf.PDFDict)
			if hasCmapCSI && hasCidCSI && !verify.SameCIDSystemInfo(cmapCSI, cidCSI) {
				cmap.Entries.Set("CIDSystemInfo", cid.Entries.Get("CIDSystemInfo"))
				*changed = true
			}
		}

		if !cmap.HasStream {
			return
		}
		dictWMode, ok := cmap.Entries.Get("WMode").(pdf.PDFInteger)
		if !ok {
			return
		}
		data, err := pdf.DecodeStream(cmap)
		if err != nil {
			return
		}
		m := verify.WmodeRe.FindSubmatch(data)
		if m == nil {
			return
		}
		if streamWMode, err := strconv.Atoi(string(m[1])); err == nil && int(dictWMode) != streamWMode {
			cmap.Entries.Set("WMode", pdf.PDFInteger(streamWMode))
			*changed = true
		}
	}, true
}

// baseFontFixer remediates Checks.Font.FontBaseFont by giving a font
// dictionary the BaseFont name it lacks, mirroring the detection in
// ValidateFontDict (checks_font.go).
//
// The name is only ever read back out of the file, never invented from
// nothing when the file already says it somewhere: the descriptor's FontName
// and the embedded program's own name both record what this font is called,
// and either is preferable to a placeholder.
type baseFontFixer struct{}

func (baseFontFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Font.FontBaseFont
}

func (f baseFontFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (baseFontFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
			return
		}
		subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)
		if subtype.Value == "Type3" {
			return
		}
		if name, ok := d.Entries.Get("BaseFont").(pdf.PDFName); ok && name.Value != "" {
			return
		}
		d.Entries.Set("BaseFont", pdf.PDFName{Value: recoverBaseFontName(d)})
		*changed = true
	}, true
}

// fallbackBaseFontName is what a font is called when neither the file nor the
// program it embeds says.
const fallbackBaseFontName = "Unknown"

// recoverBaseFontName digs a PostScript name out of the font, falling back to
// a fixed placeholder so the repair always converges.
func recoverBaseFontName(d pdf.PDFDict) string {
	desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return fallbackBaseFontName
	}
	if name, ok := desc.Entries.Get("FontName").(pdf.PDFName); ok {
		if clean := sanitizeBaseFontName(name.Value); clean != "" {
			return clean
		}
	}
	for _, key := range []string{"FontFile3", "FontFile2", "FontFile"} {
		ff, ok := desc.Entries.Get(key).(pdf.PDFDict)
		if !ok || !ff.HasStream {
			continue
		}
		data, err := pdf.DecodeStream(ff)
		if err != nil {
			continue
		}
		if clean := sanitizeBaseFontName(fontProgramName(key, data)); clean != "" {
			return clean
		}
	}
	return fallbackBaseFontName
}

// type1FontNameRe finds the /FontName of a Type 1 program's clear-text header.
var type1FontNameRe = regexp.MustCompile(`/FontName\s*/([^\s/\[\]<>(){}%]+)`)

// fontProgramName returns the name an embedded font program gives itself, or
// "" if it does not give one.
func fontProgramName(key string, data []byte) string {
	switch key {
	case "FontFile":
		if m := type1FontNameRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	case "FontFile2":
		if tables, ok := verify.ParseSfnt(data); ok {
			return sfntPostScriptName(tables["name"])
		}
	case "FontFile3":
		if cff := extractCFFBytes(data); cff != nil {
			if names, _ := verify.ParseCFFIndex(cff, int(cff[2])); len(names) > 0 {
				return string(names[0])
			}
		}
		if tables, ok := verify.ParseSfnt(data); ok {
			return sfntPostScriptName(tables["name"])
		}
	}
	return ""
}

// sfntPostScriptName reads name ID 6 (the PostScript name) out of an sfnt name
// table, decoding the UTF-16BE the Windows platform uses.
func sfntPostScriptName(nameTable []byte) string {
	if len(nameTable) < 6 {
		return ""
	}
	count := int(binary.BigEndian.Uint16(nameTable[2:4]))
	storage := int(binary.BigEndian.Uint16(nameTable[4:6]))
	for i := range count {
		rec := 6 + i*12
		if rec+12 > len(nameTable) {
			break
		}
		if binary.BigEndian.Uint16(nameTable[rec+6:rec+8]) != 6 {
			continue
		}
		platform := binary.BigEndian.Uint16(nameTable[rec : rec+2])
		length := int(binary.BigEndian.Uint16(nameTable[rec+8 : rec+10]))
		off := storage + int(binary.BigEndian.Uint16(nameTable[rec+10:rec+12]))
		if off < 0 || length < 0 || off+length > len(nameTable) {
			continue
		}
		raw := nameTable[off : off+length]
		if platform == 0 || platform == 3 {
			// UTF-16BE, and a PostScript name is ASCII, so the high bytes go.
			var b []byte
			for j := 1; j < len(raw); j += 2 {
				b = append(b, raw[j])
			}
			return string(b)
		}
		return string(raw)
	}
	return ""
}

// sanitizeBaseFontName drops the bytes a PDF name cannot hold, so a name
// recovered from a font program is always writable as-is.
func sanitizeBaseFontName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r > 32 && r < 127 && !strings.ContainsRune("/[]<>(){}%#", r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
