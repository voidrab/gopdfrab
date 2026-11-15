package pdf

// DefaultMediaBox is the PDF spec's fallback page size (US Letter) for a
// page that inherits no /MediaBox anywhere up its Pages-tree ancestry.
var DefaultMediaBox = [4]float64{0, 0, 612, 792}

// PageResources returns the resource dictionary in force for page: its own
// /Resources, or else the nearest one up the /Parent chain. /Resources is an
// inheritable page attribute, as ISO 32000-1 7.7.3.4 specifies. Reading only
// the page's own dictionary would hide the fonts and XObjects that a page
// tree root supplies, including fonts that must be embedded (PDF/A-1 6.3.4).
//
// The depth bound stops a /Parent chain that loops back on itself.
func PageResources(page PDFDict) PDFDict {
	node := page
	for depth := 0; node.Entries != nil && depth < 64; depth++ {
		if res, ok := node.Entries.Get("Resources").(PDFDict); ok {
			return res
		}
		parent, ok := node.Entries.Get("Parent").(PDFDict)
		if !ok {
			break
		}
		node = parent
	}
	return PDFDict{}
}

// WalkPages calls visit for every page under pages, in document order, with
// the /Resources and /MediaBox it inherits and its 1-based page number.
//
// It applies the same guards as Reader.BuildPageIndex: an indirect node is
// visited once, and depth is capped. These stop a cyclic /Kids entry, such as
// a Pages node listing itself or an ancestor, from overflowing the stack, which
// is a fatal error. The page numbers therefore agree with those that
// PDFError.Page reports.
func WalkPages(pages PDFDict, visit func(page, resources PDFDict, mediaBox [4]float64, pageNum int)) {
	seen := map[int]bool{}
	pageNum := 0
	var walk func(node, resources PDFDict, mediaBox [4]float64, depth int)
	walk = func(node, resources PDFDict, mediaBox [4]float64, depth int) {
		if depth > MaxPageTreeDepth {
			return
		}
		if ref, ok := node.Ref(); ok {
			if seen[ref.ObjNum] {
				return
			}
			seen[ref.ObjNum] = true
		}
		if r, ok := node.Entries.Get("Resources").(PDFDict); ok {
			resources = r
		}
		if mb, err := FloatArray(node.Entries.Get("MediaBox")); err == nil && len(mb) == 4 {
			mediaBox = [4]float64{mb[0], mb[1], mb[2], mb[3]}
		}
		if (node.Entries.Get("Type") == PDFName{Value: "Page"}) {
			pageNum++
			visit(node, resources, mediaBox, pageNum)
			return
		}
		if kids, ok := node.Entries.Get("Kids").(PDFArray); ok {
			for _, kid := range kids {
				if kd, ok := kid.(PDFDict); ok {
					walk(kd, resources, mediaBox, depth+1)
				}
			}
		}
	}
	walk(pages, PDFDict{}, DefaultMediaBox, 0)
}

// PageGeometry is the inheritable page boundary state of a page: the boxes
// in default user space, the clockwise display rotation and the user unit.
type PageGeometry struct {
	// MediaBox is the page's physical size, DefaultMediaBox when none is set.
	MediaBox [4]float64
	// CropBox is the visible region, clamped to MediaBox; it is MediaBox
	// when absent or degenerate.
	CropBox [4]float64
	// Rotate is the clockwise rotation in degrees: 0, 90, 180 or 270.
	Rotate int
	// UserUnit is the size of a user space unit in 1/72 inch, at least 1e-6.
	UserUnit float64
}

// Geometry resolves page's /MediaBox, /CropBox and /Rotate, which inherit up
// the /Parent chain, and its /UserUnit, which does not (ISO 32000-1 7.7.3.3,
// ISO 32000-2 table 31). Malformed values fall back to the defaults.
func Geometry(page PDFDict) PageGeometry {
	g := PageGeometry{MediaBox: DefaultMediaBox, UserUnit: 1}
	var haveMedia, haveCrop, haveRotate bool
	var crop [4]float64
	node := page
	for depth := 0; node.Entries != nil && depth < 64; depth++ {
		if !haveMedia {
			if b, ok := box(node.Entries.Get("MediaBox")); ok {
				g.MediaBox, haveMedia = b, true
			}
		}
		if !haveCrop {
			if b, ok := box(node.Entries.Get("CropBox")); ok {
				crop, haveCrop = b, true
			}
		}
		if !haveRotate {
			if n, ok := node.Entries.Get("Rotate").(PDFInteger); ok {
				g.Rotate, haveRotate = ((int(n)/90)*90%360+360)%360, true
			}
		}
		parent, ok := node.Entries.Get("Parent").(PDFDict)
		if !ok {
			break
		}
		node = parent
	}
	if u, ok := PDFNumberToFloat(page.Entries.Get("UserUnit")); ok && u > 0 {
		g.UserUnit = u
	}
	g.CropBox = g.MediaBox
	if haveCrop {
		c := [4]float64{
			max(crop[0], g.MediaBox[0]), max(crop[1], g.MediaBox[1]),
			min(crop[2], g.MediaBox[2]), min(crop[3], g.MediaBox[3]),
		}
		if c[2] > c[0] && c[3] > c[1] {
			g.CropBox = c
		}
	}
	return g
}

// box reads a normalized, non-degenerate rectangle.
func box(v PDFValue) ([4]float64, bool) {
	f, err := FloatArray(v)
	if err != nil || len(f) != 4 {
		return [4]float64{}, false
	}
	b := [4]float64{min(f[0], f[2]), min(f[1], f[3]), max(f[0], f[2]), max(f[1], f[3])}
	return b, b[2] > b[0] && b[3] > b[1]
}
