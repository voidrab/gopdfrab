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

func TestLZWBitReaderRead(t *testing.T) {
	r := lzwBitReader{data: []byte{0xFF}}
	if _, ok := r.read(9); ok {
		t.Error("read(9) on a single byte should report ok=false")
	}
}
