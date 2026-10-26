package fontprog

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

// subsetSource is a seven-glyph font: 0 .notdef, 1 'A', 2 'B' (a composite of
// glyphs 4 and 1), 3 unused, 4 a component-only glyph, 5 a blank space with an
// advance, 6 'C' with an odd-length record.
func subsetSource() ttFont {
	return ttFont{
		glyphs: []ttGlyph{
			{rec: simpleGlyph(12, 0xA0), aw: 500, lsb: 1},
			{rec: simpleGlyph(14, 0xA1), aw: 600, lsb: 2},
			{rec: compositeGlyph(4, 1), aw: 700, lsb: 3},
			{rec: simpleGlyph(12, 0xA3), aw: 800, lsb: 4},
			{rec: simpleGlyph(16, 0xA4), aw: 900, lsb: 5},
			{rec: nil, aw: 250, lsb: 0},
			{rec: simpleGlyph(13, 0xA6), aw: 650, lsb: 6},
		},
		cmap:  map[uint16]uint16{'A': 1, 'B': 2, ' ': 5, 'C': 6, 'Z': 3},
		extra: map[string][]byte{"OS/2": {1, 2, 3, 4}, "name": {5, 6, 7, 8}, "cvt ": {9, 9, 9, 9}},
	}
}

// tt parses a built font and fails the test if it is not a valid sfnt.
func tt(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	tables, ok := ParseSfnt(data)
	if !ok {
		t.Fatal("output is not a valid sfnt")
	}
	return tables
}

func TestGlyfRecord(t *testing.T) {
	long := tt(t, subsetSource().build())
	if got := GlyfRecord(long, 5); len(got) != 0 {
		t.Errorf("blank glyph = %v, want empty", got)
	}
	if got := GlyfRecord(long, 99); got != nil {
		t.Errorf("out-of-range glyph = %v, want nil", got)
	}
	if got := GlyfRecord(map[string][]byte{}, 0); got != nil {
		t.Error("no head table should give nil")
	}

	short := subsetSource()
	short.shortLoca = true
	st := tt(t, short.build())
	if got := GlyfRecord(st, 1); !bytes.Equal(got, simpleGlyph(14, 0xA1)) {
		t.Errorf("short-loca glyph 1 = %v", got)
	}
	if got := GlyfRecord(st, 99); got != nil {
		t.Errorf("short-loca out-of-range glyph = %v, want nil", got)
	}

	// A span reaching past the glyf table, and one running backwards.
	bad := map[string][]byte{"head": long["head"], "glyf": make([]byte, 4)}
	bad["loca"] = []byte{0, 0, 0, 0, 0, 0, 0, 100}
	if GlyfRecord(bad, 0) != nil {
		t.Error("a span past glyf should give nil")
	}
	bad["loca"] = []byte{0, 0, 0, 3, 0, 0, 0, 1}
	if GlyfRecord(bad, 0) != nil {
		t.Error("a backwards span should give nil")
	}
}

func TestGlyfIsCompositeAndComponents(t *testing.T) {
	if GlyfIsComposite(nil) || GlyfIsComposite([]byte{0}) {
		t.Error("a record shorter than 2 bytes is not composite")
	}
	if GlyfIsComposite(simpleGlyph(12, 0)) {
		t.Error("a simple glyph is not composite")
	}
	if !GlyfIsComposite(compositeGlyph(1)) {
		t.Error("numberOfContours < 0 is composite")
	}
	if GlyfComponents(simpleGlyph(12, 0)) != nil {
		t.Error("a simple glyph has no components")
	}
	if got := GlyfComponents(compositeGlyph(7, 8, 9)); !reflect.DeepEqual(got, []int{7, 8, 9}) {
		t.Errorf("components = %v, want [7 8 9]", got)
	}
	// MORE_COMPONENTS set but the record ends: the scan stops without panicking.
	cut := compositeGlyph(7, 8)[:10+6+2]
	if got := GlyfComponents(cut); !reflect.DeepEqual(got, []int{7}) {
		t.Errorf("truncated composite components = %v, want [7]", got)
	}
}

func TestPackSfntShortHead(t *testing.T) {
	// A head table shorter than the checkSumAdjustment field is packed as is,
	// without the adjustment.
	out := PackSfnt(map[string][]byte{"head": {1, 2, 3, 4}})
	tables := tt(t, out)
	if !bytes.Equal(tables["head"], []byte{1, 2, 3, 4}) {
		t.Errorf("head = %v", tables["head"])
	}
}

func TestPackSfntChecksums(t *testing.T) {
	head := make([]byte, 54)
	out := PackSfnt(map[string][]byte{"head": head, "zzzz": {1, 2, 3}})
	tables := tt(t, out)
	if !bytes.Equal(tables["zzzz"], []byte{1, 2, 3}) {
		t.Errorf("unaligned table = %v, want the original 3 bytes", tables["zzzz"])
	}

	// Directory entries are sorted by tag: head, then zzzz.
	zzzzSum := binary.BigEndian.Uint32(out[12+16+4:])
	if want := uint32(1)<<24 | 2<<16 | 3<<8; zzzzSum != want {
		t.Errorf("zzzz checksum = %#x, want %#x", zzzzSum, want)
	}

	// Per the OpenType spec, head's directory checksum is taken with
	// checkSumAdjustment as 0, and the adjustment makes the whole file sum
	// to 0xB1B0AFBA.
	headEntry := out[12 : 12+16]
	headOff := binary.BigEndian.Uint32(headEntry[8:])
	adj := binary.BigEndian.Uint32(out[headOff+8:])
	if adj == 0 {
		t.Error("checkSumAdjustment was not written")
	}
	zeroed := append([]byte(nil), tables["head"]...)
	binary.BigEndian.PutUint32(zeroed[8:], 0)
	if got, want := binary.BigEndian.Uint32(headEntry[4:]), tableChecksum(zeroed); got != want {
		t.Errorf("head directory checksum = %#x, want %#x (adjustment zeroed)", got, want)
	}
	if got := tableChecksum(out); got != SfntChecksumAdjustmentMagic {
		t.Errorf("whole-file checksum = %#x, want %#x", got, uint32(SfntChecksumAdjustmentMagic))
	}
}
