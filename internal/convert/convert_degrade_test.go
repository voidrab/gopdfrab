package convert

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
	"github.com/voidrab/gopdfrab/internal/verify"
)

func graphResolutionIssues(issues []pdf.PDFError) []pdf.PDFError {
	var out []pdf.PDFError
	for _, e := range issues {
		if e.Check() == pdf.Checks.Structure.GraphResolutionFailure {
			out = append(out, e)
		}
	}
	return out
}

// TestConvertRecoversBrokenOffset: an input whose content stream sits behind a
// wrong xref offset converts normally -- the object is recovered, the rewrite
// emits a correct xref, and the recovery is not a residual because the output
// genuinely no longer has the defect.
func TestConvertRecoversBrokenOffset(t *testing.T) {
	intact := pdfgen.PlainThreeIssue()
	off3 := int64(bytes.Index(intact, []byte("3 0 obj")))
	broken := pdfgen.BreakXrefOffset(intact, 4, off3)
	if bytes.Equal(broken, intact) {
		t.Fatal("BreakXrefOffset changed nothing")
	}

	cr, err := ConvertBytes(broken, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if len(cr.Output) == 0 {
		t.Fatal("Output is empty, want a full rewrite of the recovered document")
	}
	if got := graphResolutionIssues(cr.Residual()); len(got) != 0 {
		t.Errorf("Residual() carries recovery issues %v; the rewrite fixed the xref", got)
	}
	res, err := verify.VerifyBytes(cr.Output, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("re-verify output: %v", err)
	}
	if got := graphResolutionIssues(res.Issues); len(got) != 0 {
		t.Errorf("output re-verifies with resolution issues %v", got)
	}
}

// TestConvertDegradedObjectReportsResidual: an unrecoverable object converts
// to a best-effort rewrite, but the content loss is a residual and the result
// never claims validity -- output and honesty together (roadmap items 2+3).
func TestConvertDegradedObjectReportsResidual(t *testing.T) {
	intact := pdfgen.PlainThreeIssue()
	broken := bytes.Replace(intact, []byte("4 0 obj\n<< /Length"), []byte("4 0 obj\n<< ]Length"), 1)
	if bytes.Equal(broken, intact) {
		t.Fatal("seed no longer matches; test input needs updating")
	}

	cr, err := ConvertBytes(broken, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if len(cr.Output) == 0 {
		t.Fatal("Output is empty, want a best-effort rewrite")
	}
	if cr.Result.Valid {
		t.Error("Result.Valid = true for a conversion that lost content")
	}
	got := graphResolutionIssues(cr.Residual())
	if len(got) != 1 || !strings.Contains(got[0].Messages()[0], "treated as null") {
		t.Errorf("Residual() resolution issues = %v, want exactly one degradation record", got)
	}
}

// TestConvertUnresolvableGraphReturnsError: when even per-object degradation
// cannot produce a graph (here: a reference chain past the resolve depth cap),
// Convert must return ErrUnresolvableGraph -- never a nil error with empty
// Output. The best-effort verify Result still rides along.
func TestConvertUnresolvableGraphReturnsError(t *testing.T) {
	const chain = 70000
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Deep 4 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	for i := 4; i < 4+chain; i++ {
		b.Obj(i, fmt.Sprintf("[%d 0 R]", i+1))
	}
	b.Obj(4+chain, "42")
	data := b.FinishClassic("<< /Size 5 /Root 1 0 R >>")

	cr, err := ConvertBytes(data, pdf.PDFA1B)
	if !errors.Is(err, pdf.ErrUnresolvableGraph) {
		t.Fatalf("err = %v, want ErrUnresolvableGraph", err)
	}
	if len(cr.Output) != 0 {
		t.Errorf("Output = %d bytes, want empty alongside the error", len(cr.Output))
	}
	if cr.Result.Valid || len(cr.Result.Issues) == 0 {
		t.Errorf("Result = %+v, want an invalid best-effort verify result", cr.Result)
	}
}
