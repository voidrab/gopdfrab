package pdfrab

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
)

// parseSfnt parses an sfnt (TrueType/OpenType) table directory, returning each
// table's bytes. The second result is false if the data is not a valid sfnt.
func parseSfnt(data []byte) (map[string][]byte, bool) {
	if len(data) < 12 {
		return nil, false
	}
	switch binary.BigEndian.Uint32(data[:4]) {
	case 0x00010000, 0x74727565, 0x4F54544F: // 1.0, 'true', 'OTTO'
	default:
		return nil, false
	}
	num := int(binary.BigEndian.Uint16(data[4:6]))
	if num == 0 || 12+num*16 > len(data) {
		return nil, false
	}
	tables := map[string][]byte{}
	for i := 0; i < num; i++ {
		rec := data[12+i*16:]
		off := binary.BigEndian.Uint32(rec[8:12])
		ln := binary.BigEndian.Uint32(rec[12:16])
		if int(off) > len(data) {
			continue
		}
		end := int(off) + int(ln)
		if end > len(data) {
			end = len(data)
		}
		tables[string(rec[:4])] = data[off:end]
	}
	return tables, true
}

// fontProgramValid reports whether the embedded font program in a FontFile is a
// structurally valid font of its expected type (6.3.2).
func fontProgramValid(stream PDFDict, key string) bool {
	data, err := decodeStream(stream)
	if err != nil || len(data) == 0 {
		return false
	}
	switch key {
	case "FontFile2":
		_, ok := parseSfnt(data)
		return ok
	case "FontFile3":
		if len(data) >= 4 && data[0] == 1 { // CFF header, major version 1
			return true
		}
		_, ok := parseSfnt(data) // OpenType-wrapped CFF
		return ok
	case "FontFile":
		// Type 1: the clear-text portion begins with a PostScript marker.
		return bytes.HasPrefix(data, []byte("%!"))
	}
	return true
}

// validateFontProgram flags a damaged embedded font program (6.3.2).
func validateFontProgram(obj PDFValue, desc PDFDict, name string, ctx *ValidationContext) {
	for _, key := range []string{"FontFile", "FontFile2", "FontFile3"} {
		ff, ok := desc.Entries[key].(PDFDict)
		if !ok {
			continue
		}
		if !fontProgramValid(ff, key) {
			ctx.ReportError(obj, "6.3.2", 1, fmt.Sprintf("embedded font program for %s is damaged", name))
		}
	}
}

// trueTypeCmapSubtables returns the number of cmap subtables in an embedded
// TrueType font, and whether it could be determined.
func trueTypeCmapSubtables(desc PDFDict) (int, bool) {
	ff, ok := desc.Entries["FontFile2"].(PDFDict)
	if !ok {
		return 0, false
	}
	data, err := decodeStream(ff)
	if err != nil {
		return 0, false
	}
	tables, ok := parseSfnt(data)
	if !ok {
		return 0, false
	}
	cmap, ok := tables["cmap"]
	if !ok || len(cmap) < 4 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(cmap[2:4])), true
}

var wmodeRe = regexp.MustCompile(`/WMode\s+(\d+)\s+def`)

// validateCMapWMode flags an embedded CMap whose dictionary WMode disagrees with
// the WMode declared in its stream (6.3.3.3).
func validateCMapWMode(obj PDFValue, cmap PDFDict, ctx *ValidationContext) {
	if !cmap.HasStream {
		return
	}
	dictWMode, ok := cmap.Entries["WMode"].(PDFInteger)
	if !ok {
		return
	}
	data, err := decodeStream(cmap)
	if err != nil {
		return
	}
	m := wmodeRe.FindSubmatch(data)
	if m == nil {
		return
	}
	streamWMode, _ := strconv.Atoi(string(m[1]))
	if int(dictWMode) != streamWMode {
		ctx.ReportError(obj, "6.3.3.3", 2, "WMode in CMap dictionary and stream are inconsistent")
	}
}
