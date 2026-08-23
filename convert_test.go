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
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

	desc := pdf.NewPDFDict()
	// deliberately no _ref: inlined in the font dict
	desc.Entries.Set("Type", pdf.PDFName{Value: "FontDescriptor"})
	desc.Entries.Set("FontName", pdf.PDFName{Value: "Helvetica"})
	desc.Entries.Set("Flags", pdf.PDFInteger(32))
	desc.Entries.Set("FontBBox", pdf.PDFArray{pdf.PDFInteger(-166), pdf.PDFInteger(-225), pdf.PDFInteger(1000), pdf.PDFInteger(931)})
	desc.Entries.Set("ItalicAngle", pdf.PDFInteger(0))
	desc.Entries.Set("Ascent", pdf.PDFInteger(718))
	desc.Entries.Set("Descent", pdf.PDFInteger(-207))
	desc.Entries.Set("CapHeight", pdf.PDFInteger(718))
	desc.Entries.Set("StemV", pdf.PDFInteger(88))

	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "Helvetica"})
	font.Entries.Set("FontDescriptor", desc)
	font.Entries.Set("_ref", pdf.PDFRef{ObjNum: 5})

	fontMap := pdf.NewPDFDict()
	fontMap.Entries.Set("F1", font)
	resources := pdf.NewPDFDict()
	resources.Entries.Set("Font", fontMap)

	contents := pdf.NewPDFDict()
	contents.HasStream = true
	contents.RawStream = []byte("BT /F1 12 Tf 72 720 Td (x) Tj ET")
	contents.Entries.Set("_ref", pdf.PDFRef{ObjNum: 4})

	pages := pdf.NewPDFDict()
	pages.Entries.Set("Type", pdf.PDFName{Value: "Pages"})
	pages.Entries.Set("Count", pdf.PDFInteger(1))
	pages.Entries.Set("_ref", pdf.PDFRef{ObjNum: 2})

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Parent", pages)
	page.Entries.Set("MediaBox", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(612), pdf.PDFInteger(792)})
	page.Entries.Set("Resources", resources)
	page.Entries.Set("Contents", contents)
	page.Entries.Set("_ref", pdf.PDFRef{ObjNum: 3})
	pages.Entries.Set("Kids", pdf.PDFArray{page})

	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	catalog.Entries.Set("Pages", pages)
	catalog.Entries.Set("_ref", pdf.PDFRef{ObjNum: 1})

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)

	var buf bytes.Buffer
	if err := writer.WriteDocument(&buf, trailer, 0); err != nil {
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

	// The regression documents are local-only (gitignored), so a clean
	// checkout -- CI included -- has nothing to walk.
	basePath := filepath.Join("tests", "regression")
	if _, err := os.Stat(basePath); err != nil {
		t.Skipf("no regression corpus present at %s", basePath)
	}

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
			defer cr.Close()

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

// sha256File hashes a file without reading it into the heap. The real-world
// corpus runs to gigabytes and holds individual documents of a hundred
// megabytes, which is exactly the shape the library itself refuses to load
// whole (see doc.go on mmap), so its own inventory check must not either.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
	// Hashing a multi-gigabyte corpus is the slow half of this check, so files
	// are hashed concurrently -- but each writes only its own slot, so the
	// problem list stays in `present` order rather than completion order.
	perFile := make([][]string, len(present))
	errs := make([]error, len(present))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, f := range present {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			rel, err := filepath.Rel(realworldDir, f)
			if err != nil {
				errs[i] = err
				return
			}
			rel = filepath.ToSlash(rel)
			e, ok := byPath[rel]
			if !ok {
				perFile[i] = append(perFile[i], fmt.Sprintf("%s: present but not in manifest.json (run scripts/gen-realworld-manifest.sh)", rel))
				return
			}
			got, err := sha256File(f)
			if err != nil {
				errs[i] = err
				return
			}
			if got != e.SHA256 {
				perFile[i] = append(perFile[i], fmt.Sprintf("%s: sha256 %s != manifest %s (regenerate the manifest)", rel, got, e.SHA256))
			}
			if e.License == "" || e.License == "TODO" {
				perFile[i] = append(perFile[i], fmt.Sprintf("%s: no licence recorded in manifest.json", rel))
			}
		}()
	}
	wg.Wait()
	for i := range present {
		if errs[i] != nil {
			return nil, errs[i]
		}
		problems = append(problems, perFile[i]...)
	}
	return problems, nil
}

// realWorldRecord is one file's outcome in the optional per-file report. The
// corpus is large enough that triaging it by re-running files one at a time is
// impractical, so a run can dump every verdict at once and be sliced offline.
type realWorldRecord struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"` // should-pass | should-convert
	Bytes      int64    `json:"bytes"`
	OK         bool     `json:"ok"` // verified clean, or converted to conformance
	Error      string   `json:"error,omitempty"`
	Issues     int      `json:"issues,omitempty"`
	Clauses    []string `json:"clauses,omitempty"`
	Rasterized int      `json:"rasterizedPages,omitempty"`
	Drops      []string `json:"rasterDrops,omitempty"`
	Unstable   bool     `json:"unstable,omitempty"` // two converts, two different outputs
	// BlankedPages lists the pages the conversion emptied of visible content,
	// OverpaintedPages the pages it drew over. Only the fidelity survey fills
	// them, since it has to render both sides.
	BlankedPages     []int `json:"blankedPages,omitempty"`
	OverpaintedPages []int `json:"overpaintedPages,omitempty"`
	// CompletedMs is milliseconds from the start of the batch to this file's
	// completion. Under a worker pool an individual duration is not observable,
	// but a large gap between consecutive completions still identifies the slow
	// file, which is what triage needs. Zero for should-pass, whose batch entry
	// point returns everything at once.
	CompletedMs int64 `json:"completedMs,omitempty"`
}

// realWorldReporter collects per-file records when GOPDFRAB_REALWORLD_REPORT
// names a file to write them to. Its zero value collects nothing, so the
// default run pays only a mutex per file.
type realWorldReporter struct {
	mu      sync.Mutex
	path    string
	records []realWorldRecord
}

func newRealWorldReporter() *realWorldReporter {
	return &realWorldReporter{path: os.Getenv("GOPDFRAB_REALWORLD_REPORT")}
}

func (r *realWorldReporter) enabled() bool { return r != nil && r.path != "" }

func (r *realWorldReporter) add(rec realWorldRecord) {
	if !r.enabled() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

// flush writes the records sorted by path, so two runs over the same corpus
// produce comparable files.
func (r *realWorldReporter) flush(t *testing.T) {
	if !r.enabled() {
		return
	}
	t.Helper()
	sort.Slice(r.records, func(i, j int) bool { return r.records[i].Path < r.records[j].Path })
	data, err := json.MarshalIndent(r.records, "", " ")
	if err != nil {
		t.Errorf("report: %v", err)
		return
	}
	if err := os.WriteFile(r.path, append(data, '\n'), 0o644); err != nil {
		t.Errorf("report: %v", err)
		return
	}
	t.Logf("wrote %d records to %s", len(r.records), r.path)
}

// distinctIssueClauses is issueClauses deduped and sorted: a real-world file
// can report the same clause on dozens of objects, and triage groups by clause.
func distinctIssueClauses(issues []PDFError) []string {
	set := make(map[string]bool, len(issues))
	for _, c := range issueClauses(issues) {
		set[c] = true
	}
	return sortedClauses(set)
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// shouldPassDeviations lists should-pass corpus files gopdfrab deliberately
// rejects, keyed by sha256 (content-addressed, so a re-classified or renamed
// file keeps its justification) with the reason as the value.
//
// The corpus puts a file in should-pass when veraPDF calls it PDF/A-1b, so a
// rejection is normally a gopdfrab false positive and a bug. The exception is a
// veraPDF *false negative*: a file that genuinely violates the standard and
// that gopdfrab is right to reject. Each entry has to make that case, and a
// listed file that starts verifying clean fails the test, so a deviation cannot
// outlive the reason for it.
var shouldPassDeviations = map[string]string{
	// arXiv 2008.01611: a Word-produced subset of TimesNewRomanPS-BoldMT is
	// shown with character code 48 ('0'), and the embedded program has no
	// glyph for it -- no (3,1), (0,3), (3,0) or (1,0) cmap entry, and `post` is
	// version 3.0, so none of ISO 32000-1 9.6.6.4's three lookup paths resolves
	// it. The page renders .notdef, which 6.3.5 exists to prevent. veraPDF
	// passes the file; verified by hand against the font's tables.
	"0e1ac7d3fec6acb516bec480c4b9f9d6021b1cd3d018f4900cfc69369b2d6d57": "veraPDF false negative: shown glyph absent from the embedded subset (6.3.5)",
}

// crossCheckDeviations lists should-convert corpus files whose converted output
// veraPDF rejects and gopdfrab is right to pass, keyed by the source's sha256
// with the reason as the value.
//
// A rejection in the cross-check is normally a gopdfrab false negative and a
// bug: gopdfrab converted the file to something it calls conformant, so veraPDF
// disagreeing means a rule we do not enforce or enforce too leniently. The
// exception is a veraPDF *false positive*, and each entry has to make that
// case. A listed file whose output stops being rejected fails the test, so a
// deviation cannot outlive the reason for it.
var crossCheckDeviations = map[string]string{
	// Both are the same defect: a non-symbolic TrueType font whose (3,1) cmap
	// does not carry the code, where ISO 32000-1 9.6.6.4 has a viewer fall back
	// to the (1,0) Macintosh cmap. gopdfrab follows that fallback and finds a
	// real glyph; veraPDF renders .notdef and reports 6.3.5, with 6.3.6 coming
	// after it from notdef's width. Verified by hand against the cmap tables --
	// in each case the glyph is present and the (1,0) lookup, not the (3,0)
	// one, is what resolves it.
	//
	// veraPDF-library issue 1575 is the same defect in its u-level .notdef
	// check (6.2.11.8), closed 2026-04-22 and root-caused to assigning .notdef
	// "without consulting the font program's cmap, violating ISO 32000-1:2008
	// section 9.6.6.4". That fix did not reach the PDF/A-1 6.3.5 path: checked
	// against the 1.31.158 dev build of 2026-08-20, both files are still
	// rejected.

	// oapen 0806b1b0: ZXWDID+Helvetica, code 176 (U+00B0). The program has no
	// (3,1) subtable at all -- (0,3), seven (1,0), (1,7), (1,29) -- and (1,0)
	// maps 0x00B0 to glyph 146, which is present with advance 712.89 against a
	// /Widths entry of 713.
	"d479a8b64c39e66ffaed26c95c6d961b7aec82fa9d9f82859f7ec9ce5ecfc994": "veraPDF false positive: (1,0) cmap fallback ignored, ISO 32000-1 9.6.6.4 (6.3.5/6.3.6)",

	// zenodo 21262729: BCEAEE+SymbolMT, code 176 (U+00B0). Subtables are (1,0)
	// and (3,0), no (3,1); both map the code to glyph 113, present with advance
	// 399.90 against a /Widths entry of 400.
	"ab40e70c9de7092f015d0b50495f22863211a1bee561946d7dfd650a4182c527": "veraPDF false positive: (1,0) cmap fallback ignored, ISO 32000-1 9.6.6.4 (6.3.5/6.3.6)",

	// zenodo 21261940: SJXMLS+AlegreyaSans-Italic, code 102 (/f), a Type1
	// program with no cmap in it. veraPDF reports a program width of 0 against
	// a /Widths entry of 280, and calls the glyph absent. The charstring is
	// subroutinized: it pushes 0 and 280 and calls subr 2171, which begins with
	// hsbw. gopdfrab follows the call and reads the width as 280, so the glyph
	// is present and consistent -- veraPDF's scan stops at callsubr.
	"8d7f376c991ead1a0446b8c90a99eb5a88635b209816521273489de7ffa46604": "veraPDF false positive: hsbw reached through callsubr not followed (6.3.5/6.3.6)",
}

// checkShouldPass verifies each file against PDF/A-1b and returns the paths
// gopdfrab wrongly rejected. These are real files a tool and veraPDF both call
// PDF/A-1b, so any rejection is a false positive unless
// shouldPassDeviations justifies it.
//
// VerifyAllContext rather than a loop: it is the batch entry point real callers
// use, so the corpus exercises it on real input rather than only on fixtures.
func checkShouldPass(t *testing.T, files []string, rep *realWorldReporter) (failures []string) {
	t.Helper()
	start := time.Now()
	results, err := VerifyAllContext(context.Background(), files, PDFA1B, Options{})
	if err != nil {
		t.Errorf("should-pass batch: %v", err)
		return nil
	}
	t.Logf("should-pass: verified %d files in %s", len(files), time.Since(start).Round(time.Millisecond))
	for _, fr := range results {
		rec := realWorldRecord{Path: fr.Path, Kind: "should-pass", Bytes: fileSize(fr.Path)}
		sum, _ := sha256File(fr.Path)
		why, deviates := shouldPassDeviations[sum]
		switch {
		case fr.Err != nil:
			rec.Error = fr.Err.Error()
			failures = append(failures, fmt.Sprintf("%s: open/parse error: %v", fr.Path, fr.Err))
		case !fr.Result.Valid && deviates:
			rec.Issues = len(fr.Result.Issues)
			rec.Clauses = distinctIssueClauses(fr.Result.Issues)
			t.Logf("known deviation: %s rejected (%v) -- %s", fr.Path, rec.Clauses, why)
		case !fr.Result.Valid:
			rec.Issues = len(fr.Result.Issues)
			rec.Clauses = distinctIssueClauses(fr.Result.Issues)
			failures = append(failures, fmt.Sprintf("%s: %d issues %v", fr.Path, len(fr.Result.Issues), rec.Clauses))
		case deviates:
			// The deviation has been fixed or the file changed: drop the entry
			// rather than let it sit there unexamined.
			failures = append(failures, fmt.Sprintf("%s: now verifies clean; remove its shouldPassDeviations entry (%s)", fr.Path, why))
		default:
			rec.OK = true
		}
		rep.add(rec)
	}
	return failures
}

// convertCorpusPass converts every file once through the batch entry point and
// returns each output's hash keyed by path, plus the three metrics.
//
// ConvertEachContext rather than a Convert loop, for three reasons beyond
// speed: it is the batch API real callers use, so the corpus exercises it on
// real input; its peak memory is bounded by the worker count rather than by the
// size of the batch, which is what that entry point exists for; and it closes
// each result after the callback, so a multi-gigabyte run cannot accumulate
// spilled output files.
func convertCorpusPass(t *testing.T, files []string, rep *realWorldReporter, sample *veraSample) (hashes map[string]string, conformant, rasterized, dropped int) {
	t.Helper()
	hashes = make(map[string]string, len(files))
	start := time.Now()
	err := ConvertEachContext(context.Background(), files, PDFA1B, Options{}, func(fr FileResult[ConvertResult]) error {
		// The callback is serialized, so these counters need no synchronization.
		rec := realWorldRecord{
			Path:        fr.Path,
			Kind:        "should-convert",
			Bytes:       fileSize(fr.Path),
			CompletedMs: time.Since(start).Milliseconds(),
		}
		defer func() { rep.add(rec) }()

		if fr.Err != nil {
			rec.Error = fr.Err.Error()
			t.Errorf("%s: convert error: %v", fr.Path, fr.Err)
			return nil // keep going: one bad file must not hide the rest
		}
		cr := fr.Result
		if cr.Result.Valid {
			conformant++
			rec.OK = true
		} else {
			rec.Issues = len(cr.Result.Issues)
			rec.Clauses = distinctIssueClauses(cr.Result.Issues)
		}
		if len(cr.RasterizedPages) > 0 {
			rasterized++
			rec.Rasterized = len(cr.RasterizedPages)
		}
		if len(cr.RasterDrops) > 0 {
			dropped++
			for _, d := range cr.RasterDrops {
				rec.Drops = append(rec.Drops, fmt.Sprintf("p%d:%s", d.Page, strings.Join(d.Features, ",")))
			}
		}
		h := sha256.New()
		if _, err := cr.WriteTo(h); err != nil {
			t.Errorf("%s: reading converted output: %v", fr.Path, err)
			return nil
		}
		hashes[fr.Path] = hex.EncodeToString(h.Sum(nil))
		sample.keep(t, fr.Path, cr)
		return nil
	})
	if err != nil {
		t.Errorf("should-convert batch: %v", err)
	}
	return hashes, conformant, rasterized, dropped
}

// veraSample cross-checks converted real-world output against the veraPDF
// binary. The corpus is not in CI and so is not covered by the differential
// harness over the committed suites, which is exactly why two verifier
// false-negatives lived in it unseen: both showed up as veraPDF rejecting an
// output gopdfrab had just called conformant.
//
// Off unless GOPDFRAB_REALWORLD_VERAPDF names a sample size ("all", or a count
// of files). A JVM run over 1500 documents takes hours, so the default when it
// is set is a sample -- evenly spaced through the sorted file list, never by
// map order, so two runs check the same files.
type veraSample struct {
	dir    string
	want   map[string]bool   // source paths to check
	kept   map[string]string // written output path -> source path
	failed int
}

func newVeraSample(t *testing.T, files []string) *veraSample {
	t.Helper()
	spec := os.Getenv("GOPDFRAB_REALWORLD_VERAPDF")
	if spec == "" || len(files) == 0 {
		return nil
	}
	if _, err := os.Stat(veraPDFBin); err != nil {
		t.Skipf("veraPDF reference verifier not available: %v", err)
	}

	n := len(files)
	if spec != "all" {
		parsed, err := strconv.Atoi(spec)
		if err != nil || parsed <= 0 {
			t.Fatalf("GOPDFRAB_REALWORLD_VERAPDF = %q, want \"all\" or a positive count", spec)
		}
		n = min(parsed, len(files))
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	want := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		// Evenly spaced rather than the first n: a corpus is grouped by
		// source, and the first n would all come from one collection.
		want[sorted[i*len(sorted)/n]] = true
	}
	t.Logf("veraPDF cross-check: %d of %d converted outputs", len(want), len(files))
	return &veraSample{dir: t.TempDir(), want: want, kept: map[string]string{}}
}

// keep writes one converted output to disk for the cross-check, if it is in
// the sample. The output is otherwise closed and gone by the next callback.
func (v *veraSample) keep(t *testing.T, source string, cr ConvertResult) {
	if v == nil || !v.want[source] {
		return
	}
	t.Helper()
	out := filepath.Join(v.dir, fmt.Sprintf("%04d.pdf", len(v.kept)))
	if err := cr.Save(out); err != nil {
		t.Errorf("%s: saving output for the veraPDF cross-check: %v", source, err)
		return
	}
	v.kept[out] = source
}

// crossCheck runs veraPDF over everything kept and reports every output it
// rejects. gopdfrab converts the whole corpus to conformance, so any rejection
// is a verifier false negative -- a rule gopdfrab does not enforce, or enforces
// too leniently -- and not a matter of interpretation to be argued away.
func (v *veraSample) crossCheck(t *testing.T) {
	if v == nil || len(v.kept) == 0 {
		return
	}
	t.Helper()
	args := make([]string, 0, len(v.kept))
	for out := range v.kept {
		args = append(args, out)
	}
	sort.Strings(args)

	start := time.Now()
	rep, err := runVeraPDF(args...)
	if err != nil {
		t.Errorf("veraPDF cross-check: %v", err)
		return
	}
	seen := 0
	deviated := map[string]bool{}
	for _, job := range rep.Jobs.Job {
		source, ok := v.kept[job.Item.Name]
		if !ok {
			continue
		}
		seen++
		if job.Report == nil {
			t.Errorf("veraPDF cross-check: no verdict for the output of %s", source)
			continue
		}
		if job.Report.IsCompliant {
			continue
		}
		var clauses []string
		for _, r := range job.Report.Details.Rules {
			if r.Status == "failed" {
				clauses = append(clauses, r.Clause)
			}
		}
		sum, _ := sha256File(source)
		if why, ok := crossCheckDeviations[sum]; ok {
			deviated[sum] = true
			t.Logf("known deviation: veraPDF rejects the output of %s (%v) -- %s", source, sortedStrings(clauses), why)
			continue
		}
		v.failed++
		t.Errorf("veraPDF rejects the converted output of %s: %v", source, sortedStrings(clauses))
	}
	// A deviation that no longer happens has outlived its reason. Only over the
	// whole corpus: a sample need not contain the file.
	if os.Getenv("GOPDFRAB_REALWORLD_VERAPDF") == "all" {
		for sum, why := range crossCheckDeviations {
			if !deviated[sum] {
				t.Errorf("crossCheckDeviations lists %s (%s) but its output is no longer rejected; remove the entry", sum, why)
			}
		}
	}
	t.Logf("veraPDF cross-check: %d outputs checked, %d rejected, %d known deviations (in %s)",
		seen, v.failed, len(deviated), time.Since(start).Round(time.Second))
}

// surveyFidelity re-converts a sample of the corpus with the fidelity check on
// and reports every page that came out blank or came out drawn over.
// Conformance says nothing about whether the drawing survived, so this is the
// only metric that catches a repair which destroys the thing it was repairing
// -- and it is the metric that was missing when clamping oversized coordinates
// emptied whole documents while gopdfrab and veraPDF both called the output
// valid.
//
// Off unless GOPDFRAB_REALWORLD_FIDELITY names a sample size ("all", or a count
// of files). Rendering both sides of every document costs several times a plain
// conversion, so when it is set the default is a sample -- evenly spaced through
// the sorted file list, never by map order, so two runs survey the same files.
// What a full sweep of the real-world corpus still blanks and still draws
// over, measured for roadmap item 36 (it was 20 files and 47 pages blanked, 27
// files and 717 pages drawn over, when the item opened). Both remainders have
// a mechanism and a file against them; the item carries them.
const (
	maxBlankedFiles = 1
	maxBlankedPages = 1

	maxOverpaintedFiles = 1
	maxOverpaintedPages = 2

	// fidelitySurveyWorkers bounds how many files are rendered at once. Every
	// page of both sides of a document is measured while it is compared, so a
	// few hundred-page books in flight together is gigabytes -- one worker per
	// core ran a 16-core machine out of memory.
	fidelitySurveyWorkers = 4
)

func surveyFidelity(t *testing.T, files []string, rep *realWorldReporter) {
	t.Helper()
	sample := fidelitySample(t, files)
	if len(sample) == 0 {
		return
	}

	blanked := map[string][]int{}
	overpainted := map[string][]int{}
	start := time.Now()
	err := ConvertEachContext(context.Background(), sample, PDFA1B,
		Options{CheckFidelity: true, Workers: fidelitySurveyWorkers},
		func(fr FileResult[ConvertResult]) error {
			// The callback is serialized, so the maps need no synchronization.
			if fr.Err != nil {
				return nil // already reported by the conversion pass
			}
			if pages := fr.Result.BlankedPages; len(pages) > 0 {
				blanked[fr.Path] = pages
				t.Logf("%s: convert blanked %d page(s): %v", fr.Path, len(pages), pages)
			}
			if pages := fr.Result.OverpaintedPages; len(pages) > 0 {
				overpainted[fr.Path] = pages
				t.Logf("%s: convert drew over %d page(s): %v", fr.Path, len(pages), pages)
			}
			return nil
		})
	if err != nil {
		t.Errorf("fidelity survey: %v", err)
	}

	blankedPages, overpaintedPages := countPages(blanked), countPages(overpainted)
	t.Logf("fidelity survey of %d files: %d blanked at least one page (%d pages in all), "+
		"%d drew over at least one page (%d pages in all) (in %s)",
		len(sample), len(blanked), blankedPages, len(overpainted), overpaintedPages,
		time.Since(start).Round(time.Second))
	// A ceiling rather than zero, in the shape of the conformance floor: the
	// pages that still blank are a known open item, and pinning the count is
	// what tells a regression apart from what is already known. Only a full
	// sweep is comparable to the recorded numbers.
	if len(sample) == len(files) {
		if len(blanked) > maxBlankedFiles || blankedPages > maxBlankedPages {
			t.Errorf("blanked %d files / %d pages, over the recorded %d / %d (see roadmap item 36)",
				len(blanked), blankedPages, maxBlankedFiles, maxBlankedPages)
		}
		if len(overpainted) > maxOverpaintedFiles || overpaintedPages > maxOverpaintedPages {
			t.Errorf("drew over %d files / %d pages, over the recorded %d / %d (see roadmap item 36)",
				len(overpainted), overpaintedPages, maxOverpaintedFiles, maxOverpaintedPages)
		}
	}

	if rep.enabled() {
		rep.mu.Lock()
		for i := range rep.records {
			rep.records[i].BlankedPages = blanked[rep.records[i].Path]
			rep.records[i].OverpaintedPages = overpainted[rep.records[i].Path]
		}
		rep.mu.Unlock()
	}
}

// countPages totals the pages listed per file.
func countPages(perFile map[string][]int) int {
	n := 0
	for _, pages := range perFile {
		n += len(pages)
	}
	return n
}

// fidelitySample picks the files to survey, or nil when the survey is off.
func fidelitySample(t *testing.T, files []string) []string {
	t.Helper()
	spec := os.Getenv("GOPDFRAB_REALWORLD_FIDELITY")
	if spec == "" || len(files) == 0 {
		return nil
	}
	n := len(files)
	if spec != "all" {
		parsed, err := strconv.Atoi(spec)
		if err != nil || parsed <= 0 {
			t.Fatalf("GOPDFRAB_REALWORLD_FIDELITY = %q, want \"all\" or a positive count", spec)
		}
		n = min(parsed, len(files))
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	sample := make([]string, 0, n)
	for i := range n {
		// Evenly spaced rather than the first n: a corpus is grouped by source,
		// and the first n would all come from one collection.
		sample = append(sample, sorted[i*len(sorted)/n])
	}
	t.Logf("fidelity survey: %d of %d converted outputs", len(sample), len(files))
	return sample
}

// sortedStrings returns a sorted copy with duplicates removed, so a clause
// reported on dozens of objects is named once and in the same order every run.
func sortedStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// checkShouldConvert converts each file and returns how many reached PDF/A-1b
// conformance, how many needed a page rasterized to get there, and how many
// lost content the rasterizer could not draw.
//
// rasterized is the honest "lossy" metric: RasterDrops counts only content the
// rasterizer could not render, so a page flattened losslessly -- still a
// conversion from text and vectors to pixels -- would not appear there.
//
// With GOPDFRAB_REALWORLD_DETERMINISM=1 the corpus is converted a second time
// and the two runs' outputs compared byte for byte. Every nondeterminism this
// library has had was a map-iteration order leaking into output or into a
// message, and each was found only by a parity run -- real documents carry far
// more ambiguous state than the fixtures do, so this is where the next one
// would show up.
func checkShouldConvert(t *testing.T, files []string, rep *realWorldReporter) (conformant, rasterized, dropped int) {
	t.Helper()
	sample := newVeraSample(t, files)
	first, conformant, rasterized, dropped := convertCorpusPass(t, files, rep, sample)
	sample.crossCheck(t)
	surveyFidelity(t, files, rep)
	if os.Getenv("GOPDFRAB_REALWORLD_DETERMINISM") == "" {
		return conformant, rasterized, dropped
	}
	t.Logf("determinism sweep: converting %d files a second time", len(files))
	second, _, _, _ := convertCorpusPass(t, files, nil, nil)
	unstable := 0
	for _, p := range files {
		a, aok := first[p]
		b, bok := second[p]
		if !aok || !bok || a == b {
			continue
		}
		unstable++
		t.Errorf("nondeterministic conversion: %s produced %s then %s", p, a[:12], b[:12])
	}
	if rep.enabled() {
		rep.mu.Lock()
		for i := range rep.records {
			if a, ok := first[rep.records[i].Path]; ok {
				if b, ok := second[rep.records[i].Path]; ok && a != b {
					rep.records[i].Unstable = true
				}
			}
		}
		rep.mu.Unlock()
	}
	t.Logf("determinism sweep: %d of %d files converted nondeterministically", unstable, len(files))
	return conformant, rasterized, dropped
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

	rep := newRealWorldReporter()
	defer rep.flush(t)

	if len(passFiles) > 0 {
		failures := checkShouldPass(t, passFiles, rep)
		t.Logf("should-pass: %d files, %d rejected", len(passFiles), len(failures))
		for _, f := range failures {
			t.Errorf("should-pass false positive: %s", f)
		}
	}
	if len(convFiles) > 0 {
		start := time.Now()
		conformant, rasterized, dropped := checkShouldConvert(t, convFiles, rep)
		t.Logf("should-convert: %d files, %d conformant, %d needed a rasterized page, %d lost undrawable content (in %s)",
			len(convFiles), conformant, rasterized, dropped, time.Since(start).Round(time.Second))
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
	// A real reporter, so the report path is covered here too rather than only
	// on a machine that has the corpus.
	rep := &realWorldReporter{path: filepath.Join(t.TempDir(), "report.json")}

	pass := realWorldPDFs(t, passDir)
	if failures := checkShouldPass(t, pass, rep); len(pass) != 1 || len(failures) != 0 {
		t.Errorf("should-pass self-check: files=%d failures=%v, want 1/none", len(pass), failures)
	}

	convDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(convDir, "plain.pdf"), []byte(plainPDF), 0o644); err != nil {
		t.Fatal(err)
	}
	conv := realWorldPDFs(t, convDir)
	// The fidelity survey is off by default, so cover it here rather than only
	// on a machine that has the corpus. A plain page must survive its own
	// conversion, so it must report nothing blanked.
	t.Setenv("GOPDFRAB_REALWORLD_FIDELITY", "all")
	conformant, rasterized, dropped := checkShouldConvert(t, conv, rep)
	if len(conv) != 1 || conformant != 1 {
		t.Errorf("should-convert self-check: files=%d conformant=%d, want 1/1", len(conv), conformant)
	}
	// A plain vector page converts without any raster fallback, so both loss
	// metrics must stay at zero -- otherwise they are measuring nothing.
	if rasterized != 0 || dropped != 0 {
		t.Errorf("should-convert self-check: rasterized=%d dropped=%d, want 0/0", rasterized, dropped)
	}

	// A missing directory yields no files (the skip path).
	if got := realWorldPDFs(t, filepath.Join(t.TempDir(), "absent")); len(got) != 0 {
		t.Errorf("absent dir returned %d files, want 0", len(got))
	}

	rep.flush(t)
	data, err := os.ReadFile(rep.path)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	var records []realWorldRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("report has %d records, want 2 (one per corpus file)", len(records))
	}
	for _, r := range records {
		if !r.OK || r.Path == "" || r.Kind == "" || r.Bytes == 0 {
			t.Errorf("report record incomplete: %+v", r)
		}
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
