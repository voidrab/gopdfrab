package pdfrab

import (
	"os"
	"testing"
)

// TestConvertBytesMatchesFile checks the in-memory verify path (CW-1):
// ConvertBytes must produce the same validity/residual/iterations as the
// file-backed Convert for the same input.
func TestConvertBytesMatchesFile(t *testing.T) {
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}

	tested := 0
	for path := range fixtures {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fromFile, ferr := Convert(path)
		fromBytes, berr := ConvertBytes(data)
		if (ferr == nil) != (berr == nil) {
			t.Errorf("%s: error mismatch file=%v bytes=%v", path, ferr, berr)
			continue
		}
		if ferr != nil {
			continue
		}
		if fromFile.Result.Valid != fromBytes.Result.Valid ||
			len(fromFile.Residual()) != len(fromBytes.Residual()) ||
			fromFile.Iterations != fromBytes.Iterations {
			t.Errorf("%s: ConvertBytes diverged from Convert: file{valid=%v residual=%d iters=%d} bytes{valid=%v residual=%d iters=%d}",
				path, fromFile.Result.Valid, len(fromFile.Residual()), fromFile.Iterations,
				fromBytes.Result.Valid, len(fromBytes.Residual()), fromBytes.Iterations)
		}
		if tested++; tested >= 25 {
			break // a representative sample is enough; the corpus test covers the rest
		}
	}
	if tested == 0 {
		t.Skip("no readable fixtures")
	}
}

// TestConvertAllMatchesConvert checks that ConvertAll's per-path results
// agree with calling Convert individually.
func TestConvertAllMatchesConvert(t *testing.T) {
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}

	var paths []string
	for path := range fixtures {
		paths = append(paths, path)
		if len(paths) >= 5 {
			break // a representative sample is enough
		}
	}
	if len(paths) == 0 {
		t.Skip("no readable fixtures")
	}

	results := ConvertAll(paths)
	if len(results) != len(paths) {
		t.Fatalf("ConvertAll returned %d results, want %d", len(results), len(paths))
	}

	for i, path := range paths {
		r := results[i]
		if r.Path != path {
			t.Errorf("results[%d].Path = %q, want %q", i, r.Path, path)
		}

		want, wantErr := Convert(path)
		if (r.Err == nil) != (wantErr == nil) {
			t.Errorf("%s: ConvertAll error mismatch: got %v, want %v", path, r.Err, wantErr)
			continue
		}
		if r.Err != nil {
			continue
		}
		if r.Result.Result.Valid != want.Result.Valid || r.Result.Iterations != want.Iterations {
			t.Errorf("%s: ConvertAll diverged from Convert: got{valid=%v iters=%d} want{valid=%v iters=%d}",
				path, r.Result.Result.Valid, r.Result.Iterations, want.Result.Valid, want.Iterations)
		}

		doc, err := Open(path)
		if err != nil {
			t.Errorf("Open(%s): %v", path, err)
			continue
		}
		fromDoc, err := doc.Convert()
		doc.Close()
		if err != nil {
			t.Errorf("(*Document).Convert(%s): %v", path, err)
			continue
		}
		if fromDoc.Result.Valid != want.Result.Valid || fromDoc.Iterations != want.Iterations {
			t.Errorf("%s: (*Document).Convert diverged from Convert: got{valid=%v iters=%d} want{valid=%v iters=%d}",
				path, fromDoc.Result.Valid, fromDoc.Iterations, want.Result.Valid, want.Iterations)
		}
	}
}
