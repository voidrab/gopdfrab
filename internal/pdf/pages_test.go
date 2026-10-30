package pdf

import "testing"

// TestWalkPagesCapsDepth verifies that a page nested deeper than
// MaxPageTreeDepth is not visited, while a shallow sibling is visited and
// numbered 1, as Reader.BuildPageIndex does.
func TestWalkPagesCapsDepth(t *testing.T) {
	page := func() PDFDict {
		p := NewPDFDict()
		p.Entries.Set("Type", PDFName{Value: "Page"})
		return p
	}
	deep := page()
	for range MaxPageTreeDepth + 1 {
		n := NewPDFDict()
		n.Entries.Set("Type", PDFName{Value: "Pages"})
		n.Entries.Set("Kids", PDFArray{deep})
		deep = n
	}
	shallow := page()
	root := NewPDFDict()
	root.Entries.Set("Type", PDFName{Value: "Pages"})
	root.Entries.Set("Kids", PDFArray{deep, shallow})

	var visited []int
	WalkPages(root, func(p, _ PDFDict, _ [4]float64, n int) {
		if ValuePointer(p) != ValuePointer(shallow) {
			t.Errorf("visited a page below the depth cap (number %d)", n)
		}
		visited = append(visited, n)
	})
	if len(visited) != 1 || visited[0] != 1 {
		t.Fatalf("visited page numbers %v, want [1]", visited)
	}
}

// TestWalkPagesVisitsAnIndirectNodeOnce verifies that a page listed twice
// under one object number is visited, and numbered, once.
func TestWalkPagesVisitsAnIndirectNodeOnce(t *testing.T) {
	page := NewPDFDict()
	page.Entries.Set("Type", PDFName{Value: "Page"})
	page.SetRef(PDFRef{ObjNum: 7})
	root := NewPDFDict()
	root.Entries.Set("Kids", PDFArray{page, page})
	visits := 0
	WalkPages(root, func(_, _ PDFDict, _ [4]float64, n int) {
		visits++
		if n != 1 {
			t.Errorf("page numbered %d, want 1", n)
		}
	})
	if visits != 1 {
		t.Errorf("page visited %d times, want 1", visits)
	}
}

// TestPageResourcesStopsOnACycle verifies that PageResources returns no
// resources, rather than looping, when a page's /Parent refers back to the
// page itself.
func TestPageResourcesStopsOnACycle(t *testing.T) {
	page := NewPDFDict()
	page.Entries.Set("Parent", page)
	if got := PageResources(page); got.Entries != nil {
		t.Errorf("PageResources on a self-parented page = %v, want none", got)
	}
}
