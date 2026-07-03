package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"

	"github.com/voidrab/gopdfrab/internal/verify"
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

func init() {
	registerFixer(actionFixer{})
	registerFixer(extGStateFixer{})
	registerFixer(annotationFlagsFixer{})
	registerFixer(formFixer{})
	registerFixer(imageMetadataFixer{})
	registerFixer(postScriptXObjectFixer{})
	registerFixer(optionalContentFixer{})
	registerFixer(viewerPrefFixer{})
}

// walkDicts calls fn for every pdf.PDFDict reachable from v, recursing into dict
// entries and array elements, with cycle protection. fn may mutate the
// dict's Entries map in place (delete/set keys); since walkDicts passes each
// pdf.PDFDict by value, only edits to its Entries map (a reference type) take
// effect on the shared graph -- replacing fn's own copy's HasStream/RawStream
// fields would not propagate.
func walkDicts(v pdf.PDFValue, visited map[uintptr]bool, fn func(pdf.PDFDict)) {
	switch val := v.(type) {
	case pdf.PDFDict:
		ptr := pdf.ValuePointer(val.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		fn(val)
		for k, child := range val.Entries {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			walkDicts(child, visited, fn)
		}

	case pdf.PDFArray:
		ptr := pdf.ValuePointer(val)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for _, child := range val {
			walkDicts(child, visited, fn)
		}
	}
}

// clearDict deletes every entry from d except the synthetic "_ref" (so it
// remains a validly-targetable indirect object, just inert), used to
// neutralize a forbidden action dictionary in place.
func clearDict(d pdf.PDFDict) {
	for k := range d.Entries {
		if k != "_ref" {
			delete(d.Entries, k)
		}
	}
}

// clearJSNameTree recursively clears JS action dicts within a /Names/JavaScript
// name tree. walkDicts won't reach the subtree once the parent key is deleted.
func clearJSNameTree(d pdf.PDFDict, changed *bool) {
	if s, ok := d.Entries["S"].(pdf.PDFName); ok && verify.ForbiddenActions[s.Value] {
		clearDict(d)
		*changed = true
		return
	}
	for _, v := range d.Entries {
		switch val := v.(type) {
		case pdf.PDFDict:
			clearJSNameTree(val, changed)
		case pdf.PDFArray:
			for _, item := range val {
				if sub, ok := item.(pdf.PDFDict); ok {
					clearJSNameTree(sub, changed)
				}
			}
		}
	}
}

// --- 6.6 Actions ---

// actionFixer remediates Checks.Action.ForbiddenActionType,
// DisallowedNamedAction and AdditionalActions, mirroring
// validateActions/validateAdditionalActions in checks_dict.go.
type actionFixer struct{}

func (actionFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Action.ForbiddenActionType, pdf.Checks.Action.DisallowedNamedAction, pdf.Checks.Action.AdditionalActions:
		return true
	}
	return false
}

func (f actionFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (actionFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if _, ok := d.Entries["AA"]; ok {
			delete(d.Entries, "AA")
			*changed = true
		}

		// Remove catalog /Names/JavaScript name tree entry (6.6.1). Clear leaf
		// action dicts first -- walkDicts won't reach them after the key is gone.
		if jsTree, ok := d.Entries["JavaScript"].(pdf.PDFDict); ok {
			clearJSNameTree(jsTree, changed)
			delete(d.Entries, "JavaScript")
			*changed = true
		}

		s, ok := d.Entries["S"].(pdf.PDFName)
		if !ok || !verify.ActionTypes[s.Value] {
			return
		}
		if verify.ForbiddenActions[s.Value] {
			clearDict(d)
			*changed = true
			return
		}
		if s.Value == "Named" {
			n, ok := d.Entries["N"].(pdf.PDFName)
			if !ok || !verify.AllowedNamedActions[n.Value] {
				clearDict(d)
				*changed = true
			}
		}
	}, true
}

// --- 6.2.8 / 6.4 Extended graphics state ---

// extGStateFixer remediates the ExtGState-dictionary-level Transparency
// checks, mirroring validateExtGState in checks_dict.go. It deliberately
// does not touch Checks.Transparency.TransparencyGroup or ImageWithSoftMask
// (a different detection function, and a "harder" fix per the converter
// plan: removing the key is easy but changes rendered appearance).
type extGStateFixer struct{}

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

func (f extGStateFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (extGStateFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if t, ok := d.Entries["Type"].(pdf.PDFName); ok && t.Value != "ExtGState" {
			return
		}
		if !verify.HasAnyKey(d, "TR", "TR2", "SMask", "BM", "CA", "ca", "RI") {
			return
		}

		if _, ok := d.Entries["TR"]; ok {
			delete(d.Entries, "TR")
			*changed = true
		}
		if tr2, ok := d.Entries["TR2"]; ok {
			if name, isName := tr2.(pdf.PDFName); !isName || name.Value != "Default" {
				d.Entries["TR2"] = pdf.PDFName{Value: "Default"}
				*changed = true
			}
		}
		if ri, ok := d.Entries["RI"].(pdf.PDFName); ok && !verify.AllowedIntents[ri.Value] {
			delete(d.Entries, "RI")
			*changed = true
		}
		if sm, ok := d.Entries["SMask"]; ok {
			if name, isName := sm.(pdf.PDFName); !isName || name.Value != "None" {
				d.Entries["SMask"] = pdf.PDFName{Value: "None"}
				*changed = true
			}
		}
		if bm, ok := d.Entries["BM"]; ok && !verify.IsAllowedBlendMode(bm) {
			d.Entries["BM"] = pdf.PDFName{Value: "Normal"}
			*changed = true
		}
		if ca, ok := d.Entries["CA"]; ok {
			if f, num := verify.AsFloat(ca); num && verify.Abs64(f-1.0) > 1e-5 {
				d.Entries["CA"] = pdf.PDFReal(1.0)
				*changed = true
			}
		}
		if ca, ok := d.Entries["ca"]; ok {
			if f, num := verify.AsFloat(ca); num && verify.Abs64(f-1.0) > 1e-5 {
				d.Entries["ca"] = pdf.PDFReal(1.0)
				*changed = true
			}
		}
	}, true
}

// --- 6.5.3 Annotations ---

// annotationFlagsFixer remediates the annotation flag-bit and opacity
// checks, mirroring the relevant part of validateAnnotation in
// checks_dict.go. It deliberately does not touch DisallowedSubtype,
// ColourWithoutIntent, or the appearance-stream checks (resource synthesis
// or harder per the converter plan).
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

func (f annotationFlagsFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (annotationFlagsFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Annot"}) {
			return
		}

		flags := 0
		if f, ok := d.Entries["F"].(pdf.PDFInteger); ok {
			flags = int(f)
		}
		want := flags | verify.AnnotFlagPrint
		want &^= verify.AnnotFlagHidden | verify.AnnotFlagInvisible | verify.AnnotFlagNoView
		if want != flags {
			d.Entries["F"] = pdf.PDFInteger(want)
			*changed = true
		}

		if ca, ok := d.Entries["CA"]; ok {
			if f, num := verify.AsFloat(ca); num && f != 1.0 {
				d.Entries["CA"] = pdf.PDFReal(1.0)
				*changed = true
			}
		}
	}, true
}

// --- 6.9 Interactive forms ---

// formFixer remediates AcroForm-level (NeedAppearances, XFA) and
// field-level (A, AA) form violations, mirroring verifyInteractiveForms and
// validateFormField in checks_dict.go/document.go.
type formFixer struct{}

func (formFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Form.NeedAppearances, pdf.Checks.Form.XFA,
		pdf.Checks.Form.FieldAction, pdf.Checks.Form.FieldAdditionalActions:
		return true
	}
	return false
}

func (f formFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (formFixer) prepare(trailer *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	if root, ok := trailer.Entries["Root"].(pdf.PDFDict); ok {
		if form, ok := root.Entries["AcroForm"].(pdf.PDFDict); ok {
			if na, ok := form.Entries["NeedAppearances"].(pdf.PDFBoolean); ok && bool(na) {
				form.Entries["NeedAppearances"] = pdf.PDFBoolean(false)
				*changed = true
			}
			if _, ok := form.Entries["XFA"]; ok {
				delete(form.Entries, "XFA")
				*changed = true
			}
		}
	}

	return func(d pdf.PDFDict) {
		isWidget := d.Entries["Type"] == pdf.PDFName{Value: "Annot"} &&
			d.Entries["Subtype"] == pdf.PDFName{Value: "Widget"}
		isField := d.Entries["FT"] != nil
		if !isWidget && !isField {
			return
		}
		if _, ok := d.Entries["A"]; ok {
			delete(d.Entries, "A")
			*changed = true
		}
		if _, ok := d.Entries["AA"]; ok {
			delete(d.Entries, "AA")
			*changed = true
		}
	}, true
}

// --- 6.2.4-6.2.7 Image / Form XObject metadata ---

// imageMetadataFixer remediates the simple key-deletion Image/Form XObject
// checks, mirroring the Image/Form cases of validateXObjectDict in
// checks_dict.go, plus the inline-image flavour of ImageInterpolate
// (checkInlineImageOther, checks_content.go) via fixInlineImageInterpolate
// (fixups_inline_image.go) -- a Check can only have one registered Fixer,
// so the inline case is folded in here rather than given its own. It
// deliberately does not touch FormPostScript, FormPSEntry, FormSubtype2PS,
// or PostScriptXObject (PostScript-related checks already disabled in the
// default PDFA_1B profile; see profile.go).
type imageMetadataFixer struct{}

func (imageMetadataFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Image.ImageInterpolate, pdf.Checks.Image.ImageAlternates,
		pdf.Checks.Image.ImageOPI, pdf.Checks.Image.ImageRenderingIntent,
		pdf.Checks.Image.ReferenceXObject, pdf.Checks.Image.FormOPI:
		return true
	}
	return false
}

func (imageMetadataFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		subtype, ok := d.Entries["Subtype"].(pdf.PDFName)
		if !ok {
			return
		}
		switch subtype.Value {
		case "Image":
			if b, ok := d.Entries["Interpolate"].(pdf.PDFBoolean); ok && bool(b) {
				d.Entries["Interpolate"] = pdf.PDFBoolean(false)
				changed = true
			}
			if _, ok := d.Entries["Alternates"]; ok {
				delete(d.Entries, "Alternates")
				changed = true
			}
			if _, ok := d.Entries["OPI"]; ok {
				delete(d.Entries, "OPI")
				changed = true
			}
			if intent, ok := d.Entries["Intent"].(pdf.PDFName); ok && !verify.AllowedIntents[intent.Value] {
				delete(d.Entries, "Intent")
				changed = true
			}
		case "Form":
			if _, ok := d.Entries["Ref"]; ok {
				delete(d.Entries, "Ref")
				changed = true
			}
			if _, ok := d.Entries["OPI"]; ok {
				delete(d.Entries, "OPI")
				changed = true
			}
		}
	})
	if walkContentStreams(trailer, fixInlineImageInterpolate) {
		changed = true
	}
	return changed, nil
}

// --- 6.2.5 / 6.2.7 PostScript form XObjects ---

// postScriptXObjectFixer remediates the Form-XObject PostScript checks,
// mirroring the Form case of validateXObjectDict in checks_dict.go.
type postScriptXObjectFixer struct{}

func (postScriptXObjectFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Image.FormPSEntry, pdf.Checks.Image.FormPostScript, pdf.Checks.Image.FormSubtype2PS:
		return true
	}
	return false
}

func (f postScriptXObjectFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (postScriptXObjectFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries["Subtype"] != pdf.PDFName{Value: "Form"}) {
			return
		}
		if _, ok := d.Entries["PS"]; ok {
			delete(d.Entries, "PS")
			*changed = true
		}
		if (d.Entries["Subtype2"] == pdf.PDFName{Value: "PS"}) {
			delete(d.Entries, "Subtype2")
			*changed = true
		}
	}, true
}

// --- 6.1.13 Optional content ---

// optionalContentFixer remediates Checks.Structure.OptionalContent by
// deleting the catalog's /OCProperties entry, mirroring
// (*Document).verifyOptionalContent in verifier.go. Marked-content BDC/EMC
// wrappers in content streams that reference the removed OCGs are left in
// place: rewriting content-stream bytes is out of scope for a
// dictionary-level fix, and they have no PDF/A-meaningful effect once
// OCProperties is gone.
type optionalContentFixer struct{}

func (optionalContentFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.OptionalContent
}

func (optionalContentFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	if _, ok := root.Entries["OCProperties"]; !ok {
		return false, nil
	}
	delete(root.Entries, "OCProperties")
	return true, nil
}

// --- 6.1.2 ViewerPreferences (post-1.4 keys) ---

// viewerPrefFixer removes ViewerPreferences keys introduced after PDF 1.4
// (PrintScaling, PickTrayByPDFSize, PrintPageRange, NumCopies).
type viewerPrefFixer struct{}

func (viewerPrefFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.PostPDF14ViewerPref
}

func (viewerPrefFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	vp, ok := root.Entries["ViewerPreferences"].(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	changed := false
	for _, k := range verify.Post14ViewerPrefKeys {
		if vp.Entries[k] != nil {
			delete(vp.Entries, k)
			changed = true
		}
	}
	return changed, nil
}
