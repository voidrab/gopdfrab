package pdfrab

import (
	"os"
	"strings"
	"testing"
)

// qqNestingFixture is a corpus file whose only residual after the standard
// fixers is a q/Q-nesting StringTooLong -- a structural content defect no
// in-place fixer can clamp, the canonical target for the raster fallback.
const qqNestingFixture = "test documents/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t08-fail-a.pdf"

// TestConvertRasterFallbackClearsResidual confirms WithRasterFallback rebuilds
// the offending page as a raster and clears a residual the default pipeline
// leaves behind, while the default (opt-out) behaviour is unchanged.
func TestConvertRasterFallbackClearsResidual(t *testing.T) {
	if _, err := os.Stat(qqNestingFixture); err != nil {
		t.Skip("veraPDF suite not present")
	}

	base, err := Convert(qqNestingFixture)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if base.Result.Valid {
		t.Skip("fixture no longer residual under the default pipeline; pick another")
	}
	hasQQ := false
	for _, iss := range base.Residual() {
		if strings.Contains(iss.Error(), "q/Q nesting depth") {
			hasQQ = true
		}
	}
	if !hasQQ {
		t.Fatalf("fixture lost its q/Q-nesting residual; default residual: %v", issueClauses(base.Residual()))
	}

	raster, err := Convert(qqNestingFixture, WithRasterFallback())
	if err != nil {
		t.Fatalf("Convert(WithRasterFallback): %v", err)
	}
	if !raster.Result.Valid {
		t.Errorf("raster fallback did not produce a conformant output; residual: %v", issueClauses(raster.Residual()))
	}
}

// TestConvertRasterFallbackNoOpOnConformantInput keeps the invariant that the
// fallback never alters output that is already conformant without it.
func TestConvertRasterFallbackNoOpOnConformantInput(t *testing.T) {
	paths := passFixtures(t)
	if len(paths) == 0 {
		t.Skip("veraPDF suite not present")
	}
	for _, path := range paths[:min(5, len(paths))] {
		cr, err := Convert(path, WithRasterFallback())
		if err != nil {
			t.Errorf("Convert(%s, WithRasterFallback): %v", path, err)
			continue
		}
		if !cr.Result.Valid {
			t.Errorf("conformant input made non-conformant by raster fallback: %v", issueClauses(cr.Residual()))
		}
	}
}
