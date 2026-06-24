package convert

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Fixer attempts to remediate a specific set of violations across the entire document graph idempotently.
// It targets the graph as a whole rather than using individual issue ObjectRefs, which change during serialization.
type Fixer interface {
	// Applies reports whether this Fixer remediates violations of c.
	Applies(c pdf.Check) bool
	// Fix attempts to remediate every violation of the applicable Check(s)
	// reachable from trailer.
	Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (changed bool, err error)
}

// batchDictFixer is an optional capability for a Fixer whose remediation is a
// pure per-dict-local edit driven by a single walkDicts. Implementing it lets
// the convert loop run every such fixer in one shared graph walk per pass
// instead of one walk each. prepare runs any document-level setup once
// (recording edits via changed) and returns a per-dict visitor, or ok=false
// to do nothing this pass; the visitor likewise records edits via changed.
type batchDictFixer interface {
	Fixer
	prepare(trailer *pdf.PDFDict, changed *bool) (visit func(d pdf.PDFDict), ok bool)
}

// runDictVisitor drives a batchDictFixer's prepare + visitor over the whole
// graph in one walk -- the standalone equivalent of the batched dispatch, used
// by each such fixer's Fix so it still works when invoked on its own.
func runDictVisitor(trailer *pdf.PDFDict, prepare func(*pdf.PDFDict, *bool) (func(pdf.PDFDict), bool)) (bool, error) {
	changed := false
	visit, ok := prepare(trailer, &changed)
	if !ok {
		return false, nil
	}
	walkDicts(*trailer, map[uintptr]bool{}, visit)
	return changed, nil
}

var fixerRegistry = map[pdf.Check]Fixer{}

func registerFixer(f Fixer) {
	for _, c := range pdf.AllChecks() {
		if !f.Applies(c) {
			continue
		}
		if dup, ok := fixerRegistry[c]; ok {
			panic(fmt.Sprintf("pdfrab: check %s/%d already has a registered fixer (%T), cannot also register %T",
				c.Clause(), c.Subclause(), dup, f))
		}
		fixerRegistry[c] = f
	}
}

var preemptiveFixups []func(trailer *pdf.PDFDict) error

func registerPreemptiveFixup(f func(trailer *pdf.PDFDict) error) {
	preemptiveFixups = append(preemptiveFixups, f)
}

func applyPreemptiveFixups(trailer *pdf.PDFDict) error {
	for _, f := range preemptiveFixups {
		if err := f(trailer); err != nil {
			return err
		}
	}
	return nil
}
