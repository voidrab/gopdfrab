package convert

import (
	"image"
	"runtime"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// transparencyFlattener remediates Checks.Transparency.TransparencyGroup and
// Checks.Transparency.ImageWithSoftMask by rasterizing only the smallest
// self-contained object carrying the violation -- a Form XObject's own
// content for a transparency group, or a single Image XObject's samples for
// a soft mask -- never the whole page.
type transparencyFlattener struct{}

func init() {
	registerFixer(transparencyFlattener{})
}

func (transparencyFlattener) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Transparency.TransparencyGroup, pdf.Checks.Transparency.ImageWithSoftMask:
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

func (transparencyFlattener) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	targets := collectTransparencyTargets(*trailer)

	unique := uniqueByDict(targets)
	type result struct {
		fixed pdf.PDFDict
		ok    bool
	}
	results := make([]result, len(unique))
	workers := min(runtime.NumCPU(), len(unique))
	if workers < 1 {
		return false, nil
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				t := unique[i]
				switch t.kind {
				case "image":
					fixed, ok := bakeSoftMaskOut(t.dict, t.resources)
					results[i] = result{fixed, ok}
				case "form":
					fixed, ok := flattenFormToImage(t.dict, t.resources)
					results[i] = result{fixed, ok}
				case "page":
					_, had := t.dict.Entries["Group"]
					delete(t.dict.Entries, "Group")
					results[i] = result{pdf.PDFDict{}, had}
				}
			}
		}()
	}
	for i := range unique {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	byDict := make(map[uintptr]result, len(unique))
	for i, t := range unique {
		byDict[pdf.ValuePointer(t.dict.Entries)] = results[i]
	}
	changed := false
	for _, t := range targets {
		r := byDict[pdf.ValuePointer(t.dict.Entries)]
		if !r.ok {
			continue
		}
		changed = true
		if t.kind != "page" {
			t.xobjects.Entries[t.name] = r.fixed
		}
	}
	return changed, nil
}

// uniqueByDict drops targets addressing the same underlying object so each is
// computed only once; the caller writes the shared result back to every alias.
func uniqueByDict(targets []flaggedTarget) []flaggedTarget {
	seen := map[uintptr]bool{}
	var out []flaggedTarget
	for _, t := range targets {
		ptr := pdf.ValuePointer(t.dict.Entries)
		if seen[ptr] {
			continue
		}
		seen[ptr] = true
		out = append(out, t)
	}
	return out
}

// flaggedTarget is one object collectTransparencyTargets found needing a
// fix. For "image"/"form", xobjects+name address the resource-dictionary
// slot the fixed dict must be written back into (pdf.PDFDict.RawStream/HasStream
// changes don't propagate through a value-type copy the way Entries-map
// mutations do).
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
// each page either flags the page itself (its own inert /Group, to be
// deleted) or descends into its resource graph to flag the individual
// Form/Image XObjects responsible.
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
// matches a PDFError's 1-based Page().
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

	// Composite at the higher of the two resolutions -- a base this small
	// relative to its mask (e.g. a 2x2 colour tile under a full-res mask
	// shape) would otherwise collapse the mask's shape into a solid block.
	outW, outH := max(w, smW), max(h, smH)
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))

	// bx/sx depend only on x, not y: precompute once instead of on every row.
	bxTab := make([]int, outW)
	sxTab := make([]int, outW)
	for x := 0; x < outW; x++ {
		bxTab[x] = pdf.ClampInt(x*w/outW, 0, w-1) * 4
		sxTab[x] = pdf.ClampInt(x*smW/outW, 0, smW-1) * 4
	}

	for y := 0; y < outH; y++ {
		by := pdf.ClampInt(y*h/outH, 0, h-1)
		sy := pdf.ClampInt(y*smH/outH, 0, smH-1)
		bp := base.PixOffset(0, by)
		sp := smask.PixOffset(0, sy)
		op := out.PixOffset(0, y)
		for x := 0; x < outW; x++ {
			a := uint32(smask.Pix[sp+sxTab[x]])
			bo := bp + bxTab[x]

			out.Pix[op] = uint8((uint32(base.Pix[bo])*a + 255*(255-a)) / 255)
			out.Pix[op+1] = uint8((uint32(base.Pix[bo+1])*a + 255*(255-a)) / 255)
			out.Pix[op+2] = uint8((uint32(base.Pix[bo+2])*a + 255*(255-a)) / 255)
			out.Pix[op+3] = 255
			op += 4
		}
	}

	img.Entries["Width"] = pdf.PDFInteger(outW)
	img.Entries["Height"] = pdf.PDFInteger(outH)
	img.Entries["BitsPerComponent"] = pdf.PDFInteger(8)
	img.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceRGB"}
	delete(img.Entries, "SMask")
	delete(img.Entries, "Decode")
	delete(img.Entries, "Mask")
	if err := setStreamRGBFlate(&img, out); err != nil {
		return img, false
	}
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
	if err := setStreamRGBFlate(&img, canvas); err != nil {
		return form, false
	}

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
	if err := writer.SetStreamFlate(&form, data); err != nil {
		return form, false
	}
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
	if err := setStreamRGBFlate(&img, canvas); err != nil {
		return false
	}

	xobjects := pdf.NewPDFDict()
	xobjects.Entries["Im0"] = img
	csDict := pdf.NewPDFDict()
	csDict.Entries["DefaultRGB"] = iccBasedColourSpace(3, srgbICCProfile)
	pageResources := pdf.NewPDFDict()
	pageResources.Entries["XObject"] = xobjects
	pageResources.Entries["ColorSpace"] = csDict

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
	if err := writer.SetStreamFlate(&contents, data); err != nil {
		return false
	}

	delete(page.Entries, "Group")
	delete(page.Entries, "Rotate")
	page.Entries["Resources"] = pageResources
	page.Entries["Contents"] = contents
	return true
}

// setStreamRGBFlate stores canvas as a FlateDecode DeviceRGB stream, packing
// each row's RGB triples on the fly so no whole-raster RGB buffer is allocated
// (one reused row buffer per call instead). The same tight 8-bit row-major
// packing packRGBSamples produces.
func setStreamRGBFlate(d *pdf.PDFDict, canvas *image.RGBA) error {
	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	rowRGB := make([]byte, w*3)
	return writer.SetStreamFlateRows(d, h, func(i int) []byte {
		src := canvas.Pix[canvas.PixOffset(bounds.Min.X, bounds.Min.Y+i):]
		j := 0
		for x := 0; x < w*4; x += 4 {
			rowRGB[j], rowRGB[j+1], rowRGB[j+2] = src[x], src[x+1], src[x+2]
			j += 3
		}
		return rowRGB
	})
}

// packRGBSamples packs canvas's pixels as tightly-packed 8-bit RGB triples
// (row-major, no padding needed since DeviceRGB/8bpc rows are always a
// whole number of bytes), the sample format Image XObject expects.
func packRGBSamples(canvas *image.RGBA) []byte {
	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	out := make([]byte, w*h*3)
	o := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := canvas.Pix[canvas.PixOffset(bounds.Min.X, y):]
		for i := 0; i < w*4; i += 4 {
			out[o], out[o+1], out[o+2] = row[i], row[i+1], row[i+2]
			o += 3
		}
	}
	return out
}
