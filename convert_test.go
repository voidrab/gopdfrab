// Convert facade tests: the corpus-wide merged-verify oracle, file/bytes/batch
// parity, the raster last resort, the object-model surface, and the
// veraPDF-binary regression cross-check.

package gopdfrab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// TestConvertMergedFinalVerifyOracle converts every corpus document and
// asserts the merged final verification (byte-level structural checks on the
// output plus the reused in-loop graph verdicts, see convert.go's
// serializeAndVerify) reports exactly what an independent from-scratch
// verify of the same output bytes reports: same verdict, same check
// multiset. This is the conformance gate for reusing graph verdicts instead
// of replaying the whole graph verification against the output.
func TestConvertMergedFinalVerifyOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("corpus-wide oracle skipped in short mode")
	}

	var files []string
	for _, dir := range []string{isartorDir, veraDir} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	if len(files) == 0 {
		t.Skip("no corpora present")
	}

	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			cr, err := ConvertBytes(data, PDFA1B)
			if err != nil {
				t.Skipf("no convertible output (err=%v)", err)
			}
			defer cr.Close()
			out, err := cr.Output()
			if err != nil || len(out) == 0 {
				t.Skipf("no convertible output (err=%v)", err)
			}

			fresh, err := VerifyBytes(out, PDFA1B)
			if err != nil {
				t.Fatalf("fresh VerifyBytes of output: %v", err)
			}

			if cr.Result.Valid != fresh.Valid {
				t.Errorf("merged Valid=%v, fresh Valid=%v (merged %v, fresh %v)",
					cr.Result.Valid, fresh.Valid,
					checkMultiset(cr.Result.Issues), checkMultiset(fresh.Issues))
			}
			merged, freshSet := checkMultiset(cr.Result.Issues), checkMultiset(fresh.Issues)
			if fmt.Sprint(merged) != fmt.Sprint(freshSet) {
				t.Errorf("merged issue multiset %v != fresh %v", merged, freshSet)
			}
		})
	}
}

// checkMultiset returns the sorted "clause/subclause name xN" identity list
// of a result's issues.
func checkMultiset(issues []PDFError) []string {
	counts := map[string]int{}
	for _, iss := range issues {
		c := iss.Check()
		counts[fmt.Sprintf("%s/%d %s", c.Clause(), c.Subclause(), c.Name())]++
	}
	out := make([]string, 0, len(counts))
	for k, n := range counts {
		out = append(out, fmt.Sprintf("%s x%d", k, n))
	}
	sort.Strings(out)
	return out
}

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
		fromFile, ferr := Convert(path, pdf.PDFA1B)
		fromBytes, berr := ConvertBytes(data, pdf.PDFA1B)
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

	results, err := ConvertAll(paths, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	if len(results) != len(paths) {
		t.Fatalf("ConvertAll returned %d results, want %d", len(results), len(paths))
	}

	for i, path := range paths {
		r := results[i]
		if r.Path != path {
			t.Errorf("results[%d].Path = %q, want %q", i, r.Path, path)
		}

		want, wantErr := Convert(path, pdf.PDFA1B)
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
		fromDoc, err := doc.Convert(pdf.PDFA1B)
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

// TestConvertEachMatchesConvertAll checks the streaming ConvertEach delivers the
// same per-file outcome as ConvertAll, honouring Options.Workers.
func TestConvertEachMatchesConvertAll(t *testing.T) {
	fixtures := failFixturesByExpectedClause(t)
	if len(fixtures) == 0 {
		t.Skip("no corpora present")
	}
	var paths []string
	for path := range fixtures {
		paths = append(paths, path)
		if len(paths) >= 5 {
			break
		}
	}
	if len(paths) == 0 {
		t.Skip("no readable fixtures")
	}

	all, err := ConvertAll(paths, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	want := map[string]bool{}
	for _, r := range all {
		want[r.Path] = r.Result.Result.Valid
	}

	// The engine serializes fn, so plain map access is race-free.
	got := map[string]bool{}
	err = ConvertEachContext(context.Background(), paths, pdf.PDFA1B, Options{Workers: 2},
		func(fr FileResult[ConvertResult]) error {
			got[fr.Path] = fr.Result.Result.Valid
			return nil
		})
	if err != nil {
		t.Fatalf("ConvertEachContext: %v", err)
	}
	if len(got) != len(paths) {
		t.Fatalf("ConvertEach delivered %d results, want %d", len(got), len(paths))
	}
	for path, valid := range want {
		if got[path] != valid {
			t.Errorf("%s: ConvertEach valid=%v, want %v", path, got[path], valid)
		}
	}
}

// TestConvertEachNonContext covers the non-context ConvertEach wrapper: every
// path is delivered even when the files do not exist.
func TestConvertEachNonContext(t *testing.T) {
	paths := []string{
		filepath.Join(t.TempDir(), "a.pdf"),
		filepath.Join(t.TempDir(), "b.pdf"),
	}
	n := 0
	err := ConvertEach(paths, pdf.PDFA1B, Options{}, func(fr FileResult[ConvertResult]) error {
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("ConvertEach: %v", err)
	}
	if n != len(paths) {
		t.Errorf("ConvertEach delivered %d results, want %d", n, len(paths))
	}
}

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

	cr, err := Convert(qqNestingFixture, pdf.PDFA1B)
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
		cr, err := Convert(path, pdf.PDFA1B)
		if err != nil {
			t.Errorf("Convert(%s): %v", path, err)
			continue
		}
		if !cr.Result.Valid {
			t.Errorf("conformant input made non-conformant: %v", issueClauses(cr.Residual()))
		}
	}
}

// objModelFixture serializes a minimal one-page PDF whose only object-model
// defect is a direct FontDescriptor, which ISO 32000 requires to be indirect.
func objModelFixture(t *testing.T) []byte {
	t.Helper()

	desc := pdf.NewPDFDict() // deliberately no _ref: inlined in the font dict
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["FontName"] = pdf.PDFName{Value: "Helvetica"}
	desc.Entries["Flags"] = pdf.PDFInteger(32)
	desc.Entries["FontBBox"] = pdf.PDFArray{pdf.PDFInteger(-166), pdf.PDFInteger(-225), pdf.PDFInteger(1000), pdf.PDFInteger(931)}
	desc.Entries["ItalicAngle"] = pdf.PDFInteger(0)
	desc.Entries["Ascent"] = pdf.PDFInteger(718)
	desc.Entries["Descent"] = pdf.PDFInteger(-207)
	desc.Entries["CapHeight"] = pdf.PDFInteger(718)
	desc.Entries["StemV"] = pdf.PDFInteger(88)

	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "Helvetica"}
	font.Entries["FontDescriptor"] = desc
	font.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}

	fontMap := pdf.NewPDFDict()
	fontMap.Entries["F1"] = font
	resources := pdf.NewPDFDict()
	resources.Entries["Font"] = fontMap

	contents := pdf.NewPDFDict()
	contents.HasStream = true
	contents.RawStream = []byte("BT /F1 12 Tf 72 720 Td (x) Tj ET")
	contents.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}

	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	pages.Entries["Count"] = pdf.PDFInteger(1)
	pages.Entries["_ref"] = pdf.PDFRef{ObjNum: 2}

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Parent"] = pages
	page.Entries["MediaBox"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(612), pdf.PDFInteger(792)}
	page.Entries["Resources"] = resources
	page.Entries["Contents"] = contents
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	pages.Entries["Kids"] = pdf.PDFArray{page}

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["Pages"] = pages
	catalog.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog

	var buf bytes.Buffer
	if err := writer.WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	return buf.Bytes()
}

// TestConvertObjectModelAPI exercises the full ConvertObjectModel surface:
// bytes, path, and Document forms all repair an object-model-invalid input
// into a fully conformant rewrite.
func TestConvertObjectModelAPI(t *testing.T) {
	data := objModelFixture(t)

	res, err := VerifyObjectModelBytes(data)
	if err != nil {
		t.Fatalf("VerifyObjectModelBytes: %v", err)
	}
	if res.Valid {
		t.Fatal("fixture must be object-model invalid (direct FontDescriptor)")
	}

	cr, err := ConvertObjectModelBytes(data)
	if err != nil {
		t.Fatalf("ConvertObjectModelBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("ConvertObjectModelBytes: Valid=%v, residual %v", cr.Result.Valid, cr.Residual())
	}

	out, err := VerifyObjectModelBytes(mustOutput(t, cr))
	if err != nil {
		t.Fatalf("VerifyObjectModelBytes(output): %v", err)
	}
	if !out.Valid {
		t.Errorf("output independently re-verifies as invalid: %v", out.Issues)
	}

	path := filepath.Join(t.TempDir(), "objmodel.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cr, err = ConvertObjectModel(path)
	if err != nil {
		t.Fatalf("ConvertObjectModel: %v", err)
	}
	if !cr.Result.Valid {
		t.Errorf("ConvertObjectModel: residual %v", cr.Residual())
	}

	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()
	cr, err = doc.ConvertObjectModel()
	if err != nil {
		t.Fatalf("Document.ConvertObjectModel: %v", err)
	}
	if !cr.Result.Valid {
		t.Errorf("Document.ConvertObjectModel: residual %v", cr.Residual())
	}
}

const veraPDFBin = "benchmarks/tools/verapdf/verapdf"

type veraReport struct {
	XMLName xml.Name `xml:"report"`
	Jobs    struct {
		Job []veraJob `xml:"job"`
	} `xml:"jobs"`
}

type veraJob struct {
	Item struct {
		Name string `xml:"name"`
	} `xml:"item"`
	// Report is nil when veraPDF produced no verdict for the job (e.g. a file
	// it could not parse).
	Report *veraValidationReport `xml:"validationReport"`
}

type veraValidationReport struct {
	IsCompliant bool `xml:"isCompliant,attr"`
	Details     struct {
		Rules []struct {
			Clause string `xml:"clause,attr"`
			Status string `xml:"status,attr"`
		} `xml:"rule"`
	} `xml:"details"`
}

// runVeraPDF invokes the bundled veraPDF binary and returns the parsed MRR
// report. veraPDF exits non-zero for non-compliant files, so only an empty
// output is treated as a failed run.
func runVeraPDF(args ...string) (veraReport, error) {
	cmd := exec.Command(veraPDFBin, append([]string{"--format", "mrr", "--flavour", "1b"}, args...)...)
	out, err := cmd.Output()
	if len(out) == 0 {
		if err != nil {
			return veraReport{}, err
		}
		return veraReport{}, exec.ErrNotFound
	}
	var rep veraReport
	if err := xml.Unmarshal(out, &rep); err != nil {
		return veraReport{}, err
	}
	return rep, nil
}

// TestConvertNoResidualIssues cross-checks gopdfrab's verifier against the
// bundled veraPDF reference for every regression document, then converts the
// files whose failure causes agree and asserts the result is conformant.
func TestConvertNoResidualIssues(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testing in short mode")
	}

	if _, err := os.Stat(veraPDFBin); err != nil {
		t.Skipf("veraPDF reference verifier not available: %v", err)
	}

	basePath := "tests/regression"
	var paths []string
	err := filepath.WalkDir(basePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to gather test documents: %v", err)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			res, err := Verify(path, PDFA1B)
			if err != nil {
				t.Fatalf("Verify failed: %v", err)
			}
			gopClauses := map[string]bool{}
			for _, iss := range res.Issues {
				gopClauses[iss.Check().Clause()] = true
			}

			veraClauses, veraCompliant, err := veraPDFClauses(path)
			if err != nil {
				t.Fatalf("veraPDF reference run failed: %v", err)
			}

			if res.Valid != veraCompliant {
				t.Fatalf("conformance verdict disagrees with veraPDF: gopdfrab valid=%v, veraPDF compliant=%v (gopdfrab clauses %v, veraPDF clauses %v)",
					res.Valid, veraCompliant, sortedClauses(gopClauses), sortedClauses(veraClauses))
			}

			if res.Valid {
				// Both verifiers agree the input is already conformant; nothing
				// to convert.
				return
			}

			match, onlyGop, onlyVera := clauseSetMatches(gopClauses, veraClauses)
			if !match {
				t.Logf("failure causes disagree with veraPDF: only gopdfrab=%v, only veraPDF=%v",
					onlyGop, onlyVera)
			}

			// Causes agree: convert and require a clean PDF/A-1b result.
			cr, err := Convert(path, PDFA1B)
			if err != nil {
				t.Fatalf("Convert failed: %v", err)
			}

			tmpDir := t.TempDir()
			outPath := filepath.Join(tmpDir, "converted.pdf")

			if err := cr.Save(outPath); err != nil {
				t.Fatalf("failed to write converted PDF: %v", err)
			}

			if !cr.Result.Valid {
				t.Errorf("converted PDF is not valid, %d residual issues", len(cr.Residual()))
			}
			for _, iss := range cr.Residual() {
				t.Errorf("residual %s issue after conversion: %v", cr.Result.Summary(), iss)
			}

			veraClauses, veraCompliant, err = veraPDFClauses(outPath)
			if err != nil {
				t.Fatalf("veraPDF verification of converted PDF failed: %v", err)
			}

			if !veraCompliant {
				t.Fatalf("veraPDF reports converted PDF is still not PDF/A-1b compliant: %v",
					sortedClauses(veraClauses))
			}
		})
	}
}

// veraPDFClauses runs the reference veraPDF verifier and returns the set of
// clauses it reports as failed for path, and whether it deems the file compliant.
func veraPDFClauses(path string) (clauses map[string]bool, compliant bool, err error) {
	rep, err := runVeraPDF(path)
	if err != nil {
		return nil, false, err
	}

	clauses = map[string]bool{}
	for _, job := range rep.Jobs.Job {
		if job.Report == nil {
			continue
		}
		compliant = job.Report.IsCompliant
		for _, r := range job.Report.Details.Rules {
			if r.Status == "failed" {
				clauses[r.Clause] = true
			}
		}
	}
	return clauses, compliant, nil
}

// clauseSetMatches reports whether two clause sets describe the same causes.
// It also returns the clauses present in only one side for diagnostics.
func clauseSetMatches(got, want map[string]bool) (match bool, onlyGot, onlyWant []string) {
	for c := range got {
		if !clauseSetHas(want, c) {
			onlyGot = append(onlyGot, c)
		}
	}
	for c := range want {
		if !clauseSetHas(got, c) {
			onlyWant = append(onlyWant, c)
		}
	}
	sort.Strings(onlyGot)
	sort.Strings(onlyWant)
	return len(onlyGot) == 0 && len(onlyWant) == 0, onlyGot, onlyWant
}

// clauseSetHas reports whether set contains a clause matching c under
// clauseMatches.
func clauseSetHas(set map[string]bool, c string) bool {
	for s := range set {
		if clauseMatches(s, c) {
			return true
		}
	}
	return false
}

func sortedClauses(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// realWorldPDFs returns the .pdf files under dir, or nil if the directory is
// absent or empty. The real-world corpus is gitignored and populated out of band
// (see tests/realworld/README.md), so the corpus tests skip in a clean checkout.
func realWorldPDFs(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // absent directory: no files
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".pdf") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

// manifestEntry is one row of tests/realworld/manifest.json: the committed
// inventory of the corpus. The .pdf bytes are gitignored; this records each
// file's hash, licence, provenance, and an optional source URL.
type manifestEntry struct {
	Path     string `json:"path"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	License  string `json:"license"`
	Producer string `json:"producer"`
	Note     string `json:"note"`
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// manifestProblems checks each present corpus file against manifest.json under
// realworldDir, returning a problem per file that is unlisted, hash-mismatched,
// or missing a licence. err is non-nil only when the manifest cannot be read or
// parsed. Manifest entries with no present file are ignored, so a clean checkout
// (committed manifest, gitignored PDFs) reports nothing.
func manifestProblems(realworldDir string, present []string) (problems []string, err error) {
	data, err := os.ReadFile(filepath.Join(realworldDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var entries []manifestEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	byPath := make(map[string]manifestEntry, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}
	for _, f := range present {
		rel, err := filepath.Rel(realworldDir, f)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		e, ok := byPath[rel]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: present but not in manifest.json (run scripts/gen-realworld-manifest.sh)", rel))
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if got := sha256Hex(b); got != e.SHA256 {
			problems = append(problems, fmt.Sprintf("%s: sha256 %s != manifest %s (regenerate the manifest)", rel, got, e.SHA256))
		}
		if e.License == "" || e.License == "TODO" {
			problems = append(problems, fmt.Sprintf("%s: no licence recorded in manifest.json", rel))
		}
	}
	return problems, nil
}

// checkShouldPass verifies each file against PDF/A-1b and returns the paths
// gopdfrab wrongly rejected. These are real files a tool and veraPDF both call
// PDF/A-1b, so any rejection is a false positive.
func checkShouldPass(t *testing.T, files []string) (failures []string) {
	for _, p := range files {
		res, err := Verify(p, PDFA1B)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: open/parse error: %v", p, err))
			continue
		}
		if !res.Valid {
			failures = append(failures, fmt.Sprintf("%s: %d issues", p, len(res.Issues)))
		}
	}
	return failures
}

// checkShouldConvert converts each file and returns how many reached PDF/A-1b
// conformance and how many did so only with dropped raster content (a lossy
// fallback).
func checkShouldConvert(t *testing.T, files []string) (conformant, lossyRaster int) {
	for _, p := range files {
		cr, err := Convert(p, PDFA1B)
		if err != nil {
			t.Errorf("%s: convert error: %v", p, err)
			continue
		}
		if cr.Result.Valid {
			conformant++
		}
		if len(cr.RasterDrops) > 0 {
			lossyRaster++
		}
	}
	return conformant, lossyRaster
}

// TestRealWorldCorpus runs the two real-world metrics over tests/realworld/:
// every should-pass file must verify clean (a rejection is a false positive),
// the should-convert conformance fraction is reported, and every present file
// must be recorded in manifest.json with a matching hash and a licence. It skips
// when the corpus is absent, as in a clean checkout.
func TestRealWorldCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	base := filepath.Join("tests", "realworld")
	passFiles := realWorldPDFs(t, filepath.Join(base, "should-pass"))
	convFiles := realWorldPDFs(t, filepath.Join(base, "should-convert"))
	if len(passFiles)+len(convFiles) == 0 {
		t.Skip("no real-world corpus present (see tests/realworld/README.md)")
	}

	present := append(append([]string{}, passFiles...), convFiles...)
	if problems, err := manifestProblems(base, present); err != nil {
		t.Errorf("manifest.json: %v (run scripts/gen-realworld-manifest.sh)", err)
	} else {
		for _, p := range problems {
			t.Errorf("manifest: %s", p)
		}
	}

	if len(passFiles) > 0 {
		failures := checkShouldPass(t, passFiles)
		t.Logf("should-pass: %d files, %d rejected", len(passFiles), len(failures))
		for _, f := range failures {
			t.Errorf("should-pass false positive: %s", f)
		}
	}
	if len(convFiles) > 0 {
		conformant, lossy := checkShouldConvert(t, convFiles)
		t.Logf("should-convert: %d files, %d conformant, %d via lossy raster", len(convFiles), conformant, lossy)
	}
}

// TestRealWorldHarnessSelfCheck exercises the corpus harness against generated
// fixtures so it is covered even when no real corpus is present: a converted
// plainPDF is genuine PDF/A-1b (should-pass accepts it), and a plain PDF converts
// to conformance (should-convert counts it).
func TestRealWorldHarnessSelfCheck(t *testing.T) {
	cr, err := ConvertBytes([]byte(plainPDF), PDFA1B)
	if err != nil || !cr.Result.Valid {
		t.Fatalf("setup: ConvertBytes not conformant: err=%v valid=%v", err, cr.Result.Valid)
	}

	passDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(passDir, "conformant.pdf"), mustOutput(t, cr), 0o644); err != nil {
		t.Fatal(err)
	}
	pass := realWorldPDFs(t, passDir)
	if failures := checkShouldPass(t, pass); len(pass) != 1 || len(failures) != 0 {
		t.Errorf("should-pass self-check: files=%d failures=%v, want 1/none", len(pass), failures)
	}

	convDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(convDir, "plain.pdf"), []byte(plainPDF), 0o644); err != nil {
		t.Fatal(err)
	}
	conv := realWorldPDFs(t, convDir)
	if conformant, _ := checkShouldConvert(t, conv); len(conv) != 1 || conformant != 1 {
		t.Errorf("should-convert self-check: files=%d conformant=%d, want 1/1", len(conv), conformant)
	}

	// A missing directory yields no files (the skip path).
	if got := realWorldPDFs(t, filepath.Join(t.TempDir(), "absent")); len(got) != 0 {
		t.Errorf("absent dir returned %d files, want 0", len(got))
	}
}

// TestRealWorldManifestCheck covers manifestProblems: a correct entry is clean,
// while an unlisted file, a hash mismatch, a TODO/empty licence, and an
// unreadable manifest are each reported.
func TestRealWorldManifestCheck(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "should-pass"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdfPath := filepath.Join(base, "should-pass", "x.pdf")
	content := []byte("%PDF-1.4 stub\n")
	if err := os.WriteFile(pdfPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex(content)

	writeManifest := func(entries []manifestEntry) {
		data, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "manifest.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeManifest([]manifestEntry{{Path: "should-pass/x.pdf", SHA256: sum, License: "CC-BY-4.0"}})
	if problems, err := manifestProblems(base, []string{pdfPath}); err != nil || len(problems) != 0 {
		t.Errorf("correct manifest: problems=%v err=%v, want none", problems, err)
	}

	writeManifest([]manifestEntry{{Path: "should-pass/x.pdf", SHA256: sum, License: "TODO"}})
	if problems, _ := manifestProblems(base, []string{pdfPath}); len(problems) != 1 {
		t.Errorf("TODO licence: problems=%v, want 1", problems)
	}

	writeManifest([]manifestEntry{{Path: "should-pass/x.pdf", SHA256: "deadbeef", License: "CC-BY-4.0"}})
	if problems, _ := manifestProblems(base, []string{pdfPath}); len(problems) != 1 {
		t.Errorf("hash mismatch: problems=%v, want 1", problems)
	}

	writeManifest(nil)
	if problems, _ := manifestProblems(base, []string{pdfPath}); len(problems) != 1 {
		t.Errorf("unlisted file: problems=%v, want 1", problems)
	}

	if err := os.WriteFile(filepath.Join(base, "manifest.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manifestProblems(base, []string{pdfPath}); err == nil {
		t.Error("invalid manifest.json: want error, got nil")
	}

	if err := os.Remove(filepath.Join(base, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := manifestProblems(base, []string{pdfPath}); err == nil {
		t.Error("missing manifest.json: want error, got nil")
	}
}
