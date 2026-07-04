package pdf

import "testing"

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
