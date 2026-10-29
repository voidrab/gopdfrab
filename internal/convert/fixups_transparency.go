package convert

import (
	"context"
	"image"
	"runtime"
	"sort"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
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
	// ctx stops dispatching further renders once the run is cancelled; nil
	// (the registry prototype) never cancels.
	ctx context.Context
}

func init() {
	registerFixer(transparencyFlattener{})
}

// leadsItsClause orders the flattener before the other 6.4 fixers. The
// flattener must render a group with its original opacities; once the opacity
// repair has replaced translucent fills with opaque colours, overlapping fills
// no longer accumulate.
func (transparencyFlattener) leadsItsClause() {}

func (transparencyFlattener) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Transparency.TransparencyGroup, pdf.Checks.Transparency.ImageWithSoftMask:
		return true
	}
	return false
}

func (f transparencyFlattener) runCtx() context.Context {
	if f.ctx != nil {
		return f.ctx
	}
	return context.Background()
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
	if len(unique) == 0 {
		return false
	}

	// Workers edit a clone of each target's entries, never the target itself:
	// other workers' renders may read it (a form drawn inside another, an
	// image inside a form), and an edit mid-render is a data race. The edits
	// are published serially once every render is done.
	pdf.ParallelFor(f.runCtx(), len(unique), runtime.NumCPU(), func(i int) {
		t := unique[i]
		work := t.dict
		work.Entries = t.dict.Entries.Clone()
		switch t.kind {
		case "image":
			fixed, ok := bakeSoftMaskOut(work, t.resources)
			results[i] = result{fixed: fixed, ok: ok}
		case "form":
			// A provably transparency-free group composites the same
			// without /Group; deleting it (serially, below) skips the
			// whole rasterization.
			if canDropGroupSafely(t.dict, t.resources) {
				results[i] = result{fixed: t.dict, ok: true, dropGroup: true}
				return
			}
			fixed, drops, ok := flattenFormToImage(work, t.resources, f.renderDPI(), t.inherited)
			results[i] = result{fixed: fixed, ok: ok, drops: drops}
		case "page":
			// Only note it: Del shifts the page dict's entries, and other
			// workers' renders may read this dict (through /Parent or an
			// annotation's /P), so the delete happens serially below.
			_, had := t.dict.Entries.Lookup("Group")
			results[i] = result{fixed: t.dict, ok: had, dropGroup: true}
		}
	})

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
		r := &results[i]
		if r.ok && !r.dropGroup {
			t.dict.Entries.Assign(r.fixed.Entries)
			r.fixed.Entries = t.dict.Entries
		}
		byDict[pdf.ValuePointer(t.dict.Entries)] = *r
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
// and an image can be reached by more routes than a resource dictionary: a
// /PieceInfo entry keeps one, and no amount of rasterizing the pages it is not
// on will remove it. Images the walk already fixed no longer have a soft mask,
// so this pass skips them on its own.
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
	kind string // "image", "form", or "page"
	// inherited is the colour in force where a form is drawn, which its own
	// content need never set: a wrapper form whose background is filled under
	// the page's "1 g" comes out solid black rasterized on its own.
	inherited raster.InheritedPaint
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
	walkPageTree(pages, func(node, resources pdf.PDFDict, mediaBox [4]float64, pageNum int) {
		// Dropping a page's own group leaves what it draws as it was, so the
		// groups and soft masks inside are flattened in the same pass, before
		// the opacity repair makes them opaque.
		if hasTransparencyGroup(node) {
			out = append(out, flaggedTarget{kind: "page", dict: node, resources: resources, mediaBox: mediaBox, page: pageNum})
		}
		collectXObjectTargets(resources, visited, pageNum, pagePaintAtDo(node, resources), &out)
		collectAnnotationTargets(node, resources, visited, pageNum, &out)
	})
	return out
}

// walkPageTree calls visit for every page under pages, top-down from
// Root/Pages/Kids, with the /Resources and /MediaBox it inherits and its
// 1-based page number.
//
// It guards the walk exactly as pdf.Reader.BuildPageIndex does: an indirect
// node is visited once and depth is capped. That keeps a cyclic /Kids (a
// Pages node listing itself or an ancestor) from recursing until the stack
// overflows -- a fatal error no recover can catch -- and keeps page numbers
// aligned with the verifier's, so they match PDFError.Page().
func walkPageTree(pages pdf.PDFDict, visit func(page, resources pdf.PDFDict, mediaBox [4]float64, pageNum int)) {
	seen := map[int]bool{}
	pageNum := 0
	var walk func(node, resources pdf.PDFDict, mediaBox [4]float64, depth int)
	walk = func(node, resources pdf.PDFDict, mediaBox [4]float64, depth int) {
		if depth > pdf.MaxPageTreeDepth {
			return
		}
		if ref, ok := node.Entries.Get(pdf.RefKey).(pdf.PDFRef); ok {
			if seen[ref.ObjNum] {
				return
			}
			seen[ref.ObjNum] = true
		}
		if r, ok := node.Entries.Get("Resources").(pdf.PDFDict); ok {
			resources = r
		}
		if mb, err := pdf.FloatArray(node.Entries.Get("MediaBox")); err == nil && len(mb) == 4 {
			mediaBox = [4]float64{mb[0], mb[1], mb[2], mb[3]}
		}
		if (node.Entries.Get("Type") == pdf.PDFName{Value: "Page"}) {
			pageNum++
			visit(node, resources, mediaBox, pageNum)
			return
		}
		if kids, ok := node.Entries.Get("Kids").(pdf.PDFArray); ok {
			for _, kid := range kids {
				if kd, ok := kid.(pdf.PDFDict); ok {
					walk(kd, resources, mediaBox, depth+1)
				}
			}
		}
	}
	walk(pages, pdf.PDFDict{}, defaultMediaBox, 0)
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
	walkPageTree(pages, func(node, resources pdf.PDFDict, mediaBox [4]float64, _ int) {
		out = append(out, pageTarget{dict: node, resources: resources, mediaBox: mediaBox})
	})
	return out
}

// paintAtDo reads a content stream for the colour in force at each XObject
// invocation (Do), which is the colour the form it names inherits. First
// invocation wins: a form
// drawn twice under two colours cannot be flattened to suit both, and the
// first is what the rest of the flattening assumes.
func paintAtDo(content []byte, resources pdf.PDFDict) map[string]raster.InheritedPaint {
	type paintState struct {
		fill, stroke     [3]float64
		fillCS, strokeCS pdf.PDFValue
	}
	// A drawing starts black in DeviceGray (ISO 32000-1 table 52).
	cur := paintState{fillCS: pdf.PDFName{Value: "DeviceGray"}, strokeCS: pdf.PDFName{Value: "DeviceGray"}}
	var stack []paintState
	at := map[string]raster.InheritedPaint{}

	colour := func(space pdf.PDFValue, operands []pdf.PDFValue) ([3]float64, bool) {
		comps := pdf.NumericOperands(operands)
		if len(comps) == 0 {
			return [3]float64{}, false
		}
		r, g, b := pdf.ResolveColor(space, comps, resources)
		return [3]float64{r, g, b}, true
	}

	pdf.NewContentScanner(content).Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "q":
			stack = append(stack, cur)
		case "Q":
			if n := len(stack); n > 0 {
				cur = stack[n-1]
				stack = stack[:n-1]
			}
		case "g", "rg", "k", "sc", "scn":
			space := cur.fillCS
			if named, ok := deviceSpaces[op]; ok {
				space = pdf.PDFName{Value: named}
				cur.fillCS = space
			}
			if rgb, ok := colour(space, operands); ok {
				cur.fill = rgb
			}
		case "G", "RG", "K", "SC", "SCN":
			space := cur.strokeCS
			if named, ok := deviceSpaces[strings.ToLower(op)]; ok {
				space = pdf.PDFName{Value: named}
				cur.strokeCS = space
			}
			if rgb, ok := colour(space, operands); ok {
				cur.stroke = rgb
			}
		case "cs", "CS":
			if len(operands) == 0 {
				return
			}
			name, ok := operands[len(operands)-1].(pdf.PDFName)
			if !ok {
				return
			}
			space, ok := pdf.LookupNamedColorSpace(name.Value, resources)
			if !ok {
				space = name
			}
			if op == "cs" {
				cur.fillCS = space
			} else {
				cur.strokeCS = space
			}
		case "Do":
			if len(operands) == 0 {
				return
			}
			name, ok := operands[len(operands)-1].(pdf.PDFName)
			if !ok {
				return
			}
			if _, seen := at[name.Value]; !seen {
				at[name.Value] = raster.InheritedPaint{Set: true, Fill: cur.fill, Stroke: cur.stroke}
			}
		}
	})
	return at
}

// pagePaintAtDo and formPaintAtDo read a page's or a form's content for the
// colour each XObject invocation (Do) it makes inherits. A stream nothing can
// read hands out nothing, and every form under it is rendered from the default
// state.
func pagePaintAtDo(page pdf.PDFDict, resources pdf.PDFDict) map[string]raster.InheritedPaint {
	content, err := raster.PageContentBytes(page)
	if err != nil {
		return nil
	}
	return paintAtDo(content, resources)
}

func formPaintAtDo(form pdf.PDFDict, resources pdf.PDFDict) map[string]raster.InheritedPaint {
	content, err := pdf.DecodeStream(form)
	if err != nil {
		return nil
	}
	return paintAtDo(content, resources)
}

// collectXObjectTargets scans resources' /XObject subdictionary, flagging
// Form XObjects carrying their own /Group and Image XObjects carrying a
// non-/None /SMask, recursing into nested Forms via the same /Resources
// fallback doXObject uses (internal/raster). A flagged Form is not descended into
// further: it's about to be wholly replaced, so anything inside it is moot.
// visited guards against cyclic/shared XObject subdictionaries.
func collectXObjectTargets(resources pdf.PDFDict, visited map[uintptr]bool, page int, inherited map[string]raster.InheritedPaint, out *[]flaggedTarget) {
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
				*out = append(*out, flaggedTarget{kind: "form", dict: xobj, resources: formRes,
					xobjects: xobjects, name: name, page: page, inherited: inherited[name]})
				continue
			}
			collectXObjectTargets(formRes, visited, page, formPaintAtDo(xobj, formRes), out)
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
// cannot see it, but the verifier's graph walk reports violations there all
// the same. A digital signature's appearance can be a Form XObject whose nested
// Form carries /Group /S /Transparency. Without this walk, every signed
// document reports a 6.4 violation that convert cannot reach, and the
// verify/fix loop cannot converge.
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
		*out = append(*out, flaggedTarget{kind: "form", dict: form, resources: formRes,
			xobjects: container, name: name, page: pageNum})
		return
	}
	collectXObjectTargets(formRes, visited, pageNum, formPaintAtDo(form, formRes), out)
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

// bakeSoftMaskOut removes an image's soft mask, which PDF/A-1 prohibits, and
// preserves its effect as far as PDF/A-1 allows. The mask is thresholded into
// a 1-bit stencil /Mask and the image samples are left unchanged. A stencil
// keeps the page behind the image visible, which compositing over white would
// not.
//
// Leaves img untouched (ok=false) if either decode fails.
func bakeSoftMaskOut(img pdf.PDFDict, resources pdf.PDFDict) (pdf.PDFDict, bool) {
	smaskDict, ok := img.Entries.Get("SMask").(pdf.PDFDict)
	if !ok {
		return img, false
	}
	smask, err := raster.DecodeImageRGBA(smaskDict, resources)
	if err != nil {
		return img, false
	}

	// A uniformly-opaque mask hides nothing: drop it and keep the image as it
	// is.
	if smaskFullyOpaque(smask) {
		img.Entries.Del("SMask")
		return img, true
	}

	// A mask with nothing opaque enough to survive the threshold is not a
	// shape, and a stencil can only say it by masking the whole picture out.
	if maskIsFaint(smask) {
		return fadeSoftMaskIntoImage(img, smaskDict, smask, resources)
	}

	img.Entries.Set("Mask", stencilFromCoverage(smask, maskChannel, shapeCutoff))
	img.Entries.Del("SMask")
	return img, true
}

// maskIsFaint reports a soft mask no stencil can carry: every sample of it is
// more transparent than not, so thresholding masks every pixel out.
func maskIsFaint(smask *image.RGBA) bool {
	for i := maskChannel; i < len(smask.Pix); i += 4 {
		if int(smask.Pix[i]) >= shapeCutoff {
			return false
		}
	}
	return true
}

// hasClearPixels reports whether any pixel of coverage has a value in channel
// below clearCutoff, that is, is fully transparent.
func hasClearPixels(coverage *image.RGBA, channel int) bool {
	for i := channel; i < len(coverage.Pix); i += 4 {
		if int(coverage.Pix[i]) < clearCutoff {
			return true
		}
	}
	return false
}

// fadeSoftMaskIntoImage blends a faint soft mask into the image's own samples.
// What a picture looks like at 20% over the page is a pale picture, and that
// can be stored as samples where it cannot be stored as a stencil. The page is
// taken as white, as the opacity repair also assumes, because a pale picture is
// much less wrong than none at all.
//
// A mask carrying /Matte holds the image premultiplied against that colour, so
// the stored samples are undone by it before they are blended.
func fadeSoftMaskIntoImage(img, smaskDict pdf.PDFDict, smask *image.RGBA, resources pdf.PDFDict) (pdf.PDFDict, bool) {
	base, err := raster.DecodeImageRGBA(img, resources)
	if err != nil {
		return img, false
	}
	w, h := base.Bounds().Dx(), base.Bounds().Dy()
	mw, mh := smask.Bounds().Dx(), smask.Bounds().Dy()
	matte, premultiplied := matteColour(smaskDict)

	for y := 0; y < h; y++ {
		// Sampled by proportion, since the mask need not share the image's size.
		srow := pdf.ClampInt(y*mh/h, 0, mh-1)
		for x := 0; x < w; x++ {
			scol := pdf.ClampInt(x*mw/w, 0, mw-1)
			alpha := float64(smask.Pix[smask.PixOffset(scol, srow)+maskChannel]) / 255
			off := base.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				v := float64(base.Pix[off+c]) / 255
				if premultiplied && alpha > 0 {
					v = matte[c] + (v-matte[c])/alpha
				}
				base.Pix[off+c] = byte(pdf.ClampInt(int((alpha*v+(1-alpha))*255+0.5), 0, 255))
			}
			base.Pix[off+3] = 255
		}
	}

	// Blending kept the mask's level but not its shape: where the mask was
	// clear the picture is not pale, it is absent, and an opaque sample there
	// would paint over whatever the image was laid on. That part still needs a
	// stencil.
	img.Entries.Del("Mask")
	if hasClearPixels(smask, maskChannel) {
		img.Entries.Set("Mask", stencilFromCoverage(smask, maskChannel, clearCutoff))
	}

	setStreamRGBFlate(&img, base)
	img.Entries.Set("Width", pdf.PDFInteger(w))
	img.Entries.Set("Height", pdf.PDFInteger(h))
	img.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
	img.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	img.Entries.Del("Decode")
	img.Entries.Del("SMask")
	return img, true
}

// matteColour reads a soft mask's /Matte: the colour the image it masks was
// premultiplied against. Only a three-component matte is used, since the
// samples are blended as RGB.
func matteColour(smaskDict pdf.PDFDict) ([3]float64, bool) {
	values, err := pdf.FloatArray(smaskDict.Entries.Get("Matte"))
	if err != nil || len(values) != 3 {
		return [3]float64{}, false
	}
	return [3]float64{values[0], values[1], values[2]}, true
}

// Where the coverage of a stencil is read from: a decoded soft mask carries
// the opacity in its first channel (DecodeImageRGBA's grey), a rendered canvas
// in its alpha.
const (
	maskChannel  = 0
	alphaChannel = 3
)

// How opaque a sample has to be to survive a stencil: half way for a mask
// standing in for a shape, and merely not clear for one whose level has
// already been blended into the samples.
const (
	shapeCutoff = 128
	clearCutoff = 8
)

// stencilFromCoverage builds the stencil that stands in for per-pixel opacity:
// a bit per pixel, set where the image is less opaque than cutoff in channel,
// which is the value that masks the pixel out under the default /Decode.
// smask must have at least one pixel; every caller's raster does (see
// TestStencilCallersNeverSeeAnEmptyRaster).
func stencilFromCoverage(smask *image.RGBA, channel, cutoff int) pdf.PDFDict {
	w, h := smask.Bounds().Dx(), smask.Bounds().Dy()
	rowBytes := (w + 7) / 8
	row := make([]byte, rowBytes)

	stencil := pdf.NewPDFDict()
	stencil.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	stencil.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	stencil.Entries.Set("Width", pdf.PDFInteger(w))
	stencil.Entries.Set("Height", pdf.PDFInteger(h))
	stencil.Entries.Set("BitsPerComponent", pdf.PDFInteger(1))
	stencil.Entries.Set("ImageMask", pdf.PDFBoolean(true))
	writer.SetStreamFlateRows(&stencil, h, func(y int) []byte {
		clear(row)
		off := smask.PixOffset(smask.Bounds().Min.X, smask.Bounds().Min.Y+y)
		for x := 0; x < w; x++ {
			if int(smask.Pix[off+x*4+channel]) < cutoff {
				row[x/8] |= 0x80 >> (x % 8)
			}
		}
		return row
	})
	return stencil
}

// blendOverPaper composites every pixel of canvas over white and makes it
// opaque. Because image.RGBA samples are premultiplied, the blend adds
// 255 minus alpha to each colour channel.
func blendOverPaper(canvas *image.RGBA) {
	for i := 0; i < len(canvas.Pix); i += 4 {
		paper := 255 - canvas.Pix[i+alphaChannel]
		canvas.Pix[i] += paper
		canvas.Pix[i+1] += paper
		canvas.Pix[i+2] += paper
		canvas.Pix[i+alphaChannel] = 255
	}
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
// transparency, so deleting /Group composites identically. Every graphics
// state the content switches to must be one of resources' opaque ones, and
// the content may draw no XObject, no inline image and no pattern.
//
// A group whose graphics states all set both opacities to 1 is common and is
// therefore kept as vector content rather than rasterized.
func canDropGroupSafely(form pdf.PDFDict, resources pdf.PDFDict) bool {
	data, err := pdf.DecodeStream(form)
	if err != nil {
		return false
	}
	states, _ := resources.Entries.Get("ExtGState").(pdf.PDFDict)
	safe := true
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "gs":
			if len(operands) == 0 {
				safe = false
				return
			}
			name, _ := operands[len(operands)-1].(pdf.PDFName)
			state, ok := states.Entries.Get(name.Value).(pdf.PDFDict)
			if !ok || !opaqueGState(state) {
				safe = false
			}
		case "Do", "INLINEIMAGE":
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

// opaqueGState reports whether a graphics state dictionary leaves what is
// drawn opaque: stroke and fill opacity (/CA, /ca) 1 if set, no soft mask,
// and the Normal or Compatible blend mode. Its other entries, such as line
// width or dash pattern, do not affect transparency.
func opaqueGState(state pdf.PDFDict) bool {
	for _, key := range state.Entries.Keys() {
		v := state.Entries.Get(key)
		switch key {
		case "CA", "ca":
			if alpha, ok := pdf.PDFNumberToFloat(v); !ok || alpha != 1 {
				return false
			}
		case "SMask":
			if name, ok := v.(pdf.PDFName); !ok || name.Value != "None" {
				return false
			}
		case "BM":
			if name, ok := v.(pdf.PDFName); !ok || (name.Value != "Normal" && name.Value != "Compatible") {
				return false
			}
		}
	}
	return true
}

// flattenFormToImage rasterizes a Form XObject's own /BBox + content in
// isolation (raster.RenderFormContent) and rewrites the Form in place to
// paint a single fresh Image XObject filling that same BBox, dropping
// /Group. The Form's own identity, /Matrix and every XObject invocation (Do) of
// it are kept, so it still composes into the page in the same way; it paints a
// flat image in place of a transparency group.
// A render failure leaves the Form untouched (ok=false).
func flattenFormToImage(form pdf.PDFDict, resources pdf.PDFDict, dpi int, inherited raster.InheritedPaint) (pdf.PDFDict, []string, bool) {
	canvas, bbox, drops, err := raster.RenderFormContent(form, resources, dpi, inherited)
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
	// An image is opaque everywhere, so pixels the form left unpainted would
	// cover the page beneath it. PDF/A-1 permits a 1-bit stencil mask, which
	// masks out the fully transparent pixels. Partially transparent pixels
	// cannot be expressed by a stencil; they are blended over white, as
	// fadeSoftMaskIntoImage does.
	if hasClearPixels(canvas, alphaChannel) {
		img.Entries.Set("Mask", stencilFromCoverage(canvas, alphaChannel, clearCutoff))
	}
	blendOverPaper(canvas)
	setStreamRGBFlate(&img, canvas)

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
	// Cannot fail: ops holds only operators with integer, real and name
	// operands, which the content writer always serialises.
	data, _ := writer.WriteContentStream(ops)

	form.Entries.Del("Group")
	form.Entries.Set("Resources", formResources)
	writer.SetStreamFlate(&form, data)
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
	canvas, drops, err := raster.RenderPage(page, resources, mediaBox, dpi)
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
	setStreamRGBFlate(&img, canvas)

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
	// Cannot fail: ops holds only operators with integer, real and name
	// operands, which the content writer always serialises.
	data, _ := writer.WriteContentStream(ops)
	contents := pdf.NewPDFDict()
	writer.SetStreamFlate(&contents, data)

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
func setStreamRGBFlate(d *pdf.PDFDict, canvas *image.RGBA) {
	bounds := canvas.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	rowRGB := make([]byte, w*3)
	writer.SetStreamFlateRows(d, h, func(i int) []byte {
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
