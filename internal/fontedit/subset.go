package fontedit

import (
	"encoding/binary"
	"fmt"
	"github.com/voidrab/gopdfrab/internal/fontprog"
	"math"
	"sort"
)

// SubsetTrueType returns a minimal valid sfnt program derived from src,
// containing only the glyphs reachable from unicodes (via src's own (3,1)
// cmap) plus the transitive closure of any composite-glyph components, with
// a single (3,1) format-4 cmap mapping each resolved unicode to its new,
// densely-renumbered glyph ID. Unicodes absent from src's cmap are silently
// skipped. Glyph 0 (.notdef) is always retained.
func SubsetTrueType(src []byte, unicodes []uint16) ([]byte, error) {
	tables, ok := fontprog.ParseSfnt(src)
	if !ok {
		return nil, fmt.Errorf("SubsetTrueType: not a valid sfnt")
	}
	gidMap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
	if gidMap == nil {
		return nil, fmt.Errorf("SubsetTrueType: source has no usable (3,1) cmap")
	}

	type resolved struct {
		unicode uint16
		oldGID  int
	}
	var used []resolved
	closure := map[int]bool{0: true}
	queue := []int{0}
	addGID := func(gid int) {
		if !closure[gid] {
			closure[gid] = true
			queue = append(queue, gid)
		}
	}
	for _, u := range unicodes {
		if gid, ok := gidMap[u]; ok && gid != 0 {
			used = append(used, resolved{u, int(gid)})
			addGID(int(gid))
		}
	}
	for len(queue) > 0 {
		gid := queue[0]
		queue = queue[1:]
		for _, c := range fontprog.GlyfComponents(fontprog.GlyfRecord(tables, gid)) {
			addGID(c)
		}
	}

	oldGIDs := make([]int, 0, len(closure))
	for g := range closure {
		oldGIDs = append(oldGIDs, g)
	}
	sort.Ints(oldGIDs)
	remap := make(map[int]int, len(oldGIDs))
	oldGIDOf := make(map[int]int, len(oldGIDs))
	for newGID, oldGID := range oldGIDs {
		remap[oldGID] = newGID
		oldGIDOf[newGID] = oldGID
	}

	newCmap := map[uint16]uint16{}
	for _, r := range used {
		newCmap[r.unicode] = uint16(remap[r.oldGID])
	}

	out := buildSubsetTables(tables, len(oldGIDs), oldGIDOf, remap)
	out["cmap"] = fontprog.BuildCmapFormat4Table(3, 1, newCmap)
	return fontprog.PackSfnt(out), nil
}

// SubsetTrueTypeSymbolic returns a minimal sfnt program for a symbolic simple
// font: the glyph for each code's unicode (resolved via src's (3,1) cmap) is
// exposed through a single (3,0) format-4 cmap under both the raw code and
// its 0xF000-offset alias, so untouched content-stream bytes keep selecting
// the glyph the code originally meant. Codes whose unicode is absent from src
// are silently skipped; callers must pre-check coverage.
func SubsetTrueTypeSymbolic(src []byte, codeUnicode map[int]uint16) ([]byte, error) {
	tables, ok := fontprog.ParseSfnt(src)
	if !ok {
		return nil, fmt.Errorf("SubsetTrueTypeSymbolic: not a valid sfnt")
	}
	gidMap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
	if gidMap == nil {
		return nil, fmt.Errorf("SubsetTrueTypeSymbolic: source has no usable (3,1) cmap")
	}

	type resolved struct {
		code   int
		oldGID int
	}
	var used []resolved
	closure := map[int]bool{0: true}
	queue := []int{0}
	addGID := func(gid int) {
		if !closure[gid] {
			closure[gid] = true
			queue = append(queue, gid)
		}
	}
	for code, u := range codeUnicode {
		if code < 0 || code > 255 {
			continue
		}
		if gid, ok := gidMap[u]; ok && gid != 0 {
			used = append(used, resolved{code, int(gid)})
			addGID(int(gid))
		}
	}
	for len(queue) > 0 {
		gid := queue[0]
		queue = queue[1:]
		for _, c := range fontprog.GlyfComponents(fontprog.GlyfRecord(tables, gid)) {
			addGID(c)
		}
	}

	oldGIDs := make([]int, 0, len(closure))
	for g := range closure {
		oldGIDs = append(oldGIDs, g)
	}
	sort.Ints(oldGIDs)
	remap := make(map[int]int, len(oldGIDs))
	oldGIDOf := make(map[int]int, len(oldGIDs))
	for newGID, oldGID := range oldGIDs {
		remap[oldGID] = newGID
		oldGIDOf[newGID] = oldGID
	}

	newCmap := map[uint16]uint16{}
	for _, r := range used {
		gid := uint16(remap[r.oldGID])
		newCmap[uint16(r.code)] = gid
		newCmap[0xF000|uint16(r.code)] = gid
	}

	out := buildSubsetTables(tables, len(oldGIDs), oldGIDOf, remap)
	out["cmap"] = fontprog.BuildCmapFormat4Table(3, 0, newCmap)
	return fontprog.PackSfnt(out), nil
}

// SubsetTrueTypeForCID rebuilds src so that the glyph for each unicode in
// targetGID lands at EXACTLY its given output glyph ID, rather than an
// auto-assigned dense one -- required because the verifier's CID TrueType
// checks (validateCIDTrueTypeSubset/Metrics, checks_font_program.go) look up
// a CID directly as a glyph ID, with no /CIDToGIDMap indirection, so a
// substituted CIDFontType2 font must satisfy CID == GID exactly to pass
// them. Unicodes absent from src's cmap are silently skipped (the caller is
// expected to have already filtered for resolvability). Any output GID in
// [0, max(targetGID)] not assigned a glyph this way becomes an empty
// placeholder, since /W only ever references the CIDs the caller asked for.
//
// notdef names CIDs that drew the missing-glyph shape in the font being
// replaced: each gets src's .notdef outline at that glyph ID, advancing by
// the given width (1/1000 em) instead of the .notdef's own.
func SubsetTrueTypeForCID(src []byte, targetCIDs map[uint16][]int, notdef map[int]float64) ([]byte, error) {
	tables, ok := fontprog.ParseSfnt(src)
	if !ok {
		return nil, fmt.Errorf("SubsetTrueTypeForCID: not a valid sfnt")
	}
	gidMap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
	if gidMap == nil {
		return nil, fmt.Errorf("SubsetTrueTypeForCID: source has no usable (3,1) cmap")
	}

	remap := map[int]int{}
	oldGIDOf := map[int]int{}
	maxGID := 0
	for _, targets := range targetCIDs {
		for _, target := range targets {
			if target > maxGID {
				maxGID = target
			}
		}
	}
	for cid := range notdef {
		maxGID = max(maxGID, cid)
	}
	nextClosureGID := maxGID + 1
	var queue []int
	place := func(oldGID, newGID int) {
		remap[oldGID] = newGID
		oldGIDOf[newGID] = oldGID
		queue = append(queue, oldGID)
	}
	for u, targets := range targetCIDs {
		if oldGID, ok := gidMap[u]; ok {
			for _, target := range targets {
				place(int(oldGID), target)
			}
		}
	}
	for cid := range notdef {
		place(0, cid)
	}
	for len(queue) > 0 {
		oldGID := queue[0]
		queue = queue[1:]
		for _, c := range fontprog.GlyfComponents(fontprog.GlyfRecord(tables, oldGID)) {
			if _, already := remap[c]; !already {
				place(c, nextClosureGID)
				nextClosureGID++
			}
		}
	}

	out := buildSubsetTables(tables, nextClosureGID, oldGIDOf, remap)
	out["cmap"] = fontprog.BuildCmapFormat4Table(3, 1, nil)
	if head := out["head"]; len(head) >= 20 {
		upm := float64(binary.BigEndian.Uint16(head[18:20]))
		// buildSubsetTables writes a full hmtx: one long metric per glyph.
		for cid, width := range notdef {
			binary.BigEndian.PutUint16(out["hmtx"][cid*4:], uint16(math.Round(width*upm/1000)))
		}
	}
	return fontprog.PackSfnt(out), nil
}

// PromoteEmptyGlyphs rewrites a TrueType program so every zero-length (empty
// outline) glyph becomes an explicit zero-contour record, and every
// zero-contour record cut short of its instructionLength is padded to full
// length (completeEmptyGlyph). All real outlines, metrics and every other
// sfnt table stay byte-for-byte identical.
func PromoteEmptyGlyphs(data []byte) ([]byte, bool) {
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return data, false
	}
	n := fontprog.TTNumGlyphs(tables)
	if n == 0 {
		return data, false
	}
	changed := false
	var glyf []byte
	loca := make([]byte, 4*(n+1))
	for gid := range n {
		binary.BigEndian.PutUint32(loca[gid*4:], uint32(len(glyf)))
		rec, completed := completeEmptyGlyph(fontprog.GlyfRecord(tables, gid))
		changed = changed || completed
		glyf = append(glyf, rec...)
		if len(glyf)%2 != 0 {
			glyf = append(glyf, 0)
		}
	}
	binary.BigEndian.PutUint32(loca[n*4:], uint32(len(glyf)))
	if !changed {
		return data, false
	}
	tables["glyf"] = glyf
	tables["loca"] = loca
	head := append([]byte(nil), tables["head"]...)
	if len(head) >= 52 {
		binary.BigEndian.PutUint16(head[50:52], 1) // long loca format
	}
	tables["head"] = head
	return fontprog.PackSfnt(tables), true
}

// buildSubsetTables rebuilds glyf/loca/hmtx/hhea/maxp/head for an output
// font of outputGIDs glyphs, where oldGIDOf[newGID] (when present) names the
// source glyph to copy into that output slot -- any newGID missing from
// oldGIDOf becomes an empty placeholder glyph -- remapping composite
// component references through remap (source GID -> output GID). Shared by
// SubsetTrueType's dense renumbering and SubsetTrueTypeForCID's sparse,
// caller-dictated placement.
func buildSubsetTables(tables map[string][]byte, outputGIDs int, oldGIDOf, remap map[int]int) map[string][]byte {
	var glyf []byte
	loca := make([]byte, 4*(outputGIDs+1))
	for newGID := range outputGIDs {
		binary.BigEndian.PutUint32(loca[newGID*4:], uint32(len(glyf)))
		if oldGID, ok := oldGIDOf[newGID]; ok {
			rec := remapGlyfComponents(fontprog.GlyfRecord(tables, oldGID), remap)
			if len(rec) == 0 {
				// A genuinely blank glyph (e.g. space) commonly has zero-length
				// loca data, but TTGlyphPresent (sfnt.go) only
				// exempts an empty outline when the glyph's advance width is
				// also zero -- emit an explicit zero-contour record instead
				// (an equally valid TrueType encoding of "draws nothing") so a
				// nonzero-width blank glyph still counts as present.
				rec = fontprog.EmptyGlyfRecord
			}
			glyf = append(glyf, rec...)
			if len(glyf)%2 != 0 {
				glyf = append(glyf, 0)
			}
		}
	}
	binary.BigEndian.PutUint32(loca[outputGIDs*4:], uint32(len(glyf)))

	hmtx := make([]byte, 4*outputGIDs)
	for newGID := range outputGIDs {
		if oldGID, ok := oldGIDOf[newGID]; ok {
			aw, lsb, _ := ttRawHMetric(tables, oldGID)
			binary.BigEndian.PutUint16(hmtx[newGID*4:], aw)
			binary.BigEndian.PutUint16(hmtx[newGID*4+2:], uint16(lsb))
		}
	}
	hhea := append([]byte(nil), tables["hhea"]...)
	if len(hhea) >= 36 {
		binary.BigEndian.PutUint16(hhea[34:36], uint16(outputGIDs))
	}
	maxp := append([]byte(nil), tables["maxp"]...)
	if len(maxp) >= 6 {
		binary.BigEndian.PutUint16(maxp[4:6], uint16(outputGIDs))
	}
	head := append([]byte(nil), tables["head"]...)
	if len(head) >= 52 {
		binary.BigEndian.PutUint16(head[50:52], 1) // long loca format
	}

	out := map[string][]byte{
		"glyf": glyf, "loca": loca, "hmtx": hmtx, "hhea": hhea, "maxp": maxp, "head": head,
		"post": minimalPostTable(),
	}
	for _, t := range []string{"OS/2", "name", "cvt ", "fpgm", "prep"} {
		if data, ok := tables[t]; ok {
			out[t] = data
		}
	}
	return out
}

// TrimTrueTypeCmapToSingleSubtable returns src with its cmap table reduced to
// a single (3,1) format-4 subtable (the spec-mandated shape for a symbolic
// TrueType font, 6.3.7), leaving every other table -- including glyf/loca, so
// glyph shapes and mapping are unaffected -- byte-for-byte unchanged. Only
// format-4 source cmaps are supported; other subtable formats return an
// error so the caller can leave the violation as residual rather than risk a
// silently-wrong rebuild.
func TrimTrueTypeCmapToSingleSubtable(src []byte) ([]byte, error) {
	tables, ok := fontprog.ParseSfnt(src)
	if !ok {
		return nil, fmt.Errorf("TrimTrueTypeCmapToSingleSubtable: not a valid sfnt")
	}
	platform, encoding, gidMap, ok := FirstFormat4CmapSubtable(tables)
	if !ok {
		return nil, fmt.Errorf("TrimTrueTypeCmapToSingleSubtable: no format-4 cmap subtable found")
	}
	rebuilt := map[string][]byte{}
	for tag, data := range tables {
		rebuilt[tag] = data
	}
	rebuilt["cmap"] = fontprog.BuildCmapFormat4Table(platform, encoding, gidMap)
	return fontprog.PackSfnt(rebuilt), nil
}

// FirstFormat4CmapSubtable prefers the (3,1) Windows Unicode BMP subtable
// (ttWindowsBMPCmap), the common and most useful case; if absent, it falls
// back to the first subtable in the cmap's own directory order that
// parseCmapFormat4 can read -- a symbolic font's surviving subtable is
// commonly (3,0) or (1,0) instead.
func FirstFormat4CmapSubtable(tables map[string][]byte) (platform, encoding uint16, gidMap map[uint16]uint16, ok bool) {
	if m := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables)); m != nil {
		return 3, 1, m, true
	}
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return 0, 0, nil, false
	}
	n := int(binary.BigEndian.Uint16(cmap[2:4]))
	for i := range n {
		rec := cmap[4+i*8:]
		if len(rec) < 8 {
			break
		}
		p := binary.BigEndian.Uint16(rec[0:2])
		e := binary.BigEndian.Uint16(rec[2:4])
		off := int(binary.BigEndian.Uint32(rec[4:8]))
		if off+2 > len(cmap) {
			continue
		}
		if m := fontprog.ParseCmapFormat4(cmap[off:]); m != nil {
			return p, e, m, true
		}
	}
	return 0, 0, nil, false
}

// remapGlyfComponents returns rec with every composite component's
// glyphIndex rewritten through remap, leaving a simple glyph's bytes
// unchanged (returned as-is, no copy needed).
func remapGlyfComponents(rec []byte, remap map[int]int) []byte {
	if !fontprog.GlyfIsComposite(rec) {
		return rec
	}
	out := append([]byte(nil), rec...)
	off := 10
	for off+4 <= len(out) {
		flags := binary.BigEndian.Uint16(out[off:])
		if newGID, ok := remap[int(binary.BigEndian.Uint16(out[off+2:]))]; ok {
			binary.BigEndian.PutUint16(out[off+2:], uint16(newGID))
		}
		off += fontprog.ComponentRecordSize(flags)
		if flags&0x0020 == 0 {
			break
		}
	}
	return out
}

// ttRawHMetric returns glyph gid's unscaled advance width and left side
// bearing straight from hmtx/hhea, the raw counterpart to ttAdvanceWidth
// (which scales to PDF's 1000-unit em) needed to rebuild an hmtx table.
func ttRawHMetric(tables map[string][]byte, gid int) (advance uint16, lsb int16, ok bool) {
	hmtx := tables["hmtx"]
	hhea := tables["hhea"]
	if len(hmtx) == 0 || len(hhea) < 36 {
		return 0, 0, false
	}
	nHM := int(binary.BigEndian.Uint16(hhea[34:36]))
	if nHM <= 0 {
		return 0, 0, false
	}
	if gid < nHM {
		if gid*4+4 > len(hmtx) {
			return 0, 0, false
		}
		return binary.BigEndian.Uint16(hmtx[gid*4:]), int16(binary.BigEndian.Uint16(hmtx[gid*4+2:])), true
	}
	lastAW := binary.BigEndian.Uint16(hmtx[(nHM-1)*4:])
	lsbOff := nHM*4 + (gid-nHM)*2
	if lsbOff+2 > len(hmtx) {
		return lastAW, 0, true
	}
	return lastAW, int16(binary.BigEndian.Uint16(hmtx[lsbOff:])), true
}

// completeEmptyGlyph returns rec as a full-length record when it says "no
// outline" in fewer bytes than fontprog.EmptyGlyfRecord: zero-length, too short to hold
// numberOfContours, or a zero-contour header missing its instructionLength (a
// 10-byte .notdef some producers write). The latter keeps its bbox. Strict
// parsers -- BFO's -- read the absent instructionLength from the next glyph
// and reject the font, while lenient ones, veraPDF and gopdfrab included,
// accept it, so the defect passes every other check. Any other record,
// however short, has outline data and is returned as it is.
func completeEmptyGlyph(rec []byte) ([]byte, bool) {
	switch {
	case len(rec) < 2:
		return fontprog.EmptyGlyfRecord, true
	case len(rec) < len(fontprog.EmptyGlyfRecord) && binary.BigEndian.Uint16(rec) == 0:
		full := make([]byte, len(fontprog.EmptyGlyfRecord))
		copy(full, rec)
		return full, true
	}
	return rec, false
}

// minimalPostTable returns a format 3.0 'post' table (no per-glyph names),
// the simplest spec-valid form -- safe regardless of how many glyphs the
// font ends up with, since format 3 carries no glyph-indexed data.
func minimalPostTable() []byte {
	return make([]byte, 32) // version 0x00030000 (zero value), all other fields 0
}
