package pdfrab

func init() {
	registerFixer(disallowedAnnotFixer{})
	registerFixer(annotColourFixer{})
}

// --- 6.5.2 Annotation subtypes ---

// disallowedAnnotFixer remediates Checks.Annotation.DisallowedSubtype by
// neutralizing (clearDict) any annotation dictionary whose /Subtype is not
// PDF/A-permitted, mirroring validateAnnotation's allowedAnnotationTypes
// check in checks_dict.go. clearDict -- rather than removing the entry from
// the page's /Annots array -- mirrors actionFixer's handling of forbidden
// action dictionaries: nothing else inspects /Annots array membership, only
// each entry's own Type/Subtype, so an emptied dict is invisible to every
// other check.
type disallowedAnnotFixer struct{}

func (disallowedAnnotFixer) Applies(c Check) bool {
	return c == Checks.Annotation.DisallowedSubtype
}

func (disallowedAnnotFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		if (d.Entries["Type"] != PDFName{Value: "Annot"}) {
			return
		}
		subtype, _ := d.Entries["Subtype"].(PDFName)
		if !allowedAnnotationTypes[subtype.Value] {
			clearDict(d)
			changed = true
		}
	})
	return changed, nil
}

// --- 6.5.3 Annotation colour without intent ---

// annotColourFixer remediates Checks.Annotation.ColourWithoutIntent by
// deleting an annotation's /C or /IC colour array when its device colour
// model (gray/rgb/cmyk, by array length) is not covered by the document's
// output intent, mirroring checkAnnotColour in checks_dict.go.
type annotColourFixer struct{}

func (annotColourFixer) Applies(c Check) bool {
	return c == Checks.Annotation.ColourWithoutIntent
}

func (annotColourFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	hasOutputIntent, rgbCovered, cmykCovered := outputIntentCoverage(*trailer)
	allowed := func(model string) bool {
		switch model {
		case "rgb":
			return rgbCovered
		case "cmyk":
			return cmykCovered
		case "gray":
			return hasOutputIntent
		}
		return true
	}

	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		if (d.Entries["Type"] != PDFName{Value: "Annot"}) {
			return
		}
		for _, key := range []string{"C", "IC"} {
			arr, ok := d.Entries[key].(PDFArray)
			if !ok {
				continue
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
				continue
			}
			if !allowed(model) {
				delete(d.Entries, key)
				changed = true
			}
		}
	})
	return changed, nil
}

// outputIntentCoverage replicates Document.computeColourCoverage
// (checks_colour.go) directly over the in-memory trailer: Convert's Fixers
// operate on a resolved graph that was never opened as a *Document, so the
// ValidationContext-based helper isn't available here.
func outputIntentCoverage(trailer PDFDict) (hasOutputIntent, rgbCovered, cmykCovered bool) {
	root, ok := trailer.Entries["Root"].(PDFDict)
	if !ok {
		return false, false, false
	}
	intents, ok := root.Entries["OutputIntents"].(PDFArray)
	if !ok {
		return false, false, false
	}
	for _, it := range intents {
		intent, ok := it.(PDFDict)
		if !ok {
			continue
		}
		if (intent.Entries["S"] != PDFName{Value: "GTS_PDFA1"}) {
			continue
		}
		hasOutputIntent = true
		profile, ok := intent.Entries["DestOutputProfile"].(PDFDict)
		if !ok {
			continue
		}
		switch n, _ := profile.Entries["N"].(PDFInteger); int(n) {
		case 3:
			rgbCovered = true
		case 4:
			cmykCovered = true
		}
	}
	return hasOutputIntent, rgbCovered, cmykCovered
}
