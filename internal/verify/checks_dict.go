package verify

import (
	"fmt"
	"math"
	"sort"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// HasAnyKey reports whether dict v contains any of the given keys.
func HasAnyKey(v pdf.PDFDict, keys ...string) bool {
	for _, k := range keys {
		if v.Entries.Get(k) != nil {
			return true
		}
	}
	return false
}

// --- 6.6 Actions ---

// ActionTypes is the set of /S values that identify an action dictionary.
var ActionTypes = map[string]bool{
	"GoTo": true, "GoToR": true, "GoToE": true, "Launch": true, "Thread": true,
	"URI": true, "Sound": true, "Movie": true, "Hide": true, "Named": true,
	"SubmitForm": true, "ResetForm": true, "ImportData": true, "JavaScript": true,
	"SetOCGState": true, "Rendition": true, "Trans": true, "GoTo3DView": true,
	"SetState": true, "NOP": true,
}

// ActionOwnerKey returns the entry of d whose value is an action dictionary,
// or "" when d owns no action. The key alone does not settle it: /A on a
// structure element is an attribute dictionary and /Next on an outline item is
// a sibling, so only an annotation's /A and the catalogue's /OpenAction count.
// Exported for the fixer.
func ActionOwnerKey(d pdf.PDFDict) string {
	switch {
	case IsAnnotationDict(d):
		return "A"
	case d.Entries.Get("Type") == (pdf.PDFName{Value: "Catalog"}):
		return "OpenAction"
	}
	return ""
}

// validateActions checks an action dictionary for forbidden action types (6.6.1).
func validateActions(v pdf.PDFDict, ctx *ValidationContext) {
	// /S is required in an action dictionary (ISO 32000-1 Table 193), so a
	// target without one names an action of no known type, which 6.6.1 does not
	// permit either.
	if key := ActionOwnerKey(v); key != "" {
		if target, ok := v.Entries.Get(key).(pdf.PDFDict); ok {
			if _, hasS := target.Entries.Get("S").(pdf.PDFName); !hasS {
				ctx.Report(pdf.Checks.Action.ForbiddenActionType, v,
					fmt.Sprintf("/%s names an action dictionary with no /S", key))
			}
		}
	}

	s, ok := v.Entries.Get("S").(pdf.PDFName)
	if !ok || !ActionTypes[s.Value] {
		return
	}
	if ctx.Spec.ForbiddenActions.Has(s.Value) {
		ctx.Report(pdf.Checks.Action.ForbiddenActionType, v, fmt.Sprintf("forbidden action type /%s", s.Value))
		return
	}
	if s.Value == "Named" {
		n, ok := v.Entries.Get("N").(pdf.PDFName)
		if !ok || !ctx.Spec.NamedActions.Has(n.Value) {
			ctx.Report(pdf.Checks.Action.DisallowedNamedAction, v, "named action with disallowed name")
		}
	}
}

// validateAdditionalActions flags presence of an additional-actions dictionary (6.6.2).
func validateAdditionalActions(v pdf.PDFDict, ctx *ValidationContext) {
	if v.Entries.Get("AA") != nil {
		ctx.Report(pdf.Checks.Action.AdditionalActions, v, "additional-actions (AA) dictionary not allowed")
	}
}

// validateViewerPreferences flags ViewerPreferences keys the level's PDF
// version does not define.
func validateViewerPreferences(v pdf.PDFDict, ctx *ValidationContext) {
	if (v.Entries.Get("Type") != pdf.PDFName{Value: "Catalog"}) {
		return
	}
	vp, ok := v.Entries.Get("ViewerPreferences").(pdf.PDFDict)
	if !ok {
		return
	}
	for _, k := range ctx.Spec.ForbiddenViewerPrefKeys.All() {
		if vp.Entries.Get(k) != nil {
			ctx.Report(pdf.Checks.Structure.PostPDF14ViewerPref, v,
				fmt.Sprintf("ViewerPreferences entry /%s requires PDF 1.5 or later", k))
		}
	}
}

// --- 6.2.8 Extended graphics state ---

// validateExtGState checks an ExtGState dictionary for forbidden transfer
// functions (6.2.8) and transparency-related keys (6.4).
func validateExtGState(v pdf.PDFDict, ctx *ValidationContext) {
	// A dict with a Type other than ExtGState (Annot, XObject, Page, ...) is
	// not checked here.
	if t, ok := v.Entries.Get("Type").(pdf.PDFName); ok && t.Value != "ExtGState" {
		return
	}
	if !HasAnyKey(v, "TR", "TR2", "SMask", "BM", "CA", "ca", "RI", "HT", "HTP") {
		return
	}
	validateHalftones(v, ctx)

	if v.Entries.Get("TR") != nil {
		ctx.Report(pdf.Checks.Transparency.TransferFunction, v, "ExtGState shall not contain a TR key")
	}
	if tr2, ok := v.Entries.Lookup("TR2"); ok {
		if name, isName := tr2.(pdf.PDFName); !isName || name.Value != "Default" {
			ctx.Report(pdf.Checks.Transparency.DefaultTransferFunction, v, "ExtGState shall not contain a TR2 key other than /Default")
		}
	}
	// 6.2.8: RI must be a standard rendering intent (distinct from the 6.2.9
	// check on the content-stream `ri` operator).
	if ri, ok := v.Entries.Get("RI").(pdf.PDFName); ok && !ctx.Spec.RenderingIntents.Has(ri.Value) {
		ctx.Report(pdf.Checks.Transparency.ExtGStateRenderingIntent, v, fmt.Sprintf("ExtGState rendering intent /%s is not a standard rendering intent", ri.Value))
	}

	// 6.4: transparency soft masks and non-opaque alpha, in a level without
	// transparency, and blend modes the level does not allow.
	if sm, ok := v.Entries.Lookup("SMask"); ok && !ctx.Spec.Transparency {
		if name, isName := sm.(pdf.PDFName); !isName || name.Value != "None" {
			ctx.Report(pdf.Checks.Transparency.SoftMaskExtGState, v, "ExtGState SMask shall be /None")
		}
	}
	if bm, ok := v.Entries.Lookup("BM"); ok && !ctx.Spec.BlendModeAllowed(bm) {
		ctx.Report(pdf.Checks.Transparency.BlendMode, v, "ExtGState uses a non-Normal blend mode")
	}
	if ctx.Spec.Transparency {
		return
	}
	if ca, ok := v.Entries.Lookup("CA"); ok {
		// Allow values within 1e-5 of 1.0 to handle floating-point rounding
		// (e.g. 1.0000001 or 0.9999999 round to 1.0 in practice).
		if f, num := pdf.PDFNumberToFloat(ca); num && math.Abs(f-1.0) > 1e-5 {
			ctx.Report(pdf.Checks.Transparency.StrokingAlpha, v, "ExtGState stroking alpha (CA) shall be 1.0")
		}
	}
	if ca, ok := v.Entries.Lookup("ca"); ok {
		if f, num := pdf.PDFNumberToFloat(ca); num && math.Abs(f-1.0) > 1e-5 {
			ctx.Report(pdf.Checks.Transparency.NonStrokingAlpha, v, "ExtGState non-stroking alpha (ca) shall be 1.0")
		}
	}
}

// --- 6.4 Transparency groups ---

// validateTransparencyGroup flags a transparency group attribute dictionary
// (6.4) in a level without transparency.
func validateTransparencyGroup(v pdf.PDFDict, ctx *ValidationContext) {
	group, ok := v.Entries.Get("Group").(pdf.PDFDict)
	if !ok || ctx.Spec.Transparency {
		return
	}
	if (group.Entries.Get("S") == pdf.PDFName{Value: "Transparency"}) {
		ctx.Report(pdf.Checks.Transparency.TransparencyGroup, v, "transparency group (/S /Transparency) not allowed")
	}
}

// --- 6.2.4 Images / 6.2.5-6.2.7 XObjects ---

// validateXObjectDict checks image and form/PostScript XObjects.
func validateXObjectDict(v pdf.PDFDict, ctx *ValidationContext) {
	subtype, ok := v.Entries.Get("Subtype").(pdf.PDFName)
	if !ok {
		return
	}

	switch subtype.Value {
	case "Image":
		if b, ok := v.Entries.Get("Interpolate").(pdf.PDFBoolean); ok && bool(b) {
			ctx.Report(pdf.Checks.Image.ImageInterpolate, v, "image Interpolate shall not be true")
		}
		if v.Entries.Get("Alternates") != nil {
			ctx.Report(pdf.Checks.Image.ImageAlternates, v, "image shall not contain Alternates")
		}
		if v.Entries.Get("OPI") != nil {
			ctx.Report(pdf.Checks.Image.ImageOPI, v, "image shall not contain OPI")
		}
		if intent, ok := v.Entries.Get("Intent").(pdf.PDFName); ok && !ctx.Spec.RenderingIntents.Has(intent.Value) {
			ctx.Report(pdf.Checks.Image.ImageRenderingIntent, v, fmt.Sprintf("image uses invalid rendering intent /%s", intent.Value))
		}
		// 6.2.4-4 / 6.2.4-5: BitsPerComponent must be 1, 2, 4 or 8 (or, where
		// the level allows it, 16), and 1 for image masks.
		if bpc, ok := v.Entries.Get("BitsPerComponent").(pdf.PDFInteger); ok {
			isMask, _ := v.Entries.Get("ImageMask").(pdf.PDFBoolean)
			if bool(isMask) {
				if bpc != 1 {
					ctx.Report(pdf.Checks.Image.ImageMaskBitsPerComponent, v, fmt.Sprintf("image mask BitsPerComponent is %d, must be 1", int(bpc)))
				}
			} else if !ctx.Spec.BitsPerComponentAllowed(int(bpc)) {
				allowed := "1, 2, 4, or 8"
				if ctx.Spec.SixteenBitImages {
					allowed = "1, 2, 4, 8 or 16"
				}
				ctx.Report(pdf.Checks.Image.ImageBitsPerComponent, v, fmt.Sprintf("image BitsPerComponent is %d, must be %s", int(bpc), allowed))
			}
		}
		// 6.4: soft-masked images introduce transparency.
		if sm, ok := v.Entries.Lookup("SMask"); ok && !ctx.Spec.Transparency {
			if name, isName := sm.(pdf.PDFName); !isName || name.Value != "None" {
				ctx.Report(pdf.Checks.Transparency.ImageWithSoftMask, v, "image shall not contain a soft mask (SMask)")
			}
		}
	case "Form":
		// Lenient profiles (PDFA1B) skip unreachable Form XObjects; strict
		// profiles (Legacy1B) treat every Form XObject as reachable.
		if !ctx.isReachableXObject(v) {
			return
		}
		if v.Entries.Get("Ref") != nil {
			ctx.Report(pdf.Checks.Image.ReferenceXObject, v, "reference XObject (/Ref) not allowed")
		}
		if v.Entries.Get("OPI") != nil {
			ctx.Report(pdf.Checks.Image.FormOPI, v, "form XObject shall not contain OPI")
		}
		if v.Entries.Get("PS") != nil {
			// Reported under both clauses; filterByProfile picks the active one
			// (6.2.7/1 strict/Isartor, 6.2.5/3 lenient/veraPDF).
			ctx.Report(pdf.Checks.Image.FormPostScript, v, "form XObject shall not contain PostScript (PS)")
			ctx.Report(pdf.Checks.Image.FormPSEntry, v, "form XObject shall not contain PostScript passthrough (PS)")
		}
		if v.Entries.Get("Subtype2") == (pdf.PDFName{Value: "PS"}) {
			ctx.Report(pdf.Checks.Image.FormSubtype2PS, v, "form XObject shall not have Subtype2=PS")
		}
	case "PS":
		// Reachability-gated like Form XObjects: veraPDF passes an
		// unreferenced PostScript XObject and fails a referenced one.
		if !ctx.isReachableXObject(v) {
			return
		}
		ctx.Report(pdf.Checks.Image.PostScriptXObject, v, "PostScript XObject not allowed")
	}
}

// --- 6.5 Annotations ---

// resolveInheritedFT returns the FT value for a widget by walking its Parent chain,
// following AcroForm field-attribute inheritance (ISO 32000-1 12.7.3.4).
func resolveInheritedFT(v pdf.PDFDict) pdf.PDFValue {
	for range 20 {
		if ft := v.Entries.Get("FT"); ft != nil {
			return ft
		}
		parent, ok := v.Entries.Get("Parent").(pdf.PDFDict)
		if !ok {
			return nil
		}
		v = parent
	}
	return nil
}

// Annotation flag bits (PDF 32000 12.5.3).
const (
	AnnotFlagInvisible    = 1 << 0
	AnnotFlagHidden       = 1 << 1
	AnnotFlagPrint        = 1 << 2
	AnnotFlagNoView       = 1 << 5
	AnnotFlagToggleNoView = 1 << 8
)

// AnnotFlags returns the flags of annotation d and whether its F entry is a
// number. A real F is read as the integer it truncates to, as veraPDF does.
func AnnotFlags(d pdf.PDFDict) (int, bool) {
	f, ok := pdf.PDFNumberToFloat(d.Entries.Get("F"))
	return int(f), ok
}

// FlagsRequired reports whether annotation d must have an F key under
// PDF/A-2: every annotation but a Popup one must (ISO 19005-2 6.3.2).
func FlagsRequired(d pdf.PDFDict) bool {
	return d.Entries.Get("Subtype") != pdf.PDFName{Value: "Popup"}
}

// ZeroSizeRect reports whether d's Rect has two equal corners, so the
// annotation covers no area. Under PDF/A-2 such an annotation needs no
// appearance (ISO 19005-2 6.3.3).
func ZeroSizeRect(d pdf.PDFDict) bool {
	v, err := pdf.FloatArray(d.Entries.Get("Rect"))
	return err == nil && len(v) == 4 && v[0] == v[2] && v[1] == v[3]
}

// ButtonAppearances reports whether n, the normal appearance of a Btn
// widget, is an appearance subdictionary holding at least one appearance
// stream, rather than a stream or a subdictionary of none.
func ButtonAppearances(n pdf.PDFDict) bool {
	return !n.IsStream() && len(pdf.AppearanceStreams(n)) > 0
}

// IsAnnotationDict reports whether d is an annotation dictionary. /Type is
// optional on one (ISO 32000-1 12.5.2), and a viewer reaches an annotation
// through a page's /Annots rather than by its /Type, so a dict that declares
// no /Type counts when it carries /Subtype and /Rect, which every annotation
// is required to have. A dict declaring some other /Type is not one.
func IsAnnotationDict(d pdf.PDFDict) bool {
	if t, ok := d.Entries.Get("Type").(pdf.PDFName); ok {
		return t.Value == "Annot"
	}
	_, hasSubtype := d.Entries.Get("Subtype").(pdf.PDFName)
	_, hasRect := d.Entries.Get("Rect").(pdf.PDFArray)
	return hasSubtype && hasRect
}

// validateAnnotation checks annotation types (6.5.2) and annotation
// dictionaries (6.5.3), under PDF/A-2 annotation types (6.3.1), dictionaries
// (6.3.2) and appearances (6.3.3).
func validateAnnotation(v pdf.PDFDict, ctx *ValidationContext) {
	if !IsAnnotationDict(v) {
		return
	}

	subtype, _ := v.Entries.Get("Subtype").(pdf.PDFName)

	// Under PDF/A-2 the rest of a disallowed annotation is checked too, as
	// veraPDF does.
	if !ctx.Spec.AnnotationSubtypes.Has(subtype.Value) {
		ctx.Report(pdf.Checks.Annotation.DisallowedSubtype, v, fmt.Sprintf("annotation subtype /%s not allowed", subtype.Value))
		if !ctx.part2() {
			return
		}
	}

	// Under PDF/A-2 the flag bits are checked only when F is present. A missing
	// F is reported unless FlagsRequired exempts the annotation.
	if f, hasF := AnnotFlags(v); hasF || !ctx.part2() {
		checkAnnotFlags(v, f, ctx)
	} else if FlagsRequired(v) {
		ctx.Report(pdf.Checks.Annotation.AnnotationFlagsMissing, v, "annotation dictionary has no F key")
	}

	if ca, ok := v.Entries.Lookup("CA"); ok {
		if f, num := pdf.PDFNumberToFloat(ca); num && f != 1.0 {
			ctx.Report(pdf.Checks.Annotation.OpacityNotOne, v, "annotation opacity (CA) shall be 1.0")
		}
	}

	checkAnnotColour(v, v.Entries.Get("C"), ctx)
	checkAnnotColour(v, v.Entries.Get("IC"), ctx)

	for _, f := range AppearanceFaults(v, ctx.Spec) {
		ctx.Report(f.Check, v, f.Message)
	}
}

// AppearanceFault is one appearance rule an annotation breaks.
type AppearanceFault struct {
	Check   pdf.Check
	Message string
}

// AppearanceFaults returns the appearance rules that annotation d breaks under
// spec: 6.5.3 and 6.9, or 6.3.3 under PDF/A-2. Apart from a zero-area
// annotation that spec exempts, an annotation other than a Popup or Link one
// needs an appearance dictionary whose only entry is N. N is an appearance
// stream, or for a Btn widget a subdictionary of appearance streams.
func AppearanceFaults(d pdf.PDFDict, spec *pdfa.Spec) []AppearanceFault {
	subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)
	ft := resolveInheritedFT(d)
	ap, hasAP := d.Entries.Get("AP").(pdf.PDFDict)
	if !hasAP {
		if subtype.Value == "Popup" || subtype.Value == "Link" || spec.ZeroAreaAnnotsExempt && ZeroSizeRect(d) {
			return nil
		}
		// Before PDF/A-2, a form field widget without an appearance breaks 6.9
		// rather than 6.5.3. Under PDF/A-2, 6.3.3 applies to every annotation.
		if ft != nil && spec.Part < 2 {
			return []AppearanceFault{{pdf.Checks.Form.WidgetMissingAppearance, "form field widget annotation lacks an appearance dictionary (AP)"}}
		}
		return []AppearanceFault{{pdf.Checks.Annotation.MissingAppearance, "annotation lacks a normal (N) appearance stream"}}
	}

	var faults []AppearanceFault
	n, hasN := ap.Entries.Lookup("N")
	if !hasN {
		faults = append(faults, AppearanceFault{pdf.Checks.Annotation.AppearanceMissingN, "appearance dictionary has no N entry"})
	}
	var extras []string
	for k := range ap.Entries.All() {
		if k != "N" {
			extras = append(extras, k)
		}
	}
	if len(extras) > 0 {
		// Name the lexicographically first extra entry, so that the message
		// does not depend on the order of the entries.
		sort.Strings(extras)
		faults = append(faults, AppearanceFault{pdf.Checks.Annotation.AppearanceExtraEntries,
			fmt.Sprintf("appearance dictionary has entry other than N: %s", extras[0])})
	}
	if !hasN {
		return faults
	}
	nd, ok := n.(pdf.PDFDict)
	switch {
	case !ok:
		faults = append(faults, AppearanceFault{pdf.Checks.Annotation.AppearanceNNotStream, "appearance N value is not a stream or subdictionary"})
	case ft == pdf.PDFName{Value: "Btn"}:
		if !ButtonAppearances(nd) {
			faults = append(faults, AppearanceFault{pdf.Checks.Annotation.AppearanceNNotStream, "Btn widget appearance N shall be a subdictionary of appearance streams"})
		}
	case !nd.IsStream():
		faults = append(faults, AppearanceFault{pdf.Checks.Annotation.AppearanceNNotStream, "appearance N value is not a stream"})
	}
	return faults
}

// IsButtonField reports whether d is a Btn form field, with FT resolved
// through the Parent chain as resolveInheritedFT does.
func IsButtonField(d pdf.PDFDict) bool {
	return resolveInheritedFT(d) == pdf.PDFName{Value: "Btn"}
}

// checkAnnotFlags reports each of the flag bits in flags that the level
// requires set or clear.
func checkAnnotFlags(v pdf.PDFDict, flags int, ctx *ValidationContext) {
	if flags&AnnotFlagPrint == 0 {
		ctx.Report(pdf.Checks.Annotation.PrintFlagNotSet, v, "annotation Print flag shall be set")
	}
	if flags&AnnotFlagHidden != 0 {
		ctx.Report(pdf.Checks.Annotation.HiddenFlagSet, v, "annotation Hidden flag shall be clear")
	}
	if flags&AnnotFlagInvisible != 0 {
		ctx.Report(pdf.Checks.Annotation.InvisibleFlagSet, v, "annotation Invisible flag shall be clear")
	}
	if flags&AnnotFlagNoView != 0 {
		ctx.Report(pdf.Checks.Annotation.NoViewFlagSet, v, "annotation NoView flag shall be clear")
	}
	if flags&AnnotFlagToggleNoView != 0 && ctx.part2() {
		ctx.Report(pdf.Checks.Annotation.ToggleNoViewFlagSet, v, "annotation ToggleNoView flag shall be clear")
	}
}

// checkAnnotColour flags an annotation C/IC colour array whose device model is
// not covered by an output intent (6.5.3).
func checkAnnotColour(v pdf.PDFDict, c pdf.PDFValue, ctx *ValidationContext) {
	arr, ok := c.(pdf.PDFArray)
	if !ok {
		return
	}
	var model string
	switch len(arr) {
	case 1:
		model = "gray"
	case 3:
		model = "rgb"
	case 4:
		model = "cmyk"
	default:
		return
	}
	if !ctx.deviceColourAllowed(model) {
		ctx.Report(pdf.Checks.Annotation.ColourWithoutIntent, v, fmt.Sprintf("annotation colour (%s) without matching OutputIntent", model))
	}
}

// --- 6.9 Interactive form fields ---

// IsFieldOrWidget reports whether d has an FT entry or is a widget annotation.
// The Type entry of a widget annotation is optional.
func IsFieldOrWidget(d pdf.PDFDict) bool {
	if d.Entries.Get("FT") != nil {
		return true
	}
	return IsAnnotationDict(d) && d.Entries.Get("Subtype") == pdf.PDFName{Value: "Widget"}
}

// validateFormField flags actions on interactive form fields and widget
// annotations (6.9, under PDF/A-2 6.4.1).
func validateFormField(v pdf.PDFDict, ctx *ValidationContext) {
	if !IsFieldOrWidget(v) {
		return
	}
	if v.Entries.Get("A") != nil {
		ctx.Report(pdf.Checks.Form.FieldAction, v, "form field shall not contain an A action")
	}
	if v.Entries.Get("AA") != nil {
		ctx.Report(pdf.Checks.Form.FieldAdditionalActions, v, "form field shall not contain AA additional actions")
	}
}

// --- 6.9 Interactive forms ---

// verifyInteractiveForms checks the AcroForm dictionary (6.9, under PDF/A-2
// 6.4.1 and 6.4.2) and, when part2 is set, the catalog's NeedsRendering
// entry (6.4.2).
func verifyInteractiveForms(d *pdf.Reader, part2 bool) []pdf.PDFError {
	// A catalog that does not resolve is no dictionary.
	value, _ := d.ResolveGraphByPath([]string{"Root"})
	root, ok := value.(pdf.PDFDict)
	if !ok {
		return nil
	}

	var errs []pdf.PDFError
	if part2 && NeedsRendering(root) {
		errs = append(errs, pdf.NewError(pdf.Checks.Form.NeedsRendering, []error{fmt.Errorf("catalog NeedsRendering shall be false")}, 0, refOf(root)))
	}
	form, ok := root.Entries.Get("AcroForm").(pdf.PDFDict)
	if !ok {
		return errs
	}
	if na, ok := form.Entries.Get("NeedAppearances").(pdf.PDFBoolean); ok && bool(na) {
		errs = append(errs, pdf.NewError(pdf.Checks.Form.NeedAppearances, []error{fmt.Errorf("AcroForm NeedAppearances shall not be true")}, 0, refOf(form)))
	}
	if form.Entries.Get("XFA") != nil {
		errs = append(errs, pdf.NewError(pdf.Checks.Form.XFA, []error{fmt.Errorf("AcroForm shall not contain XFA")}, 0, refOf(form)))
	}
	return errs
}

// NeedsRendering reports whether the catalog has a NeedsRendering entry
// other than false. A value that is not a boolean also counts.
func NeedsRendering(root pdf.PDFDict) bool {
	v, ok := root.Entries.Lookup("NeedsRendering")
	return ok && v != pdf.PDFBoolean(false)
}
