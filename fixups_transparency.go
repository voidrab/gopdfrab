package pdfrab

import "image"

// transparencyFlattener remediates Checks.Transparency.TransparencyGroup and
// Checks.Transparency.ImageWithSoftMask: removing /Group or /SMask outright
// would silently change a page's rendered appearance, so the only faithful
// fix is to rasterize the affected page (RenderPage, raster.go) and replace
// it with a flat, opaque image -- no transparency construct survives that.
type transparencyFlattener struct{}

func init() {
	registerFixer(transparencyFlattener{})
}

func (transparencyFlattener) Applies(c Check) bool {
	switch c {
	case Checks.Transparency.TransparencyGroup, Checks.Transparency.ImageWithSoftMask:
		return true
	}
	return false
}

// flattenDPI is the resolution RenderPage rasterizes a flattened page at --
// high enough to stay legible, low enough to keep the replacement image's
// byte size reasonable.
const flattenDPI = 150

// defaultMediaBox is the PDF spec's fallback page size (US Letter) for a
// page that inherits no /MediaBox anywhere up its Pages-tree ancestry.
var defaultMediaBox = [4]float64{0, 0, 612, 792}

func (transparencyFlattener) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false
	for _, pc := range pagesNeedingFlattening(*trailer) {
		if flattenPageToImage(pc.page, pc.resources, pc.mediaBox) {
			changed = true
		}
	}
	return changed, nil
}

// flaggedPage bundles a page dict with its inherited /Resources and
// /MediaBox, resolved while descending the page tree (see
// pagesNeedingFlattening).
type flaggedPage struct {
	page      PDFDict
	resources PDFDict
	mediaBox  [4]float64
}

// pagesNeedingFlattening walks the page tree top-down from Root/Pages/Kids
// (never via /Parent, which forms an intentional cycle back up the tree --
// see document.go), tracking inherited /Resources and /MediaBox, and
// collects every Page whose own subtree contains a transparency group or a
// soft-masked image.
func pagesNeedingFlattening(trailer PDFDict) []flaggedPage {
	root, ok := trailer.Entries["Root"].(PDFDict)
	if !ok {
		return nil
	}
	pages, ok := root.Entries["Pages"].(PDFDict)
	if !ok {
		return nil
	}

	var out []flaggedPage
	var walk func(node PDFDict, resources PDFDict, mediaBox [4]float64)
	walk = func(node PDFDict, resources PDFDict, mediaBox [4]float64) {
		if r, ok := node.Entries["Resources"].(PDFDict); ok {
			resources = r
		}
		if mb, err := floatArray(node.Entries["MediaBox"]); err == nil && len(mb) == 4 {
			mediaBox = [4]float64{mb[0], mb[1], mb[2], mb[3]}
		}
		if (node.Entries["Type"] == PDFName{Value: "Page"}) {
			if pageHasFlattenableTransparency(node) {
				out = append(out, flaggedPage{page: node, resources: resources, mediaBox: mediaBox})
			}
			return
		}
		if kids, ok := node.Entries["Kids"].(PDFArray); ok {
			for _, kid := range kids {
				if kd, ok := kid.(PDFDict); ok {
					walk(kd, resources, mediaBox)
				}
			}
		}
	}
	walk(pages, PDFDict{}, defaultMediaBox)
	return out
}

// pageHasFlattenableTransparency reports whether page's own subtree (its
// dict and anything reachable from it, e.g. via nested Form XObjects)
// contains a /Group /S /Transparency dict or an Image XObject with a
// non-/None /SMask, mirroring validateTransparencyGroup/validateXObjectDict
// (checks_dict.go) exactly so this only flags what those checks would.
func pageHasFlattenableTransparency(page PDFDict) bool {
	found := false
	walkPageSubtree(page, map[uintptr]bool{}, func(d PDFDict) {
		if found {
			return
		}
		if group, ok := d.Entries["Group"].(PDFDict); ok {
			if (group.Entries["S"] == PDFName{Value: "Transparency"}) {
				found = true
				return
			}
		}
		if subtype, ok := d.Entries["Subtype"].(PDFName); ok && subtype.Value == "Image" {
			if sm, ok := d.Entries["SMask"]; ok {
				if name, isName := sm.(PDFName); !isName || name.Value != "None" {
					found = true
				}
			}
		}
	})
	return found
}

// walkPageSubtree is walkDicts restricted to a single page's own subtree: it
// additionally never follows /Parent, which would otherwise walk back up to
// the shared Pages node and across into every sibling page's subtree too,
// since /Parent is an intentional cycle in the resolved graph.
func walkPageSubtree(v PDFValue, visited map[uintptr]bool, fn func(PDFDict)) {
	switch val := v.(type) {
	case PDFDict:
		ptr := pdfValuePointer(val.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		fn(val)
		for k, child := range val.Entries {
			if k == "_ref" || k == "_dirty" || k == "Parent" {
				continue
			}
			walkPageSubtree(child, visited, fn)
		}
	case PDFArray:
		ptr := pdfValuePointer(val)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for _, child := range val {
			walkPageSubtree(child, visited, fn)
		}
	}
}

// flattenPageToImage rasterizes page (RenderPage) and rebuilds it in place
// as a single flat Image XObject painted by a fresh, minimal content
// stream, replacing /Resources and /Contents and dropping /Group and
// /Rotate (a flattened raster has no remaining rotation to apply). A render
// failure (e.g. an unresolvable graph or an unsupported image codec) leaves
// page untouched, reporting no change rather than erroring the whole
// Convert.
func flattenPageToImage(page PDFDict, resources PDFDict, mediaBox [4]float64) bool {
	canvas, err := RenderPage(page, resources, mediaBox, flattenDPI)
	if err != nil {
		return false
	}

	img := NewPDFDict()
	img.Entries["Type"] = PDFName{Value: "XObject"}
	img.Entries["Subtype"] = PDFName{Value: "Image"}
	img.Entries["Width"] = PDFInteger(canvas.Bounds().Dx())
	img.Entries["Height"] = PDFInteger(canvas.Bounds().Dy())
	img.Entries["BitsPerComponent"] = PDFInteger(8)
	img.Entries["ColorSpace"] = PDFName{Value: "DeviceRGB"}
	img.HasStream = true
	img.RawStream = packRGBSamples(canvas)
	MarkStreamDirty(&img)

	xobjects := NewPDFDict()
	xobjects.Entries["Im0"] = img
	pageResources := NewPDFDict()
	pageResources.Entries["XObject"] = xobjects

	w, h := mediaBox[2]-mediaBox[0], mediaBox[3]-mediaBox[1]
	ops := []contentOp{
		{Op: "q"},
		{Op: "cm", Operands: []PDFValue{
			PDFReal(w), PDFInteger(0), PDFInteger(0), PDFReal(h),
			PDFReal(mediaBox[0]), PDFReal(mediaBox[1]),
		}},
		{Op: "Do", Operands: []PDFValue{PDFName{Value: "Im0"}}},
		{Op: "Q"},
	}
	data, err := writeContentStream(ops)
	if err != nil {
		return false
	}
	contents := NewPDFDict()
	contents.HasStream = true
	contents.RawStream = data
	MarkStreamDirty(&contents)

	delete(page.Entries, "Group")
	delete(page.Entries, "Rotate")
	page.Entries["Resources"] = pageResources
	page.Entries["Contents"] = contents
	return true
}

// packRGBSamples packs canvas's pixels as tightly-packed 8-bit RGB triples
// (row-major, no padding needed since DeviceRGB/8bpc rows are always a
// whole number of bytes), the sample format Image XObject expects.
func packRGBSamples(canvas *image.RGBA) []byte {
	bounds := canvas.Bounds()
	out := make([]byte, 0, bounds.Dx()*bounds.Dy()*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			px := canvas.RGBAAt(x, y)
			out = append(out, px.R, px.G, px.B)
		}
	}
	return out
}
