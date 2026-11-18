package ber

import (
	"bytes"
	"testing"
)

func TestReadBERRejectsMalformed(t *testing.T) {
	deep := bytes.Repeat([]byte{0x30, 0x80}, maxDepth+2)
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"truncated", []byte{0x30}},
		{"multi-octet tag", []byte{0x1f, 0x01, 0x00}},
		{"indefinite primitive", []byte{0x04, 0x80, 0x00, 0x00}},
		{"indefinite with a bad element", []byte{0x30, 0x80, 0x04}},
		{"indefinite too deep", deep},
		{"length of five octets", []byte{0x04, 0x85, 1, 0, 0, 0, 0}},
		{"length octets missing", []byte{0x04, 0x82, 1}},
		{"content shorter than its length", []byte{0x04, 0x81, 0x05, 1}},
	} {
		if _, _, ok := ReadBER(tc.in); ok {
			t.Errorf("%s: ReadBER(% x) succeeded", tc.name, tc.in)
		}
	}
	e, rest, ok := ReadBER([]byte{0x04, 0x81, 0x01, 0xaa, 0xbb})
	if !ok || e.Tag != 0x04 || !bytes.Equal(e.Content, []byte{0xaa}) || !bytes.Equal(rest, []byte{0xbb}) {
		t.Errorf("ReadBER(long form) = %+v, % x, %v", e, rest, ok)
	}
}

func TestToDERConstructedString(t *testing.T) {
	// An OCTET STRING in two pieces, with an indefinite length.
	in := []byte{0x24, 0x80, 0x04, 0x02, 'a', 'b', 0x04, 0x01, 'c', 0, 0}
	der, err := ToDER(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0x04, 0x03, 'a', 'b', 'c'}; string(der) != string(want) {
		t.Errorf("ToDER = % x, want % x", der, want)
	}
	if _, err := ToDER([]byte{0x30, 0x80, 0x04}); err == nil {
		t.Error("ToDER accepted a truncated value")
	}
}
