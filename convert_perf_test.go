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
