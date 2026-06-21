package pdfrab

import "fmt"

// Fixer attempts to remediate a specific set of violations across the entire document graph idempotently.
// It targets the graph as a whole rather than using individual issue ObjectRefs, which change during serialization.
type Fixer interface {
	// Applies reports whether this Fixer remediates violations of c.
	Applies(c Check) bool
	// Fix attempts to remediate every violation of the applicable Check(s)
	// reachable from trailer.
	Fix(trailer *PDFDict, issues []PDFError) (changed bool, err error)
}

var fixerRegistry = map[Check]Fixer{}

func registerFixer(f Fixer) {
	for _, c := range AllChecks() {
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

var preemptiveFixups []func(trailer *PDFDict) error

func registerPreemptiveFixup(f func(trailer *PDFDict) error) {
	preemptiveFixups = append(preemptiveFixups, f)
}

func applyPreemptiveFixups(trailer *PDFDict) error {
	for _, f := range preemptiveFixups {
		if err := f(trailer); err != nil {
			return err
		}
	}
	return nil
}
