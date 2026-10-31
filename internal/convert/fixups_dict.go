package convert

import (
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/raster"

	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// This file registers Fixers for the dictionary-level violations classified
// as "easy" (pure key deletion/normalization, no resource synthesis) in the
// converter plan: actions (6.6), ExtGState transparency keys (6.2.8/6.4),
// annotation flags (6.5.3), interactive forms (6.9), image/form XObject
// metadata keys (6.2.4-6.2.7), PostScript form XObjects (6.2.5/6.2.7), and
// optional content (6.1.13). Each Fixer walks the whole graph via walkDicts,
// mirroring, in reverse, the exact detection logic in checks_dict.go so a
// Fixer only ever "fixes" what its matching check would actually flag.
//
// Deliberately out of scope here (see checks_dict.go and the converter
// plan's difficulty classification): annotation appearance-stream
// generation, transparency groups and image soft masks (removing the key is
// easy but changes rendered appearance), and a literal Subtype /PS XObject
// (no PDF/A-permitted substitute subtype exists, so fixing it would require
// editing every reference to the object, not just the object itself).
// Annotation subtype/colour fixers live in fixups_annot.go.

// walkDicts calls fn for every pdf.PDFDict reachable from v, recursing into dict
// entries and array elements, with cycle protection. fn may edit the dict in
// place; every slot holding it sees the edit.
func walkDicts(v pdf.PDFValue, visited map[uintptr]bool, fn func(pdf.PDFDict)) {
	walkGraph(v, visited, func(d pdf.PDFDict) bool { fn(d); return true }, nil)
}

// maxGraphWalkDepth bounds walkGraph's recursion, matching the verifier's
// walk cap so a pathologically nested graph cannot grow the stack unbounded.
const maxGraphWalkDepth = 1 << 17

// walkGraph is the one graph traversal the fixers share. It visits every dict
// and array reachable from v once (cycle-safe via visited, depth-capped),
// skipping the synthetic bookkeeping keys.
//
// visit, when non-nil, runs on each dict before its children; returning false
// skips them. edit, when non-nil, runs on each child value of a dict or array:
// a replacement it returns is written back into that slot, and the walk
// descends into the replacement.
func walkGraph(v pdf.PDFValue, visited map[uintptr]bool, visit func(pdf.PDFDict) bool, edit func(pdf.PDFValue) (pdf.PDFValue, bool)) {
	var walk func(v pdf.PDFValue, depth int)
	descend := func(child pdf.PDFValue, set func(pdf.PDFValue), depth int) {
		if edit != nil {
			if updated, ok := edit(child); ok {
				set(updated)
				child = updated
			}
		}
		walk(child, depth+1)
	}
	walk = func(v pdf.PDFValue, depth int) {
		if depth > maxGraphWalkDepth {
			return
		}
		switch val := v.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(val.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			if visit != nil && !visit(val) {
				return
			}
			for k, child := range val.Entries.All() {
				descend(child, func(u pdf.PDFValue) { val.Entries.Set(k, u) }, depth)
			}

		case pdf.PDFArray:
			ptr := pdf.ArrayPointer(val)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			for i, child := range val {
				descend(child, func(u pdf.PDFValue) { val[i] = u }, depth)
			}
		}
	}
	walk(v, 0)
}

// clearDict deletes every entry from d and keeps its reference, so references
// to d stay valid. It neutralizes a forbidden action dictionary in place.
func clearDict(d pdf.PDFDict) {
	d.Entries.DeleteFunc(func(string, pdf.PDFValue) bool { return true })
}

// clearJSNameTree recursively clears JS action dicts within a /Names/JavaScript
// name tree. walkDicts won't reach the subtree once the parent key is deleted.
func clearJSNameTree(d pdf.PDFDict, changed *bool, spec *pdfa.Spec) {
	if s, ok := d.Entries.Get("S").(pdf.PDFName); ok && spec.ForbiddenActions.Has(s.Value) {
		clearDict(d)
		*changed = true
		return
	}
	for _, v := range d.Entries.All() {
		switch val := v.(type) {
		case pdf.PDFDict:
			clearJSNameTree(val, changed, spec)
		case pdf.PDFArray:
			for _, item := range val {
				if sub, ok := item.(pdf.PDFDict); ok {
					clearJSNameTree(sub, changed, spec)
				}
			}
		}
	}
}

// deadAction reports whether v is an action dictionary that can no longer be
// used -- a forbidden type, or one already emptied by this pass. /S is
// required in an action dictionary (ISO 32000-1 Table 193), so an /A pointing
// at a dict without one reads as an action of unknown type. Clearing an action
// in place produces exactly that state.
func deadAction(v pdf.PDFValue, spec *pdfa.Spec) bool {
	d, ok := v.(pdf.PDFDict)
	if !ok {
		return false
	}
	s, ok := d.Entries.Get("S").(pdf.PDFName)
	return !ok || spec.ForbiddenActions.Has(s.Value)
}

// isCleared reports whether v is one of the dictionaries in cleared.
func isCleared(v pdf.PDFValue, cleared map[uintptr]bool) bool {
	d, ok := v.(pdf.PDFDict)
	return ok && cleared[pdf.ValuePointer(d.Entries)]
}

// --- 6.6 Actions ---

// actionFixer remediates Checks.Action.ForbiddenActionType,
// DisallowedNamedAction and AdditionalActions, mirroring
// validateActions/validateAdditionalActions in checks_dict.go. Each repair
// runs only when its own check was reported. The JavaScript name tree goes
// with the forbidden action types, because its leaves are JavaScript actions.
type actionFixer struct{ spec *pdfa.Spec }

func (actionFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Action.ForbiddenActionType, pdf.Checks.Action.DisallowedNamedAction, pdf.Checks.Action.AdditionalActions:
		return true
	}
	return false
}

func (f actionFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (f actionFixer) prepare(_ *pdf.PDFDict, issues []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	reported := reportedChecks(issues)
	ac := pdf.Checks.Action
	forbidden, named := reported[ac.ForbiddenActionType], reported[ac.DisallowedNamedAction]
	additional := reported[ac.AdditionalActions]
	// An owner whose named action this pass emptied loses its entry too,
	// even when the forbidden types were not reported.
	cleared := map[uintptr]bool{}
	return func(d pdf.PDFDict) {
		if _, ok := d.Entries.Lookup("AA"); ok && additional {
			d.Entries.Del("AA")
			*changed = true
		}

		// Remove catalog /Names/JavaScript name tree entry (6.6.1). Clear leaf
		// action dicts first -- walkDicts won't reach them after the key is gone.
		if jsTree, ok := d.Entries.Get("JavaScript").(pdf.PDFDict); ok && forbidden {
			clearJSNameTree(jsTree, changed, f.spec)
			d.Entries.Del("JavaScript")
			*changed = true
		}

		// Take a dead action off whatever points at it. Emptying the action
		// dictionary is not enough on its own: the owner still names it.
		if key := verify.ActionOwnerKey(d); key != "" {
			if v, ok := d.Entries.Lookup(key); ok && (forbidden && deadAction(v, f.spec) || isCleared(v, cleared)) {
				d.Entries.Del(key)
				*changed = true
			}
		}

		s, ok := d.Entries.Get("S").(pdf.PDFName)
		if !ok || !verify.ActionTypes[s.Value] {
			return
		}
		if f.spec.ForbiddenActions.Has(s.Value) {
			if forbidden {
				clearDict(d)
				*changed = true
			}
			return
		}
		if s.Value == "Named" && named {
			n, ok := d.Entries.Get("N").(pdf.PDFName)
			if !ok || !f.spec.NamedActions.Has(n.Value) {
				clearDict(d)
				cleared[pdf.ValuePointer(d.Entries)] = true
				*changed = true
			}
		}
	}, true
}

// --- 6.2.8 / 6.4 Extended graphics state ---

// extGStateFixer remediates the ExtGState-dictionary-level Transparency
// checks, mirroring validateExtGState in checks_dict.go, and repairs only the
// keys whose checks were reported. It deliberately does not touch
// Checks.Transparency.TransparencyGroup or ImageWithSoftMask (a different
// detection function, and a harder fix: removing the key is easy but changes
// rendered appearance).
//
// An opacity of zero is a special case, and the one place this fixer touches
// content: what is drawn at zero opacity is taken out of the content stream
// first (fixups_alpha.go), because putting the opacity back to 1 would
// otherwise paint it over the page.
type extGStateFixer struct{ spec *pdfa.Spec }

func (extGStateFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Transparency.TransferFunction,
		pdf.Checks.Transparency.DefaultTransferFunction,
		pdf.Checks.Transparency.ExtGStateRenderingIntent,
		pdf.Checks.Transparency.SoftMaskExtGState,
		pdf.Checks.Transparency.BlendMode,
		pdf.Checks.Transparency.StrokingAlpha,
		pdf.Checks.Transparency.NonStrokingAlpha:
		return true
	}
	return false
}

func (f extGStateFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (f extGStateFixer) prepare(trailer *pdf.PDFDict, issues []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	reported := reportedChecks(issues)
	// Ahead of the dictionary pass below, which is what makes the opacities
	// opaque: once they are, nothing says which drawing was invisible.
	tr := pdf.Checks.Transparency
	if (reported[tr.StrokingAlpha] || reported[tr.NonStrokingAlpha]) && repairOpacity(trailer) {
		*changed = true
	}
	return extGStateDictVisitor(changed, f.spec, reported), true
}

// extGStateDictVisitor is the dictionary half on its own, without the content
// rewriting that has to come before it. Each key is repaired only when its
// own check was reported.
func extGStateDictVisitor(changed *bool, spec *pdfa.Spec, reported map[pdf.Check]bool) func(pdf.PDFDict) {
	tr := pdf.Checks.Transparency
	return func(d pdf.PDFDict) {
		if t, ok := d.Entries.Get("Type").(pdf.PDFName); ok && t.Value != "ExtGState" {
			return
		}
		if !verify.HasAnyKey(d, "TR", "TR2", "SMask", "BM", "CA", "ca", "RI") {
			return
		}

		if _, ok := d.Entries.Lookup("TR"); ok && reported[tr.TransferFunction] {
			d.Entries.Del("TR")
			*changed = true
		}
		if tr2, ok := d.Entries.Lookup("TR2"); ok && reported[tr.DefaultTransferFunction] {
			if name, isName := tr2.(pdf.PDFName); !isName || name.Value != "Default" {
				d.Entries.Set("TR2", pdf.PDFName{Value: "Default"})
				*changed = true
			}
		}
		if ri, ok := d.Entries.Get("RI").(pdf.PDFName); ok && reported[tr.ExtGStateRenderingIntent] && !spec.RenderingIntents.Has(ri.Value) {
			d.Entries.Del("RI")
			*changed = true
		}
		if sm, ok := d.Entries.Lookup("SMask"); ok && reported[tr.SoftMaskExtGState] && !spec.Transparency {
			if name, isName := sm.(pdf.PDFName); !isName || name.Value != "None" {
				d.Entries.Set("SMask", pdf.PDFName{Value: "None"})
				*changed = true
			}
		}
		if bm, ok := d.Entries.Lookup("BM"); ok && reported[tr.BlendMode] && !spec.BlendModeAllowed(bm) {
			d.Entries.Set("BM", pdf.PDFName{Value: "Normal"})
			*changed = true
		}
		if spec.Transparency {
			return
		}
		if ca, ok := d.Entries.Lookup("CA"); ok && reported[tr.StrokingAlpha] {
			if f, num := pdf.PDFNumberToFloat(ca); num && math.Abs(f-1.0) > 1e-5 {
				d.Entries.Set("CA", pdf.PDFReal(1.0))
				*changed = true
			}
		}
		if ca, ok := d.Entries.Lookup("ca"); ok && reported[tr.NonStrokingAlpha] {
			if f, num := pdf.PDFNumberToFloat(ca); num && math.Abs(f-1.0) > 1e-5 {
				d.Entries.Set("ca", pdf.PDFReal(1.0))
				*changed = true
			}
		}
	}
}

// --- 6.5.3 Annotations ---

// annotationFlagsFixer remediates the annotation flag-bit and opacity
// checks, mirroring the relevant part of validateAnnotation in
// checks_dict.go. Each flag bit and the constant opacity (/CA) is repaired
// only when its own check was reported. It deliberately does not touch
// DisallowedSubtype, ColourWithoutIntent, or the appearance-stream checks
// (resource synthesis or harder per the converter plan).
type annotationFlagsFixer struct{}

func (annotationFlagsFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Annotation.PrintFlagNotSet, pdf.Checks.Annotation.HiddenFlagSet,
		pdf.Checks.Annotation.InvisibleFlagSet, pdf.Checks.Annotation.NoViewFlagSet,
		pdf.Checks.Annotation.OpacityNotOne:
		return true
	}
	return false
}

func (f annotationFlagsFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (annotationFlagsFixer) prepare(_ *pdf.PDFDict, issues []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	reported := reportedChecks(issues)
	an := pdf.Checks.Annotation
	set, unset := 0, 0
	if reported[an.PrintFlagNotSet] {
		set |= verify.AnnotFlagPrint
	}
	if reported[an.HiddenFlagSet] {
		unset |= verify.AnnotFlagHidden
	}
	if reported[an.InvisibleFlagSet] {
		unset |= verify.AnnotFlagInvisible
	}
	if reported[an.NoViewFlagSet] {
		unset |= verify.AnnotFlagNoView
	}
	opacity := reported[an.OpacityNotOne]
	return func(d pdf.PDFDict) {
		if !verify.IsAnnotationDict(d) {
			return
		}

		flags := 0
		if f, ok := d.Entries.Get("F").(pdf.PDFInteger); ok {
			flags = int(f)
		}
		if want := (flags | set) &^ unset; want != flags {
			d.Entries.Set("F", pdf.PDFInteger(want))
			*changed = true
		}

		if ca, ok := d.Entries.Lookup("CA"); ok && opacity {
			if f, num := pdf.PDFNumberToFloat(ca); num && f != 1.0 {
				d.Entries.Set("CA", pdf.PDFReal(1.0))
				*changed = true
			}
		}
	}, true
}

// --- 6.9 Interactive forms ---

// formFixer remediates AcroForm-level (NeedAppearances, XFA) and
// field-level (A, AA) form violations, mirroring verifyInteractiveForms and
// validateFormField in checks_dict.go/document.go. Each key is repaired only
// when its own check was reported.
type formFixer struct{}

func (formFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Form.NeedAppearances, pdf.Checks.Form.XFA,
		pdf.Checks.Form.FieldAction, pdf.Checks.Form.FieldAdditionalActions:
		return true
	}
	return false
}

func (f formFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (formFixer) prepare(trailer *pdf.PDFDict, issues []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	reported := reportedChecks(issues)
	fm := pdf.Checks.Form
	if root, ok := trailer.Entries.Get("Root").(pdf.PDFDict); ok {
		if form, ok := root.Entries.Get("AcroForm").(pdf.PDFDict); ok {
			if na, ok := form.Entries.Get("NeedAppearances").(pdf.PDFBoolean); ok && bool(na) && reported[fm.NeedAppearances] {
				form.Entries.Set("NeedAppearances", pdf.PDFBoolean(false))
				*changed = true
			}
			if _, ok := form.Entries.Lookup("XFA"); ok && reported[fm.XFA] {
				form.Entries.Del("XFA")
				*changed = true
			}
		}
	}

	action, additional := reported[fm.FieldAction], reported[fm.FieldAdditionalActions]
	return func(d pdf.PDFDict) {
		isWidget := d.Entries.Get("Type") == pdf.PDFName{Value: "Annot"} &&
			d.Entries.Get("Subtype") == pdf.PDFName{Value: "Widget"}
		isField := d.Entries.Get("FT") != nil
		if !isWidget && !isField {
			return
		}
		if _, ok := d.Entries.Lookup("A"); ok && action {
			d.Entries.Del("A")
			*changed = true
		}
		if _, ok := d.Entries.Lookup("AA"); ok && additional {
			d.Entries.Del("AA")
			*changed = true
		}
	}, true
}

// --- 6.2.4-6.2.7 Image / Form XObject metadata ---

// imageMetadataFixer remediates the simple key-deletion Image/Form XObject
// checks, mirroring the Image/Form cases of validateXObjectDict in
// checks_dict.go. It also repairs the inline-image case of ImageInterpolate
// and InlineImageRenderingIntent, which checkInlineImageOther in
// checks_content.go reports; fixInlineImage handles it because a Check can
// have only one registered Fixer. The PostScript-related checks
// (FormPostScript, FormPSEntry, FormSubtype2PS, PostScriptXObject) belong to
// postScriptXObjectFixer below.
type imageMetadataFixer struct{ spec *pdfa.Spec }

func (imageMetadataFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Image.ImageInterpolate, pdf.Checks.Image.ImageAlternates,
		pdf.Checks.Image.ImageOPI, pdf.Checks.Image.ImageRenderingIntent,
		pdf.Checks.Image.InlineImageRenderingIntent,
		pdf.Checks.Image.ReferenceXObject, pdf.Checks.Image.FormOPI:
		return true
	}
	return false
}

func (f imageMetadataFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		subtype, ok := d.Entries.Get("Subtype").(pdf.PDFName)
		if !ok {
			return
		}
		switch subtype.Value {
		case "Image":
			if b, ok := d.Entries.Get("Interpolate").(pdf.PDFBoolean); ok && bool(b) {
				d.Entries.Set("Interpolate", pdf.PDFBoolean(false))
				changed = true
			}
			if _, ok := d.Entries.Lookup("Alternates"); ok {
				d.Entries.Del("Alternates")
				changed = true
			}
			if _, ok := d.Entries.Lookup("OPI"); ok {
				d.Entries.Del("OPI")
				changed = true
			}
			if intent, ok := d.Entries.Get("Intent").(pdf.PDFName); ok && !f.spec.RenderingIntents.Has(intent.Value) {
				d.Entries.Del("Intent")
				changed = true
			}
		case "Form":
			if _, ok := d.Entries.Lookup("Ref"); ok {
				d.Entries.Del("Ref")
				changed = true
			}
			if _, ok := d.Entries.Lookup("OPI"); ok {
				d.Entries.Del("OPI")
				changed = true
			}
		}
	})
	if walkContentStreams(trailer, f.fixInlineImage) {
		changed = true
	}
	return changed, nil
}

// fixInlineImage repairs an inline image's Interpolate and rendering intent.
func (f imageMetadataFixer) fixInlineImage(op string, operands []pdf.PDFValue, changed *bool) (writer.ContentOp, bool) {
	cop, keep := fixInlineImageInterpolate(op, operands, changed)
	if op != "INLINEIMAGE" {
		return cop, keep
	}
	if fixed, ok := fixInlineImageRenderingIntent(cop.Operands, f.spec); ok {
		cop.Operands = fixed
		*changed = true
	}
	return cop, keep
}

// --- 6.2.4 sample depth ---

// imageSampleDepthFixer remediates ImageBitsPerComponent: PDF/A-1 allows 1, 2,
// 4 or 8 bits per component, PDF/A-2 also 16, and other depths turn up in
// real documents.
// The samples are decoded and written back at 8 bits, which is the depth the
// standard allows and the one a viewer shows anyway; the alternative -- leaving
// it -- ends with the page rasterized around it.
//
// It also clears the object model's matching finding on the same key, since the
// enumeration it fails is the same rule.
type imageSampleDepthFixer struct{ spec *pdfa.Spec }

func (imageSampleDepthFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Image.ImageBitsPerComponent
}

func (f imageSampleDepthFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Image"}) {
			return false
		}
		if mask, _ := d.Entries.Get("ImageMask").(pdf.PDFBoolean); bool(mask) {
			return false // an image mask is 6.2.4-6's business, always 1 bit
		}
		switch bpc, _ := d.Entries.Get("BitsPerComponent").(pdf.PDFInteger); {
		case bpc == 0:
			return false // absent: nothing to repair
		case f.spec.BitsPerComponentAllowed(int(bpc)):
			return false
		}
		canvas, err := raster.DecodeImageRGBA(d, pdf.PDFDict{})
		if err != nil {
			return false
		}
		fixed := d
		setStreamRGBFlate(&fixed, canvas)
		fixed.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
		fixed.Entries.Set("Width", pdf.PDFInteger(canvas.Bounds().Dx()))
		fixed.Entries.Set("Height", pdf.PDFInteger(canvas.Bounds().Dy()))
		// The samples are now device RGB, whatever they were converted from, so
		// the space they name has to say that rather than the profile they came
		// through.
		fixed.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
		fixed.Entries.Del("Decode")
		changed = true
		return true
	})
	return changed, nil
}

// --- 6.2.5 / 6.2.7 PostScript XObjects ---

// postScriptXObjectFixer remediates the PostScript XObject checks, mirroring
// the Form and PS cases of validateXObjectDict in checks_dict.go. A PostScript
// XObject (Subtype /PS) is neutered into an empty Form XObject: PDF viewers
// never render PS passthrough content, so the visual output is unchanged.
type postScriptXObjectFixer struct{}

func (postScriptXObjectFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Image.FormPSEntry, pdf.Checks.Image.FormPostScript,
		pdf.Checks.Image.FormSubtype2PS, pdf.Checks.Image.PostScriptXObject:
		return true
	}
	return false
}

func (postScriptXObjectFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		switch d.Entries.Get("Subtype") {
		case pdf.PDFName{Value: "Form"}:
			hit := false
			if _, ok := d.Entries.Lookup("PS"); ok {
				d.Entries.Del("PS")
				hit = true
			}
			if (d.Entries.Get("Subtype2") == pdf.PDFName{Value: "PS"}) {
				d.Entries.Del("Subtype2")
				hit = true
			}
			if hit {
				changed = true
			}
			return hit
		case pdf.PDFName{Value: "PS"}:
			// PS passthrough renders nothing in a PDF viewer, so replace the
			// object with an empty Form XObject rather than trying to preserve
			// its (encoded) PostScript body. Writing empty uncompressed content
			// keeps stream framing and /Filter consistent.
			d.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
			d.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
			d.Entries.Set("FormType", pdf.PDFInteger(1))
			if _, ok := d.Entries.Get("BBox").(pdf.PDFArray); !ok {
				d.Entries.Set("BBox", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0)})
			}
			d.Entries.Del("Filter")
			d.Entries.Del("DecodeParms")
			d.Entries.Del("DP")
			d.Entries.Del("Level1")
			d.SetStream([]byte("\n"))
			changed = true
			return true
		}
		return false
	})
	return changed, nil
}

// --- 6.1.2 ViewerPreferences (post-1.4 keys) ---

// viewerPrefFixer removes the ViewerPreferences keys the level's PDF version
// does not define.
type viewerPrefFixer struct{ spec *pdfa.Spec }

func (viewerPrefFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.PostPDF14ViewerPref
}

func (f viewerPrefFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	vp, ok := root.Entries.Get("ViewerPreferences").(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	changed := false
	for _, k := range f.spec.ForbiddenViewerPrefKeys.All() {
		if vp.Entries.Get(k) != nil {
			vp.Entries.Del(k)
			changed = true
		}
	}
	return changed, nil
}
