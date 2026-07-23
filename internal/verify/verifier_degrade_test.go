package verify

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

func issueStrings(issues []pdf.PDFError) []string {
	var out []string
	for _, e := range issues {
		out = append(out, e.String())
	}
	slices.Sort(out)
	return out
}

func splitGraphResolution(issues []pdf.PDFError) (rest, graphRes []pdf.PDFError) {
	for _, e := range issues {
		if e.Check() == pdf.Checks.Structure.GraphResolutionFailure {
			graphRes = append(graphRes, e)
		} else {
			rest = append(rest, e)
		}
	}
	return rest, graphRes
}

// TestBrokenXrefOffsetOracle is the roadmap item-2 oracle: a file with one
// deliberately broken xref offset must verify to exactly the same issue set as
// the intact original, plus one recovery issue. Before per-object degradation,
// the bad offset suppressed the colour and Metadata findings entirely.
func TestBrokenXrefOffsetOracle(t *testing.T) {
	intact := pdfgen.PlainThreeIssue()
	intactRes, err := VerifyBytes(intact, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes(intact): %v", err)
	}
	if len(intactRes.Issues) == 0 {
		t.Fatal("intact seed reports no issues; the oracle needs a non-conformant baseline")
	}

	off3 := int64(bytes.Index(intact, []byte("3 0 obj")))
	broken := pdfgen.BreakXrefOffset(intact, 4, off3)
	if bytes.Equal(broken, intact) {
		t.Fatal("BreakXrefOffset changed nothing")
	}
	brokenRes, err := VerifyBytes(broken, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes(broken): %v", err)
	}

	rest, graphRes := splitGraphResolution(brokenRes.Issues)
	if len(graphRes) != 1 || !strings.Contains(graphRes[0].Messages()[0], "recovered") {
		t.Errorf("GraphResolutionFailure issues = %v, want exactly one recovery record", graphRes)
	}
	if ref, ok := graphRes[0].ObjectRef(); !ok || ref.ObjNum != 4 {
		t.Errorf("recovery issue ref = %v, want object 4", graphRes[0])
	}
	want := issueStrings(intactRes.Issues)
	got := issueStrings(rest)
	if !slices.Equal(got, want) {
		t.Errorf("issue sets differ:\nintact: %v\nbroken minus recovery: %v", want, got)
	}
}

// TestBrokenStartxrefOracle is the roadmap item-4 oracle for whole-table
// damage: a file whose startxref offset is destroyed (so the entire
// cross-reference table must be rebuilt by scanning for objects) must verify to
// exactly the same issue set as the intact original, plus one 6.1.4 recovery
// issue.
func TestBrokenStartxrefOracle(t *testing.T) {
	intact := pdfgen.PlainThreeIssue()
	intactRes, err := VerifyBytes(intact, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes(intact): %v", err)
	}
	if len(intactRes.Issues) == 0 {
		t.Fatal("intact seed reports no issues; the oracle needs a non-conformant baseline")
	}

	broken := pdfgen.BreakStartxref(intact)
	if bytes.Equal(broken, intact) {
		t.Fatal("BreakStartxref changed nothing")
	}
	brokenRes, err := VerifyBytes(broken, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes(broken): %v", err)
	}

	// The intact file has no 6.1.4 issues; the rebuilt one reports the broken
	// cross-reference section under 6.1.4 (both the verify-time format check and
	// the parse-time recovery diagnostic). Every other finding must survive
	// unchanged -- that is the oracle.
	var rest, recovery []pdf.PDFError
	for _, e := range brokenRes.Issues {
		if e.Check() == pdf.Checks.Structure.XRefKeyword {
			recovery = append(recovery, e)
		} else {
			rest = append(rest, e)
		}
	}
	if len(recovery) == 0 {
		t.Error("no 6.1.4 issue reported for the rebuilt cross-reference table")
	}
	if !slices.Equal(issueStrings(rest), issueStrings(intactRes.Issues)) {
		t.Errorf("issue sets differ:\nintact: %v\nbroken minus 6.1.4: %v",
			issueStrings(intactRes.Issues), issueStrings(rest))
	}
}

// TestDegradedObjectKeepsUnrelatedChecks: when the broken object cannot be
// recovered, findings inside it are gone -- but every unrelated check must
// still run, and the loss must be reported.
func TestDegradedObjectKeepsUnrelatedChecks(t *testing.T) {
	intact := pdfgen.PlainThreeIssue()
	broken := bytes.Replace(intact, []byte("4 0 obj\n<< /Length"), []byte("4 0 obj\n<< ]Length"), 1)
	if bytes.Equal(broken, intact) {
		t.Fatal("seed no longer matches; test input needs updating")
	}

	res, err := VerifyBytes(broken, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	rest, graphRes := splitGraphResolution(res.Issues)
	if len(graphRes) != 1 || !strings.Contains(graphRes[0].Messages()[0], "treated as null") {
		t.Errorf("GraphResolutionFailure issues = %v, want exactly one degradation record", graphRes)
	}
	var clauses []string
	for _, e := range rest {
		clauses = append(clauses, e.Check().Clause())
	}
	for _, want := range []string{"6.1.3", "6.7.2"} {
		if !slices.Contains(clauses, want) {
			t.Errorf("clause %s missing from %v; unrelated checks were suppressed", want, clauses)
		}
	}
	if slices.Contains(clauses, "6.2.3.3") {
		t.Errorf("colour finding reported for a nulled content stream: %v", rest)
	}
}

// TestDegradedObjectDiscardsUsageSuppressions: an unused non-embedded font is
// suppressed under PDFA1B when content usage is fully known, but a degraded
// object makes the usage sets a subset of the truth, so the suppression must
// be discarded and the font flagged.
func TestDegradedObjectDiscardsUsageSuppressions(t *testing.T) {
	build := func() []byte {
		b := pdfgen.NewBuilder("%PDF-1.4\n")
		b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
		b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
		b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 6 0 R >> >> /Contents [4 0 R 5 0 R] >>")
		b.StreamObj(4, "<<", []byte("q\nQ\n"))
		b.StreamObj(5, "<<", []byte("q\nQ\n"))
		b.Obj(6, "<< /Type /Font /Subtype /TrueType /BaseFont /ArialMT /FontDescriptor 7 0 R >>")
		b.Obj(7, "<< /Type /FontDescriptor /FontName /ArialMT /Flags 32 >>")
		return b.FinishClassic("<< /Size 8 /Root 1 0 R >>")
	}

	hasSimpleNotEmbedded := func(data []byte) bool {
		res, err := VerifyBytes(data, pdf.PDFA1B)
		if err != nil {
			t.Fatalf("VerifyBytes: %v", err)
		}
		for _, e := range res.Issues {
			if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
				return true
			}
		}
		return false
	}

	intact := build()
	if hasSimpleNotEmbedded(intact) {
		t.Fatal("intact document flags the unused font; suppression baseline is broken")
	}
	broken := bytes.Replace(intact, []byte("5 0 obj\n<< /Length"), []byte("5 0 obj\n<< ]Length"), 1)
	if bytes.Equal(broken, intact) {
		t.Fatal("document no longer matches; test input needs updating")
	}
	if !hasSimpleNotEmbedded(broken) {
		t.Error("degraded object did not discard the unused-font suppression")
	}
}

// TestDegradedCatalogSurfacesPostStructural: when the catalog itself degrades,
// verification bails at page-index construction -- but the bail must still
// surface the per-object diagnostics instead of dropping them.
func TestDegradedCatalogSurfacesPostStructural(t *testing.T) {
	intact := pdfgen.PlainThreeIssue()
	broken := bytes.Replace(intact, []byte("<< /Type /Catalog"), []byte("<< ]Type /Catalog"), 1)
	if bytes.Equal(broken, intact) {
		t.Fatal("seed no longer matches; test input needs updating")
	}

	res, err := VerifyBytes(broken, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	_, graphRes := splitGraphResolution(res.Issues)
	var doc, perObject bool
	for _, e := range graphRes {
		if ref, ok := e.ObjectRef(); ok && ref.ObjNum == 1 {
			perObject = true
		} else {
			doc = true
		}
	}
	if !doc || !perObject {
		t.Errorf("GraphResolutionFailure issues = %v, want both the bail and the object-1 degradation", graphRes)
	}
}
