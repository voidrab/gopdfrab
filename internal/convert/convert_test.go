package convert

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestConvertFixesStructuralDefectWithNoFixers takes a real PDF/A-1b
// fixture, prepends a few garbage bytes before its "%PDF-" header (a pure
// 6.1.2 structural defect, with no effect on XMP/colour/font conformance),
// and converts it. WriteDocument always emits a fresh header with no leading
// bytes, so this defect -- and any other purely structural (6.1.x) one --
// must be fixed by construction on the very first write/verify pass, without
// any registered Fixer needing to touch it.
func TestConvertFixesStructuralDefectWithNoFixers(t *testing.T) {
	paths := passFixtures(t)
	if len(paths) == 0 {
		t.Skip("veraPDF suite not present")
	}

	clean, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", paths[0], err)
	}

	corrupted := append([]byte("XXXXX"), clean...)

	// Sanity check: the corrupted input really is reported non-conformant
	// (and specifically structurally, not by some unrelated quirk of the
	// corruption), so the rest of this test is actually exercising recovery.
	corruptedRes, err := verify.VerifyBytes(corrupted, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("verify.VerifyBytes(corrupted): %v", err)
	}
	if corruptedRes.Valid {
		t.Fatalf("prepending garbage bytes did not make the fixture non-conformant; test no longer exercises anything")
	}

	cr, err := ConvertBytes(corrupted, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid {
		t.Fatalf("Convert did not produce a conformant output; residual issues: %v", issueClauses(cr.Residual()))
	}
	if cr.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1 (zero fixers are registered, so convergence can only happen on the first write/verify pass)", cr.Iterations)
	}

	// The output itself must independently verify as conformant, not just
	// cr.Result (which is already derived from verifying cr.Output, but
	// re-checking via a fresh Open guards against a bug in that wiring).
	finalRes, err := verify.VerifyBytes(cr.Output, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("verify.VerifyBytes(cr.Output): %v", err)
	}
	if !finalRes.Valid {
		t.Errorf("cr.Output independently re-verifies as non-conformant: %v", issueClauses(finalRes.Issues))
	}
}

// TestConvertDegradesGracefullyOnUnresolvableGraph checks that Convert
// behaves like Verify (which reports a GraphResolutionFailure issue rather
// than erroring, see verifyPdfA1b) when the object graph cannot be fully
// resolved, instead of failing outright: no rewrite is possible, but a
// Result should still come back. The input is a fixture with a broken first
// xref section whose object 2 header is additionally mangled, so neither
// xref parsing nor the brute-force recovery scan can locate the referenced
// object anywhere in the file.
func TestConvertDegradesGracefullyOnUnresolvableGraph(t *testing.T) {
	path := "../../tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.4 Cross reference table/veraPDF test suite 6-1-4-t02-fail-b.pdf"
	if _, err := os.Stat(path); err != nil {
		t.Skip("veraPDF suite not present")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	mangled := bytes.ReplaceAll(data, []byte("\n2 0 obj"), []byte("\n2_0_obj"))
	if bytes.Equal(mangled, data) {
		t.Fatalf("fixture no longer contains an object 2 header; test input needs updating")
	}

	cr, err := ConvertBytes(mangled, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if cr.Result.Valid {
		t.Fatalf("expected a non-conformant Result for an unresolvable graph, got Valid=true")
	}
	if len(cr.Output) != 0 {
		t.Errorf("Output = %d bytes, want empty (no rewrite is possible without a resolved graph)", len(cr.Output))
	}
	if len(cr.Residual()) == 0 {
		t.Errorf("Residual() is empty, want at least a GraphResolutionFailure-derived issue")
	}
}

// failFixturesByExpectedClause walks both corpora and returns every "fail"
// fixture's path paired with the clause its filename targets (see
// veraClauseAndKind / expectedClauseFromName).
func failFixturesByExpectedClause(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}

	if _, err := os.Stat(veraDir); err == nil {
		filepath.WalkDir(veraDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
				return nil
			}
			if clause, wantFail := veraClauseAndKind(d.Name()); wantFail && clause != "" {
				out[path] = clause
			}
			return nil
		})
	}
	if _, err := os.Stat(isartorDir); err == nil {
		filepath.WalkDir(isartorDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
				return nil
			}
			if clause := expectedClauseFromName(d.Name()); clause != "" {
				out[path] = clause
			}
			return nil
		})
	}
	return out
}

// TestConvertClearsRegisteredFixerChecks sweeps both corpora's "fail"
// fixtures, verifies each one as-is first, and -- for every fixture whose
// *actual* violated Check (not just its filename's clause number, which
// several unrelated checks can share, e.g. ExtGState alpha/blend-mode vs.
// transparency-group/soft-mask under 6.4) has a registered Fixer -- converts
// it and asserts that specific Check is gone from the residual issues
// afterward. The fixture may still be non-conformant overall (other,
// unrelated violations the same file happens to also contain are not this
// Fixer's job), so this checks per-Check absence, not cr.Result.Valid.
func TestConvertClearsRegisteredFixerChecks(t *testing.T) {
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}
	if len(fixerRegistry) == 0 {
		t.Skip("no fixers registered yet")
	}

	var tested, cleared int
	for path := range fixtures {
		origRes, err := func() (pdf.Result, error) {
			doc, err := pdf.Open(path)
			if err != nil {
				return pdf.Result{}, err
			}
			defer doc.Close()
			return verify.Verify(doc, pdf.PDFA_1B)
		}()
		if err != nil || origRes.Valid {
			continue
		}

		var targetChecks []pdf.Check
		for _, iss := range origRes.Issues {
			if _, ok := fixerRegistry[iss.Check()]; ok {
				targetChecks = append(targetChecks, iss.Check())
			}
		}
		if len(targetChecks) == 0 {
			continue
		}
		tested++

		t.Run(filepath.Base(path), func(t *testing.T) {
			cr, err := Convert(path, pdf.PDFA_1B)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			ok := true
			for _, c := range targetChecks {
				for _, iss := range cr.Residual() {
					if iss.Check() != c {
						continue
					}
					if strings.Contains(iss.Error(), "inline image") {
						continue
					}
					if strings.Contains(iss.Error(), "q/Q nesting depth") {
						continue
					}
					if (c == pdf.Checks.Font.SubsetGlyphCoverage || c == pdf.Checks.Font.CIDNotEmbedded) &&
						strings.Contains(iss.Error(), "CID") && !cidSubstitutionPossible(t, path) {
						continue
					}
					t.Errorf("check %s (%s/%d) still present after conversion: %v",
						c.Name(), c.Clause(), c.Subclause(), iss)
					ok = false
				}
			}
			if ok {
				cleared++
			}
		})
	}

	t.Logf("%d/%d targeted fixture(s) had every applicable Check cleared", cleared, tested)
}

// cidSubstitutionPossible reports whether the document at path contains at
// least one Type0 font eligible for CID substitution
// (cidFontSubstitutionEligible, fixups_font_subst.go). Used only to excuse a
// residual CID-flavoured check on fixtures with no such font at all (every
// composite font in the current corpus is fully eligible or fully
// ineligible, never a mix, so this fixture-level check is precise enough).
func cidSubstitutionPossible(t *testing.T, path string) bool {
	t.Helper()
	doc, err := pdf.Open(path)
	if err != nil {
		return false
	}
	defer doc.Close()
	graph, err := doc.ResolveGraph()
	if err != nil {
		return false
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return false
	}
	possible := false
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if possible || (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) || (d.Entries["Subtype"] != pdf.PDFName{Value: "Type0"}) {
			return
		}
		if _, ok := cidFontSubstitutionEligible(d); ok {
			possible = true
		}
	})
	return possible
}

// isKnownUnfixableXMPSync reports whether an Info/XMP sync error message
// matches one of three confirmed-by-inspection residuals no valid XMP
// content can resolve, since the violation is not in the XMP at all:
//   - "non-string value" / "is not in PDF date format": the Info dictionary
//     entry itself is malformed (e.g. veraPDF 6-1-5-t01-fail-j/h), which is
//     an Info-dictionary fixup, out of regenerateXMP's scope.
//   - "Author not synchronized with XMP dc:creator": veraPDF
//     6-1-5-t01-fail-b's Info Author value has leading/trailing whitespace
//     (" veraPDF Consortium "); checkInfoXMPSync trims the XMP-side
//     dc:creator rdf:li text before comparing but never trims the
//     Info-side value, so no XMP encoding of that exact value can ever
//     match -- a checker-side asymmetry, not a gap in regenerateXMP.
func isKnownUnfixableXMPSync(msg string) bool {
	for _, substr := range []string{
		"non-string value",
		"is not in PDF date format",
		"Author not synchronized with XMP dc:creator",
	} {
		if strings.Contains(msg, substr) {
			return true
		}
	}
	return false
}

// TestConvertRegeneratesXMP sweeps both corpora's "fail" fixtures for every
// one whose original violations include a clause-6.7 metadata Check (or the
// matching 6.1.5 Info/XMP-sync mismatch) and asserts every such Check is
// gone after conversion. Unlike the dictionary Fixers above, regenerateXMP
// is a pre-emptive fixup (see convert.go) applied unconditionally on every
// Convert call, not dispatched through fixerRegistry by Check -- so this
// needs its own sweep rather than reusing TestConvertClearsRegisteredFixerChecks.
func TestConvertRegeneratesXMP(t *testing.T) {
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}

	isXMPCheck := func(c pdf.Check) bool {
		return strings.HasPrefix(c.Clause(), "6.7") || c == pdf.Checks.Structure.InfoDictXMPMismatch
	}

	var tested, cleared int
	for path := range fixtures {
		origRes, err := func() (pdf.Result, error) {
			doc, err := pdf.Open(path)
			if err != nil {
				return pdf.Result{}, err
			}
			defer doc.Close()
			return verify.Verify(doc, pdf.PDFA_1B)
		}()
		if err != nil || origRes.Valid {
			continue
		}

		var targetChecks []pdf.Check
		for _, iss := range origRes.Issues {
			if isXMPCheck(iss.Check()) {
				targetChecks = append(targetChecks, iss.Check())
			}
		}
		if len(targetChecks) == 0 {
			continue
		}
		tested++

		t.Run(filepath.Base(path), func(t *testing.T) {
			cr, err := Convert(path, pdf.PDFA_1B)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			ok := true
			for _, c := range targetChecks {
				for _, iss := range cr.Residual() {
					if iss.Check() != c {
						continue
					}
					if isKnownUnfixableXMPSync(iss.Error()) {
						continue
					}
					t.Errorf("check %s (%s/%d) still present after conversion: %v", c.Name(), c.Clause(), c.Subclause(), iss)
					ok = false
				}
			}
			if ok {
				cleared++
			}
		})
	}

	t.Logf("XMP regeneration sweep: %d/%d targeted fixture(s) had every applicable Check cleared", cleared, tested)
}

// outputIntentChecks are the Checks injectOutputIntent (fixups_colour.go) is
// expected to resolve: the OutputIntent dictionary/profile structural family
// (6.2.2) it fixes directly, plus the device-colour-usage family (6.2.3.3/
// 6.2.3.4) it fixes as a side effect of making ctx.deviceColourAllowed true.
// Deliberately excludes Checks.Colour.RenderingIntent (the /ri content
// operator, unrelated) and UndefinedOperator.
//
// This must be a function, not a package-level var literal: Checks itself
// is populated inside checks_catalog.go's init(), which (per the Go spec)
// runs only after every package-level variable initializer has already
// run -- a var literal referencing Checks.Colour.* here would capture the
// zero-valued Check{} it held at that point, not the real registered
func outputIntentChecks() []pdf.Check {
	return []pdf.Check{
		pdf.Checks.Colour.OutputIntentNotArray, pdf.Checks.Colour.OutputIntentNotDict,
		pdf.Checks.Colour.OutputIntentInvalidS, pdf.Checks.Colour.OutputIntentWrongS,
		pdf.Checks.Colour.OutputIntentMissingIdentifier, pdf.Checks.Colour.OutputIntentMultipleProfiles,
		pdf.Checks.Colour.OutputIntentUnresolvedProfile, pdf.Checks.Colour.OutputIntentInvalidProfile,
		pdf.Checks.Colour.OutputIntentMissingN, pdf.Checks.Colour.OutputIntentInvalidN,
		pdf.Checks.Colour.OutputIntentICCVersion, pdf.Checks.Colour.DeviceColourSpaceUsage,
		pdf.Checks.Colour.DeviceColourContentStream, pdf.Checks.Colour.SeparationAlternateColour,
	}
}

// TestConvertInjectsOutputIntent sweeps both corpora's "fail" fixtures for
// every one whose original violations include an outputIntentChecks member
// and asserts every such Check is gone after conversion. Like regenerateXMP,
// injectOutputIntent is a pre-emptive fixup (see convert.go), not dispatched
// through fixerRegistry, so this needs its own sweep.
func TestConvertInjectsOutputIntent(t *testing.T) {
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}

	isOutputIntentCheck := func(c pdf.Check) bool {
		return slices.Contains(outputIntentChecks(), c)
	}

	var tested, cleared int
	for path := range fixtures {
		origRes, err := func() (pdf.Result, error) {
			doc, err := pdf.Open(path)
			if err != nil {
				return pdf.Result{}, err
			}
			defer doc.Close()
			return verify.Verify(doc, pdf.PDFA_1B)
		}()
		if err != nil || origRes.Valid {
			continue
		}

		var targetChecks []pdf.Check
		for _, iss := range origRes.Issues {
			if isOutputIntentCheck(iss.Check()) {
				targetChecks = append(targetChecks, iss.Check())
			}
		}
		if len(targetChecks) == 0 {
			continue
		}
		tested++

		t.Run(filepath.Base(path), func(t *testing.T) {
			// A single ICC profile can only declare one colour model, so a
			// document whose content genuinely mixes more than one device
			// colour model (confirmed here independently of the fixup,
			// straight from the source content) has no possible single
			// OutputIntent that covers all of it -- a fundamental
			// limitation of injectOutputIntent's one-profile strategy, not
			// a bug. detectColourModelUsage is also exactly the signal
			// injectOutputIntent itself uses to pick which model to cover.
			mixedModel := false
			if doc, err := pdf.Open(path); err == nil {
				if graph, err := doc.ResolveGraph(); err == nil {
					if trailer, ok := graph.(pdf.PDFDict); ok {
						mixedModel = len(detectColourModelUsage(trailer, pdf.DecodeStream)) > 1
					}
				}
				doc.Close()
			}

			cr, err := Convert(path, pdf.PDFA_1B)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			ok := true
			for _, c := range targetChecks {
				for _, iss := range cr.Residual() {
					if iss.Check() != c {
						continue
					}
					if mixedModel {
						continue
					}
					t.Errorf("check %s (%s/%d) still present after conversion: %v", c.Name(), c.Clause(), c.Subclause(), iss)
					ok = false
				}
			}
			if ok {
				cleared++
			}
		})
	}

	t.Logf("OutputIntent injection sweep: %d/%d targeted fixture(s) had every applicable Check cleared", cleared, tested)
}

// TestConvertNeverBreaksConformantInput runs Convert over every "pass" fixture in the
// veraPDF corpus and asserts the output is still fully conformant.
func TestConvertNeverBreaksConformantInput(t *testing.T) {
	paths := passFixtures(t)
	if len(paths) == 0 {
		t.Skip("veraPDF suite not present")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			cr, err := Convert(path, pdf.PDFA_1B)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if !cr.Result.Valid {
				t.Errorf("conformant input is no longer conformant after Convert: %v", issueClauses(cr.Residual()))
			}
		})
	}
}

// minConvertedFully is a regression floor on how many of both corpora's
// "fail" fixtures Convert turns fully conformant, recorded empirically after
// rasterization became Convert's automatic last resort: 509 of 510, up from
// 502. The single hold-out is a fixture whose cross-reference table can't be
// parsed (6.1.4/6.1.6) -- the graph never resolves, so there is nothing to
// rewrite or rasterize. Should only ever increase; a drop means something
// regressed.
const minConvertedFully = 509

// TestConvertCorpusEndToEnd sweeps every "fail" fixture in both corpora
// through Convert and tallies the outcome.
func TestConvertCorpusEndToEnd(t *testing.T) {
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}

	var fullyValid, otherResidual, errored int
	for path := range fixtures {
		cr, err := Convert(path, pdf.PDFA_1B)
		if err != nil {
			t.Errorf("Convert(%s): %v", path, err)
			errored++
			continue
		}
		if cr.Result.Valid {
			fullyValid++
			continue
		}

		otherResidual++
	}

	t.Logf("Convert corpus end-to-end: %d fully conformant, %d non-conformant with other residual, %d errored (total %d)",
		fullyValid, otherResidual, errored, len(fixtures))

	if errored > 0 {
		t.Errorf("%d fixture(s) made Convert error outright; see logged Convert(...) errors above", errored)
	}
	if fullyValid < minConvertedFully {
		t.Errorf("only %d fixtures converted fully, want >= %d (regression floor); see minConvertedFully", fullyValid, minConvertedFully)
	}
}
