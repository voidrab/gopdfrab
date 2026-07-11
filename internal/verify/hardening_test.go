package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// buildDeepGraph returns a resolved graph whose object tree nests `depth` dicts
// before reaching a dict carrying an over-8191-element array (which the graph
// walk flags as ArrayTooLarge only if it descends far enough to reach it). The
// deep chain hangs off the trailer beside a minimal but valid page tree so
// BuildPageIndex -- which runs before the walk -- succeeds.
func buildDeepGraph(depth int) pdf.PDFDict {
	big := make(pdf.PDFArray, 8192)
	for i := range big {
		big[i] = pdf.PDFInteger(0)
	}
	bottom := pdf.NewPDFDict()
	bottom.Entries["Big"] = big

	node := pdf.PDFValue(bottom)
	for i := 0; i < depth; i++ {
		d := pdf.NewPDFDict()
		d.Entries["K"] = node
		node = d
	}

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	pages.Entries["Kids"] = pdf.PDFArray{page}
	root := pdf.NewPDFDict()
	root.Entries["Pages"] = pages
	graph := pdf.NewPDFDict()
	graph.Entries["Root"] = root
	graph.Entries["DeepChain"] = node
	return graph
}

func verifyDeepGraph(t *testing.T, graph pdf.PDFDict) pdf.Result {
	t.Helper()
	r, err := pdf.OpenBytes(pdfgen.Seeds()[0])
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	r.SeedResolvedGraph(graph, map[int]pdf.PDFValue{})
	res, err := Verify(r, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return res
}

func resultHasCheck(res pdf.Result, c pdf.Check) bool {
	for _, iss := range res.Issues {
		if iss.Check() == c {
			return true
		}
	}
	return false
}

// TestWalkDepthLimit verifies the object-graph walk stops at maxWalkDepth rather
// than recursing one native frame per level into a stack overflow. With the cap
// below the buried array's depth the walk never reaches it (ArrayTooLarge
// absent); with the cap above it, it does (present) -- proving the guard gates
// traversal rather than being a no-op.
func TestWalkDepthLimit(t *testing.T) {
	const depth = 120
	graph := buildDeepGraph(depth)

	restoreLow := SetMaxWalkDepth(50)
	shallow := verifyDeepGraph(t, graph)
	restoreLow()
	if resultHasCheck(shallow, pdf.Checks.Structure.ArrayTooLarge) {
		t.Error("cap below the array's depth: walk reached past the cap")
	}

	restoreHigh := SetMaxWalkDepth(1 << 17)
	deep := verifyDeepGraph(t, graph)
	restoreHigh()
	if !resultHasCheck(deep, pdf.Checks.Structure.ArrayTooLarge) {
		t.Error("cap above the array's depth: walk should have reached the array")
	}
}
