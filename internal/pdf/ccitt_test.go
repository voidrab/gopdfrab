package pdf

import (
	"bytes"
	"testing"
)

func TestDecodeCCITT1DAllWhite(t *testing.T) {
	// 8 white pixels: white terminating run 8 = code 10011 (5 bits),
	// byte-aligned to 0x98. With BlackIs1 false, white -> sample bit 1.
	got, err := DecodeCCITT([]byte{0x98}, CCITTParams{Columns: 8, Rows: 1, K: 0})
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}
	if want := []byte{0xFF}; !bytes.Equal(got, want) {
		t.Errorf("all-white row = %08b, want %08b", got, want)
	}
}

func TestDecodeCCITT1DSplit(t *testing.T) {
	// 4 white (1011) then 4 black (011): 1011011 -> 0xB6.
	got, err := DecodeCCITT([]byte{0xB6}, CCITTParams{Columns: 8, Rows: 1, K: 0})
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}
	if want := []byte{0xF0}; !bytes.Equal(got, want) {
		t.Errorf("4w4b row = %08b, want %08b", got, want)
	}
}

func TestDecodeCCITT1DBlackIs1(t *testing.T) {
	// Same 4w4b stream, but BlackIs1 flips the sample mapping: black -> 1.
	got, err := DecodeCCITT([]byte{0xB6}, CCITTParams{Columns: 8, Rows: 1, K: 0, BlackIs1: true})
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}
	if want := []byte{0x0F}; !bytes.Equal(got, want) {
		t.Errorf("4w4b row (BlackIs1) = %08b, want %08b", got, want)
	}
}

func TestDecodeCCITTGroup4Vertical(t *testing.T) {
	// Group 4 (K<0): an all-white first row is a single V0 mode bit (1),
	// padded to 0x80. Reference line is implicitly all white.
	got, err := DecodeCCITT([]byte{0x80}, CCITTParams{Columns: 8, Rows: 1, K: -1})
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}
	if want := []byte{0xFF}; !bytes.Equal(got, want) {
		t.Errorf("G4 all-white row = %08b, want %08b", got, want)
	}
}

func TestDecodeCCITTGroup4HorizontalSplit(t *testing.T) {
	// Group 4 row, 4 white + 4 black via Horizontal mode (001) then the two
	// runs (white 4 = 1011, black 4 = 011): 001 1011 011 = 00110110 11,
	// padded to 0x36 0xC0.
	got, err := DecodeCCITT([]byte{0x36, 0xC0}, CCITTParams{Columns: 8, Rows: 1, K: -1})
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}
	if want := []byte{0xF0}; !bytes.Equal(got, want) {
		t.Errorf("G4 4w4b row = %08b, want %08b", got, want)
	}
}

func TestDecodeCCITTRowPadding(t *testing.T) {
	// One encoded all-white row but Rows=2: the missing row is padded white.
	got, err := DecodeCCITT([]byte{0x98}, CCITTParams{Columns: 8, Rows: 2, K: 0})
	if err != nil {
		t.Fatalf("decodeCCITT: %v", err)
	}
	if want := []byte{0xFF, 0xFF}; !bytes.Equal(got, want) {
		t.Errorf("padded rows = %08b, want %08b", got, want)
	}
}

// TestCCITTReadMode drives readMode through every 2D mode code plus the EOF,
// 2D-extension, and EOL disambiguation branches.
func TestCCITTReadMode(t *testing.T) {
	cases := []struct {
		data []byte
		want int
	}{
		{[]byte{0b10000000}, modeV0},    // 1
		{[]byte{0b01100000}, modeVR1},   // 011
		{[]byte{0b01000000}, modeVL1},   // 010
		{[]byte{0b00100000}, modeHoriz}, // 001
		{[]byte{0b00010000}, modePass},  // 0001
		{[]byte{0b00001100}, modeVR2},   // 000011
		{[]byte{0b00001000}, modeVL2},   // 000010
		{[]byte{0b00000110}, modeVR3},   // 0000011
		{[]byte{0b00000100}, modeVL3},   // 0000010
	}
	for _, c := range cases {
		br := &ccittBitReader{data: c.data}
		got, err := br.readMode()
		if err != nil || got != c.want {
			t.Errorf("readMode(%08b) = %d, %v; want %d", c.data[0], got, err, c.want)
		}
	}

	// Empty stream -> EOF.
	if _, err := (&ccittBitReader{}).readMode(); err != errCCITTEOF {
		t.Errorf("readMode(empty) err = %v, want EOF", err)
	}
	// 2D extension code (0000001 1) -> error.
	if _, err := (&ccittBitReader{data: []byte{0b00000011}}).readMode(); err == nil {
		t.Error("expected error for a 2D extension code")
	}
	// >=11 leading zeros then 1 -> EOL.
	if _, err := (&ccittBitReader{data: []byte{0x00, 0x10}}).readMode(); err != errCCITTEOL {
		t.Errorf("readMode(EOL) err = %v, want EOL", err)
	}
	// Runs out of bits mid-scan -> EOF.
	if _, err := (&ccittBitReader{data: []byte{0x00}}).readMode(); err != errCCITTEOF {
		t.Errorf("readMode(truncated) err = %v, want EOF", err)
	}
}

// TestCCITTVDelta covers vDelta for every mode.
func TestCCITTVDelta(t *testing.T) {
	want := map[int]int{
		modeV0: 0, modeVR1: 1, modeVR2: 2, modeVR3: 3,
		modeVL1: -1, modeVL2: -2, modeVL3: -3, modeHoriz: 0,
	}
	for mode, w := range want {
		if got := vDelta(mode); got != w {
			t.Errorf("vDelta(%d) = %d, want %d", mode, got, w)
		}
	}
}

// TestCCITTByteAlign exercises the ByteAlign path (align between rows).
func TestCCITTByteAlign(t *testing.T) {
	// Two byte-aligned all-white G3-1D rows (white run 8 = 0x98, 5 bits each).
	got, err := DecodeCCITT([]byte{0x98, 0x98}, CCITTParams{Columns: 8, Rows: 2, K: 0, ByteAlign: true})
	if err != nil {
		t.Fatalf("DecodeCCITT: %v", err)
	}
	if want := []byte{0xFF, 0xFF}; !bytes.Equal(got, want) {
		t.Errorf("byte-aligned rows = %08b, want %08b", got, want)
	}
}

// TestCCITTGroup4TwoRowsVertical decodes a second G4 row that reproduces the
// first via V0 modes, exercising decode2DRow/findB1B2 against a non-trivial
// reference line.
func TestCCITTGroup4TwoRowsVertical(t *testing.T) {
	// Row1: 4w4b via Horizontal (001 1011 011). Row2: two V0 codes ("11").
	got, err := DecodeCCITT([]byte{0x36, 0xF0}, CCITTParams{Columns: 8, Rows: 2, K: -1})
	if err != nil {
		t.Fatalf("DecodeCCITT: %v", err)
	}
	if want := []byte{0xF0, 0xF0}; !bytes.Equal(got, want) {
		t.Errorf("G4 two rows = %08b, want %08b", got, want)
	}
}

// TestCCITTErrorString covers the ccittError.Error method.
func TestCCITTErrorString(t *testing.T) {
	if errCCITTEOF.Error() != "ccitt: unexpected end of data" {
		t.Errorf("errCCITTEOF.Error() = %q", errCCITTEOF.Error())
	}
	if errCCITTEOL.Error() == "" {
		t.Error("errCCITTEOL.Error() is empty")
	}
}
