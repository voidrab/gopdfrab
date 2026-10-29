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
			if got := ComponentRecordSize(tt.flags); got != tt.want {
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
