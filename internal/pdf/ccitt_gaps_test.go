package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// bitsToBytes packs a string of '0'/'1' (spaces ignored) MSB-first, zero
// padding the final byte.
func bitsToBytes(s string) []byte {
	s = strings.ReplaceAll(s, " ", "")
	out := make([]byte, (len(s)+7)/8)
	for i, c := range s {
		if c == '1' {
			out[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return out
}

func TestCCITTDefaultColumns(t *testing.T) {
	// Columns omitted -> 1728. White 1728 = make-up 010011011 + terminator 00110101.
	got, err := DecodeCCITT(bitsToBytes("010011011 00110101"), CCITTParams{Rows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := bytes.Repeat([]byte{0xFF}, 216); !bytes.Equal(got, want) {
		t.Errorf("default-width row wrong: len %d", len(got))
	}
}

func TestCCITTSizeLimits(t *testing.T) {
	if _, err := DecodeCCITT([]byte{0}, CCITTParams{Columns: 1<<20 + 1}); err == nil {
		t.Error("expected error for oversized Columns")
	}
	if _, err := DecodeCCITT([]byte{0}, CCITTParams{Columns: 1 << 20, Rows: 1 << 10}); err == nil {
		t.Error("expected error for oversized declared image")
	}
}

func TestCCITTOnlyEOLDecodesNothing(t *testing.T) {
	// A lone EOL consumes the whole stream: no row, hence an error.
	_, err := DecodeCCITT(bitsToBytes("0000000000000001"), CCITTParams{Columns: 8, Rows: 1})
	if err == nil || !strings.Contains(err.Error(), "no Rows decoded") {
		t.Errorf("err = %v, want no Rows decoded", err)
	}
}

func TestCCITTMixedGroup3(t *testing.T) {
	// K>0: tag bit 1 = a 1D row (white 8), tag bit 0 = a 2D row (V0).
	got, err := DecodeCCITT(bitsToBytes("1 10011 0 1"), CCITTParams{Columns: 8, Rows: 2, K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xFF, 0xFF}; !bytes.Equal(got, want) {
		t.Errorf("rows = %08b, want %08b", got, want)
	}
}

func TestCCITTInvalidRunCode(t *testing.T) {
	if _, err := DecodeCCITT([]byte{0, 0}, CCITTParams{Columns: 8, Rows: 1}); err == nil {
		t.Error("expected invalid run code error")
	}
}

func TestCCITT1DRowKeepsRunsBeforeBadCode(t *testing.T) {
	// White 4, then an undecodable code: the row keeps what it had and the
	// rest of the row is black.
	got, err := DecodeCCITT(bitsToBytes("1011 00000000000000"), CCITTParams{Columns: 8, Rows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xF0}; !bytes.Equal(got, want) {
		t.Errorf("row = %08b, want %08b", got, want)
	}
}

func TestCCITTReadModeTruncatedAtEveryDepth(t *testing.T) {
	for left := 1; left <= 7; left++ {
		br := &ccittBitReader{data: []byte{0}, pos: 8 - left}
		if _, err := br.readMode(); err != errCCITTEOF {
			t.Errorf("%d zero bits left: err = %v, want EOF", left, err)
		}
	}
}

func TestCCITTInvalid2DMode(t *testing.T) {
	// Seven zeros then a 1: neither a valid mode nor a long-enough EOL.
	if _, err := (&ccittBitReader{data: []byte{0x01}}).readMode(); err == nil || err == errCCITTEOL {
		t.Errorf("err = %v, want invalid 2D mode code", err)
	}
	if _, err := DecodeCCITT([]byte{0x01}, CCITTParams{Columns: 8, Rows: 1, K: -1}); err == nil {
		t.Error("DecodeCCITT should surface the invalid mode")
	}
}

func TestCCITTPassMode(t *testing.T) {
	// Pass (0001) skips to b2 = end of the all-white reference: a white row.
	got, err := DecodeCCITT(bitsToBytes("0001"), CCITTParams{Columns: 8, Rows: 1, K: -1})
	if err != nil || !bytes.Equal(got, []byte{0xFF}) {
		t.Errorf("got %08b, %v; want all white", got, err)
	}
}

func TestCCITTHorizontalRunErrors(t *testing.T) {
	bad := strings.Repeat("0", 14)
	for name, bits := range map[string]string{
		"first run":  "001 " + bad,
		"second run": "001 1011 " + bad,
	} {
		if _, err := DecodeCCITT(bitsToBytes(bits), CCITTParams{Columns: 8, Rows: 1, K: -1}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestCCITTReferenceInsideWrongColourRun(t *testing.T) {
	// Row 1: 4 white, 4 black. Row 2: Horizontal 2 white + 2 black leaves a0 at
	// 4 in white, so b1 must skip past the reference's black->white change.
	got, err := DecodeCCITT(bitsToBytes("001 1011 011  001 0111 11  1"), CCITTParams{Columns: 8, Rows: 2, K: -1})
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xF0, 0xCF}; !bytes.Equal(got, want) {
		t.Errorf("rows = %08b, want %08b", got, want)
	}
}
