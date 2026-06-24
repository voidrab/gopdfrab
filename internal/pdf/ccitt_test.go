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
