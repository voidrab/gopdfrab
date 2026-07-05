package pdf

import "testing"

// packLZWCodes packs codes MSB-first into bytes, matching lzwBitReader.read,
// so tests can hand-craft LZW bitstreams without a real encoder.
func packLZWCodes(codes []int, width int) []byte {
	var out []byte
	var buf byte
	var nbits int
	for _, code := range codes {
		for i := width - 1; i >= 0; i-- {
			buf = buf<<1 | byte((code>>i)&1)
			nbits++
			if nbits == 8 {
				out = append(out, buf)
				buf = 0
				nbits = 0
			}
		}
	}
	if nbits > 0 {
		buf <<= 8 - nbits
		out = append(out, buf)
	}
	return out
}

func TestDecodeLZW(t *testing.T) {
	tests := []struct {
		name    string
		codes   []int
		want    string
		wantErr bool
	}{
		{"literal repeat", []int{65, 65, lzwEOD}, "AA", false},
		{"KwKwK self-reference", []int{65, lzwFirstCode, lzwEOD}, "AAA", false},
		{"clear table mid-stream", []int{65, lzwClearTable, 66, lzwEOD}, "AB", false},
		{"invalid code", []int{65, 300}, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := packLZWCodes(tc.codes, 9)
			got, err := DecodeLZW(data)
			if (err != nil) != tc.wantErr {
				t.Fatalf("DecodeLZW() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && string(got) != tc.want {
				t.Errorf("DecodeLZW() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDecodeLZWTruncatedStream(t *testing.T) {
	// Fewer than 9 bits available: br.read fails immediately, so DecodeLZW
	// returns whatever was accumulated so far (nothing) with no error.
	got, err := DecodeLZW([]byte{0x01})
	if err != nil {
		t.Fatalf("DecodeLZW() error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("DecodeLZW() = %q, want empty", got)
	}
}

// packLZWCodesVarWidth is packLZWCodes generalized to a per-code width, for
// streams that cross a code-width growth boundary mid-stream.
func packLZWCodesVarWidth(codes []int, widths []int) []byte {
	var out []byte
	var buf byte
	var nbits int
	for i, code := range codes {
		width := widths[i]
		for b := width - 1; b >= 0; b-- {
			buf = buf<<1 | byte((code>>b)&1)
			nbits++
			if nbits == 8 {
				out = append(out, buf)
				buf = 0
				nbits = 0
			}
		}
	}
	if nbits > 0 {
		buf <<= 8 - nbits
		out = append(out, buf)
	}
	return out
}

// TestDecodeLZWWidthGrowsAt511 covers the codeWidth-growth boundary: enough
// table entries are added (one per repeated literal after the first) that
// nextCode reaches 511 mid-stream, bumping codeWidth from 9 to 10 bits one
// code before the table size alone would require.
func TestDecodeLZWWidthGrowsAt511(t *testing.T) {
	// 254 width-9 codes: literal 65, then 253 repeats. Each repeat after the
	// first adds one table entry (prev+entry[0]), so nextCode goes
	// 258 -> 511 over those 253 additions.
	codes := make([]int, 254)
	widths := make([]int, 254)
	for i := range codes {
		codes[i] = 65
		widths[i] = 9
	}
	// Once nextCode hits 511, codeWidth becomes 10 for all subsequent codes.
	codes = append(codes, 66, lzwEOD)
	widths = append(widths, 10, 10)

	data := packLZWCodesVarWidth(codes, widths)
	got, err := DecodeLZW(data)
	if err != nil {
		t.Fatalf("DecodeLZW() error = %v, want nil", err)
	}
	want := make([]byte, 0, 255)
	for i := 0; i < 254; i++ {
		want = append(want, 'A')
	}
	want = append(want, 'B')
	if string(got) != string(want) {
		t.Errorf("DecodeLZW() = %q (len %d), want %d 'A's followed by 'B' (len %d)", got, len(got), 254, len(want))
	}
}

func TestLZWBitReaderRead(t *testing.T) {
	r := lzwBitReader{data: []byte{0xFF}}
	if _, ok := r.read(9); ok {
		t.Error("read(9) on a single byte should report ok=false")
	}
}
