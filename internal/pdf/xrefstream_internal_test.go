package pdf

import (
	"bytes"
	"testing"
)

// newXRefTestFileReader wraps data as a fileSource for direct, white-box
// calls into parseIndirectObjectAt/tryParseXRefStream.
func newXRefTestFileReader(data []byte) fileSource {
	return bytesFileSource{bytes.NewReader(data)}
}

// TestParseIndirectObjectAtErrors covers every malformed-input error branch:
// a bad seek offset, each malformed header token, a dictionary parse error,
// a non-integer or negative /Length, a missing EOL after 'stream', and a
// truncated stream body.
func TestParseIndirectObjectAtErrors(t *testing.T) {
	t.Run("seek error", func(t *testing.T) {
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader([]byte("1 0 obj")), -1); err == nil {
			t.Error("expected a seek error for a negative offset")
		}
	})

	t.Run("bad object number", func(t *testing.T) {
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader([]byte("X 0 obj\n<<>>\nendobj\n")), 0); err == nil {
			t.Error("expected error for a non-integer object number")
		}
	})

	t.Run("bad generation number", func(t *testing.T) {
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader([]byte("1 X obj\n<<>>\nendobj\n")), 0); err == nil {
			t.Error("expected error for a non-integer generation number")
		}
	})

	t.Run("missing obj keyword", func(t *testing.T) {
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader([]byte("1 0 XXX\n<<>>\nendobj\n")), 0); err == nil {
			t.Error("expected error for a missing 'obj' keyword")
		}
	})

	t.Run("missing dictionary", func(t *testing.T) {
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader([]byte("1 0 obj\n42\nendobj\n")), 0); err == nil {
			t.Error("expected error when the object body is not a dictionary")
		}
	})

	t.Run("dictionary parse error", func(t *testing.T) {
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader([]byte("1 0 obj\n<< /A 1\nendobj\n")), 0); err == nil {
			t.Error("expected error for an unterminated dictionary")
		}
	})

	t.Run("no stream body: unread and return dict", func(t *testing.T) {
		objNum, dict, err := parseIndirectObjectAt(newXRefTestFileReader([]byte("1 0 obj\n<< /A 1 >>\nendobj\n")), 0)
		if err != nil {
			t.Fatalf("parseIndirectObjectAt: %v", err)
		}
		if objNum != 1 || dict.HasStream || !EqualPDFValue(dict.Entries["A"], PDFInteger(1)) {
			t.Errorf("got objNum=%d dict=%#v, want objNum=1, A=1, HasStream=false", objNum, dict)
		}
	})

	t.Run("Length not a direct integer", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Length /X >>\nstream\ndata\nendstream\nendobj\n")
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader(data), 0); err == nil {
			t.Error("expected error for a non-integer /Length")
		}
	})

	t.Run("negative Length", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Length -1 >>\nstream\ndata\nendstream\nendobj\n")
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader(data), 0); err == nil {
			t.Error("expected error for a negative /Length")
		}
	})

	t.Run("skipEOL hits EOF right after stream keyword", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Length 4 >>\nstream")
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader(data), 0); err == nil {
			t.Error("expected error when EOF follows the 'stream' keyword directly")
		}
	})

	t.Run("truncated stream body", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Length 100 >>\nstream\nshort\nendstream\nendobj\n")
		if _, _, err := parseIndirectObjectAt(newXRefTestFileReader(data), 0); err == nil {
			t.Error("expected error reading a stream body past EOF")
		}
	})
}

// buildXRefStreamBytes assembles a minimal cross-reference stream object
// (object 1) covering objects 0-2 with /W [1 1 1]: object 0 is free, object 1
// is a type-1 (classic offset) entry, object 2 is type-2 (compressed).
func buildXRefStreamBytes(dictExtra string) []byte {
	raw := []byte{
		0, 0, 0, // object 0: free
		1, 50, 0, // object 1: in use, offset 50
		2, 5, 2, // object 2: compressed in stream 5, index 2
	}
	body := "<< /Type /XRef" + dictExtra + " /Length " + itoa(len(raw)) + " >>\nstream\n"
	full := append([]byte("1 0 obj\n"+body), raw...)
	full = append(full, []byte("\nendstream\nendobj\n")...)
	return full
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// TestTryParseXRefStream covers a full success parse (type-0/1/2 entries),
// the fillIn skip-existing branches, and every error branch: a propagated
// parseIndirectObjectAt error, a missing stream body, a non-/XRef object, a
// decode failure, malformed /W, malformed /Index, and a truncated entry
// table.
func TestTryParseXRefStream(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		data := buildXRefStreamBytes(" /W [1 1 1] /Index [0 3]")
		d := &Reader{file: newXRefTestFileReader(data)}
		dict, err := d.tryParseXRefStream(0, false)
		if err != nil {
			t.Fatalf("tryParseXRefStream: %v", err)
		}
		if !EqualPDFValue(dict.Entries["Type"], PDFName{Value: "XRef"}) {
			t.Errorf("returned dict Type = %v, want /XRef", dict.Entries["Type"])
		}
		if d.xrefTable[1] != 50 {
			t.Errorf("xrefTable[1] = %d, want 50", d.xrefTable[1])
		}
		if _, ok := d.xrefTable[0]; ok {
			t.Error("object 0 (free) should not be recorded in xrefTable")
		}
		entry, ok := d.compressedXref[2]
		if !ok || entry.streamObjNum != 5 || entry.index != 2 {
			t.Errorf("compressedXref[2] = %+v, ok=%v; want {5 2}", entry, ok)
		}
	})

	t.Run("fillIn skips existing entries", func(t *testing.T) {
		data := buildXRefStreamBytes(" /W [1 1 1] /Index [0 3]")
		d := &Reader{
			file:           newXRefTestFileReader(data),
			xrefTable:      map[int]int64{1: 999},
			compressedXref: map[int]compressedXrefEntry{2: {streamObjNum: 1, index: 1}},
		}
		if _, err := d.tryParseXRefStream(0, true); err != nil {
			t.Fatalf("tryParseXRefStream (fillIn): %v", err)
		}
		if d.xrefTable[1] != 999 {
			t.Errorf("fillIn should preserve existing xrefTable[1]=999, got %d", d.xrefTable[1])
		}
		if d.compressedXref[2] != (compressedXrefEntry{streamObjNum: 1, index: 1}) {
			t.Errorf("fillIn should preserve existing compressedXref[2], got %+v", d.compressedXref[2])
		}
	})

	t.Run("propagated parseIndirectObjectAt error", func(t *testing.T) {
		d := &Reader{file: newXRefTestFileReader([]byte("X 0 obj\n"))}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected propagated error")
		}
	})

	t.Run("no stream body", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Type /XRef >>\nendobj\n")
		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected error: object has no stream body")
		}
	})

	t.Run("not a cross-reference stream", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Type /Other /Length 4 >>\nstream\ndata\nendstream\nendobj\n")
		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected error: not a /Type /XRef object")
		}
	})

	t.Run("decode failure", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Type /XRef /Filter /JPXDecode /Length 4 >>\nstream\ndata\nendstream\nendobj\n")
		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected error decoding an unsupported filter")
		}
	})

	t.Run("malformed W", func(t *testing.T) {
		data := buildXRefStreamBytes(" /Index [0 3]") // no /W at all
		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected error for missing /W")
		}
	})

	t.Run("malformed Index", func(t *testing.T) {
		data := buildXRefStreamBytes(" /W [1 1 1] /Index [0]") // odd length
		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected error for a malformed /Index")
		}
	})

	t.Run("truncated entry table", func(t *testing.T) {
		data := buildXRefStreamBytes(" /W [1 1 1] /Index [0 10]") // claims 10 entries, only has 3
		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected error for a truncated entry table")
		}
	})

	t.Run("zero-width /W fields", func(t *testing.T) {
		data := buildXRefStreamBytes(" /W [0 0 0] /Index [0 3]")
		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err == nil {
			t.Error("expected error for all-zero /W field widths")
		}
	})

	t.Run("free entry with a non-zero object number", func(t *testing.T) {
		// Object 0 is always free (skipped before the type switch); object 1
		// here is *explicitly* typed free (type 0) to exercise the switch's
		// own free-entry case, and object 2 is a normal in-use entry.
		raw := []byte{
			0, 0, 0, // object 0: free (skipped by the objNum==0 guard)
			0, 0, 0, // object 1: explicitly free via the type switch
			1, 60, 0, // object 2: in use, offset 60
		}
		body := "<< /Type /XRef /W [1 1 1] /Index [0 3] /Length " + itoa(len(raw)) + " >>\nstream\n"
		data := append([]byte("1 0 obj\n"+body), raw...)
		data = append(data, []byte("\nendstream\nendobj\n")...)

		d := &Reader{file: newXRefTestFileReader(data)}
		if _, err := d.tryParseXRefStream(0, false); err != nil {
			t.Fatalf("tryParseXRefStream: %v", err)
		}
		if _, ok := d.xrefTable[1]; ok {
			t.Error("object 1 (explicitly free) should not be recorded in xrefTable")
		}
		if d.xrefTable[2] != 60 {
			t.Errorf("xrefTable[2] = %d, want 60", d.xrefTable[2])
		}
	})
}

// TestXrefFieldWidths covers the missing-/W, wrong-length, non-integer, and
// negative-entry error branches, plus the success path.
func TestXrefFieldWidths(t *testing.T) {
	if _, err := xrefFieldWidths(PDFDict{Entries: map[string]PDFValue{}}); err == nil {
		t.Error("expected error for a missing /W")
	}
	if _, err := xrefFieldWidths(PDFDict{Entries: map[string]PDFValue{
		"W": PDFArray{PDFInteger(1), PDFInteger(1)},
	}}); err == nil {
		t.Error("expected error for a /W array of the wrong length")
	}
	if _, err := xrefFieldWidths(PDFDict{Entries: map[string]PDFValue{
		"W": PDFArray{PDFName{Value: "X"}, PDFInteger(1), PDFInteger(1)},
	}}); err == nil {
		t.Error("expected error for a non-integer /W entry")
	}
	if _, err := xrefFieldWidths(PDFDict{Entries: map[string]PDFValue{
		"W": PDFArray{PDFInteger(-1), PDFInteger(1), PDFInteger(1)},
	}}); err == nil {
		t.Error("expected error for a negative /W entry")
	}
	w, err := xrefFieldWidths(PDFDict{Entries: map[string]PDFValue{
		"W": PDFArray{PDFInteger(1), PDFInteger(2), PDFInteger(1)},
	}})
	if err != nil || w != [3]int{1, 2, 1} {
		t.Errorf("xrefFieldWidths = %v, %v; want {1 2 1}", w, err)
	}
}

// TestXrefIndexRanges covers the default-from-/Size path (and its
// missing-/Size error), the odd-length and non-integer /Index errors, and an
// explicit multi-range /Index success.
func TestXrefIndexRanges(t *testing.T) {
	if _, err := xrefIndexRanges(PDFDict{Entries: map[string]PDFValue{}}); err == nil {
		t.Error("expected error when both /Index and /Size are missing")
	}

	ranges, err := xrefIndexRanges(PDFDict{Entries: map[string]PDFValue{"Size": PDFInteger(5)}})
	if err != nil || len(ranges) != 1 || ranges[0] != (xrefRange{start: 0, count: 5}) {
		t.Errorf("default range = %v, %v; want [{0 5}]", ranges, err)
	}

	if _, err := xrefIndexRanges(PDFDict{Entries: map[string]PDFValue{
		"Index": PDFArray{PDFInteger(0)},
	}}); err == nil {
		t.Error("expected error for an odd-length /Index")
	}

	if _, err := xrefIndexRanges(PDFDict{Entries: map[string]PDFValue{
		"Index": PDFArray{PDFName{Value: "X"}, PDFInteger(1)},
	}}); err == nil {
		t.Error("expected error for a non-integer /Index entry")
	}

	ranges, err = xrefIndexRanges(PDFDict{Entries: map[string]PDFValue{
		"Index": PDFArray{PDFInteger(0), PDFInteger(1), PDFInteger(10), PDFInteger(2)},
	}})
	want := []xrefRange{{start: 0, count: 1}, {start: 10, count: 2}}
	if err != nil || len(ranges) != 2 || ranges[0] != want[0] || ranges[1] != want[1] {
		t.Errorf("explicit ranges = %v, %v; want %v", ranges, err, want)
	}
}

// TestBeUint covers big-endian decoding across widths 0-3.
func TestBeUint(t *testing.T) {
	tests := []struct {
		b    []byte
		want uint64
	}{
		{nil, 0},
		{[]byte{0x01}, 1},
		{[]byte{0x01, 0x00}, 256},
		{[]byte{0x00, 0x01, 0x00}, 256},
	}
	for _, tc := range tests {
		if got := beUint(tc.b); got != tc.want {
			t.Errorf("beUint(%v) = %d, want %d", tc.b, got, tc.want)
		}
	}
}
