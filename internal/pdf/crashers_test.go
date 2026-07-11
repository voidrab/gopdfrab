package pdf_test

import (
	"bytes"
	"compress/zlib"
	"fmt"
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

// TestCrasher_OverflowStreamLength: a /Length near math.MaxInt64 overflowed
// streamStart+Length to a negative end (fixed: overflow guard).
func TestCrasher_OverflowStreamLength(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Contents 4 0 R >>")
	b.Obj(4, "<< /Length 9223372036854775807 >>\nstream\nq Q\nendstream")
	openReproduces(t, "overflow-length", b.FinishClassic("<< /Size 5 /Root 1 0 R >>"))
}

// TestCrasher_DeepDictNesting: deeply nested dictionaries recursed
// parseDictionary (fixed: maxParseDepth, same guard as arrays).
func TestCrasher_DeepDictNesting(t *testing.T) {
	deep := strings.Repeat("<< /K ", 20000) + "0" + strings.Repeat(" >>", 20000)
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Junk "+deep+" >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>")
	openReproduces(t, "deep-dict", b.FinishClassic("<< /Size 4 /Root 1 0 R >>"))
}

// TestCrasher_CCITTColumnsAmplification: an absurd Columns drove a huge per-row
// allocation (fixed: Columns cap).
func TestCrasher_CCITTColumnsAmplification(t *testing.T) {
	p := pdf.CCITTParams{Columns: 2 << 20, Rows: 1}
	if _, err := pdf.DecodeCCITT([]byte{0x00, 0x01}, p); err == nil {
		t.Error("expected an error for an absurd CCITT Columns value")
	}
}

// TestBuildPageIndexBranches exercises BuildPageIndex directly (GetPageCount
// does not route through it) across every guard the fixes added: a non-dict
// graph, a missing Root, a non-dict Root, a cyclic page tree, a page tree
// deeper than the cap, and the normal Page-collecting walk.
func TestBuildPageIndexBranches(t *testing.T) {
	r, err := pdf.OpenBytes(pdfgen.Seeds()[0])
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer r.Close()

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}

	pagesDict := func(kids pdf.PDFArray, ref int) pdf.PDFDict {
		p := pdf.NewPDFDict()
		p.Entries["Type"] = pdf.PDFName{Value: "Pages"}
		p.Entries["Kids"] = kids
		p.Entries["_ref"] = pdf.PDFRef{ObjNum: ref}
		return p
	}
	graphWith := func(pages pdf.PDFValue) pdf.PDFDict {
		root := pdf.NewPDFDict()
		root.Entries["Pages"] = pages
		g := pdf.NewPDFDict()
		g.Entries["Root"] = root
		return g
	}

	// Non-dict graph.
	if _, err := r.BuildPageIndex(pdf.PDFInteger(0)); err == nil {
		t.Error("non-dict graph: want error")
	}
	// Missing Root.
	if _, err := r.BuildPageIndex(pdf.NewPDFDict()); err == nil {
		t.Error("missing Root: want error")
	}
	// Non-dict Root.
	badRoot := pdf.NewPDFDict()
	badRoot.Entries["Root"] = pdf.PDFHexString{Value: "abc"}
	if _, err := r.BuildPageIndex(badRoot); err == nil {
		t.Error("non-dict Root: want error")
	}
	// Happy path: one real Page is indexed; a non-dict Kids entry is skipped.
	idx, err := r.BuildPageIndex(graphWith(pagesDict(pdf.PDFArray{page, pdf.PDFInteger(0)}, 2)))
	if err != nil || idx[3] != 1 {
		t.Errorf("happy path: idx=%v err=%v; want page 3 -> 1", idx, err)
	}
	// Cyclic page tree: Pages whose Kids contains itself must terminate.
	cyc := pagesDict(nil, 2)
	cyc.Entries["Kids"] = pdf.PDFArray{cyc}
	if _, err := r.BuildPageIndex(graphWith(cyc)); err != nil {
		t.Errorf("cyclic page tree: unexpected error %v", err)
	}
	// Over-deep page tree must be rejected, not overflow the stack.
	deep := pdf.PDFValue(page)
	for i := 0; i < (1<<16)+8; i++ {
		deep = pagesDict(pdf.PDFArray{deep}, 1000+i)
	}
	if _, err := r.BuildPageIndex(graphWith(deep)); err == nil {
		t.Error("over-deep page tree: want error")
	}
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

// TestParseFunctionRejectsMalformed exercises the shape-validation branches
// added to newSampledFunction (Type 0) and newStitchingFunction (Type 3) so a
// malformed function is rejected at construction rather than panicking Eval.
func TestParseFunctionRejectsMalformed(t *testing.T) {
	unit := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}

	sampled := func(entries map[string]pdf.PDFValue) pdf.PDFValue {
		d := pdf.NewPDFDict()
		d.HasStream = true
		d.RawStream = []byte{0, 0, 0, 0, 0, 0, 0, 0}
		d.Entries["FunctionType"] = pdf.PDFInteger(0)
		d.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFInteger(1)}
		d.Entries["Range"] = unit
		d.Entries["Size"] = pdf.PDFArray{pdf.PDFInteger(2), pdf.PDFInteger(2)}
		d.Entries["BitsPerSample"] = pdf.PDFInteger(8)
		for k, v := range entries {
			d.Entries[k] = v
		}
		return d
	}
	stitching := func(entries map[string]pdf.PDFValue) pdf.PDFValue {
		sub := pdf.NewPDFDict()
		sub.Entries["FunctionType"] = pdf.PDFInteger(2)
		sub.Entries["Domain"] = unit
		sub.Entries["N"] = pdf.PDFInteger(1)
		d := pdf.NewPDFDict()
		d.Entries["FunctionType"] = pdf.PDFInteger(3)
		d.Entries["Domain"] = unit
		d.Entries["Functions"] = pdf.PDFArray{sub, sub}
		d.Entries["Bounds"] = pdf.PDFArray{pdf.PDFReal(0.5)}
		d.Entries["Encode"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFInteger(1)}
		for k, v := range entries {
			d.Entries[k] = v
		}
		return d
	}

	cases := []struct {
		name string
		fn   pdf.PDFValue
	}{
		{"sampled-empty-size", sampled(map[string]pdf.PDFValue{"Size": pdf.PDFArray{}})},
		{"sampled-zero-size", sampled(map[string]pdf.PDFValue{"Size": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(2)}})},
		{"sampled-empty-range", sampled(map[string]pdf.PDFValue{"Range": pdf.PDFArray{}})},
		{"sampled-short-domain", sampled(map[string]pdf.PDFValue{"Domain": unit})},
		{"sampled-short-decode", sampled(map[string]pdf.PDFValue{"Decode": pdf.PDFArray{pdf.PDFInteger(0)}})},
		{"stitch-empty-functions", stitching(map[string]pdf.PDFValue{"Functions": pdf.PDFArray{}})},
		{"stitch-short-domain", stitching(map[string]pdf.PDFValue{"Domain": pdf.PDFArray{pdf.PDFInteger(0)}})},
		{"stitch-short-bounds", stitching(map[string]pdf.PDFValue{"Bounds": pdf.PDFArray{}})},
		{"stitch-short-encode", stitching(map[string]pdf.PDFValue{"Encode": pdf.PDFArray{}})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := pdf.ParseFunction(c.fn); err == nil {
				t.Errorf("ParseFunction(%s) = nil error, want rejection", c.name)
			}
		})
	}
}

// TestCrasher_FlateBomb: a highly compressible stream inflated without bound
// (fixed: maxInflateOutput cap).
func TestCrasher_FlateBomb(t *testing.T) {
	restore := pdf.SetMaxInflateOutput(1024)
	defer restore()

	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(make([]byte, 1<<16)); err != nil { // 64 KiB -> tiny stream
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}

	out, err := pdf.InflateZlib(compressed.Bytes())
	if err != nil {
		t.Fatalf("InflateZlib: %v", err)
	}
	if int64(len(out)) > 1024 {
		t.Errorf("decoded %d bytes, want <= 1024", len(out))
	}
}

// TestCrasher_PNGPredictorColumns: an absurd /Columns drove a huge per-row
// allocation (fixed: maxPredictorColumns).
func TestCrasher_PNGPredictorColumns(t *testing.T) {
	if _, err := pdf.UndoPNGPredictor([]byte{0x00, 0x01, 0x02}, 1<<30, 1, 8); err == nil {
		t.Error("expected an error for an absurd predictor Columns value")
	}
}

// TestCrasher_NegativeBitsPerSample: a negative /BitsPerSample panicked Eval on
// `1 << bps` (fixed: validate 1 <= bps <= 32).
func TestCrasher_NegativeBitsPerSample(t *testing.T) {
	d := pdf.NewPDFDict()
	d.HasStream = true
	d.RawStream = []byte{0, 0, 0, 0}
	d.Entries["FunctionType"] = pdf.PDFInteger(0)
	d.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
	d.Entries["Range"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
	d.Entries["Size"] = pdf.PDFArray{pdf.PDFInteger(2)}
	d.Entries["BitsPerSample"] = pdf.PDFInteger(-1)
	d.Entries["Length"] = pdf.PDFInteger(4)
	if _, err := pdf.ParseFunction(d); err == nil {
		t.Error("expected an error for a negative BitsPerSample")
	}
}

// TestCrasher_XRefFieldWidthOverflow: an out-of-range /W entry overflowed
// entryLen into a panicking slice (fixed: per-width cap).
func TestCrasher_XRefFieldWidthOverflow(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries["W"] = pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(9), pdf.PDFInteger(1)}
	if _, err := pdf.XRefFieldWidths(d); err == nil {
		t.Error("expected an error for an out-of-range /W field width")
	}
}

// TestCrasher_ObjStmHugeN: an object stream /N far larger than its data drove a
// huge pre-allocation (fixed: bound N against the stream length).
func TestCrasher_ObjStmHugeN(t *testing.T) {
	r, err := pdf.OpenBytes(pdfgen.Seeds()[0])
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer r.Close()

	objstm := pdf.NewPDFDict()
	objstm.HasStream = true
	objstm.RawStream = []byte("1 0 ")
	objstm.Entries["Type"] = pdf.PDFName{Value: "ObjStm"}
	objstm.Entries["N"] = pdf.PDFInteger(100_000_000)
	objstm.Entries["First"] = pdf.PDFInteger(0)
	r.SeedResolvedGraph(pdf.NewPDFDict(), map[int]pdf.PDFValue{6: objstm})

	// Assert the /N-capacity error specifically (not the later "malformed
	// header" one that fires after the bad allocation).
	err = pdf.DecodeObjStmForTest(r, 6)
	if err == nil || !strings.Contains(err.Error(), "exceeds stream capacity") {
		t.Errorf("want an /N-capacity error, got %v", err)
	}
}

// TestCrasher_CyclicStreamLength: a stream whose /Length references its own
// object re-entered ResolveReference forever (fixed: resolvingInProgress guard).
func TestCrasher_CyclicStreamLength(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Contents 4 0 R >>")
	b.Obj(4, "<< /Length 4 0 R >>\nstream\nq Q\nendstream")
	openReproduces(t, "cyclic-length", b.FinishClassic("<< /Size 5 /Root 1 0 R >>"))
}

// TestResolveDepthLimit: a long acyclic reference chain overflowed the stack
// when resolved (fixed: maxResolveDepth).
func TestResolveDepthLimit(t *testing.T) {
	restore := pdf.SetMaxResolveDepth(100)
	defer restore()

	const chain = 400
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Chain 4 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>")
	for i := 0; i < chain; i++ {
		b.Obj(4+i, fmt.Sprintf("<< /Next %d 0 R >>", 4+i+1))
	}
	b.Obj(4+chain, "<< /End true >>")

	data := b.FinishClassic(fmt.Sprintf("<< /Size %d /Root 1 0 R >>", 5+chain))
	r, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer r.Close()
	if _, err := r.ResolveGraph(); err == nil {
		t.Error("expected ResolveGraph to reject a chain deeper than the resolve-depth cap")
	}
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
