package pdf

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadLineBytes(t *testing.T) {
	if _, _, ok := readLineBytes([]byte("x"), 5); ok {
		t.Error("readLineBytes past the end reported ok")
	}
	if line, next, ok := readLineBytes([]byte("abc"), 0); !ok || string(line) != "abc" || next != 3 {
		t.Errorf("readLineBytes(no terminator) = %q, %d, %v", line, next, ok)
	}
	if line, next, ok := readLineBytes([]byte("ab\ncd"), 0); !ok || string(line) != "ab" || next != 3 {
		t.Errorf("readLineBytes(LF) = %q, %d, %v", line, next, ok)
	}
	if line, next, ok := readLineBytes([]byte("ab\r\ncd"), 0); !ok || string(line) != "ab" || next != 4 {
		t.Errorf("readLineBytes(CRLF) = %q, %d, %v", line, next, ok)
	}
}

func TestXrefEntryOffset(t *testing.T) {
	cases := []struct {
		field string
		trim  bool
		want  int64
	}{
		{"0000000017", false, 17},
		{" 000000017", false, 0}, // strict mode: all ten bytes must be digits
		{"   17     ", true, 17}, // trimmed mode tolerates padding
		{"          ", true, 0},  // empty after trim
		{"00000x0017", true, 0},  // interior non-digit
		{"9999999999", false, 9999999999},
	}
	for _, c := range cases {
		if got := xrefEntryOffset([]byte(c.field), c.trim); got != c.want {
			t.Errorf("xrefEntryOffset(%q, trim=%v) = %d, want %d", c.field, c.trim, got, c.want)
		}
	}
}

func TestParseXRefTableBytesMalformed(t *testing.T) {
	newDoc := func(data string) *Reader { return &Reader{data: []byte(data)} }

	if err := newDoc("xref").parseXRefTable(99); !errors.Is(err, io.EOF) {
		t.Errorf("offset past end: err = %v, want io.EOF", err)
	}
	if err := newDoc("xref").parseXRefTable(-1); !errors.Is(err, io.EOF) {
		t.Errorf("negative offset: err = %v, want io.EOF", err)
	}
	if err := newDoc("nope\n").parseXRefTable(0); err == nil || !strings.Contains(err.Error(), "expected 'xref'") {
		t.Errorf("wrong keyword: err = %v", err)
	}
	if err := newDoc("xref\n").parseXRefTable(0); !errors.Is(err, io.EOF) {
		t.Errorf("EOF after keyword: err = %v, want io.EOF", err)
	}
	if err := newDoc("xref\n0 2\n0000000000 65535 f \n").parseXRefTable(0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated entry: err = %v, want io.ErrUnexpectedEOF", err)
	}
	// A malformed subsection header ends the walk without error, matching
	// the reader path.
	if err := newDoc("xref\ngarbage header here\n").parseXRefTable(0); err != nil {
		t.Errorf("malformed header: err = %v, want nil", err)
	}
}

func TestParseXRefTableBytesParsesEntries(t *testing.T) {
	data := "xref\n" +
		"0 2\n" +
		"0000000000 65535 f \n" +
		"0000000017 00000 n \n" +
		"trailer\n"
	d := &Reader{data: []byte(data)}
	if err := d.parseXRefTable(0); err != nil {
		t.Fatalf("parseXRefTable: %v", err)
	}
	if got := d.xrefTable[1]; got != 17 {
		t.Errorf("xrefTable[1] = %d, want 17", got)
	}
	if _, exists := d.xrefTable[0]; exists {
		t.Error("free entry 0 was recorded")
	}
}

func TestParseXRefSectionAtBytes(t *testing.T) {
	newDoc := func(data string) *Reader {
		return &Reader{data: []byte(data), xrefTable: map[int]int64{}}
	}

	if _, err := newDoc("x").ParseXRefSectionAt(50, false); !errors.Is(err, io.EOF) {
		t.Errorf("offset past end: err = %v, want io.EOF", err)
	}
	if _, err := newDoc("bogus\n").ParseXRefSectionAt(0, false); err == nil || !strings.Contains(err.Error(), "expected 'xref'") {
		t.Errorf("wrong keyword: err = %v", err)
	}
	// Truncated entries end the subsection walk; the missing trailer then
	// fails the lex, matching the reader path.
	if _, err := newDoc("xref\n0 3\n0000000000 65535 f \n").ParseXRefSectionAt(0, false); err == nil || !strings.Contains(err.Error(), "expected 'trailer'") {
		t.Errorf("truncated entries: err = %v", err)
	}

	// Happy path: entries recorded (with space-padding tolerance) and the
	// trailer dict parsed straight from the backing bytes.
	data := "xref\n" +
		"0 2\n" +
		"0000000000 65535 f \n" +
		"0000000017 00000 n \n" +
		"trailer\n<< /Size 2 >>\n"
	d := newDoc(data)
	dict, err := d.ParseXRefSectionAt(0, false)
	if err != nil {
		t.Fatalf("ParseXRefSectionAt: %v", err)
	}
	if dict.Entries["Size"] != PDFInteger(2) {
		t.Errorf("trailer Size = %v, want 2", dict.Entries["Size"])
	}
	if got := d.xrefTable[1]; got != 17 {
		t.Errorf("xrefTable[1] = %d, want 17", got)
	}

	// fillIn keeps existing entries.
	d2 := newDoc(data)
	d2.xrefTable[1] = 99
	if _, err := d2.ParseXRefSectionAt(0, true); err != nil {
		t.Fatalf("ParseXRefSectionAt(fillIn): %v", err)
	}
	if got := d2.xrefTable[1]; got != 99 {
		t.Errorf("fillIn overwrote existing entry: xrefTable[1] = %d, want 99", got)
	}

	// A malformed subsection header ends the walk; when the trailer happens
	// to follow, it still parses.
	d3 := newDoc("xref\n1 2 3\ntrailer\n<< /Size 1 >>\n")
	if dict, err := d3.ParseXRefSectionAt(0, false); err != nil || dict.Entries["Size"] != PDFInteger(1) {
		t.Errorf("bad header then trailer: dict = %v, err = %v", dict.Entries, err)
	}

	// The trailer window is capped at 8192 bytes, like the reader path's
	// LimitReader; trailing junk beyond it is never touched.
	d4 := newDoc("xref\n0 0\ntrailer\n<< /Size 1 >>\n" + strings.Repeat(" ", 9000))
	if dict, err := d4.ParseXRefSectionAt(0, false); err != nil || dict.Entries["Size"] != PDFInteger(1) {
		t.Errorf("oversized window: dict = %v, err = %v", dict.Entries, err)
	}
}
