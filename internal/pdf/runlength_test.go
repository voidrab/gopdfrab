package pdf

import (
	"bytes"
	"errors"
	"testing"
)

// TestDecodeRunLength covers the literal, repeat and EOD forms plus the
// leniency toward truncated runs.
func TestDecodeRunLength(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		want []byte
	}{
		{"single literal", []byte{0, 'A', 128}, []byte("A")},
		{"literal run", []byte{2, 'a', 'b', 'c', 128}, []byte("abc")},
		{"repeat run", []byte{255, 'z', 128}, []byte("zz")},
		{"long repeat", []byte{129, 'q', 128}, bytes.Repeat([]byte("q"), 128)},
		{"literal then repeat", []byte{1, 'h', 'i', 254, '!', 128}, []byte("hi!!!")},
		{"empty", []byte{128}, []byte{}},
		{"no EOD decodes to the end", []byte{1, 'o', 'k'}, []byte("ok")},
		{"bytes after EOD are ignored", []byte{0, 'A', 128, 0, 'B'}, []byte("A")},
		{"truncated literal returns the prefix", []byte{5, 'a', 'b'}, []byte("ab")},
		{"truncated repeat header returns the prefix", []byte{0, 'a', 200}, []byte("a")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeRunLength(tc.in)
			if err != nil {
				t.Fatalf("DecodeRunLength: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("DecodeRunLength(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// A maximal literal run copies 128 bytes.
	maxLiteral := append([]byte{127}, bytes.Repeat([]byte("x"), 128)...)
	got, err := DecodeRunLength(append(maxLiteral, 128))
	if err != nil {
		t.Fatalf("DecodeRunLength(max literal): %v", err)
	}
	if len(got) != 128 {
		t.Errorf("max literal run = %d bytes, want 128", len(got))
	}
}

// TestDecodeRunLengthOutputCap covers the size ceiling that keeps a crafted
// run of repeats from exhausting memory.
func TestDecodeRunLengthOutputCap(t *testing.T) {
	restore := maxRunLengthOutput
	maxRunLengthOutput = 64
	defer func() { maxRunLengthOutput = restore }()

	// Each repeat run emits 128 bytes, so the second trips the 64-byte cap.
	in := []byte{129, 'a', 129, 'b', 128}
	if _, err := DecodeRunLength(in); !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("DecodeRunLength over cap = %v, want ErrOutputTooLarge", err)
	}
}
