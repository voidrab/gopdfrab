package pdf_test

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// This file holds minimal, hand-written reproducers for every crash the fuzz
// harness has surfaced and had fixed. Each test documents one root cause and
// guards against regression independently of the generator: a failure here (a
// panic or, for the recursion cases, an unrecoverable stack overflow) means the
// bug is back. They must all pass -- i.e. return an error gracefully rather than
// crash.

// openReproduces runs a hand-built malformed PDF through the read + resolve +
// page-count path, failing only if it panics (an error return is the expected,
// fixed behaviour).
func openReproduces(t *testing.T, name string, data []byte) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		r, err := pdf.OpenBytes(data)
		if err != nil {
			return
		}
		defer r.Close()
		r.ResolveGraph()
		r.GetPageCount()
	})
}

// TestCrasher_NegativeStreamLength: a stream whose /Length is negative sliced
// out of range in validateStream (fixed: reject negative/overflowing Length).
func TestCrasher_NegativeStreamLength(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Contents 4 0 R >>")
	b.Obj(4, "<< /Length -5 >>\nstream\nq Q\nendstream")
	openReproduces(t, "negative-length", b.FinishClassic("<< /Size 5 /Root 1 0 R >>"))
}

// TestCrasher_NonDictRoot: a trailer /Root pointing at a non-dictionary object
// panicked an unchecked type assertion in BuildPageIndex (fixed: comma-ok).
func TestCrasher_NonDictRoot(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<616263>") // Root resolves to a hex string, not a dict
	b.Obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	openReproduces(t, "non-dict-root", b.FinishClassic("<< /Size 3 /Root 1 0 R >>"))
}

// TestCrasher_CyclicPageTree: a /Kids entry referring back to its own Pages
// node recursed BuildPageIndex into a stack overflow (fixed: visited set).
func TestCrasher_CyclicPageTree(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [2 0 R] /Count 1 >>") // self-referential
	openReproduces(t, "cyclic-kids", b.FinishClassic("<< /Size 3 /Root 1 0 R >>"))
}

// TestCrasher_DeepNesting: a deeply nested array in an object body recursed
// parseObject/parseArray into a stack overflow (fixed: maxParseDepth).
func TestCrasher_DeepNesting(t *testing.T) {
	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Junk "+deep+" >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>")
	openReproduces(t, "deep-array", b.FinishClassic("<< /Size 4 /Root 1 0 R >>"))
}

// TestCrasher_PostScriptDepth: a Type 4 function whose program nests procedures
// deeply recursed the PostScript parser (fixed: maxPostScriptDepth).
func TestCrasher_PostScriptDepth(t *testing.T) {
	prog := strings.Repeat("{", 5000) + strings.Repeat("}", 5000)
	d := pdf.NewPDFDict()
	d.HasStream = true
	d.RawStream = []byte(prog)
	d.Entries["FunctionType"] = pdf.PDFInteger(4)
	d.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
	d.Entries["Range"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
	d.Entries["Length"] = pdf.PDFInteger(len(prog))
	if _, err := pdf.ParseFunction(d); err == nil {
		t.Error("expected an error for an over-deep PostScript program")
	}
}

// TestCrasher_StitchingDepth: a Type 3 function whose sub-functions nest Type 3s
// deeply recursed ParseFunction (fixed: maxFunctionDepth).
func TestCrasher_StitchingDepth(t *testing.T) {
	// Innermost is a Type 2; wrap it in `depth` stitching functions.
	inner := pdf.NewPDFDict()
	inner.Entries["FunctionType"] = pdf.PDFInteger(2)
	inner.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
	inner.Entries["N"] = pdf.PDFInteger(1)

	cur := pdf.PDFValue(inner)
	for i := 0; i < 200; i++ {
		s := pdf.NewPDFDict()
		s.Entries["FunctionType"] = pdf.PDFInteger(3)
		s.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
		s.Entries["Functions"] = pdf.PDFArray{cur}
		s.Entries["Bounds"] = pdf.PDFArray{}
		s.Entries["Encode"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
		cur = s
	}
	if _, err := pdf.ParseFunction(cur); err == nil {
		t.Error("expected an error for over-deep stitching nesting")
	}
}

// TestCrasher_SampledBlowup: a Type 0 function with a high-dimension Size would
// allocate 2^len(Size) corners (fixed: bound dimensionality).
func TestCrasher_SampledBlowup(t *testing.T) {
	size := make(pdf.PDFArray, 40)
	for i := range size {
		size[i] = pdf.PDFInteger(2)
	}
	d := pdf.NewPDFDict()
	d.HasStream = true
	d.RawStream = []byte{0, 0, 0, 0}
	d.Entries["FunctionType"] = pdf.PDFInteger(0)
	d.Entries["Domain"] = size // reuse: just needs to be long enough not to matter
	d.Entries["Range"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
	d.Entries["Size"] = size
	d.Entries["BitsPerSample"] = pdf.PDFInteger(8)
	d.Entries["Length"] = pdf.PDFInteger(4)
	if _, err := pdf.ParseFunction(d); err == nil {
		t.Error("expected an error for an over-dimensional sampled function")
	}
}

// TestCrasher_CCITTRowsAmplification: a huge declared Rows drove the tail-
// padding loop into an OOM (fixed: bound declared image size).
func TestCrasher_CCITTRowsAmplification(t *testing.T) {
	p := pdf.CCITTParams{Columns: 1728, Rows: 1 << 30}
	if _, err := pdf.DecodeCCITT([]byte{0x00, 0x01}, p); err == nil {
		t.Error("expected an error for an absurd CCITT Rows value")
	}
}

// TestCrasher_ClampDomainShort: evaluating a function with more inputs than its
// Domain covers indexed out of range in clampDomain (fixed: bounds-check).
func TestCrasher_ClampDomainShort(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries["FunctionType"] = pdf.PDFInteger(2)
	d.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)} // one input
	d.Entries["N"] = pdf.PDFInteger(1)
	fn, err := pdf.ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}
	fn.Eval([]float64{0.1, 0.2, 0.3}) // three inputs -- must not panic
}

// TestCrasher_CyclicColorSpace: a named colour space that resolves back to
// itself recursed ResolveColor forever (fixed: maxColorSpaceDepth).
func TestCrasher_CyclicColorSpace(t *testing.T) {
	inner := pdf.NewPDFDict()
	inner.Entries["CS0"] = pdf.PDFName{Value: "CS0"} // resolves to itself
	resources := pdf.NewPDFDict()
	resources.Entries["ColorSpace"] = inner
	pdf.ResolveColor(pdf.PDFName{Value: "CS0"}, []float64{0, 0.5, 1}, resources) // must not hang/panic
}
