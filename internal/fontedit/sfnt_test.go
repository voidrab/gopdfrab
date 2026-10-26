package fontedit

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

func TestBuildCmapFormat4Table(t *testing.T) {
	// One mapping, 'A' -> gid 1, spelled out byte by byte: header, the 'A'
	// segment plus the 0xFFFF terminator, each segment's idRangeOffset
	// pointing at its own slot in the glyph array.
	want := []byte{
		0, 0, 0, 1, // cmap: version, numTables
		0, 3, 0, 1, 0, 0, 0, 12, // record: (3,1) at offset 12
		0, 4, 0, 34, 0, 0, // format 4, length 34, language 0
		0, 4, 0, 4, 0, 1, 0, 0, // segCountX2, searchRange, entrySelector, rangeShift
		0, 65, 0xFF, 0xFF, // endCode
		0, 0, // reservedPad
		0, 65, 0xFF, 0xFF, // startCode
		0, 0, 0, 1, // idDelta
		0, 4, 0, 0, // idRangeOffset
		0, 1, // glyphIdArray
	}
	if got := BuildCmapFormat4Table(3, 1, map[uint16]uint16{65: 1}); !reflect.DeepEqual(got, want) {
		t.Errorf("buildCmapFormat4Table =\n%v\nwant\n%v", got, want)
	}

	// Round trip with several codes (sorted into segments) and the empty map.
	in := map[uint16]uint16{300: 9, 65: 1, 66: 2, 0x20: 3}
	if out := fontprog.ParseCmapFormat4(BuildCmapFormat4Table(3, 1, in)[12:]); !reflect.DeepEqual(out, in) {
		t.Errorf("round trip = %v, want %v", out, in)
	}
	if out := fontprog.ParseCmapFormat4(BuildCmapFormat4Table(3, 1, nil)[12:]); out == nil || len(out) != 0 {
		t.Errorf("empty cmap decoded to %v, want an empty non-nil map", out)
	}
}

func TestMaxPow2LE(t *testing.T) {
	cases := []struct{ n, pow, exp int }{{1, 1, 0}, {2, 2, 1}, {3, 2, 1}, {4, 4, 2}, {17, 16, 4}}
	for _, c := range cases {
		if pow, exp := maxPow2LE(c.n); pow != c.pow || exp != c.exp {
			t.Errorf("maxPow2LE(%d) = %d, %d, want %d, %d", c.n, pow, exp, c.pow, c.exp)
		}
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
