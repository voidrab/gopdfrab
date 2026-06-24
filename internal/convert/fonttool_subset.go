package convert

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file is CW-4's write side: a minimal TrueType subsetter and sfnt
// table-repacker, the inverse of the readers in checks_font_program.go
// (parseSfnt, tt*, parseCmapFormat4). It exists to shrink a bundled
// substitute face (fixups_font_subst.go, Phase 10) down to the glyphs a
// document actually needs before embedding it, and to trim an otherwise-good
// embedded font's cmap to a single subtable (6.3.7's SymbolicTrueTypeCmap)
// without touching its outlines. No CFF subsetter exists -- every
// substitution target is a bundled TrueType face, so there is no scenario
// that needs one.

// subsetTrueType returns a minimal valid sfnt program derived from src,
// containing only the glyphs reachable from unicodes (via src's own (3,1)
// cmap) plus the transitive closure of any composite-glyph components, with
// a single (3,1) format-4 cmap mapping each resolved unicode to its new,
// densely-renumbered glyph ID. Unicodes absent from src's cmap are silently
// skipped. Glyph 0 (.notdef) is always retained.
func subsetTrueType(src []byte, unicodes []uint16) ([]byte, error) {
	tables, ok := verify.ParseSfnt(src)
	if !ok {
		return nil, fmt.Errorf("subsetTrueType: not a valid sfnt")
	}
	gidMap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	if gidMap == nil {
		return nil, fmt.Errorf("subsetTrueType: source has no usable (3,1) cmap")
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
		for _, c := range glyfComponents(glyfRecord(tables, gid)) {
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
	out["cmap"] = buildCmapFormat4Table(3, 1, newCmap)
	return packSfnt(out), nil
}

// subsetTrueTypeForCID rebuilds src so that the glyph for each unicode in
// targetGID lands at EXACTLY its given output glyph ID, rather than an
// auto-assigned dense one -- required because the verifier's CID TrueType
// checks (validateCIDTrueTypeSubset/Metrics, checks_font_program.go) look up
// a CID directly as a glyph ID, with no /CIDToGIDMap indirection, so a
// substituted CIDFontType2 font must satisfy CID == GID exactly to pass
// them. Unicodes absent from src's cmap are silently skipped (the caller is
// expected to have already filtered for resolvability). Any output GID in
// [0, max(targetGID)] not assigned a glyph this way becomes an empty
// placeholder, since /W only ever references the CIDs the caller asked for.
func subsetTrueTypeForCID(src []byte, targetGID map[uint16]int) ([]byte, error) {
	tables, ok := verify.ParseSfnt(src)
	if !ok {
		return nil, fmt.Errorf("subsetTrueTypeForCID: not a valid sfnt")
	}
	gidMap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	if gidMap == nil {
		return nil, fmt.Errorf("subsetTrueTypeForCID: source has no usable (3,1) cmap")
	}

	remap := map[int]int{}
	oldGIDOf := map[int]int{}
	maxGID := 0
	for _, target := range targetGID {
		if target > maxGID {
			maxGID = target
		}
	}
	nextClosureGID := maxGID + 1
	var queue []int
	place := func(oldGID, newGID int) {
		remap[oldGID] = newGID
		oldGIDOf[newGID] = oldGID
		queue = append(queue, oldGID)
	}
	for u, target := range targetGID {
		if oldGID, ok := gidMap[u]; ok {
			place(int(oldGID), target)
		}
	}
	for len(queue) > 0 {
		oldGID := queue[0]
		queue = queue[1:]
		for _, c := range glyfComponents(glyfRecord(tables, oldGID)) {
			if _, already := remap[c]; !already {
				place(c, nextClosureGID)
				nextClosureGID++
			}
		}
	}

	out := buildSubsetTables(tables, nextClosureGID, oldGIDOf, remap)
	out["cmap"] = buildCmapFormat4Table(3, 1, nil)
	return packSfnt(out), nil
}

// buildSubsetTables rebuilds glyf/loca/hmtx/hhea/maxp/head for an output
// font of outputGIDs glyphs, where oldGIDOf[newGID] (when present) names the
// source glyph to copy into that output slot -- any newGID missing from
// oldGIDOf becomes an empty placeholder glyph -- remapping composite
// component references through remap (source GID -> output GID). Shared by
// subsetTrueType's dense renumbering and subsetTrueTypeForCID's sparse,
// caller-dictated placement.
func buildSubsetTables(tables map[string][]byte, outputGIDs int, oldGIDOf, remap map[int]int) map[string][]byte {
	var glyf []byte
	loca := make([]byte, 4*(outputGIDs+1))
	for newGID := range outputGIDs {
		binary.BigEndian.PutUint32(loca[newGID*4:], uint32(len(glyf)))
		if oldGID, ok := oldGIDOf[newGID]; ok {
			rec := remapGlyfComponents(glyfRecord(tables, oldGID), remap)
			if len(rec) == 0 {
				// A genuinely blank glyph (e.g. space) commonly has zero-length
				// loca data, but ttGlyphPresent (checks_font_program.go) only
				// exempts an empty outline when the glyph's advance width is
				// also zero -- emit an explicit zero-contour record instead
				// (an equally valid TrueType encoding of "draws nothing") so a
				// nonzero-width blank glyph still counts as present.
				rec = emptyGlyfRecord
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

// trimTrueTypeCmapToSingleSubtable returns src with its cmap table reduced to
// a single (3,1) format-4 subtable (the spec-mandated shape for a symbolic
// TrueType font, 6.3.7), leaving every other table -- including glyf/loca, so
// glyph shapes and mapping are unaffected -- byte-for-byte unchanged. Only
// format-4 source cmaps are supported; other subtable formats return an
// error so the caller can leave the violation as residual rather than risk a
// silently-wrong rebuild.
func trimTrueTypeCmapToSingleSubtable(src []byte) ([]byte, error) {
	tables, ok := verify.ParseSfnt(src)
	if !ok {
		return nil, fmt.Errorf("trimTrueTypeCmapToSingleSubtable: not a valid sfnt")
	}
	platform, encoding, gidMap, ok := firstFormat4CmapSubtable(tables)
	if !ok {
		return nil, fmt.Errorf("trimTrueTypeCmapToSingleSubtable: no format-4 cmap subtable found")
	}
	rebuilt := map[string][]byte{}
	for tag, data := range tables {
		rebuilt[tag] = data
	}
	rebuilt["cmap"] = buildCmapFormat4Table(platform, encoding, gidMap)
	return packSfnt(rebuilt), nil
}

// firstFormat4CmapSubtable prefers the (3,1) Windows Unicode BMP subtable
// (ttWindowsBMPCmap), the common and most useful case; if absent, it falls
// back to the first subtable in the cmap's own directory order that
// parseCmapFormat4 can read -- a symbolic font's surviving subtable is
// commonly (3,0) or (1,0) instead.
func firstFormat4CmapSubtable(tables map[string][]byte) (platform, encoding uint16, gidMap map[uint16]uint16, ok bool) {
	if m := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables)); m != nil {
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
		if m := verify.ParseCmapFormat4(cmap[off:]); m != nil {
			return p, e, m, true
		}
	}
	return 0, 0, nil, false
}

// glyfRecord returns the raw glyf bytes for gid, or nil if gid is out of
// range or has an empty outline (e.g. space).
func glyfRecord(tables map[string][]byte, gid int) []byte {
	loca := tables["loca"]
	glyf := tables["glyf"]
	head := tables["head"]
	if len(head) < 52 {
		return nil
	}
	locFmt := int(binary.BigEndian.Uint16(head[50:52]))
	var start, end int
	if locFmt == 0 {
		if (gid+1)*2+2 > len(loca) {
			return nil
		}
		start = int(binary.BigEndian.Uint16(loca[gid*2:])) * 2
		end = int(binary.BigEndian.Uint16(loca[(gid+1)*2:])) * 2
	} else {
		if (gid+1)*4+4 > len(loca) {
			return nil
		}
		start = int(binary.BigEndian.Uint32(loca[gid*4:]))
		end = int(binary.BigEndian.Uint32(loca[(gid+1)*4:]))
	}
	if start < 0 || end > len(glyf) || start > end {
		return nil
	}
	return glyf[start:end]
}

// glyfIsComposite reports whether a glyf record's numberOfContours is
// negative, the TrueType signal for a composite (compound) glyph.
func glyfIsComposite(rec []byte) bool {
	return len(rec) >= 2 && int16(binary.BigEndian.Uint16(rec)) < 0
}

// componentRecordSize returns the byte length of a single composite-glyph
// component record, given its flags word.
func componentRecordSize(flags uint16) int {
	size := 4 // flags + glyphIndex
	if flags&0x0001 != 0 {
		size += 4 // ARG_1_AND_2_ARE_WORDS
	} else {
		size += 2
	}
	switch {
	case flags&0x0008 != 0: // WE_HAVE_A_SCALE
		size += 2
	case flags&0x0040 != 0: // WE_HAVE_AN_X_AND_Y_SCALE
		size += 4
	case flags&0x0080 != 0: // WE_HAVE_A_TWO_BY_TWO
		size += 8
	}
	return size
}

// glyfComponents returns the component glyph IDs referenced by a composite
// glyph record, or nil for a simple glyph (no outline data, or contours >= 0).
func glyfComponents(rec []byte) []int {
	if !glyfIsComposite(rec) {
		return nil
	}
	var gids []int
	off := 10
	for off+4 <= len(rec) {
		flags := binary.BigEndian.Uint16(rec[off:])
		gids = append(gids, int(binary.BigEndian.Uint16(rec[off+2:])))
		off += componentRecordSize(flags)
		if flags&0x0020 == 0 { // no MORE_COMPONENTS
			break
		}
	}
	return gids
}

// remapGlyfComponents returns rec with every composite component's
// glyphIndex rewritten through remap, leaving a simple glyph's bytes
// unchanged (returned as-is, no copy needed).
func remapGlyfComponents(rec []byte, remap map[int]int) []byte {
	if !glyfIsComposite(rec) {
		return rec
	}
	out := append([]byte(nil), rec...)
	off := 10
	for off+4 <= len(out) {
		flags := binary.BigEndian.Uint16(out[off:])
		if newGID, ok := remap[int(binary.BigEndian.Uint16(out[off+2:]))]; ok {
			binary.BigEndian.PutUint16(out[off+2:], uint16(newGID))
		}
		off += componentRecordSize(flags)
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

// emptyGlyfRecord is a minimal simple-glyph record (numberOfContours=0,
// zero bbox, no points/instructions) -- a valid, explicit way to say "this
// glyph has no outline," used in place of a zero-length loca span. See
// buildSubsetTables.
var emptyGlyfRecord = make([]byte, 10)

// minimalPostTable returns a format 3.0 'post' table (no per-glyph names),
// the simplest spec-valid form -- safe regardless of how many glyphs the
// font ends up with, since format 3 carries no glyph-indexed data.
func minimalPostTable() []byte {
	return make([]byte, 32) // version 0x00030000 (zero value), all other fields 0
}

// maxPow2LE returns the largest power of two <= n, and its base-2 exponent,
// the "searchRange/entrySelector" pair every sfnt binary-search table
// (the table directory, and a format-4 cmap subtable) declares.
func maxPow2LE(n int) (pow, exp int) {
	pow, exp = 1, 0
	for pow*2 <= n {
		pow *= 2
		exp++
	}
	return
}

// buildCmapFormat4Table builds a complete 'cmap' table with a single (platform,
// encoding) format-4 subtable, mapping each unicode in uniToGID to its glyph
// ID via one segment per code point -- simpler and more robust than merging
// contiguous runs, at the cost of a slightly larger table.
func buildCmapFormat4Table(platform, encoding uint16, uniToGID map[uint16]uint16) []byte {
	unicodes := make([]uint16, 0, len(uniToGID))
	for u := range uniToGID {
		unicodes = append(unicodes, u)
	}
	sort.Slice(unicodes, func(i, j int) bool { return unicodes[i] < unicodes[j] })

	segCount := len(unicodes) + 1 // +1 terminator segment
	segCountX2 := segCount * 2
	pow, exp := maxPow2LE(segCount)
	searchRange := pow * 2

	endOff := 14
	startOff := endOff + segCountX2 + 2 // +2 reservedPad
	deltaOff := startOff + segCountX2
	rangeOff := deltaOff + segCountX2
	glyphArrayOff := rangeOff + segCountX2
	subLen := glyphArrayOff + len(unicodes)*2

	sub := make([]byte, subLen)
	binary.BigEndian.PutUint16(sub[0:], 4)
	binary.BigEndian.PutUint16(sub[2:], uint16(subLen))
	binary.BigEndian.PutUint16(sub[6:], uint16(segCountX2))
	binary.BigEndian.PutUint16(sub[8:], uint16(searchRange))
	binary.BigEndian.PutUint16(sub[10:], uint16(exp))
	binary.BigEndian.PutUint16(sub[12:], uint16(segCountX2-searchRange))

	for i, u := range unicodes {
		binary.BigEndian.PutUint16(sub[endOff+i*2:], u)
		binary.BigEndian.PutUint16(sub[startOff+i*2:], u)
		glyphSlot, rangeSlot := glyphArrayOff+i*2, rangeOff+i*2
		binary.BigEndian.PutUint16(sub[rangeSlot:], uint16(glyphSlot-rangeSlot))
		binary.BigEndian.PutUint16(sub[glyphSlot:], uniToGID[u])
	}
	term := len(unicodes)
	binary.BigEndian.PutUint16(sub[endOff+term*2:], 0xFFFF)
	binary.BigEndian.PutUint16(sub[startOff+term*2:], 0xFFFF)
	binary.BigEndian.PutUint16(sub[deltaOff+term*2:], 1)

	cmap := make([]byte, 4+8+len(sub))
	binary.BigEndian.PutUint16(cmap[2:], 1) // numTables
	binary.BigEndian.PutUint16(cmap[4:], platform)
	binary.BigEndian.PutUint16(cmap[6:], encoding)
	binary.BigEndian.PutUint32(cmap[8:], 12) // subtable offset
	copy(cmap[12:], sub)
	return cmap
}

// tableChecksum sums b (which must be a multiple of 4 bytes) as a sequence of
// big-endian uint32 words, the checksum algorithm every sfnt table directory
// entry -- and the whole-file checkSumAdjustment -- uses.
func tableChecksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i+4 <= len(b); i += 4 {
		sum += binary.BigEndian.Uint32(b[i:])
	}
	return sum
}

// sfntChecksumAdjustmentMagic is the constant every TrueType font's
// head.checkSumAdjustment is defined relative to: adjustment = magic -
// (checksum of the whole file with checkSumAdjustment itself zeroed).
const sfntChecksumAdjustmentMagic = 0xB1B0AFBA

// packSfnt assembles tables into a complete sfnt binary: a sorted table
// directory with correct offsets/lengths/per-table checksums, 4-byte-aligned
// table data, and (if a 'head' table is present) a correctly patched
// checkSumAdjustment.
func packSfnt(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	numTables := len(tags)
	type packedEntry struct {
		tag            string
		padded         []byte
		rawLen, offset int
	}
	entries := make([]packedEntry, 0, numTables)
	offset := 12 + 16*numTables
	for _, tag := range tags {
		raw := tables[tag]
		padded := raw
		if r := len(raw) % 4; r != 0 {
			padded = append(append([]byte(nil), raw...), make([]byte, 4-r)...)
		}
		entries = append(entries, packedEntry{tag, padded, len(raw), offset})
		offset += len(padded)
	}

	buf := make([]byte, offset)
	binary.BigEndian.PutUint32(buf[0:4], 0x00010000)
	binary.BigEndian.PutUint16(buf[4:6], uint16(numTables))
	pow, exp := maxPow2LE(numTables)
	searchRange := pow * 16
	binary.BigEndian.PutUint16(buf[6:8], uint16(searchRange))
	binary.BigEndian.PutUint16(buf[8:10], uint16(exp))
	binary.BigEndian.PutUint16(buf[10:12], uint16(numTables*16-searchRange))

	headOffset := -1
	for i, e := range entries {
		rec := buf[12+i*16 : 12+i*16+16]
		copy(rec[0:4], e.tag)
		binary.BigEndian.PutUint32(rec[8:12], uint32(e.offset))
		binary.BigEndian.PutUint32(rec[12:16], uint32(e.rawLen))
		copy(buf[e.offset:e.offset+len(e.padded)], e.padded)
		if e.tag == "head" {
			headOffset = e.offset
		}
	}
	patchChecksums := func() {
		for i, e := range entries {
			cs := tableChecksum(buf[e.offset : e.offset+len(e.padded)])
			binary.BigEndian.PutUint32(buf[12+i*16+4:12+i*16+8], cs)
		}
	}
	patchChecksums()

	if headOffset >= 0 && headOffset+12 <= len(buf) {
		binary.BigEndian.PutUint32(buf[headOffset+8:headOffset+12], 0)
		adjustment := sfntChecksumAdjustmentMagic - tableChecksum(buf)
		binary.BigEndian.PutUint32(buf[headOffset+8:headOffset+12], adjustment)
		patchChecksums() // head's own bytes changed, so its directory checksum must be redone
	}
	return buf
}
