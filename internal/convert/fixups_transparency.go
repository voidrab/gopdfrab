package convert

import (
	"image"

	"github.com/voidrab/gopdfrab/internal/check"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// transparencyFlattener remediates check.Checks.Transparency.TransparencyGroup and
// check.Checks.Transparency.ImageWithSoftMask by rasterizing only the smallest
// self-contained object carrying the violation -- a Form XObject's own
// content for a transparency group, or a single Image XObject's samples for
// a soft mask -- never the whole page, since neither construct can simply be
// dropped without changing a page's rendered appearance. Whole-page
// rasterization is used only as a last resort, when /Group sits directly on
// the Page dict and no narrower object exists to target instead.
type transparencyFlattener struct{}

func init() {
	registerFixer(transparencyFlattener{})
}

func (transparencyFlattener) Applies(c check.Check) bool {
	switch c {
	case check.Checks.Transparency.TransparencyGroup, check.Checks.Transparency.ImageWithSoftMask:
		return true
	}
	return false
}

// flattenDPI is the resolution a flattened Form/page is rasterized at --
// high enough to stay legible, low enough to keep the replacement image's
// byte size reasonable.
const flattenDPI = 150

// defaultMediaBox is the PDF spec's fallback page size (US Letter) for a
// page that inherits no /MediaBox anywhere up its Pages-tree ancestry.
var defaultMediaBox = [4]float64{0, 0, 612, 792}

func (transparencyFlattener) Fix(trailer *pdf.PDFDict, _ []check.PDFError) (bool, error) {
	changed := false
	for _, t := range collectTransparencyTargets(*trailer) {
		switch t.kind {
		case "image":
			if fixed, ok := bakeSoftMaskOut(t.dict, t.resources); ok {
				t.xobjects.Entries[t.name] = fixed
				changed = true
			}
		case "form":
			if fixed, ok := flattenFormToImage(t.dict, t.resources); ok {
				t.xobjects.Entries[t.name] = fixed
				changed = true
			}
		case "page":
			if flattenPageToImage(t.dict, t.resources, t.mediaBox) {
				changed = true
			}
		}
	}
	return changed, nil
}

// flaggedTarget is one object collectTransparencyTargets found needing a
// fix. For "image"/"form", xobjects+name address the resource-dictionary
// slot the fixed dict must be written back into (pdf.PDFDict.RawStream/HasStream
// changes don't propagate through a value-type copy the way Entries-map
// mutations do). For "page", mediaBox is the page's own resolved /MediaBox.
type flaggedTarget struct {
	kind      string // "image", "form", or "page"
	dict      pdf.PDFDict
	resources pdf.PDFDict
	xobjects  pdf.PDFDict
	name      string
	mediaBox  [4]float64
}

// collectTransparencyTargets walks the page tree top-down from
// Root/Pages/Kids (never via /Parent, an intentional cycle back up the tree
// -- see document.go), tracking inherited /Resources and /MediaBox, and for
// each page either flags the page itself (its own /Group, the rare
// no-narrower-object case) or descends into its resource graph to flag the
// individual Form/Image XObjects responsible.
func collectTransparencyTargets(trailer pdf.PDFDict) []flaggedTarget {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return nil
	}
	pages, ok := root.Entries["Pages"].(pdf.PDFDict)
	if !ok {
		return nil
	}

	var out []flaggedTarget
	visited := map[uintptr]bool{}
	var walk func(node pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64)
	walk = func(node pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64) {
		if r, ok := node.Entries["Resources"].(pdf.PDFDict); ok {
			resources = r
		}
		if mb, err := pdf.FloatArray(node.Entries["MediaBox"]); err == nil && len(mb) == 4 {
			mediaBox = [4]float64{mb[0], mb[1], mb[2], mb[3]}
		}
		if (node.Entries["Type"] == pdf.PDFName{Value: "Page"}) {
			if hasTransparencyGroup(node) {
				out = append(out, flaggedTarget{kind: "page", dict: node, resources: resources, mediaBox: mediaBox})
				return
			}
			collectXObjectTargets(resources, visited, &out)
			return
		}
		if kids, ok := node.Entries["Kids"].(pdf.PDFArray); ok {
			for _, kid := range kids {
				if kd, ok := kid.(pdf.PDFDict); ok {
					walk(kd, resources, mediaBox)
				}
			}
		}
	}
	walk(pages, pdf.PDFDict{}, defaultMediaBox)
	return out
}

// pageTarget addresses a page dict in the graph together with the /Resources
// and /MediaBox in effect for it (resolved up the Pages tree), in page order.
type pageTarget struct {
	dict      pdf.PDFDict
	resources pdf.PDFDict
	mediaBox  [4]float64
}

// orderedPages returns every page in the document in page order, with its
// inherited resources and resolved media box, using the same top-down
// Root/Pages/Kids walk the verifier numbers pages by -- so the slice index
// matches a check.PDFError's 1-based Page().
func orderedPages(trailer pdf.PDFDict) []pageTarget {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return nil
	}
	pages, ok := root.Entries["Pages"].(pdf.PDFDict)
	if !ok {
		return nil
	}

	var out []pageTarget
	var walk func(node, resources pdf.PDFDict, mediaBox [4]float64)
	walk = func(node, resources pdf.PDFDict, mediaBox [4]float64) {
		if r, ok := node.Entries["Resources"].(pdf.PDFDict); ok {
			resources = r
		}
		if mb, err := pdf.FloatArray(node.Entries["MediaBox"]); err == nil && len(mb) == 4 {
			mediaBox = [4]float64{mb[0], mb[1], mb[2], mb[3]}
		}
		if (node.Entries["Type"] == pdf.PDFName{Value: "Page"}) {
			out = append(out, pageTarget{dict: node, resources: resources, mediaBox: mediaBox})
			return
		}
		if kids, ok := node.Entries["Kids"].(pdf.PDFArray); ok {
			for _, kid := range kids {
				if kd, ok := kid.(pdf.PDFDict); ok {
					walk(kd, resources, mediaBox)
				}
			}
		}
	}
	walk(pages, pdf.PDFDict{}, defaultMediaBox)
	return out
}

// collectXObjectTargets scans resources' /XObject subdictionary, flagging
// Form XObjects carrying their own /Group and Image XObjects carrying a
// non-/None /SMask, recursing into nested Forms via the same /Resources
// fallback doXObject uses (raster.go). A flagged Form is not descended into
// further: it's about to be wholly replaced, so anything inside it is moot.
// visited guards against cyclic/shared XObject subdictionaries.
func collectXObjectTargets(resources pdf.PDFDict, visited map[uintptr]bool, out *[]flaggedTarget) {
	xobjects, ok := resources.Entries["XObject"].(pdf.PDFDict)
	if !ok {
		return
	}
	ptr := pdf.ValuePointer(xobjects.Entries)
	if visited[ptr] {
		return
	}
	visited[ptr] = true

	for name, v := range xobjects.Entries {
		xobj, ok := v.(pdf.PDFDict)
		if !ok {
			continue
		}
		subtype, _ := xobj.Entries["Subtype"].(pdf.PDFName)
		switch subtype.Value {
		case "Form":
			if hasTransparencyGroup(xobj) {
				*out = append(*out, flaggedTarget{kind: "form", dict: xobj, resources: resources, xobjects: xobjects, name: name})
				continue
			}
			formRes, _ := xobj.Entries["Resources"].(pdf.PDFDict)
			if formRes.Entries == nil {
				formRes = resources
			}
			collectXObjectTargets(formRes, visited, out)
		case "Image":
			if hasSoftMask(xobj) {
				*out = append(*out, flaggedTarget{kind: "image", dict: xobj, resources: resources, xobjects: xobjects, name: name})
			}
		}
	}
}

// hasTransparencyGroup mirrors validateTransparencyGroup's (checks_dict.go)
// /Group /S /Transparency test.
func hasTransparencyGroup(d pdf.PDFDict) bool {
	group, ok := d.Entries["Group"].(pdf.PDFDict)
	if !ok {
		return false
	}
	return group.Entries["S"] == pdf.PDFName{Value: "Transparency"}
}

// hasSoftMask mirrors validateXObjectDict's (checks_dict.go) ImageWithSoftMask
// test: an /SMask entry present and not the literal name /None.
func hasSoftMask(img pdf.PDFDict) bool {
	sm, ok := img.Entries["SMask"]
	if !ok {
		return false
	}
	name, isName := sm.(pdf.PDFName)
	return !isName || name.Value != "None"
}

// bakeSoftMaskOut decodes img's base samples and its /SMask's luminosity
// (DecodeImageRGBA for each), composites the two against an opaque white
// backdrop -- gopdfrab has no way to know what the image was meant to be
// composited over without rendering everything beneath it -- and rewrites
// img in place as a flat, opaque DeviceRGB image with /SMask removed.
// Leaves img untouched (ok=false) if either decode fails.
func bakeSoftMaskOut(img pdf.PDFDict, resources pdf.PDFDict) (pdf.PDFDict, bool) {
	base, err := DecodeImageRGBA(img, resources)
	if err != nil {
		return img, false
	}
	smaskDict, ok := img.Entries["SMask"].(pdf.PDFDict)
	if !ok {
		return img, false
	}
	smask, err := DecodeImageRGBA(smaskDict, resources)
	if err != nil {
		return img, false
	}

	w, h := base.Bounds().Dx(), base.Bounds().Dy()
	smW, smH := smask.Bounds().Dx(), smask.Bounds().Dy()
	if w == 0 || h == 0 || smW == 0 || smH == 0 {
		return img, false
	}

	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px := base.RGBAAt(x, y)
			scol := pdf.ClampInt(x*smW/w, 0, smW-1)
			srow := pdf.ClampInt(y*smH/h, 0, smH-1)
			alpha := float64(smask.RGBAAt(scol, srow).R) / 255

			r := float64(px.R)/255*alpha + (1 - alpha)
			g := float64(px.G)/255*alpha + (1 - alpha)
			b := float64(px.B)/255*alpha + (1 - alpha)
			out.Set(x, y, colorRGBA64{r, g, b, 1})
		}
	}

	img.Entries["Width"] = pdf.PDFInteger(w)
	img.Entries["Height"] = pdf.PDFInteger(h)
	img.Entries["BitsPerComponent"] = pdf.PDFInteger(8)
	img.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceRGB"}
	delete(img.Entries, "SMask")
	delete(img.Entries, "Decode")
	delete(img.Entries, "Mask")
	img.HasStream = true
	img.RawStream = packRGBSamples(out)
	writer.MarkStreamDirty(&img)
	return img, true
}

// flattenFormToImage rasterizes a Form XObject's own /BBox + content in
// isolation (renderFormContent, raster.go) and rewrites the Form in place to
// paint a single fresh Image XObject filling that same BBox, dropping
// /Group. The Form's own identity, /Matrix and every existing /Do reference
// to it are untouched, so it keeps composing into the page exactly as
// before -- it now just paints a flat image instead of a transparency group.
// A render failure leaves the Form untouched (ok=false).
func flattenFormToImage(form pdf.PDFDict, resources pdf.PDFDict) (pdf.PDFDict, bool) {
	canvas, bbox, err := renderFormContent(form, resources, flattenDPI)
	if err != nil {
		return form, false
	}

	img := pdf.NewPDFDict()
	img.Entries["Type"] = pdf.PDFName{Value: "XObject"}
	img.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	img.Entries["Width"] = pdf.PDFInteger(canvas.Bounds().Dx())
	img.Entries["Height"] = pdf.PDFInteger(canvas.Bounds().Dy())
	img.Entries["BitsPerComponent"] = pdf.PDFInteger(8)
	img.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceRGB"}
	img.HasStream = true
	img.RawStream = packRGBSamples(canvas)
	writer.MarkStreamDirty(&img)

	xobjects := pdf.NewPDFDict()
	xobjects.Entries["Im0"] = img
	formResources := pdf.NewPDFDict()
	formResources.Entries["XObject"] = xobjects

	w, h := bbox[2]-bbox[0], bbox[3]-bbox[1]
	ops := []writer.ContentOp{
		{Op: "q"},
		{Op: "cm", Operands: []pdf.PDFValue{
			pdf.PDFReal(w), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFReal(h),
			pdf.PDFReal(bbox[0]), pdf.PDFReal(bbox[1]),
		}},
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "Im0"}}},
		{Op: "Q"},
	}
	data, err := writer.WriteContentStream(ops)
	if err != nil {
		return form, false
	}

	delete(form.Entries, "Group")
	form.Entries["Resources"] = formResources
	form.HasStream = true
	form.RawStream = data
	writer.MarkStreamDirty(&form)
	return form, true
}

// flattenPageToImage rasterizes page (RenderPage) and rebuilds it in place
// as a single flat Image XObject painted by a fresh, minimal content
// stream, replacing /Resources and /Contents and dropping /Group and
// /Rotate (a flattened raster has no remaining rotation to apply). Used only
// when /Group sits directly on the Page dict itself, with no narrower Form
// XObject to target instead. A render failure (e.g. an unresolvable graph or
// an unsupported image codec) leaves page untouched, reporting no change
// rather than erroring the whole Convert.
func flattenPageToImage(page pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64) bool {
	canvas, err := RenderPage(page, resources, mediaBox, flattenDPI)
	if err != nil {
		return false
	}

	img := pdf.NewPDFDict()
	img.Entries["Type"] = pdf.PDFName{Value: "XObject"}
	img.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	img.Entries["Width"] = pdf.PDFInteger(canvas.Bounds().Dx())
	img.Entries["Height"] = pdf.PDFInteger(canvas.Bounds().Dy())
	img.Entries["BitsPerComponent"] = pdf.PDFInteger(8)
	img.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceRGB"}
	img.HasStream = true
	img.RawStream = packRGBSamples(canvas)
	writer.MarkStreamDirty(&img)

	xobjects := pdf.NewPDFDict()
	xobjects.Entries["Im0"] = img
	pageResources := pdf.NewPDFDict()
	pageResources.Entries["XObject"] = xobjects

	w, h := mediaBox[2]-mediaBox[0], mediaBox[3]-mediaBox[1]
	ops := []writer.ContentOp{
		{Op: "q"},
		{Op: "cm", Operands: []pdf.PDFValue{
			pdf.PDFReal(w), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFReal(h),
			pdf.PDFReal(mediaBox[0]), pdf.PDFReal(mediaBox[1]),
		}},
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "Im0"}}},
		{Op: "Q"},
	}
	data, err := writer.WriteContentStream(ops)
	if err != nil {
		return false
	}
	contents := pdf.NewPDFDict()
	contents.HasStream = true
	contents.RawStream = data
	writer.MarkStreamDirty(&contents)

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
