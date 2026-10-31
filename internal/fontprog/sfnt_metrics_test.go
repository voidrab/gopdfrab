package fontprog

import (
	"encoding/binary"
	"os"
	"testing"
)

// TestScaledCapHeight covers the too-short-table and upm==0 fallbacks
// alongside the normal scaled-reading path.
func TestScaledCapHeight(t *testing.T) {
	validHead := make([]byte, 20)
	binary.BigEndian.PutUint16(validHead[18:20], 1000) // unitsPerEm

	zeroUPMHead := make([]byte, 20) // unitsPerEm left at 0

	validOS2 := make([]byte, 90)
	binary.BigEndian.PutUint16(validOS2[88:90], 700) // sCapHeight

	tests := []struct {
		name   string
		tables map[string][]byte
		want   int
	}{
		{"short OS/2", map[string][]byte{"OS/2": make([]byte, 10), "head": validHead}, -1},
		{"short head", map[string][]byte{"OS/2": validOS2, "head": make([]byte, 5)}, -1},
		{"upm zero", map[string][]byte{"OS/2": validOS2, "head": zeroUPMHead}, -1},
		{"valid", map[string][]byte{"OS/2": validOS2, "head": validHead}, 700},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const fallback = -1
			if got := ScaledCapHeight(tt.tables, fallback); got != tt.want {
				t.Errorf("ScaledCapHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCmapSubtableCount covers the ParseSfnt-failure path (garbage
// input) and the success path (a real embedded face).
func TestCmapSubtableCount(t *testing.T) {
	if _, ok := CmapSubtableCount([]byte("not a font")); ok {
		t.Error("CmapSubtableCount(garbage) ok = true, want false")
	}

	data, err := os.ReadFile("../fontsubst/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("bundled font not present: %v", err)
	}
	n, ok := CmapSubtableCount(data)
	if !ok || n <= 0 {
		t.Errorf("CmapSubtableCount(Liberation Sans) = (%d,%v), want a positive count", n, ok)
	}
}

// TestScaledAscentDescent covers the short-table and zero-unitsPerEm
// fallbacks alongside the scaled reading.
func TestScaledAscentDescent(t *testing.T) {
	head := make([]byte, 20)
	binary.BigEndian.PutUint16(head[18:20], 2000) // unitsPerEm
	hhea := make([]byte, 8)
	binary.BigEndian.PutUint16(hhea[4:6], 1600)           // ascender
	binary.BigEndian.PutUint16(hhea[6:8], uint16(0xFC18)) // descender -1000

	for name, tc := range map[string]struct {
		tables          map[string][]byte
		ascent, descent int
	}{
		"short hhea": {map[string][]byte{"hhea": make([]byte, 4), "head": head}, 0, 0},
		"short head": {map[string][]byte{"hhea": hhea, "head": make([]byte, 4)}, 0, 0},
		"zero upm":   {map[string][]byte{"hhea": hhea, "head": make([]byte, 20)}, 0, 0},
		"scaled":     {map[string][]byte{"hhea": hhea, "head": head}, 800, -500},
	} {
		if a, d := ScaledAscentDescent(tc.tables); a != tc.ascent || d != tc.descent {
			t.Errorf("%s: ScaledAscentDescent = %d, %d; want %d, %d", name, a, d, tc.ascent, tc.descent)
		}
	}
}
