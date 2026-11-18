package verify

import (
	"testing"
)

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
