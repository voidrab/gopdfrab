package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// newTestFileReader builds a bare Reader over in-memory bytes, bypassing
// Open/OpenBytes's structure parsing so ParseXRefSectionAt can be driven
// directly against hand-crafted xref sections at a known offset.
func newTestFileReader(data []byte) *Reader {
	return &Reader{file: bytesFileSource{bytes.NewReader(data)}, xrefTable: map[int]int64{}}
}

// TestParseXRefSectionAt covers a valid classic table+trailer, a bad (past
// EOF) offset, a missing "xref" keyword, and a missing "trailer" keyword.
func TestParseXRefSectionAt(t *testing.T) {
	t.Run("valid table and trailer", func(t *testing.T) {
		data := []byte("xref\n0 2\n0000000000 65535 f \n0000000010 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\n")
		r := newTestFileReader(data)
		dict, err := r.ParseXRefSectionAt(0, false)
		if err != nil {
			t.Fatalf("ParseXRefSectionAt: %v", err)
		}
		if !EqualPDFValue(dict.Entries["Root"], PDFRef{ObjNum: 1, GenNum: 0}) {
			t.Errorf("trailer Root = %v, want 1 0 R", dict.Entries["Root"])
		}
		if r.xrefTable[1] != 10 {
			t.Errorf("xrefTable[1] = %d, want 10", r.xrefTable[1])
		}
		if _, ok := r.xrefTable[0]; ok {
			t.Error("free entry (object 0) should not be recorded")
		}
	})

	t.Run("bad offset past EOF", func(t *testing.T) {
		data := []byte("xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 >>\n")
		r := newTestFileReader(data)
		if _, err := r.ParseXRefSectionAt(int64(len(data)+100), false); err == nil {
			t.Error("expected error reading past EOF")
		}
	})

	t.Run("missing xref keyword", func(t *testing.T) {
		data := []byte("notxref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 >>\n")
		r := newTestFileReader(data)
		_, err := r.ParseXRefSectionAt(0, false)
		if err == nil || !strings.Contains(err.Error(), "expected 'xref'") {
			t.Errorf("err = %v, want mention of missing 'xref'", err)
		}
	})

	t.Run("missing trailer keyword", func(t *testing.T) {
		data := []byte("xref\n0 1\n0000000000 65535 f \nNOTRAILER\n")
		r := newTestFileReader(data)
		_, err := r.ParseXRefSectionAt(0, false)
		if err == nil || !strings.Contains(err.Error(), "expected 'trailer'") {
			t.Errorf("err = %v, want mention of missing 'trailer'", err)
		}
	})

	t.Run("fillIn true skips, false overwrites", func(t *testing.T) {
		data := []byte("xref\n0 2\n0000000000 65535 f \n0000000123 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\n")
		r := newTestFileReader(data)
		r.xrefTable = map[int]int64{1: 999} // pre-existing "newer revision" entry

		if _, err := r.ParseXRefSectionAt(0, true); err != nil {
			t.Fatalf("ParseXRefSectionAt (fillIn): %v", err)
		}
		if r.xrefTable[1] != 999 {
			t.Errorf("fillIn=true overwrote existing entry: xrefTable[1] = %d, want 999", r.xrefTable[1])
		}

		if _, err := r.ParseXRefSectionAt(0, false); err != nil {
			t.Fatalf("ParseXRefSectionAt (no fillIn): %v", err)
		}
		if r.xrefTable[1] != 123 {
			t.Errorf("fillIn=false did not overwrite: xrefTable[1] = %d, want 123", r.xrefTable[1])
		}
	})
}
