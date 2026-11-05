package verify

import (
	"bytes"
	"testing"
)

func TestReadBERRejectsMalformed(t *testing.T) {
	deep := bytes.Repeat([]byte{0x30, 0x80}, maxBERDepth+2)
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
		if _, _, ok := readBER(tc.in); ok {
			t.Errorf("%s: readBER(% x) succeeded", tc.name, tc.in)
		}
	}
	e, rest, ok := readBER([]byte{0x04, 0x81, 0x01, 0xaa, 0xbb})
	if !ok || e.tag != 0x04 || !bytes.Equal(e.content, []byte{0xaa}) || !bytes.Equal(rest, []byte{0xbb}) {
		t.Errorf("readBER(long form) = %+v, % x, %v", e, rest, ok)
	}
}

func TestReadSignedDataRejectsOtherShapes(t *testing.T) {
	seq := func(content ...byte) []byte { return append([]byte{0x30, byte(len(content))}, content...) }
	oid := append([]byte{0x06, byte(len(oidSignedData))}, oidSignedData...)
	wrap := func(sd []byte) []byte {
		return seq(append(append([]byte{}, oid...), append([]byte{0xa0, byte(len(sd))}, sd...)...)...)
	}
	integer := []byte{0x02, 0x01, 0x01}
	set := []byte{0x31, 0x00}
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"not a sequence", []byte{0x04, 0x00}},
		{"bad content", seq(0x04)},
		{"other content type", seq(0x06, 0x01, 0x2a, 0xa0, 0x00)},
		{"SignedData not a sequence", wrap([]byte{0x04, 0x00})},
		{"too few fields", wrap(seq(integer...))},
		{"version not an integer", wrap(seq(append(append(append([]byte{0x04, 0x00}, set...), seq()...), set...)...))},
		{"bad certificates", wrap(seq(append(append(append(append([]byte{}, integer...), set...), seq()...), 0xa0, 0x01, 0x30)...))},
	} {
		if s := readSignedData(tc.in); s != (signedDataSummary{}) {
			t.Errorf("%s: readSignedData = %+v, want the zero summary", tc.name, s)
		}
	}

	// An attribute certificate is skipped, and signerInfos must be a SET.
	attrCert := []byte{0xa1, 0x00}
	fields := append(append(append(append([]byte{}, integer...), set...), seq()...), 0xa0, byte(len(attrCert)))
	fields = append(append(fields, attrCert...), 0x04, 0x00)
	if s := readSignedData(wrap(seq(fields...))); s != (signedDataSummary{}) {
		t.Errorf("attribute certificate only: readSignedData = %+v, want the zero summary", s)
	}
}
