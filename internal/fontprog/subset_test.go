package fontprog

import (
	"encoding/binary"
	"testing"
)

// TestComponentRecordSize covers every argument/scale flag combination:
// word vs. byte args, and no-scale/scale/x-and-y-scale/two-by-two.
func TestComponentRecordSize(t *testing.T) {
	tests := []struct {
		name  string
		flags uint16
		want  int
	}{
		{"byte args, no scale", 0x0000, 6},
		{"word args, no scale", 0x0001, 8},
		{"byte args, WE_HAVE_A_SCALE", 0x0008, 8},
		{"byte args, WE_HAVE_AN_X_AND_Y_SCALE", 0x0040, 10},
		{"byte args, WE_HAVE_A_TWO_BY_TWO", 0x0080, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := componentRecordSize(tt.flags); got != tt.want {
				t.Errorf("componentRecordSize(0x%04x) = %d, want %d", tt.flags, got, tt.want)
			}
		})
	}
}

// hheaWithNumHMetrics builds a minimal 36-byte hhea table with
// numberOfHMetrics (offset 34) set to n; the other fields are unused by
// ttRawHMetric.
func hheaWithNumHMetrics(n int) []byte {
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:36], uint16(n))
	return hhea
}

// TestTTRawHMetric covers every branch: the too-short-table guards, an
// explicit hmtx entry, the beyond-numberOfHMetrics fallback that reuses the
// last advance with its own lsb, and that same fallback when even the lsb
// column is missing (advance only).
func TestTTRawHMetric(t *testing.T) {
	hhea := hheaWithNumHMetrics(2)
	// gid0: advance 500 lsb 10; gid1: advance 600 lsb 20; gid2: lsb-only 30.
	hmtx := []byte{0x01, 0xF4, 0x00, 0x0A, 0x02, 0x58, 0x00, 0x14, 0x00, 0x1E}
	tables := map[string][]byte{"hhea": hhea, "hmtx": hmtx}

	if _, _, ok := ttRawHMetric(map[string][]byte{"hhea": hhea}, 0); ok {
		t.Error("ttRawHMetric with no hmtx table: ok = true, want false")
	}
	if _, _, ok := ttRawHMetric(map[string][]byte{"hmtx": hmtx, "hhea": hheaWithNumHMetrics(0)[:10]}, 0); ok {
		t.Error("ttRawHMetric with a truncated hhea: ok = true, want false")
	}
	if _, _, ok := ttRawHMetric(map[string][]byte{"hmtx": hmtx, "hhea": hheaWithNumHMetrics(0)}, 0); ok {
		t.Error("ttRawHMetric with numberOfHMetrics=0: ok = true, want false")
	}

	if aw, lsb, ok := ttRawHMetric(tables, 0); !ok || aw != 500 || lsb != 10 {
		t.Errorf("ttRawHMetric(gid 0) = (%d, %d, %v), want (500, 10, true)", aw, lsb, ok)
	}
	if aw, lsb, ok := ttRawHMetric(tables, 2); !ok || aw != 600 || lsb != 30 {
		t.Errorf("ttRawHMetric(gid 2, beyond numberOfHMetrics) = (%d, %d, %v), want (600, 30, true)", aw, lsb, ok)
	}
	if aw, lsb, ok := ttRawHMetric(tables, 3); !ok || aw != 600 || lsb != 0 {
		t.Errorf("ttRawHMetric(gid 3, past the lsb column too) = (%d, %d, %v), want (600, 0, true)", aw, lsb, ok)
	}

	// gid < numberOfHMetrics but hmtx is truncated before that entry.
	truncated := map[string][]byte{"hhea": hheaWithNumHMetrics(5), "hmtx": hmtx[:8]}
	if _, _, ok := ttRawHMetric(truncated, 3); ok {
		t.Error("ttRawHMetric(gid within numberOfHMetrics but past truncated hmtx) ok = true, want false")
	}
}
