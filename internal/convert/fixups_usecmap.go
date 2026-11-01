package convert

import (
	"regexp"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// maxUseCMapDepth bounds how deep a chain of embedded CMaps, each using the
// next, useCMapFixer follows.
const maxUseCMapDepth = 8

// cmapBlockRe matches one mapping block of a CMap program, with its count:
// the code space ranges and the CID and notdef mappings.
var cmapBlockRe = regexp.MustCompile(`(?s)\d+\s+begin(codespacerange|cidrange|cidchar|notdefrange|notdefchar)\b.*?\bend(codespacerange|cidrange|cidchar|notdefrange|notdefchar)\b`)

// useCMapOpRe matches the usecmap operator with its operand.
var useCMapOpRe = regexp.MustCompile(`/\S+\s+usecmap\b`)

// endCMapRe matches the operator that ends a CMap's definitions.
var endCMapRe = regexp.MustCompile(`\bendcmap\b`)

// useCMapFixer remediates Checks.Font.UseCMapNotPredefined (6.2.11.3.3): an
// embedded CMap whose UseCMap is an embedded CMap the level does not
// predefine. The used CMap's mappings are copied into the CMap ahead of its
// own, which take precedence as they did over the used ones, and the UseCMap
// entry and usecmap operator go. A UseCMap that names a CMap the file does
// not embed cannot be merged and stays reported.
type useCMapFixer struct{ spec *pdfa.Spec }

func (useCMapFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Font.UseCMapNotPredefined
}

func (f useCMapFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (f useCMapFixer) prepare(_ *pdf.PDFDict, _ []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) ||
			(d.Entries.Get("Subtype") != pdf.PDFName{Value: "Type0"}) {
			return
		}
		cmap, ok := d.Entries.Get("Encoding").(pdf.PDFDict)
		if !ok || !cmap.IsStream() {
			return
		}
		if f.mergeUsedCMaps(cmap, 0) {
			*changed = true
		}
	}, true
}

// mergeUsedCMaps merges into cmap, in place, the embedded CMap it uses and
// any that one uses in turn, and reports whether it changed cmap. It stops at
// a CMap the level predefines, which the merged CMap then uses instead.
func (f useCMapFixer) mergeUsedCMaps(cmap pdf.PDFDict, depth int) bool {
	used, ok := cmap.Entries.Get("UseCMap").(pdf.PDFDict)
	if !ok || !used.IsStream() || depth >= maxUseCMapDepth {
		return false
	}
	if name, ok := used.Entries.Get("CMapName").(pdf.PDFName); ok && f.spec.PredefinedCMaps.Has(name.Value) {
		return false
	}
	f.mergeUsedCMaps(used, depth+1)
	if _, chained := used.Entries.Get("UseCMap").(pdf.PDFDict); chained {
		return false // a chain too deep to flatten
	}
	own, err := pdf.DecodeStream(cmap)
	if err != nil {
		return false
	}
	base, err := pdf.DecodeStream(used)
	if err != nil {
		return false
	}
	next, _ := used.Entries.Get("UseCMap").(pdf.PDFName)
	writer.SetStreamFlate(&cmap, mergeCMapPrograms(own, base, next.Value))
	if next.Value != "" {
		cmap.Entries.Set("UseCMap", next)
	} else {
		cmap.Entries.Del("UseCMap")
	}
	return true
}

// mergeCMapPrograms returns the CMap program own with the mapping blocks of
// base, the program it uses, inserted before its own blocks, and its usecmap
// operator replaced by one for next, the CMap base uses, or removed when next
// is empty. A reader resolving a code that both map takes own's mapping,
// defined later, as usecmap semantics require.
func mergeCMapPrograms(own, base []byte, next string) []byte {
	own = useCMapOpRe.ReplaceAll(own, nil)
	var blocks []byte
	if next != "" {
		blocks = append(blocks, "/"+next+" usecmap\n"...)
	}
	for _, b := range cmapBlockRe.FindAll(base, -1) {
		blocks = append(append(blocks, b...), '\n')
	}
	at := len(own)
	if loc := cmapBlockRe.FindIndex(own); loc != nil {
		at = loc[0]
	} else if loc := endCMapRe.FindIndex(own); loc != nil {
		at = loc[0]
	}
	merged := make([]byte, 0, len(own)+len(blocks))
	merged = append(merged, own[:at]...)
	merged = append(merged, blocks...)
	return append(merged, own[at:]...)
}
