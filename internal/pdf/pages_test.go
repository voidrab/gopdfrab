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

func geomDict(kv ...any) PDFDict {
	d := NewPDFDict()
	for i := 0; i < len(kv); i += 2 {
		d.Entries.Set(kv[i].(string), kv[i+1])
	}
	return d
}

func nums(v ...float64) PDFArray {
	a := PDFArray{}
	for _, f := range v {
		a = append(a, PDFReal(f))
	}
	return a
}

// TestGeometryInherits checks that the boxes and rotation come from the
// nearest ancestor that sets them, that CropBox is clamped to MediaBox, and
// that UserUnit is read from the page only.
func TestGeometryInherits(t *testing.T) {
	parent := geomDict(
		"MediaBox", nums(0, 0, 200, 100),
		"CropBox", nums(-10, 20, 150, 400),
		"Rotate", PDFInteger(-90),
		"UserUnit", PDFReal(5),
	)
	page := geomDict("Parent", parent, "UserUnit", PDFReal(2))
	g := Geometry(page)
	want := PageGeometry{
		MediaBox: [4]float64{0, 0, 200, 100},
		CropBox:  [4]float64{0, 20, 150, 100},
		Rotate:   270,
		UserUnit: 2,
	}
	if g != want {
		t.Fatalf("Geometry = %+v, want %+v", g, want)
	}
	if u := Geometry(geomDict("Parent", parent)).UserUnit; u != 1 {
		t.Errorf("UserUnit leaked from the parent: %v", u)
	}
}

// TestGeometryDefaults covers absent, malformed and degenerate entries.
func TestGeometryDefaults(t *testing.T) {
	g := Geometry(PDFDict{})
	if g.MediaBox != DefaultMediaBox || g.CropBox != DefaultMediaBox || g.Rotate != 0 || g.UserUnit != 1 {
		t.Errorf("empty page = %+v", g)
	}
	bad := geomDict(
		"MediaBox", nums(5, 5, 5, 9),
		"CropBox", nums(700, 800, 900, 900),
		"Rotate", PDFInteger(45),
		"UserUnit", PDFReal(-1),
	)
	g = Geometry(bad)
	if g.MediaBox != DefaultMediaBox || g.CropBox != DefaultMediaBox || g.Rotate != 0 || g.UserUnit != 1 {
		t.Errorf("malformed page = %+v", g)
	}
	// Reversed corners are normalized; a /Parent cycle terminates.
	a := geomDict("MediaBox", nums(100, 50, 0, 0))
	a.Entries.Set("Parent", a)
	if g := Geometry(a); g.MediaBox != [4]float64{0, 0, 100, 50} {
		t.Errorf("reversed box = %v", g.MediaBox)
	}
}
