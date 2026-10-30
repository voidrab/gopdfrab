package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// --- 6.5.2 Annotation subtypes ---

// disallowedAnnotFixer remediates Checks.Annotation.DisallowedSubtype by
// clearing (clearDict) any annotation dictionary whose /Subtype is not
// permitted by the level, mirroring validateAnnotation in checks_dict.go. The
// dictionary is emptied rather than removed from the page's /Annots array, as
// actionFixer does for forbidden action dictionaries: no check reads /Annots
// membership, only each entry's own Type and Subtype, so an emptied dictionary
// is ignored by every other check.
type disallowedAnnotFixer struct{ spec *pdfa.Spec }

func (disallowedAnnotFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Annotation.DisallowedSubtype
}

func (f disallowedAnnotFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (f disallowedAnnotFixer) prepare(_ *pdf.PDFDict, _ []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Annot"}) {
			return
		}
		subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)
		if !f.spec.AnnotationSubtypes.Has(subtype.Value) {
			clearDict(d)
			*changed = true
		}
	}, true
}

// --- 6.5.3 Annotation colour without intent ---

// annotColourFixer remediates Checks.Annotation.ColourWithoutIntent by
// deleting an annotation's /C or /IC colour array when its device colour
// model (gray/rgb/cmyk, by array length) is not covered by the document's
// output intent, mirroring checkAnnotColour in checks_dict.go.
type annotColourFixer struct{ spec *pdfa.Spec }

func (annotColourFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Annotation.ColourWithoutIntent
}

func (f annotColourFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (f annotColourFixer) prepare(trailer *pdf.PDFDict, _ []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	hasOutputIntent, rgbCovered, cmykCovered := outputIntentCoverage(*trailer, f.spec.OutputIntentSubtype)
	return func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Annot"}) {
			return
		}
		for _, key := range []string{"C", "IC"} {
			arr, ok := d.Entries.Get(key).(pdf.PDFArray)
			if !ok {
				continue
			}
			var covered bool
			switch len(arr) {
			case 1: // gray
				covered = hasOutputIntent
			case 3: // rgb
				covered = rgbCovered
			case 4: // cmyk
				covered = cmykCovered
			default:
				continue
			}
			if !covered {
				d.Entries.Del(key)
				*changed = true
			}
		}
	}, true
}

// outputIntentCoverage replicates Document.computeColourCoverage
// (checks_colour.go) directly over the in-memory trailer: Convert's Fixers
// operate on a resolved graph that was never opened as a *Document, so the
// ValidationContext-based helper isn't available here.
func outputIntentCoverage(trailer pdf.PDFDict, subtype string) (hasOutputIntent, rgbCovered, cmykCovered bool) {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return false, false, false
	}
	intents, ok := root.Entries.Get("OutputIntents").(pdf.PDFArray)
	if !ok {
		return false, false, false
	}
	for _, it := range intents {
		intent, ok := it.(pdf.PDFDict)
		if !ok {
			continue
		}
		if (intent.Entries.Get("S") != pdf.PDFName{Value: subtype}) {
			continue
		}
		hasOutputIntent = true
		profile, ok := intent.Entries.Get("DestOutputProfile").(pdf.PDFDict)
		if !ok {
			continue
		}
		switch n, _ := profile.Entries.Get("N").(pdf.PDFInteger); int(n) {
		case 3:
			rgbCovered = true
		case 4:
			cmykCovered = true
		}
	}
	return hasOutputIntent, rgbCovered, cmykCovered
}
