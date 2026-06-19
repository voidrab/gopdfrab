package pdfrab

import "fmt"

// hasAnyKey reports whether dict v contains any of the given keys.
func hasAnyKey(v PDFDict, keys ...string) bool {
	for _, k := range keys {
		if v.Entries[k] != nil {
			return true
		}
	}
	return false
}

// allowedIntents are the permitted rendering-intent names (6.2.9).
var allowedIntents = map[string]bool{
	"AbsoluteColorimetric": true, "RelativeColorimetric": true,
	"Saturation": true, "Perceptual": true,
}

// asFloat returns the numeric value of a PDF integer or real.
func asFloat(v PDFValue) (float64, bool) {
	switch n := v.(type) {
	case PDFInteger:
		return float64(n), true
	case PDFReal:
		return float64(n), true
	}
	return 0, false
}

func abs64(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// --- 6.6 Actions ---

// actionTypes is the set of /S values that identify an action dictionary.
var actionTypes = map[string]bool{
	"GoTo": true, "GoToR": true, "GoToE": true, "Launch": true, "Thread": true,
	"URI": true, "Sound": true, "Movie": true, "Hide": true, "Named": true,
	"SubmitForm": true, "ResetForm": true, "ImportData": true, "JavaScript": true,
	"SetOCGState": true, "Rendition": true, "Trans": true, "GoTo3DView": true,
	"SetState": true, "NOP": true,
}

// forbiddenActions lists action types not permitted in PDF/A-1b (6.6.1).
var forbiddenActions = map[string]bool{
	"Launch": true, "Sound": true, "Movie": true, "ResetForm": true,
	"ImportData": true, "JavaScript": true, "Hide": true, "SetOCGState": true,
	"Rendition": true, "Trans": true, "GoTo3DView": true, "SetState": true, "NOP": true,
}

// allowedNamedActions are the only permitted Named action names.
var allowedNamedActions = map[string]bool{
	"NextPage": true, "PrevPage": true, "FirstPage": true, "LastPage": true,
}

// validateActions checks an action dictionary for forbidden action types (6.6.1).
func validateActions(v PDFDict, ctx *ValidationContext) {
	s, ok := v.Entries["S"].(PDFName)
	if !ok || !actionTypes[s.Value] {
		return
	}
	if forbiddenActions[s.Value] {
		ctx.Report(Checks.Action.ForbiddenActionType, v, fmt.Sprintf("forbidden action type /%s", s.Value))
		return
	}
	if s.Value == "Named" {
		n, ok := v.Entries["N"].(PDFName)
		if !ok || !allowedNamedActions[n.Value] {
			ctx.Report(Checks.Action.DisallowedNamedAction, v, "named action with disallowed name")
		}
	}
}

// validateAdditionalActions flags presence of an additional-actions dictionary (6.6.2).
func validateAdditionalActions(v PDFDict, ctx *ValidationContext) {
	if v.Entries["AA"] != nil {
		ctx.Report(Checks.Action.AdditionalActions, v, "additional-actions (AA) dictionary not allowed")
	}
}

// --- 6.2.8 Extended graphics state ---

// validateExtGState checks an ExtGState dictionary for forbidden transfer
// functions (6.2.8) and transparency-related keys (6.4).
func validateExtGState(v PDFDict, ctx *ValidationContext) {
	// A dict with a Type other than ExtGState (Annot, XObject, Page, ...) is
	// handled by another check.
	if t, ok := v.Entries["Type"].(PDFName); ok && t.Value != "ExtGState" {
		return
	}
	if !hasAnyKey(v, "TR", "TR2", "SMask", "BM", "CA", "ca", "RI") {
		return
	}

	if v.Entries["TR"] != nil {
		ctx.Report(Checks.Transparency.TransferFunction, v, "ExtGState shall not contain a TR key")
	}
	if tr2, ok := v.Entries["TR2"]; ok {
		if name, isName := tr2.(PDFName); !isName || name.Value != "Default" {
			ctx.Report(Checks.Transparency.DefaultTransferFunction, v, "ExtGState shall not contain a TR2 key other than /Default")
		}
	}
	// 6.2.8: RI must be a standard rendering intent (distinct from the 6.2.9
	// check on the content-stream `ri` operator).
	if ri, ok := v.Entries["RI"].(PDFName); ok && !allowedIntents[ri.Value] {
		ctx.Report(Checks.Transparency.ExtGStateRenderingIntent, v, fmt.Sprintf("ExtGState rendering intent /%s is not a standard rendering intent", ri.Value))
	}

	// 6.4: transparency soft masks, blend modes and non-opaque alpha.
	if sm, ok := v.Entries["SMask"]; ok {
		if name, isName := sm.(PDFName); !isName || name.Value != "None" {
			ctx.Report(Checks.Transparency.SoftMaskExtGState, v, "ExtGState SMask shall be /None")
		}
	}
	if bm, ok := v.Entries["BM"]; ok && !isAllowedBlendMode(bm) {
		ctx.Report(Checks.Transparency.BlendMode, v, "ExtGState uses a non-Normal blend mode")
	}
	if ca, ok := v.Entries["CA"]; ok {
		// Allow values within 1e-5 of 1.0 to handle floating-point rounding
		// (e.g. 1.0000001 or 0.9999999 round to 1.0 in practice).
		if f, num := asFloat(ca); num && abs64(f-1.0) > 1e-5 {
			ctx.Report(Checks.Transparency.StrokingAlpha, v, "ExtGState stroking alpha (CA) shall be 1.0")
		}
	}
	if ca, ok := v.Entries["ca"]; ok {
		if f, num := asFloat(ca); num && abs64(f-1.0) > 1e-5 {
			ctx.Report(Checks.Transparency.NonStrokingAlpha, v, "ExtGState non-stroking alpha (ca) shall be 1.0")
		}
	}
}

// isAllowedBlendMode reports whether a /BM value is permitted (Normal/Compatible only).
func isAllowedBlendMode(bm PDFValue) bool {
	switch v := bm.(type) {
	case PDFName:
		return v.Value == "Normal" || v.Value == "Compatible"
	case PDFArray:
		for _, item := range v {
			name, ok := item.(PDFName)
			if !ok || (name.Value != "Normal" && name.Value != "Compatible") {
				return false
			}
		}
		return true
	}
	return false
}

// --- 6.4 Transparency groups ---

// validateTransparencyGroup flags a transparency group attribute dictionary (6.4).
func validateTransparencyGroup(v PDFDict, ctx *ValidationContext) {
	group, ok := v.Entries["Group"].(PDFDict)
	if !ok {
		return
	}
	if (group.Entries["S"] == PDFName{Value: "Transparency"}) {
		ctx.Report(Checks.Transparency.TransparencyGroup, v, "transparency group (/S /Transparency) not allowed")
	}
}

// --- 6.2.4 Images / 6.2.5-6.2.7 XObjects ---

// validateXObjectDict checks image and form/PostScript XObjects.
func validateXObjectDict(v PDFDict, ctx *ValidationContext) {
	subtype, ok := v.Entries["Subtype"].(PDFName)
	if !ok {
		return
	}

	switch subtype.Value {
	case "Image":
		if b, ok := v.Entries["Interpolate"].(PDFBoolean); ok && bool(b) {
			ctx.Report(Checks.Image.ImageInterpolate, v, "image Interpolate shall not be true")
		}
		if v.Entries["Alternates"] != nil {
			ctx.Report(Checks.Image.ImageAlternates, v, "image shall not contain Alternates")
		}
		if v.Entries["OPI"] != nil {
			ctx.Report(Checks.Image.ImageOPI, v, "image shall not contain OPI")
		}
		if intent, ok := v.Entries["Intent"].(PDFName); ok && !allowedIntents[intent.Value] {
			ctx.Report(Checks.Image.ImageRenderingIntent, v, fmt.Sprintf("image uses invalid rendering intent /%s", intent.Value))
		}
		// 6.4: soft-masked images introduce transparency.
		if sm, ok := v.Entries["SMask"]; ok {
			if name, isName := sm.(PDFName); !isName || name.Value != "None" {
				ctx.Report(Checks.Transparency.ImageWithSoftMask, v, "image shall not contain a soft mask (SMask)")
			}
		}
	case "Form":
		// Lenient profiles (PDFA_1B) skip unreachable Form XObjects; strict
		// profiles (Legacy_1B) treat every Form XObject as reachable.
		if !ctx.isReachableXObject(v) {
			return
		}
		if v.Entries["Ref"] != nil {
			ctx.Report(Checks.Image.ReferenceXObject, v, "reference XObject (/Ref) not allowed")
		}
		if v.Entries["OPI"] != nil {
			ctx.Report(Checks.Image.FormOPI, v, "form XObject shall not contain OPI")
		}
		if v.Entries["PS"] != nil {
			// Reported under both clauses; filterByProfile picks the active one
			// (6.2.7/1 strict/Isartor, 6.2.5/3 lenient/veraPDF).
			ctx.Report(Checks.Image.FormPostScript, v, "form XObject shall not contain PostScript (PS)")
			ctx.Report(Checks.Image.FormPSEntry, v, "form XObject shall not contain PostScript passthrough (PS)")
		}
		if v.Entries["Subtype2"] == (PDFName{Value: "PS"}) {
			ctx.Report(Checks.Image.FormSubtype2PS, v, "form XObject shall not have Subtype2=PS")
		}
	case "PS":
		ctx.Report(Checks.Image.PostScriptXObject, v, "PostScript XObject not allowed")
	}
}

// --- 6.5 Annotations ---

// allowedAnnotationTypes are the annotation subtypes permitted by PDF/A-1b (6.5.2).
var allowedAnnotationTypes = map[string]bool{
	"Text": true, "Link": true, "FreeText": true, "Line": true, "Square": true,
	"Circle": true, "Highlight": true, "Underline": true, "Squiggly": true,
	"StrikeOut": true, "Stamp": true, "Ink": true, "Popup": true, "Widget": true,
	"PrinterMark": true, "TrapNet": true,
}

// Annotation flag bits (PDF 32000 12.5.3).
const (
	annotFlagInvisible = 1 << 0
	annotFlagHidden    = 1 << 1
	annotFlagPrint     = 1 << 2
	annotFlagNoView    = 1 << 5
)

// validateAnnotation checks annotation types (6.5.2) and annotation
// dictionaries (6.5.3).
func validateAnnotation(v PDFDict, ctx *ValidationContext) {
	if (v.Entries["Type"] != PDFName{Value: "Annot"}) {
		return
	}

	subtype, _ := v.Entries["Subtype"].(PDFName)

	if !allowedAnnotationTypes[subtype.Value] {
		ctx.Report(Checks.Annotation.DisallowedSubtype, v, fmt.Sprintf("annotation subtype /%s not allowed", subtype.Value))
		return
	}

	flags := 0
	if f, ok := v.Entries["F"].(PDFInteger); ok {
		flags = int(f)
	}
	if flags&annotFlagPrint == 0 {
		ctx.Report(Checks.Annotation.PrintFlagNotSet, v, "annotation Print flag shall be set")
	}
	if flags&annotFlagHidden != 0 {
		ctx.Report(Checks.Annotation.HiddenFlagSet, v, "annotation Hidden flag shall be clear")
	}
	if flags&annotFlagInvisible != 0 {
		ctx.Report(Checks.Annotation.InvisibleFlagSet, v, "annotation Invisible flag shall be clear")
	}
	if flags&annotFlagNoView != 0 {
		ctx.Report(Checks.Annotation.NoViewFlagSet, v, "annotation NoView flag shall be clear")
	}

	if ca, ok := v.Entries["CA"]; ok {
		if f, num := asFloat(ca); num && f != 1.0 {
			ctx.Report(Checks.Annotation.OpacityNotOne, v, "annotation opacity (CA) shall be 1.0")
		}
	}

	checkAnnotColour(v, v.Entries["C"], ctx)
	checkAnnotColour(v, v.Entries["IC"], ctx)

	// 6.5.3: appearance dictionary, where present, shall contain only N, an
	// appearance stream. Non-Popup/Link annotations require an appearance.
	ap, hasAP := v.Entries["AP"].(PDFDict)
	isFormField := v.Entries["FT"] != nil
	switch {
	case !hasAP:
		if subtype.Value != "Popup" && subtype.Value != "Link" {
			if isFormField {
				// Missing AP on a form-field widget is a 6.9 violation, not 6.5.3.
				ctx.Report(Checks.Form.WidgetMissingAppearance, v, "form field widget annotation lacks an appearance dictionary (AP)")
			} else {
				ctx.Report(Checks.Annotation.MissingAppearance, v, "annotation lacks a normal (N) appearance stream")
			}
		}
	default:
		n, hasN := ap.Entries["N"]
		if !hasN {
			ctx.Report(Checks.Annotation.AppearanceMissingN, v, "appearance dictionary has no N entry")
		}
		for k := range ap.Entries {
			if k != "N" && k != "_ref" {
				ctx.Report(Checks.Annotation.AppearanceExtraEntries, v, fmt.Sprintf("appearance dictionary has entry other than N: %s", k))
				break
			}
		}
		if hasN {
			isBtn := v.Entries["FT"] == (PDFName{Value: "Btn"})
			if nd, ok := n.(PDFDict); !ok {
				ctx.Report(Checks.Annotation.AppearanceNNotStream, v, "appearance N value is not a stream or subdictionary")
			} else if isBtn {
				// Btn widget N shall be a state-name-to-stream subdictionary, not direct.
				if nd.HasStream {
					ctx.Report(Checks.Annotation.AppearanceNNotStream, v, "Btn widget appearance N shall be a subdictionary, not a direct stream")
				}
			} else if !nd.HasStream {
				ctx.Report(Checks.Annotation.AppearanceNNotStream, v, "appearance N value is not a stream")
			}
		}
	}
}

// checkAnnotColour flags an annotation C/IC colour array whose device model is
// not covered by an output intent (6.5.3).
func checkAnnotColour(v PDFDict, c PDFValue, ctx *ValidationContext) {
	arr, ok := c.(PDFArray)
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
		ctx.Report(Checks.Annotation.ColourWithoutIntent, v, fmt.Sprintf("annotation colour (%s) without matching OutputIntent", model))
	}
}

// --- 6.9 Interactive form fields ---

// validateFormField flags actions on interactive form fields / widget
// annotations (6.9).
func validateFormField(v PDFDict, ctx *ValidationContext) {
	isWidget := v.Entries["Type"] == PDFName{Value: "Annot"} &&
		v.Entries["Subtype"] == PDFName{Value: "Widget"}
	isField := v.Entries["FT"] != nil
	if !isWidget && !isField {
		return
	}
	if v.Entries["A"] != nil {
		ctx.Report(Checks.Form.FieldAction, v, "form field shall not contain an A action")
	}
	if v.Entries["AA"] != nil {
		ctx.Report(Checks.Form.FieldAdditionalActions, v, "form field shall not contain AA additional actions")
	}
}

// --- 6.9 Interactive forms ---

// verifyInteractiveForms checks the AcroForm dictionary (6.9).
func (d *Document) verifyInteractiveForms() []PDFError {
	value, err := d.ResolveGraphByPath([]string{"Root", "AcroForm"})
	if err != nil || value == nil {
		return nil
	}
	form, ok := value.(PDFDict)
	if !ok {
		return nil
	}

	errs := []PDFError{}
	if na, ok := form.Entries["NeedAppearances"].(PDFBoolean); ok && bool(na) {
		errs = append(errs, PDFError{
			check: Checks.Form.NeedAppearances,
			errs:  []error{fmt.Errorf("AcroForm NeedAppearances shall not be true")},
			page:  0,
		})
	}
	if form.Entries["XFA"] != nil {
		errs = append(errs, PDFError{
			check: Checks.Form.XFA,
			errs:  []error{fmt.Errorf("AcroForm shall not contain XFA")},
			page:  0,
		})
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}
