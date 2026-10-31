package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// phase is a part of Parts a check may report into.
type phase uint8

const (
	phasePre phase = 1 << iota
	phaseGraph
	phasePost
)

// checkPhases declares, for every check that is not graph-only, the parts it
// may report into. Convert's clean-graph replay reruns only the structural
// parts against its output and reuses the previous graph part, which is sound
// only while each check stays in the parts declared here.
var checkPhases = map[pdf.Check]phase{
	pdf.Checks.Structure.FileHeaderSignature:        phasePre,
	pdf.Checks.Structure.FileHeaderComment:          phasePre,
	pdf.Checks.Structure.FileHeaderCommentLength:    phasePre,
	pdf.Checks.Structure.FileHeaderCommentBytes:     phasePre,
	pdf.Checks.Structure.TrailerID:                  phasePre,
	pdf.Checks.Structure.TrailerEncrypt:             phasePre,
	pdf.Checks.Structure.TrailerEOF:                 phasePre,
	pdf.Checks.Structure.XRefKeyword:                phasePre | phasePost,
	pdf.Checks.Structure.XRefSubsectionHeader:       phasePre,
	pdf.Checks.Structure.XRefSubsectionHeaderFormat: phasePre,
	pdf.Checks.Structure.IndirectObjectsExceeded:    phasePost,
	pdf.Checks.Structure.ObjectFraming:              phasePost,
	pdf.Checks.Structure.StreamKeywordEOL:           phasePost,
	pdf.Checks.Structure.StreamLengthIncludesEOL:    phasePost,
	pdf.Checks.Structure.StreamLengthMismatch:       phasePost,
	pdf.Checks.Structure.EndstreamEOL:               phasePost,
	pdf.Checks.Structure.XRefStream:                 phasePost,
	pdf.Checks.Structure.GraphResolutionFailure:     phaseGraph | phasePost,
}

// declaredPhase returns the parts check c may report into.
func declaredPhase(c pdf.Check) phase {
	if ph, ok := checkPhases[c]; ok {
		return ph
	}
	return phaseGraph
}

// TestDiagnosticChecksArePostStructural verifies that every check a reader
// diagnostic maps to is declared PostStructural, the part the diagnostics are
// reported into.
func TestDiagnosticChecksArePostStructural(t *testing.T) {
	spec := a1b()
	for kind := pdf.DiagKind(1); kind < 255; kind++ {
		e, ok := spec.Issue(pdf.Diagnostic{Kind: kind})
		if !ok {
			continue
		}
		if declaredPhase(e.Check())&phasePost == 0 {
			t.Errorf("diagnostic kind %d maps to %s, which is not declared PostStructural", kind, e.Check().Name())
		}
	}
}

// TestCheckPhasesMatchParts verifies that, across the reference corpora under
// both PDF/A-1b profiles, every issue VerifyParts returns sits in a part its
// check is declared for.
func TestCheckPhasesMatchParts(t *testing.T) {
	files := a1bCorpusFiles()
	if len(files) == 0 {
		t.Skip("reference corpora not present")
	}
	if testing.Short() {
		var sample []string
		for i := 0; i < len(files); i += 10 {
			sample = append(sample, files[i])
		}
		files = sample
	}

	seen := map[pdf.Check]phase{}
	for _, path := range files {
		for _, prof := range []*pdf.Profile{pdf.PDFA1B, pdf.Legacy1B} {
			d, err := pdf.Open(path)
			if err != nil {
				continue
			}
			pt := VerifyParts(d, prof, a1b())
			d.Close()
			for _, part := range []struct {
				ph     phase
				issues []pdf.PDFError
			}{
				{phasePre, pt.PreStructural},
				{phaseGraph, pt.Graph},
				{phasePost, pt.PostStructural},
			} {
				for _, e := range part.issues {
					c := e.Check()
					if declaredPhase(c)&part.ph == 0 && seen[c]&part.ph == 0 {
						t.Errorf("%s: %s reported in part %b, declared %b", path, c.Name(), part.ph, declaredPhase(c))
					}
					seen[c] |= part.ph
				}
			}
		}
	}
}
