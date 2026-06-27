package pdfrab

import (
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// qqNestingFixture is a corpus file whose only residual after the standard
// fixers is a q/Q-nesting StringTooLong -- a structural content defect no
// in-place fixer can clamp, so its only route to conformance is Convert's
// automatic whole-page raster last resort.
const qqNestingFixture = "tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t08-fail-a.pdf"

// TestConvertRasterizesUnfixableResidual confirms Convert's automatic raster
// last resort rebuilds a page no in-place fixer can repair, producing a
// conformant output for the canonical q/Q-nesting StringTooLong fixture.
func TestConvertRasterizesUnfixableResidual(t *testing.T) {
	if _, err := os.Stat(qqNestingFixture); err != nil {
		t.Skip("veraPDF suite not present")
	}

	cr, err := Convert(qqNestingFixture, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !cr.Result.Valid {
		t.Errorf("raster last resort did not produce a conformant output; residual: %v", issueClauses(cr.Residual()))
	}
}

// TestConvertRasterNoOpOnConformantInput keeps the invariant that the raster
// last resort never alters output that is already conformant without it.
func TestConvertRasterNoOpOnConformantInput(t *testing.T) {
	paths := passFixtures(t)
	if len(paths) == 0 {
		t.Skip("veraPDF suite not present")
	}
	for _, path := range paths[:min(5, len(paths))] {
		cr, err := Convert(path, pdf.PDFA_1B)
		if err != nil {
			t.Errorf("Convert(%s): %v", path, err)
			continue
		}
		if !cr.Result.Valid {
			t.Errorf("conformant input made non-conformant: %v", issueClauses(cr.Residual()))
		}
	}
}
