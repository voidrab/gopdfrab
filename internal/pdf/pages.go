package pdf

// DefaultMediaBox is the PDF spec's fallback page size (US Letter) for a
// page that inherits no /MediaBox anywhere up its Pages-tree ancestry.
var DefaultMediaBox = [4]float64{0, 0, 612, 792}

// PageResources returns the resource dictionary in force for a page: its
// own /Resources, or else the nearest one up the /Parent chain. /Resources is
// an inheritable page attribute (ISO 32000-1 7.7.3.4), and a document that
// hangs one shared dict on the page tree root is common enough that reading
// only the page's own leaves every font and XObject it names invisible --
// including fonts that are shown, and so have to be embedded (6.3.4).
//
// The depth bound is for a /Parent chain that loops back on itself; a page
// tree deeper than this has bigger problems than its resources.
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

// WalkPages calls visit for every page under pages, top-down from
// Root/Pages/Kids, with the /Resources and /MediaBox it inherits and its
// 1-based page number.
//
// It guards the walk exactly as Reader.BuildPageIndex does: an indirect
// node is visited once and depth is capped. That keeps a cyclic /Kids (a
// Pages node listing itself or an ancestor) from recursing until the stack
// overflows -- a fatal error no recover can catch -- and keeps page numbers
// aligned with the verifier's, so they match PDFError.Page().
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
