package pdf

import (
	"bytes"
	"errors"
	"testing"
)

// TestConsumeStreamEOL covers the LF, CRLF, bare-CR, stray-bytes-before-EOL,
// and EOF-with-no-EOL paths.
func TestConsumeStreamEOL(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool // "bad" return value
	}{
		{"LF only", "\nrest", false},
		{"CRLF", "\r\nrest", false},
		{"bare CR", "\rrest", false},
		{"stray bytes before LF", "  \nrest", true},
		{"EOF with no EOL", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := &Reader{}
			l := NewLexerBytes([]byte(tc.data), 0)
			defer l.Release()
			if got := d.consumeStreamEOL(l); got != tc.want {
				t.Errorf("consumeStreamEOL(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

// TestWindowAt covers the negative-offset guard, the in-memory (d.data)
// path (including past-EOF), and the file-backed fallback path.
func TestWindowAt(t *testing.T) {
	data := []byte("0123456789")

	dMem := &Reader{data: data}
	if got := dMem.windowAt(-1, 4); got != nil {
		t.Errorf("windowAt(-1) = %v, want nil", got)
	}
	if got := dMem.windowAt(100, 4); got != nil {
		t.Errorf("windowAt(past EOF) = %v, want nil", got)
	}
	if got := dMem.windowAt(3, 4); string(got) != "3456" {
		t.Errorf("windowAt(3,4) = %q, want %q", got, "3456")
	}
	if got := dMem.windowAt(8, 10); string(got) != "89" { // clamps to len(data)
		t.Errorf("windowAt(8,10) = %q, want %q", got, "89")
	}

	dFile := newTestFileReader(data)
	if got := dFile.windowAt(3, 4); string(got) != "3456" {
		t.Errorf("file-backed windowAt(3,4) = %q, want %q", got, "3456")
	}
}

// TestCheckEndstreamFramingRecordsMissingEOL covers both the well-formed
// (EOL present) no-op and the malformed (no EOL before endstream) diagnostic,
// across both the in-memory and file-backed data sources.
func TestCheckEndstreamFramingRecordsMissingEOL(t *testing.T) {
	t.Run("in-memory, missing EOL", func(t *testing.T) {
		data := []byte("BODYendstream")
		d := &Reader{data: data}
		d.checkEndstreamFraming(1, 0, 4) // "BODY" is 4 bytes, no EOL before "endstream"
		if len(d.parseDiagnostics) != 1 {
			t.Errorf("expected one diagnostic, got %d", len(d.parseDiagnostics))
		}
	})

	t.Run("in-memory, EOL present", func(t *testing.T) {
		data := []byte("BODY\nendstream")
		d := &Reader{data: data}
		d.checkEndstreamFraming(1, 0, 4)
		if len(d.parseDiagnostics) != 0 {
			t.Errorf("expected no diagnostic, got %d", len(d.parseDiagnostics))
		}
	})

	t.Run("file-backed, missing EOL", func(t *testing.T) {
		data := []byte("BODYendstream")
		d := newTestFileReader(data)
		d.checkEndstreamFraming(1, 0, 4)
		if len(d.parseDiagnostics) != 1 {
			t.Errorf("expected one diagnostic, got %d", len(d.parseDiagnostics))
		}
	})
}

// TestValidateStream covers validateStream's Length-lookup errors, the
// in-memory and file-backed body-capture paths (including the
// Length-includes-EOL diagnostic), the missing-endstream error, the
// stray-bytes-before-endstream diagnostic, and the file-backed seek/reset
// on success.
func TestValidateStream(t *testing.T) {
	t.Run("missing Length", func(t *testing.T) {
		data := []byte("\nHello\nendstream")
		d := &Reader{data: data}
		l := NewLexerBytes(data, 0)
		dict := PDFDict{Entries: map[string]PDFValue{}}
		if err := d.validateStream(l, &dict, 1); err == nil {
			t.Error("expected error for a missing Length entry")
		}
	})

	t.Run("Length not an integer", func(t *testing.T) {
		data := []byte("\nHello\nendstream")
		d := &Reader{data: data}
		l := NewLexerBytes(data, 0)
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFName{Value: "X"}}}
		if err := d.validateStream(l, &dict, 1); err == nil {
			t.Error("expected error for a non-integer Length")
		}
	})

	t.Run("Length resolve error", func(t *testing.T) {
		prefix := "99 0 obj\n[1 2\nendobj\n"
		streamRegion := "\nHello\nendstream"
		full := []byte(prefix + streamRegion)
		d := &Reader{data: full, xrefTable: map[int]int64{99: 0}}
		l := NewLexerBytes(full, int64(len(prefix)))
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFRef{ObjNum: 99}}}
		if err := d.validateStream(l, &dict, 1); err == nil {
			t.Error("expected error resolving a malformed Length reference")
		}
	})

	t.Run("in-memory body extends past EOF", func(t *testing.T) {
		data := []byte("\nHello\nendstream")
		d := &Reader{data: data}
		l := NewLexerBytes(data, 0)
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(1000)}}
		if err := d.validateStream(l, &dict, 1); err == nil {
			t.Error("expected error when Length extends past EOF")
		}
	})

	t.Run("in-memory Length includes EOL", func(t *testing.T) {
		data := []byte("\ndataendstream")
		d := &Reader{data: data}
		l := NewLexerBytes(data, 0)
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(4)}}
		if err := d.validateStream(l, &dict, 1); err != nil {
			t.Fatalf("validateStream: %v", err)
		}
		if string(dict.RawStream) != "data" {
			t.Errorf("RawStream = %q, want %q", dict.RawStream, "data")
		}
	})

	t.Run("file-backed ReadAt error", func(t *testing.T) {
		data := []byte("\nHi")
		d := newTestFileReader(data)
		l := NewLexerAt(d.file, 0)
		defer l.Release()
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(1000)}}
		if err := d.validateStream(l, &dict, 1); err == nil {
			t.Error("expected error reading a stream body past EOF")
		}
	})

	t.Run("file-backed Length includes EOL", func(t *testing.T) {
		data := []byte("\ndataendstream")
		d := newTestFileReader(data)
		l := NewLexerAt(d.file, 0)
		defer l.Release()
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(4)}}
		if err := d.validateStream(l, &dict, 1); err != nil {
			t.Fatalf("validateStream: %v", err)
		}
		if string(dict.RawStream) != "data" {
			t.Errorf("RawStream = %q, want %q", dict.RawStream, "data")
		}
	})

	t.Run("endstream not found", func(t *testing.T) {
		data := []byte("\ndataNOTHINGHERE")
		d := &Reader{data: data}
		l := NewLexerBytes(data, 0)
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(4)}}
		if err := d.validateStream(l, &dict, 1); err == nil {
			t.Error("expected error when endstream cannot be found")
		}
	})

	t.Run("stray bytes before endstream", func(t *testing.T) {
		data := []byte("\ndataXXendstream")
		d := &Reader{data: data}
		l := NewLexerBytes(data, 0)
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(4)}}
		if err := d.validateStream(l, &dict, 1); err != nil {
			t.Fatalf("validateStream: %v", err)
		}
		if len(d.parseDiagnostics) == 0 {
			t.Error("expected a stray-bytes diagnostic")
		}
	})

	t.Run("stray bytes before the stream keyword's EOL", func(t *testing.T) {
		data := []byte("  \ndataendstream") // stray spaces before the EOL
		d := &Reader{data: data}
		l := NewLexerBytes(data, 0)
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(4)}}
		if err := d.validateStream(l, &dict, 1); err != nil {
			t.Fatalf("validateStream: %v", err)
		}
		if len(d.parseDiagnostics) == 0 {
			t.Error("expected a diagnostic for stray bytes before the 'stream' EOL")
		}
	})

	t.Run("file-backed final seek error", func(t *testing.T) {
		data := []byte("\ndataendstream")
		d := &Reader{file: seekFailFileSource{bytesFileSource{bytes.NewReader(data)}}}
		l := NewLexerAt(d.file, 0)
		defer l.Release()
		dict := PDFDict{Entries: map[string]PDFValue{"Length": PDFInteger(4)}}
		if err := d.validateStream(l, &dict, 1); err == nil {
			t.Error("expected the final seek-back error to propagate")
		}
	})
}

// seekFailFileSource wraps bytesFileSource but always fails Seek, to exercise
// validateStream's file-backed post-endstream seek-back error branch.
type seekFailFileSource struct{ bytesFileSource }

func (seekFailFileSource) Seek(offset int64, whence int) (int64, error) {
	return 0, errors.New("seek always fails")
}

// TestCheckEndstreamFramingStartPastWindow covers the "start > len(window)"
// early return: a declared length far larger than the actual available data.
func TestCheckEndstreamFramingStartPastWindow(t *testing.T) {
	d := &Reader{data: []byte("ab")}
	d.checkEndstreamFraming(1, 0, 1000) // length=1000 but only 2 bytes exist
	if len(d.parseDiagnostics) != 0 {
		t.Errorf("expected no diagnostic when start is past the available window, got %d", len(d.parseDiagnostics))
	}
}
