package verify

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// HasAnyKey reports whether dict v contains any of the given keys.
func HasAnyKey(v pdf.PDFDict, keys ...string) bool {
	for _, k := range keys {
		if v.Entries[k] != nil {
			return true
		}
	}
	return false
}

// AllowedIntents are the permitted rendering-intent names (6.2.9).
var AllowedIntents = map[string]bool{
	"AbsoluteColorimetric": true, "RelativeColorimetric": true,
	"Saturation": true, "Perceptual": true,
}

// AsFloat returns the numeric value of a PDF integer or real.
func AsFloat(v pdf.PDFValue) (float64, bool) {
	switch n := v.(type) {
	case pdf.PDFInteger:
		return float64(n), true
	case pdf.PDFReal:
		return float64(n), true
	}
	return 0, false
}

func Abs64(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
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

// ForbiddenActions lists action types not permitted in PDF/A-1b (6.6.1).
var ForbiddenActions = map[string]bool{
	"Launch": true, "Sound": true, "Movie": true, "ResetForm": true,
	"ImportData": true, "JavaScript": true, "Hide": true, "SetOCGState": true,
	"Rendition": true, "Trans": true, "GoTo3DView": true, "SetState": true, "NOP": true,
}

// AllowedNamedActions are the only permitted Named action names.
var AllowedNamedActions = map[string]bool{
	"NextPage": true, "PrevPage": true, "FirstPage": true, "LastPage": true,
}

// validateActions checks an action dictionary for forbidden action types (6.6.1).
func validateActions(v pdf.PDFDict, ctx *ValidationContext) {
	s, ok := v.Entries["S"].(pdf.PDFName)
	if !ok || !ActionTypes[s.Value] {
		return
	}
	if ForbiddenActions[s.Value] {
		ctx.Report(pdf.Checks.Action.ForbiddenActionType, v, fmt.Sprintf("forbidden action type /%s", s.Value))
		return
	}
	if s.Value == "Named" {
		n, ok := v.Entries["N"].(pdf.PDFName)
		if !ok || !AllowedNamedActions[n.Value] {
			ctx.Report(pdf.Checks.Action.DisallowedNamedAction, v, "named action with disallowed name")
		}
	}
}

// validateAdditionalActions flags presence of an additional-actions dictionary (6.6.2).
func validateAdditionalActions(v pdf.PDFDict, ctx *ValidationContext) {
	if v.Entries["AA"] != nil {
		ctx.Report(pdf.Checks.Action.AdditionalActions, v, "additional-actions (AA) dictionary not allowed")
	}
}

// --- 6.2.8 Extended graphics state ---

// validateExtGState checks an ExtGState dictionary for forbidden transfer
// functions (6.2.8) and transparency-related keys (6.4).
func validateExtGState(v pdf.PDFDict, ctx *ValidationContext) {
	// A dict with a Type other than ExtGState (Annot, XObject, Page, ...) is
	// handled by another
	if t, ok := v.Entries["Type"].(pdf.PDFName); ok && t.Value != "ExtGState" {
		return
	}
	if !HasAnyKey(v, "TR", "TR2", "SMask", "BM", "CA", "ca", "RI") {
		return
	}

	if v.Entries["TR"] != nil {
		ctx.Report(pdf.Checks.Transparency.TransferFunction, v, "ExtGState shall not contain a TR key")
	}
	if tr2, ok := v.Entries["TR2"]; ok {
		if name, isName := tr2.(pdf.PDFName); !isName || name.Value != "Default" {
			ctx.Report(pdf.Checks.Transparency.DefaultTransferFunction, v, "ExtGState shall not contain a TR2 key other than /Default")
		}
	}
	// 6.2.8: RI must be a standard rendering intent (distinct from the 6.2.9
	// check on the content-stream `ri` operator).
	if ri, ok := v.Entries["RI"].(pdf.PDFName); ok && !AllowedIntents[ri.Value] {
		ctx.Report(pdf.Checks.Transparency.ExtGStateRenderingIntent, v, fmt.Sprintf("ExtGState rendering intent /%s is not a standard rendering intent", ri.Value))
	}

	// 6.4: transparency soft masks, blend modes and non-opaque alpha.
	if sm, ok := v.Entries["SMask"]; ok {
		if name, isName := sm.(pdf.PDFName); !isName || name.Value != "None" {
			ctx.Report(pdf.Checks.Transparency.SoftMaskExtGState, v, "ExtGState SMask shall be /None")
		}
	}
	if bm, ok := v.Entries["BM"]; ok && !IsAllowedBlendMode(bm) {
		ctx.Report(pdf.Checks.Transparency.BlendMode, v, "ExtGState uses a non-Normal blend mode")
	}
	if ca, ok := v.Entries["CA"]; ok {
		// Allow values within 1e-5 of 1.0 to handle floating-point rounding
		// (e.g. 1.0000001 or 0.9999999 round to 1.0 in practice).
		if f, num := AsFloat(ca); num && Abs64(f-1.0) > 1e-5 {
			ctx.Report(pdf.Checks.Transparency.StrokingAlpha, v, "ExtGState stroking alpha (CA) shall be 1.0")
		}
	}
	if ca, ok := v.Entries["ca"]; ok {
		if f, num := AsFloat(ca); num && Abs64(f-1.0) > 1e-5 {
			ctx.Report(pdf.Checks.Transparency.NonStrokingAlpha, v, "ExtGState non-stroking alpha (ca) shall be 1.0")
		}
	}
}

// IsAllowedBlendMode reports whether a /BM value is permitted (Normal/Compatible only).
func IsAllowedBlendMode(bm pdf.PDFValue) bool {
	switch v := bm.(type) {
	case pdf.PDFName:
		return v.Value == "Normal" || v.Value == "Compatible"
	case pdf.PDFArray:
		for _, item := range v {
			name, ok := item.(pdf.PDFName)
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
func validateTransparencyGroup(v pdf.PDFDict, ctx *ValidationContext) {
	group, ok := v.Entries["Group"].(pdf.PDFDict)
	if !ok {
		return
	}
	if (group.Entries["S"] == pdf.PDFName{Value: "Transparency"}) {
		ctx.Report(pdf.Checks.Transparency.TransparencyGroup, v, "transparency group (/S /Transparency) not allowed")
	}
}

// --- 6.2.4 Images / 6.2.5-6.2.7 XObjects ---

// validateXObjectDict checks image and form/PostScript XObjects.
func validateXObjectDict(v pdf.PDFDict, ctx *ValidationContext) {
	subtype, ok := v.Entries["Subtype"].(pdf.PDFName)
	if !ok {
		return
	}

	switch subtype.Value {
	case "Image":
		if b, ok := v.Entries["Interpolate"].(pdf.PDFBoolean); ok && bool(b) {
			ctx.Report(pdf.Checks.Image.ImageInterpolate, v, "image Interpolate shall not be true")
		}
		if v.Entries["Alternates"] != nil {
			ctx.Report(pdf.Checks.Image.ImageAlternates, v, "image shall not contain Alternates")
		}
		if v.Entries["OPI"] != nil {
			ctx.Report(pdf.Checks.Image.ImageOPI, v, "image shall not contain OPI")
		}
		if intent, ok := v.Entries["Intent"].(pdf.PDFName); ok && !AllowedIntents[intent.Value] {
			ctx.Report(pdf.Checks.Image.ImageRenderingIntent, v, fmt.Sprintf("image uses invalid rendering intent /%s", intent.Value))
		}
		// 6.4: soft-masked images introduce transparency.
		if sm, ok := v.Entries["SMask"]; ok {
			if name, isName := sm.(pdf.PDFName); !isName || name.Value != "None" {
				ctx.Report(pdf.Checks.Transparency.ImageWithSoftMask, v, "image shall not contain a soft mask (SMask)")
			}
		}
	case "Form":
		// Lenient profiles (PDFA_1B) skip unreachable Form XObjects; strict
		// profiles (Legacy_1B) treat every Form XObject as reachable.
		if !ctx.isReachableXObject(v) {
			return
		}
		if v.Entries["Ref"] != nil {
			ctx.Report(pdf.Checks.Image.ReferenceXObject, v, "reference XObject (/Ref) not allowed")
		}
		if v.Entries["OPI"] != nil {
			ctx.Report(pdf.Checks.Image.FormOPI, v, "form XObject shall not contain OPI")
		}
		if v.Entries["PS"] != nil {
			// Reported under both clauses; filterByProfile picks the active one
			// (6.2.7/1 strict/Isartor, 6.2.5/3 lenient/veraPDF).
			ctx.Report(pdf.Checks.Image.FormPostScript, v, "form XObject shall not contain PostScript (PS)")
			ctx.Report(pdf.Checks.Image.FormPSEntry, v, "form XObject shall not contain PostScript passthrough (PS)")
		}
		if v.Entries["Subtype2"] == (pdf.PDFName{Value: "PS"}) {
			ctx.Report(pdf.Checks.Image.FormSubtype2PS, v, "form XObject shall not have Subtype2=PS")
		}
	case "PS":
		ctx.Report(pdf.Checks.Image.PostScriptXObject, v, "PostScript XObject not allowed")
	}
}

// --- 6.5 Annotations ---

// AllowedAnnotationTypes are the annotation subtypes permitted by PDF/A-1b (6.5.2).
var AllowedAnnotationTypes = map[string]bool{
	"Text": true, "Link": true, "FreeText": true, "Line": true, "Square": true,
	"Circle": true, "Highlight": true, "Underline": true, "Squiggly": true,
	"StrikeOut": true, "Stamp": true, "Ink": true, "Popup": true, "Widget": true,
	"PrinterMark": true, "TrapNet": true,
}

// Annotation flag bits (PDF 32000 12.5.3).
const (
	AnnotFlagInvisible = 1 << 0
	AnnotFlagHidden    = 1 << 1
	AnnotFlagPrint     = 1 << 2
	AnnotFlagNoView    = 1 << 5
)

// validateAnnotation checks annotation types (6.5.2) and annotation
// dictionaries (6.5.3).
func validateAnnotation(v pdf.PDFDict, ctx *ValidationContext) {
	if (v.Entries["Type"] != pdf.PDFName{Value: "Annot"}) {
		return
	}

	subtype, _ := v.Entries["Subtype"].(pdf.PDFName)

	if !AllowedAnnotationTypes[subtype.Value] {
		ctx.Report(pdf.Checks.Annotation.DisallowedSubtype, v, fmt.Sprintf("annotation subtype /%s not allowed", subtype.Value))
		return
	}

	flags := 0
	if f, ok := v.Entries["F"].(pdf.PDFInteger); ok {
		flags = int(f)
	}
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

	if ca, ok := v.Entries["CA"]; ok {
		if f, num := AsFloat(ca); num && f != 1.0 {
			ctx.Report(pdf.Checks.Annotation.OpacityNotOne, v, "annotation opacity (CA) shall be 1.0")
		}
	}

	checkAnnotColour(v, v.Entries["C"], ctx)
	checkAnnotColour(v, v.Entries["IC"], ctx)

	// 6.5.3: appearance dictionary, where present, shall contain only N, an
	// appearance stream. Non-Popup/Link annotations require an appearance.
	ap, hasAP := v.Entries["AP"].(pdf.PDFDict)
	isFormField := v.Entries["FT"] != nil
	switch {
	case !hasAP:
		if subtype.Value != "Popup" && subtype.Value != "Link" {
			if isFormField {
				// Missing AP on a form-field widget is a 6.9 violation, not 6.5.3.
				ctx.Report(pdf.Checks.Form.WidgetMissingAppearance, v, "form field widget annotation lacks an appearance dictionary (AP)")
			} else {
				ctx.Report(pdf.Checks.Annotation.MissingAppearance, v, "annotation lacks a normal (N) appearance stream")
			}
		}
	default:
		n, hasN := ap.Entries["N"]
		if !hasN {
			ctx.Report(pdf.Checks.Annotation.AppearanceMissingN, v, "appearance dictionary has no N entry")
		}
		for k := range ap.Entries {
			if k != "N" && k != "_ref" {
				ctx.Report(pdf.Checks.Annotation.AppearanceExtraEntries, v, fmt.Sprintf("appearance dictionary has entry other than N: %s", k))
				break
			}
		}
		if hasN {
			isBtn := v.Entries["FT"] == (pdf.PDFName{Value: "Btn"})
			if nd, ok := n.(pdf.PDFDict); !ok {
				ctx.Report(pdf.Checks.Annotation.AppearanceNNotStream, v, "appearance N value is not a stream or subdictionary")
			} else if isBtn {
				// Btn widget N shall be a state-name-to-stream subdictionary, not direct.
				if nd.HasStream {
					ctx.Report(pdf.Checks.Annotation.AppearanceNNotStream, v, "Btn widget appearance N shall be a subdictionary, not a direct stream")
				}
			} else if !nd.HasStream {
				ctx.Report(pdf.Checks.Annotation.AppearanceNNotStream, v, "appearance N value is not a stream")
			}
		}
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

// validateFormField flags actions on interactive form fields / widget
// annotations (6.9).
func validateFormField(v pdf.PDFDict, ctx *ValidationContext) {
	isWidget := v.Entries["Type"] == pdf.PDFName{Value: "Annot"} &&
		v.Entries["Subtype"] == pdf.PDFName{Value: "Widget"}
	isField := v.Entries["FT"] != nil
	if !isWidget && !isField {
		return
	}
	if v.Entries["A"] != nil {
		ctx.Report(pdf.Checks.Form.FieldAction, v, "form field shall not contain an A action")
	}
	if v.Entries["AA"] != nil {
		ctx.Report(pdf.Checks.Form.FieldAdditionalActions, v, "form field shall not contain AA additional actions")
	}
}

// --- 6.9 Interactive forms ---

// verifyInteractiveForms checks the AcroForm dictionary (6.9).
func verifyInteractiveForms(d *pdf.Reader) []pdf.PDFError {
	value, err := d.ResolveGraphByPath([]string{"Root", "AcroForm"})
	if err != nil || value == nil {
		return nil
	}
	form, ok := value.(pdf.PDFDict)
	if !ok {
		return nil
	}

	errs := []pdf.PDFError{}
	if na, ok := form.Entries["NeedAppearances"].(pdf.PDFBoolean); ok && bool(na) {
		errs = append(errs, pdf.NewError(pdf.Checks.Form.NeedAppearances, []error{fmt.Errorf("AcroForm NeedAppearances shall not be true")}, 0, nil))
	}
	if form.Entries["XFA"] != nil {
		errs = append(errs, pdf.NewError(pdf.Checks.Form.XFA, []error{fmt.Errorf("AcroForm shall not contain XFA")}, 0, nil))
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}
