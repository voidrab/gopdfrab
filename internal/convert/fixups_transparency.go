package convert

import (
	"image"
	"runtime"
	"sort"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// transparencyFlattener remediates Checks.Transparency.TransparencyGroup and
// Checks.Transparency.ImageWithSoftMask by rasterizing only the smallest
// self-contained object carrying the violation -- a Form XObject's own
// content for a transparency group, or a single Image XObject's samples for
// a soft mask -- never the whole page.
// transparencyFlattener carries the raster resolution (dpi) its flattening
// renders at; buildLocalFixers stamps the per-run value from Options.RasterDPI.
// A zero dpi (the registry prototype) falls back to defaultRasterDPI.
// drops, when non-nil, collects the features flattening could not render, so
// they reach ConvertResult.RasterDrops like the page-level fallback's do. The
// registry prototype leaves it nil.
type transparencyFlattener struct {
	dpi   int
	drops *[]RasterDrop
}

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

func (f transparencyFlattener) renderDPI() int {
	if f.dpi > 0 {
		return f.dpi
	}
	return defaultRasterDPI
}

// defaultMediaBox is the PDF spec's fallback page size (US Letter) for a
// page that inherits no /MediaBox anywhere up its Pages-tree ancestry.
var defaultMediaBox = [4]float64{0, 0, 612, 792}

func (f transparencyFlattener) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := f.flattenPageTargets(trailer)
	if bakeStraySoftMasks(trailer) {
		changed = true
	}
	return changed, nil
}

// flattenPageTargets does the page-tree walk's share of the work: every
// transparency group and soft-masked image the pages reach, flattened in
// parallel and written back through the resource slot that named it.
func (f transparencyFlattener) flattenPageTargets(trailer *pdf.PDFDict) bool {
	targets := collectTransparencyTargets(*trailer)

	unique := uniqueByDict(targets)
	type result struct {
		fixed     pdf.PDFDict
		ok        bool
		dropGroup bool
		drops     []string
	}
	results := make([]result, len(unique))
	workers := min(runtime.NumCPU(), len(unique))
	if workers < 1 {
		return false
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
					results[i] = result{fixed: fixed, ok: ok}
				case "form":
					// A provably transparency-free group composites the same
					// without /Group; deleting it (serially, below) skips the
					// whole rasterization.
					if canDropGroupSafely(t.dict) {
						results[i] = result{fixed: t.dict, ok: true, dropGroup: true}
						continue
					}
					fixed, drops, ok := flattenFormToImage(t.dict, t.resources, f.renderDPI())
					results[i] = result{fixed: fixed, ok: ok, drops: drops}
				case "page":
					_, had := t.dict.Entries.Lookup("Group")
					t.dict.Entries.Del("Group")
					results[i] = result{fixed: pdf.PDFDict{}, ok: had}
				}
			}
		}()
	}
	for i := range unique {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// Each worker wrote only its own slot, so collecting the drops here --
	// serially, in target order -- needs no synchronization.
	if f.drops != nil {
		for i, t := range unique {
			if results[i].ok && len(results[i].drops) > 0 {
				*f.drops = append(*f.drops, RasterDrop{Page: t.page, Features: results[i].drops})
			}
		}
	}

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
		if r.dropGroup {
			r.fixed.Entries.Del("Group")
			continue
		}
		if t.kind != "page" {
			t.xobjects.Entries.Set(t.name, r.fixed)
		}
	}
	return changed
}

// bakeStraySoftMasks composites out the soft mask of every image the page walk
// above cannot see. 6.4 is reported against any image dictionary in the graph,
// and an image reaches the graph by more routes than a resource dictionary --
// a Photoshop /PieceInfo keeps one, and no amount of rasterizing the pages it
// is not on will remove it. Images the walk already fixed no longer have a
// soft mask, so this pass skips them on its own.
func bakeStraySoftMasks(trailer *pdf.PDFDict) bool {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Image"}) || !hasSoftMask(d) {
			return d, false
		}
		// No resource dictionary: an image reached outside any content stream
		// has no named colour spaces in scope, and one that needs them stays
		// as it is rather than being decoded against the wrong names.
		fixed, ok := bakeSoftMaskOut(d, pdf.PDFDict{})
		if !ok {
			return d, false
		}
		changed = true
		return fixed, true
	})
	return changed
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
	page      int // 1-based, for the drop report
}

// collectTransparencyTargets walks the page tree top-down from
// Root/Pages/Kids (never via /Parent, an intentional cycle back up the tree
// -- see document.go), tracking inherited /Resources and /MediaBox, and for
// each page either flags the page itself (its own inert /Group, to be
// deleted) or descends into its resource graph to flag the individual
// Form/Image XObjects responsible.
func collectTransparencyTargets(trailer pdf.PDFDict) []flaggedTarget {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return nil
	}
	pages, ok := root.Entries.Get("Pages").(pdf.PDFDict)
	if !ok {
		return nil
	}

	var out []flaggedTarget
	visited := map[uintptr]bool{}
	// Pages are numbered by this same top-down walk, so the counter matches a
	// PDFError's 1-based Page() and the page-level raster fallback's numbering.
	pageNum := 0
	var walk func(node pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64)
	walk = func(node pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64) {
		if r, ok := node.Entries.Get("Resources").(pdf.PDFDict); ok {
			resources = r
		}
		if mb, err := pdf.FloatArray(node.Entries.Get("MediaBox")); err == nil && len(mb) == 4 {
			mediaBox = [4]float64{mb[0], mb[1], mb[2], mb[3]}
		}
		if (node.Entries.Get("Type") == pdf.PDFName{Value: "Page"}) {
			pageNum++
			if hasTransparencyGroup(node) {
				out = append(out, flaggedTarget{kind: "page", dict: node, resources: resources, mediaBox: mediaBox, page: pageNum})
				return
			}
			collectXObjectTargets(resources, visited, pageNum, &out)
			collectAnnotationTargets(node, resources, visited, pageNum, &out)
			return
		}
		if kids, ok := node.Entries.Get("Kids").(pdf.PDFArray); ok {
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
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return nil
	}
	pages, ok := root.Entries.Get("Pages").(pdf.PDFDict)
	if !ok {
		return nil
	}

	var out []pageTarget
	var walk func(node, resources pdf.PDFDict, mediaBox [4]float64)
	walk = func(node, resources pdf.PDFDict, mediaBox [4]float64) {
		if r, ok := node.Entries.Get("Resources").(pdf.PDFDict); ok {
			resources = r
		}
		if mb, err := pdf.FloatArray(node.Entries.Get("MediaBox")); err == nil && len(mb) == 4 {
			mediaBox = [4]float64{mb[0], mb[1], mb[2], mb[3]}
		}
		if (node.Entries.Get("Type") == pdf.PDFName{Value: "Page"}) {
			out = append(out, pageTarget{dict: node, resources: resources, mediaBox: mediaBox})
			return
		}
		if kids, ok := node.Entries.Get("Kids").(pdf.PDFArray); ok {
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
func collectXObjectTargets(resources pdf.PDFDict, visited map[uintptr]bool, page int, out *[]flaggedTarget) {
	xobjects, ok := resources.Entries.Get("XObject").(pdf.PDFDict)
	if !ok {
		return
	}
	ptr := pdf.ValuePointer(xobjects.Entries)
	if visited[ptr] {
		return
	}
	visited[ptr] = true

	// Sorted, not map order: the targets' order reaches the user through
	// ConvertResult.RasterDrops, so iterating the map directly would report a
	// page's drops in a different order on every run.
	for _, name := range sortedKeys(xobjects.Entries) {
		xobj, ok := xobjects.Entries.Get(name).(pdf.PDFDict)
		if !ok {
			continue
		}
		subtype, _ := xobj.Entries.Get("Subtype").(pdf.PDFName)
		switch subtype.Value {
		case "Form":
			// The form's own resources, and the ones around it only when it
			// brings none: a form names what it draws from its own dictionary,
			// and handing it the page's instead means every image, font and
			// colour inside it resolves to nothing when it is rasterized.
			formRes := resourcesOf(xobj, resources)
			if hasTransparencyGroup(xobj) {
				*out = append(*out, flaggedTarget{kind: "form", dict: xobj, resources: formRes, xobjects: xobjects, name: name, page: page})
				continue
			}
			collectXObjectTargets(formRes, visited, page, out)
		case "Image":
			if hasSoftMask(xobj) {
				*out = append(*out, flaggedTarget{kind: "image", dict: xobj, resources: resources, xobjects: xobjects, name: name, page: page})
			}
		}
	}
}

// collectAnnotationTargets flags the Form XObjects reachable through a page's
// annotation appearance streams (/Annots -> /AP -> /N, /R, /D, and the
// appearance sub-dictionaries keyed by state).
//
// An appearance stream is not in the page's /Resources, so the walk above
// cannot see it -- but the verifier's graph walk reports violations there all
// the same. Acrobat writes a signature's appearance as a Form XObject whose
// nested Form carries /Group /S /Transparency, which is exactly this shape, so
// without this every digitally signed document reported a 6.4 violation that
// convert could not reach and the verify/fix loop could never converge.
func collectAnnotationTargets(page, resources pdf.PDFDict, visited map[uintptr]bool, pageNum int, out *[]flaggedTarget) {
	annots, ok := page.Entries.Get("Annots").(pdf.PDFArray)
	if !ok {
		return
	}
	for _, a := range annots {
		annot, ok := a.(pdf.PDFDict)
		if !ok {
			continue
		}
		ap, ok := annot.Entries.Get("AP").(pdf.PDFDict)
		if !ok {
			continue
		}
		for _, state := range []string{"N", "R", "D"} {
			entry, ok := ap.Entries.Get(state).(pdf.PDFDict)
			if !ok {
				continue
			}
			if isFormXObject(entry) {
				collectAppearanceForm(entry, ap, state, resources, visited, pageNum, out)
				continue
			}
			// /AP /N << /Off ... /On ... >>: one appearance per state name.
			for _, name := range sortedKeys(entry.Entries) {
				if form, ok := entry.Entries.Get(name).(pdf.PDFDict); ok && isFormXObject(form) {
					collectAppearanceForm(form, entry, name, resources, visited, pageNum, out)
				}
			}
		}
	}
}

// collectAppearanceForm flags one appearance Form XObject, or descends into its
// resources when it is itself clean. container+name address the slot the fixed
// dict is written back into, exactly as an /XObject resource entry does.
func collectAppearanceForm(form, container pdf.PDFDict, name string, resources pdf.PDFDict, visited map[uintptr]bool, pageNum int, out *[]flaggedTarget) {
	formRes := resourcesOf(form, resources)
	if hasTransparencyGroup(form) {
		*out = append(*out, flaggedTarget{kind: "form", dict: form, resources: formRes, xobjects: container, name: name, page: pageNum})
		return
	}
	collectXObjectTargets(formRes, visited, pageNum, out)
}

// isFormXObject distinguishes an appearance stream from the sub-dictionary of
// appearance states that can stand in the same slot.
func isFormXObject(d pdf.PDFDict) bool {
	if !d.HasStream {
		return false
	}
	subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)
	return subtype.Value == "Form"
}

// sortedKeys returns a dictionary's keys in a stable order, for walks whose
// order reaches the user.
func sortedKeys(entries pdf.Dict) []string {
	out := entries.Keys()
	sort.Strings(out)
	return out
}

// hasTransparencyGroup mirrors validateTransparencyGroup's (checks_dict.go)
// /Group /S /Transparency test.
func hasTransparencyGroup(d pdf.PDFDict) bool {
	group, ok := d.Entries.Get("Group").(pdf.PDFDict)
	if !ok {
		return false
	}
	return group.Entries.Get("S") == pdf.PDFName{Value: "Transparency"}
}

// hasSoftMask mirrors validateXObjectDict's (checks_dict.go) ImageWithSoftMask
// test: an /SMask entry present and not the literal name /None.
func hasSoftMask(img pdf.PDFDict) bool {
	sm, ok := img.Entries.Lookup("SMask")
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
	smaskDict, ok := img.Entries.Get("SMask").(pdf.PDFDict)
	if !ok {
		return img, false
	}
	smask, err := DecodeImageRGBA(smaskDict, resources)
	if err != nil {
		return img, false
	}

	// A uniformly-opaque mask composites to the base unchanged: drop the
	// SMask and keep the original image encoding untouched.
	if smaskFullyOpaque(smask) {
		img.Entries.Del("SMask")
		return img, true
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
	img.Entries.Set("Width", pdf.PDFInteger(outW))
	img.Entries.Set("Height", pdf.PDFInteger(outH))
	img.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
	img.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	img.Entries.Del("SMask")
	img.Entries.Del("Decode")
	img.Entries.Del("Mask")
	if err := setStreamRGBFlate(&img, out); err != nil {
		return img, false
	}
	return img, true
}

// smaskFullyOpaque reports whether every alpha sample (the red channel the
// composite loop reads) of a decoded soft mask is 255.
func smaskFullyOpaque(smask *image.RGBA) bool {
	if smask.Bounds().Dx() == 0 || smask.Bounds().Dy() == 0 {
		return false
	}
	for i := 0; i < len(smask.Pix); i += 4 {
		if smask.Pix[i] != 255 {
			return false
		}
	}
	return true
}

// canDropGroupSafely reports whether a form's content provably uses no
// transparency, so deleting /Group composites identically: no gs, no Do, no
// inline images, and no pattern fill/stroke anywhere in the stream.
func canDropGroupSafely(form pdf.PDFDict) bool {
	data, err := pdf.DecodeStream(form)
	if err != nil {
		return false
	}
	safe := true
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "gs", "Do", "INLINEIMAGE":
			safe = false
		case "scn", "SCN":
			if len(operands) > 0 {
				if _, isName := operands[len(operands)-1].(pdf.PDFName); isName {
					safe = false
				}
			}
		}
	})
	return safe
}

// flattenFormToImage rasterizes a Form XObject's own /BBox + content in
// isolation (renderFormContent, raster.go) and rewrites the Form in place to
// paint a single fresh Image XObject filling that same BBox, dropping
// /Group. The Form's own identity, /Matrix and every existing /Do reference
// to it are untouched, so it keeps composing into the page exactly as
// before -- it now just paints a flat image instead of a transparency group.
// A render failure leaves the Form untouched (ok=false).
func flattenFormToImage(form pdf.PDFDict, resources pdf.PDFDict, dpi int) (pdf.PDFDict, []string, bool) {
	canvas, bbox, drops, err := renderFormContent(form, resources, dpi)
	if err != nil {
		return form, nil, false
	}

	img := pdf.NewPDFDict()
	img.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	img.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	img.Entries.Set("Width", pdf.PDFInteger(canvas.Bounds().Dx()))
	img.Entries.Set("Height", pdf.PDFInteger(canvas.Bounds().Dy()))
	img.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
	img.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	if err := setStreamRGBFlate(&img, canvas); err != nil {
		return form, nil, false
	}

	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Im0", img)
	formResources := pdf.NewPDFDict()
	formResources.Entries.Set("XObject", xobjects)

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
		return form, nil, false
	}

	form.Entries.Del("Group")
	form.Entries.Set("Resources", formResources)
	if err := writer.SetStreamFlate(&form, data); err != nil {
		return form, nil, false
	}
	return form, drops, true
}

// flattenPageToImage rasterizes page (RenderPage) and rebuilds it in place
// as a single flat Image XObject painted by a fresh, minimal content
// stream, replacing /Resources and /Contents and dropping /Group and
// /Rotate (a flattened raster has no remaining rotation to apply). Used only
// when /Group sits directly on the Page dict itself, with no narrower Form
// XObject to target instead. A render failure (e.g. an unresolvable graph or
// an unsupported image codec) leaves page untouched, reporting no change
// rather than erroring the whole Convert.
func flattenPageToImage(page pdf.PDFDict, resources pdf.PDFDict, mediaBox [4]float64, dpi int) ([]string, bool) {
	canvas, drops, err := RenderPage(page, resources, mediaBox, dpi)
	if err != nil {
		return nil, false
	}

	img := pdf.NewPDFDict()
	img.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	img.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	img.Entries.Set("Width", pdf.PDFInteger(canvas.Bounds().Dx()))
	img.Entries.Set("Height", pdf.PDFInteger(canvas.Bounds().Dy()))
	img.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
	img.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	if err := setStreamRGBFlate(&img, canvas); err != nil {
		return nil, false
	}

	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Im0", img)
	csDict := pdf.NewPDFDict()
	csDict.Entries.Set("DefaultRGB", iccBasedColourSpace(3, srgbICCProfile))
	pageResources := pdf.NewPDFDict()
	pageResources.Entries.Set("XObject", xobjects)
	pageResources.Entries.Set("ColorSpace", csDict)

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
		return nil, false
	}
	contents := pdf.NewPDFDict()
	if err := writer.SetStreamFlate(&contents, data); err != nil {
		return nil, false
	}

	page.Entries.Del("Group")
	page.Entries.Del("Rotate")
	page.Entries.Set("Resources", pageResources)
	page.Entries.Set("Contents", contents)
	return drops, true
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
		off := canvas.PixOffset(bounds.Min.X, bounds.Min.Y+i)
		src := canvas.Pix[off : off+w*4 : off+w*4]
		for x, j := 0, 0; x < len(src); x, j = x+4, j+3 {
			rowRGB[j], rowRGB[j+1], rowRGB[j+2] = src[x], src[x+1], src[x+2]
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
