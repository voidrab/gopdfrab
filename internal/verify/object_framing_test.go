package verify

import (
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestIndirectObjectLimit pins 6.1.12's object-count limit at its boundary.
// The limit applies to the object count, so an empty cross-reference table
// must not be reported as exceeding it.
func TestIndirectObjectLimit(t *testing.T) {
	maxIndirectObjects := a1b().Limits.MaxIndirectObjects
	if got := indirectObjectLimit(0, a1b()); got != nil {
		t.Errorf("0 objects: %v, want no issue", got)
	}
	if got := indirectObjectLimit(maxIndirectObjects, a1b()); got != nil {
		t.Errorf("%d objects: %v, want no issue at the limit", maxIndirectObjects, got)
	}
	got := indirectObjectLimit(maxIndirectObjects+1, a1b())
	if len(got) != 1 || got[0].Check() != pdf.Checks.Structure.IndirectObjectsExceeded ||
		got[0].Messages()[0] != "8388608 indirect objects exceed the limit of 8,388,607" {
		t.Errorf("%d objects: %v, want one IndirectObjectsExceeded", maxIndirectObjects+1, got)
	}

	d := pdf.NewRawReader(nil, pdf.NewPDFDict(), 0, 0)
	for _, e := range verifyAllObjectFraming(d, a1b()) {
		if e.Check() == pdf.Checks.Structure.IndirectObjectsExceeded {
			t.Fatal("an empty cross-reference table must not report too many objects")
		}
	}
}

// TestObjectFramingDiagnosticsInObjectOrder pins that unreachable objects are
// resolved -- and their framing diagnostics recorded -- in object-number
// order rather than map order, so StructErrors is the same on every run.
func TestObjectFramingDiagnosticsInObjectOrder(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	// Unreferenced streams whose 'stream' keyword has trailing junk (6.1.7).
	for n := 3; n < 23; n++ {
		b.Obj(n, "<< /Length 3 >>\nstream junk\nabc\nendstream")
	}
	data := b.FinishClassic("<< /Size 23 /Root 1 0 R >>")

	d, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	verifyAllObjectFraming(d, a1b())

	var objs []int
	for _, e := range a1b().Issues(d.Diagnostics()) {
		if e.Check() == pdf.Checks.Structure.StreamKeywordEOL {
			if e.Page() != 0 {
				t.Errorf("framing diagnostic reports page %d; an object number is not a page", e.Page())
			}
			ref, _ := e.ObjectRef()
			objs = append(objs, ref.ObjNum)
		}
	}
	if len(objs) != 20 {
		t.Fatalf("got %d StreamKeywordEOL diagnostics, want 20", len(objs))
	}
	if !slices.IsSorted(objs) {
		t.Fatalf("diagnostics not in object order: %v", objs)
	}
}
