package pdfrab

// This file registers Fixers for the dictionary-level violations classified
// as "easy" (pure key deletion/normalization, no resource synthesis) in the
// converter plan: actions (6.6), ExtGState transparency keys (6.2.8/6.4),
// annotation flags (6.5.3), interactive forms (6.9), image/form XObject
// metadata keys (6.2.4-6.2.7), PostScript form XObjects (6.2.5/6.2.7), and
// optional content (6.1.13). Each Fixer walks the whole graph via walkDicts
// rather than targeting issues' ObjectRef -- see convert_fixers.go for why
// -- mirroring, in reverse, the exact detection logic in checks_dict.go so a
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
}

// walkDicts calls fn for every PDFDict reachable from v, recursing into dict
// entries and array elements, with cycle protection. fn may mutate the
// dict's Entries map in place (delete/set keys); since walkDicts passes each
// PDFDict by value, only edits to its Entries map (a reference type) take
// effect on the shared graph -- replacing fn's own copy's HasStream/RawStream
// fields would not propagate.
func walkDicts(v PDFValue, visited map[uintptr]bool, fn func(PDFDict)) {
	switch val := v.(type) {
	case PDFDict:
		ptr := pdfValuePointer(val.Entries)
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

	case PDFArray:
		ptr := pdfValuePointer(val)
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
func clearDict(d PDFDict) {
	for k := range d.Entries {
		if k != "_ref" {
			delete(d.Entries, k)
		}
	}
}

// --- 6.6 Actions ---

// actionFixer remediates Checks.Action.ForbiddenActionType,
// DisallowedNamedAction and AdditionalActions, mirroring
// validateActions/validateAdditionalActions in checks_dict.go.
type actionFixer struct{}

func (actionFixer) Applies(c Check) bool {
	switch c {
	case Checks.Action.ForbiddenActionType, Checks.Action.DisallowedNamedAction, Checks.Action.AdditionalActions:
		return true
	}
	return false
}

func (f actionFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (actionFixer) prepare(_ *PDFDict, changed *bool) (func(PDFDict), bool) {
	return func(d PDFDict) {
		if _, ok := d.Entries["AA"]; ok {
			delete(d.Entries, "AA")
			*changed = true
		}

		s, ok := d.Entries["S"].(PDFName)
		if !ok || !actionTypes[s.Value] {
			return
		}
		if forbiddenActions[s.Value] {
			clearDict(d)
			*changed = true
			return
		}
		if s.Value == "Named" {
			n, ok := d.Entries["N"].(PDFName)
			if !ok || !allowedNamedActions[n.Value] {
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

func (extGStateFixer) Applies(c Check) bool {
	switch c {
	case Checks.Transparency.TransferFunction,
		Checks.Transparency.DefaultTransferFunction,
		Checks.Transparency.ExtGStateRenderingIntent,
		Checks.Transparency.SoftMaskExtGState,
		Checks.Transparency.BlendMode,
		Checks.Transparency.StrokingAlpha,
		Checks.Transparency.NonStrokingAlpha:
		return true
	}
	return false
}

func (f extGStateFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (extGStateFixer) prepare(_ *PDFDict, changed *bool) (func(PDFDict), bool) {
	return func(d PDFDict) {
		if t, ok := d.Entries["Type"].(PDFName); ok && t.Value != "ExtGState" {
			return
		}
		if !hasAnyKey(d, "TR", "TR2", "SMask", "BM", "CA", "ca", "RI") {
			return
		}

		if _, ok := d.Entries["TR"]; ok {
			delete(d.Entries, "TR")
			*changed = true
		}
		if tr2, ok := d.Entries["TR2"]; ok {
			if name, isName := tr2.(PDFName); !isName || name.Value != "Default" {
				d.Entries["TR2"] = PDFName{Value: "Default"}
				*changed = true
			}
		}
		if ri, ok := d.Entries["RI"].(PDFName); ok && !allowedIntents[ri.Value] {
			delete(d.Entries, "RI")
			*changed = true
		}
		if sm, ok := d.Entries["SMask"]; ok {
			if name, isName := sm.(PDFName); !isName || name.Value != "None" {
				d.Entries["SMask"] = PDFName{Value: "None"}
				*changed = true
			}
		}
		if bm, ok := d.Entries["BM"]; ok && !isAllowedBlendMode(bm) {
			d.Entries["BM"] = PDFName{Value: "Normal"}
			*changed = true
		}
		if ca, ok := d.Entries["CA"]; ok {
			if f, num := asFloat(ca); num && abs64(f-1.0) > 1e-5 {
				d.Entries["CA"] = PDFReal(1.0)
				*changed = true
			}
		}
		if ca, ok := d.Entries["ca"]; ok {
			if f, num := asFloat(ca); num && abs64(f-1.0) > 1e-5 {
				d.Entries["ca"] = PDFReal(1.0)
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

func (annotationFlagsFixer) Applies(c Check) bool {
	switch c {
	case Checks.Annotation.PrintFlagNotSet, Checks.Annotation.HiddenFlagSet,
		Checks.Annotation.InvisibleFlagSet, Checks.Annotation.NoViewFlagSet,
		Checks.Annotation.OpacityNotOne:
		return true
	}
	return false
}

func (f annotationFlagsFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (annotationFlagsFixer) prepare(_ *PDFDict, changed *bool) (func(PDFDict), bool) {
	return func(d PDFDict) {
		if (d.Entries["Type"] != PDFName{Value: "Annot"}) {
			return
		}

		flags := 0
		if f, ok := d.Entries["F"].(PDFInteger); ok {
			flags = int(f)
		}
		want := flags | annotFlagPrint
		want &^= annotFlagHidden | annotFlagInvisible | annotFlagNoView
		if want != flags {
			d.Entries["F"] = PDFInteger(want)
			*changed = true
		}

		if ca, ok := d.Entries["CA"]; ok {
			if f, num := asFloat(ca); num && f != 1.0 {
				d.Entries["CA"] = PDFReal(1.0)
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

func (formFixer) Applies(c Check) bool {
	switch c {
	case Checks.Form.NeedAppearances, Checks.Form.XFA,
		Checks.Form.FieldAction, Checks.Form.FieldAdditionalActions:
		return true
	}
	return false
}

func (f formFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (formFixer) prepare(trailer *PDFDict, changed *bool) (func(PDFDict), bool) {
	if root, ok := trailer.Entries["Root"].(PDFDict); ok {
		if form, ok := root.Entries["AcroForm"].(PDFDict); ok {
			if na, ok := form.Entries["NeedAppearances"].(PDFBoolean); ok && bool(na) {
				form.Entries["NeedAppearances"] = PDFBoolean(false)
				*changed = true
			}
			if _, ok := form.Entries["XFA"]; ok {
				delete(form.Entries, "XFA")
				*changed = true
			}
		}
	}

	return func(d PDFDict) {
		isWidget := d.Entries["Type"] == PDFName{Value: "Annot"} &&
			d.Entries["Subtype"] == PDFName{Value: "Widget"}
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

func (imageMetadataFixer) Applies(c Check) bool {
	switch c {
	case Checks.Image.ImageInterpolate, Checks.Image.ImageAlternates,
		Checks.Image.ImageOPI, Checks.Image.ImageRenderingIntent,
		Checks.Image.ReferenceXObject, Checks.Image.FormOPI:
		return true
	}
	return false
}

func (imageMetadataFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		subtype, ok := d.Entries["Subtype"].(PDFName)
		if !ok {
			return
		}
		switch subtype.Value {
		case "Image":
			if b, ok := d.Entries["Interpolate"].(PDFBoolean); ok && bool(b) {
				d.Entries["Interpolate"] = PDFBoolean(false)
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
			if intent, ok := d.Entries["Intent"].(PDFName); ok && !allowedIntents[intent.Value] {
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

func (postScriptXObjectFixer) Applies(c Check) bool {
	switch c {
	case Checks.Image.FormPSEntry, Checks.Image.FormPostScript, Checks.Image.FormSubtype2PS:
		return true
	}
	return false
}

func (f postScriptXObjectFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (postScriptXObjectFixer) prepare(_ *PDFDict, changed *bool) (func(PDFDict), bool) {
	return func(d PDFDict) {
		if (d.Entries["Subtype"] != PDFName{Value: "Form"}) {
			return
		}
		if _, ok := d.Entries["PS"]; ok {
			delete(d.Entries, "PS")
			*changed = true
		}
		if (d.Entries["Subtype2"] == PDFName{Value: "PS"}) {
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

func (optionalContentFixer) Applies(c Check) bool {
	return c == Checks.Structure.OptionalContent
}

func (optionalContentFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	root, ok := trailer.Entries["Root"].(PDFDict)
	if !ok {
		return false, nil
	}
	if _, ok := root.Entries["OCProperties"]; !ok {
		return false, nil
	}
	delete(root.Entries, "OCProperties")
	return true, nil
}
