package convert

import (
	"context"
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// Fixer idempotently remediates the violations of a set of checks. It repairs
// through at least one capability: graphFixer, targetedFixer or
// batchDictFixer.
type Fixer interface {
	// Applies reports whether this Fixer remediates violations of c.
	Applies(c pdf.Check) bool
}

// graphFixer is a Fixer that can repair the whole graph. dispatchFixers runs
// Fix when the Fixer has no other capability, or when its targeted repair
// could not handle the issues.
type graphFixer interface {
	Fixer
	// Fix attempts to remediate every violation of the applicable Check(s)
	// reachable from trailer.
	Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (changed bool, err error)
}

// batchDictFixer is an optional capability for a Fixer whose remediation is a
// pure per-dict-local edit driven by a single walkDicts. Implementing it lets
// the convert loop run every such fixer in one shared graph walk per pass
// instead of one walk each. prepare is handed the pass's issues for every
// check the fixer covers, runs any document-level setup once (recording edits
// via changed) and returns a per-dict visitor, or ok=false to do nothing this
// pass; the visitor likewise records edits via changed.
type batchDictFixer interface {
	graphFixer
	prepare(trailer *pdf.PDFDict, issues []pdf.PDFError, changed *bool) (visit func(d pdf.PDFDict), ok bool)
}

// runDictVisitor drives a batchDictFixer's prepare + visitor over the whole
// graph in one walk -- the standalone equivalent of the batched dispatch, used
// by each such fixer's Fix so it still works when invoked on its own.
func runDictVisitor(trailer *pdf.PDFDict, issues []pdf.PDFError, prepare func(*pdf.PDFDict, []pdf.PDFError, *bool) (func(pdf.PDFDict), bool)) (bool, error) {
	changed := false
	visit, ok := prepare(trailer, issues, &changed)
	if !ok {
		return false, nil
	}
	walkDicts(*trailer, map[uintptr]bool{}, visit)
	return changed, nil
}

// reportedChecks is the set of checks issues were reported under: for a
// fixer covering several checks, the rules it was asked to repair.
func reportedChecks(issues []pdf.PDFError) map[pdf.Check]bool {
	reported := make(map[pdf.Check]bool, len(issues))
	for _, iss := range issues {
		reported[iss.Check()] = true
	}
	return reported
}

// leadsItsClause is implemented by a Fixer that may need to run before the
// other fixers of its clause. dispatchFixers orders it first within the clause
// when its leadsItsClause method returns true.
type leadsItsClause interface {
	Fixer
	leadsItsClause() bool
}

// fixerRun is what one conversion run lends its fixers: the level's rules
// and the run-scoped state that a fixer reads or reports into.
type fixerRun struct {
	ctx  context.Context
	doc  *pdf.Reader
	spec *pdfa.Spec
	dpi  int
	// decode is the Reader's concurrent decode cache, shared with the
	// pre-loop colour usage scan.
	decode decodeFunc
	// fontSrc is the appearance font, loaded once per run.
	fontSrc *appearanceFontSource
	// formDrops and lost collect what fixers render or carry over
	// incompletely, since fixers have no handle on the result.
	formDrops *[]RasterDrop
	lost      *[]pdf.PDFError
}

// repairs lists everything convert can do to a document. Phase 0, the
// pre-emptive steps, runs once before the first verify whatever the input
// reports; fixers then run per pass on what the verify reported.
type repairs struct {
	// preFixups run first, in order, each over the graph on its own.
	preFixups []preemptiveFixup
	// preVisitors share one walk after preFixups, so a visitor sees their
	// edits. A visitor builder that returns nil contributes no visitor.
	preVisitors []preemptiveVisitor
	// postWalk run last, for steps that must see the visitors' edits.
	postWalk []preemptiveFixup
	// fixers builds each fixer for a run. Each check has at most one; the
	// dispatch order follows the reported checks, not this order.
	fixers []func(r *fixerRun) Fixer
}

type (
	preemptiveFixup   func(trailer *pdf.PDFDict, doc *pdf.Reader, spec pdfa.Spec) error
	preemptiveVisitor func(trailer *pdf.PDFDict, doc *pdf.Reader, spec pdfa.Spec) func(pdf.PDFDict)
)

// standardRepairs is the repair set every conversion uses.
var standardRepairs = repairs{
	preFixups: []preemptiveFixup{
		injectOutputIntent,
		dropEncryption,
		regenerateXMP,
	},
	preVisitors: []preemptiveVisitor{
		// Font-program repairs for PDF/A's font rules.
		pdfaOnlyVisitor(promoteEmptyGlyphsInFont),
		pdfaOnlyVisitor(repairNonSymbolicTrueTypeCmap),
		pdfaOnlyVisitor(inlineType1WidthSubrsInFont),
		pdfaOnlyVisitor(normalizeCFFWidthDefaultsInFont),
		pagesKidsRebalance,
		// Its builder captures the catalog's metadata identity after
		// regenerateXMP has installed the fresh packet.
		stripEmbeddedMetadataVisitor,
	},
	// The structure drop runs after the Kids rebalance, so it never sees an
	// oversized Kids array that the rebalance could have split (the
	// structure tree reaches Pages nodes through Pg references).
	postWalk: []preemptiveFixup{dropOversizedStructureFixup},
	fixers: []func(r *fixerRun) Fixer{
		func(r *fixerRun) Fixer { return disallowedAnnotFixer{spec: r.spec, lost: r.lost} },
		func(r *fixerRun) Fixer { return annotColourFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return appearanceFixer{fontSrc: r.fontSrc, spec: r.spec} },
		func(r *fixerRun) Fixer { return iccBasedProfileFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return separationAlternateFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return contentLimitsFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return deviceColourFixer{decode: r.decode, spec: r.spec} },
		func(r *fixerRun) Fixer { return deviceNColorantsFixer{} },
		func(r *fixerRun) Fixer { return deviceNColorantEntriesFixer{} },
		func(r *fixerRun) Fixer { return separationConsistencyFixer{} },
		func(r *fixerRun) Fixer { return overprintModeFixer{} },
		func(r *fixerRun) Fixer { return inheritedResourcesFixer{decode: r.decode} },
		func(r *fixerRun) Fixer { return actionFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return extGStateFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return halftoneFixer{} },
		func(r *fixerRun) Fixer { return annotationFlagsFixer{} },
		func(r *fixerRun) Fixer { return formFixer{} },
		func(r *fixerRun) Fixer { return imageMetadataFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return imageSampleDepthFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return jpxColourSpaceFixer{} },
		func(r *fixerRun) Fixer { return blendingSpaceFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return postScriptXObjectFixer{} },
		func(r *fixerRun) Fixer { return optionalContentFixer{lost: r.lost} },
		func(r *fixerRun) Fixer { return viewerPrefFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return unicodeNameFixer{} },
		func(r *fixerRun) Fixer { return permissionsFixer{} },
		func(r *fixerRun) Fixer { return pageBoundaryFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return fileSpecFixer{lost: r.lost} },
		func(r *fixerRun) Fixer { return fontDictFixer{} },
		func(r *fixerRun) Fixer { return type0FontFixer{} },
		func(r *fixerRun) Fixer { return baseFontFixer{} },
		func(r *fixerRun) Fixer { return fontMetricFixer{} },
		func(r *fixerRun) Fixer { return fontSubsetMetaFixer{} },
		func(r *fixerRun) Fixer { return fontFileSubtypeFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return fontSubstitutionFixer{doc: r.doc, spec: r.spec} },
		func(r *fixerRun) Fixer { return trueTypeEncodingFixer{doc: r.doc} },
		func(r *fixerRun) Fixer { return inlineImageLZWFixer{} },
		func(r *fixerRun) Fixer { return inlineImageFilterFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return pagesTreeArrayFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return resourceDictPruneFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return nameTooLongFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return cmapCIDClampFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return indirectRequiredFixer{} },
		func(r *fixerRun) Fixer { return disallowedValueFixer{} },
		func(r *fixerRun) Fixer { return constraintFixer{} },
		func(r *fixerRun) Fixer { return missingRequiredKeyFixer{} },
		func(r *fixerRun) Fixer { return wrongValueTypeFixer{} },
		func(r *fixerRun) Fixer { return post14KeyFixer{} },
		func(r *fixerRun) Fixer { return qNestingFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return streamFilterFixer{spec: r.spec} },
		func(r *fixerRun) Fixer { return undecodableStreamFixer{lost: r.lost} },
		func(r *fixerRun) Fixer {
			return transparencyFlattener{dpi: r.dpi, drops: r.formDrops, ctx: r.ctx}
		},
	},
}

// pdfaOnlyVisitor joins visit to the pre-emptive walk for a PDF/A level only.
func pdfaOnlyVisitor(visit func(pdf.PDFDict)) preemptiveVisitor {
	return func(_ *pdf.PDFDict, _ *pdf.Reader, spec pdfa.Spec) func(pdf.PDFDict) {
		if !spec.PDFA {
			return nil
		}
		return visit
	}
}

// pagesKidsRebalance splits the oversized Kids arrays of the page tree, for a
// level that limits array length.
func pagesKidsRebalance(trailer *pdf.PDFDict, _ *pdf.Reader, spec pdfa.Spec) func(pdf.PDFDict) {
	if spec.Limits.MaxArrayElements == 0 {
		return nil
	}
	return pagesKidsRebalanceVisitor(trailer, nil, spec.Limits.MaxArrayElements)
}

// dropOversizedStructureFixup drops the structure tree's oversized arrays,
// for a level that limits array length.
func dropOversizedStructureFixup(trailer *pdf.PDFDict, _ *pdf.Reader, spec pdfa.Spec) error {
	if spec.Limits.MaxArrayElements != 0 {
		dropOversizedStructure(trailer, spec.Limits.MaxArrayElements)
	}
	return nil
}

// fixersFor builds the run's fixer for each check. It panics if two fixers
// claim one check.
func (rs repairs) fixersFor(r *fixerRun) map[pdf.Check]Fixer {
	byCheck := make(map[pdf.Check]Fixer, len(rs.fixers))
	for _, build := range rs.fixers {
		f := build(r)
		for _, c := range pdf.AllChecks() {
			if !f.Applies(c) {
				continue
			}
			if dup, ok := byCheck[c]; ok {
				panic(fmt.Sprintf("gopdfrab: check %s/%d is claimed by both %T and %T",
					c.Clause(), c.Subclause(), dup, f))
			}
			byCheck[c] = f
		}
	}
	return byCheck
}

// applyPreemptive runs phase 0 over the graph.
func (rs repairs) applyPreemptive(trailer *pdf.PDFDict, doc *pdf.Reader, spec pdfa.Spec) error {
	for _, f := range rs.preFixups {
		if err := f(trailer, doc, spec); err != nil {
			return err
		}
	}
	var visitors []func(pdf.PDFDict)
	for _, prepare := range rs.preVisitors {
		if visit := prepare(trailer, doc, spec); visit != nil {
			visitors = append(visitors, visit)
		}
	}
	if len(visitors) > 0 {
		walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
			for _, visit := range visitors {
				visit(d)
			}
		})
	}
	for _, f := range rs.postWalk {
		if err := f(trailer, doc, spec); err != nil {
			return err
		}
	}
	return nil
}
