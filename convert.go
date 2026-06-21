// Package-level pipeline for converting an arbitrary PDF into a PDF/A-1b
// rewrite:
//
//	PDF -> pre-emptive fixups -> [serialize -> verify -> targeted fixups]* -> output
//
// Pre-emptive fixups (registered via registerPreemptiveFixup) run once,
// unconditionally, before the first verify pass, since they're always safe
// and clear the bulk of typical violations without needing a verify
// round-trip to discover them (e.g. regenerating XMP metadata, injecting a
// missing OutputIntent). The bounded loop that follows re-serializes the
// graph through WriteDocument on every iteration -- which by construction
// already fixes the entire 6.1.x structural clause family, since the writer
// always emits a clean classic xref table and correctly-framed objects -- so
// verifying the freshly-written bytes (rather than the original file) skips
// a large class of noise the writer has already resolved for free, letting
// most documents converge in one or two passes. Only the Checks still
// violated after that get dispatched to a registered Fixer (see
// convert_fixers.go); each Fixer mutates the whole in-memory graph being
// converted, not just the specific objects an issue's ObjectRef names,
// because that issue came from verifying a separately-opened copy of the
// freshly-serialized bytes (with its own, unrelated renumbering) rather than
// from the graph itself.
package pdfrab

import (
	"bytes"
	"fmt"
	"os"
)

const maxConvertIterations = 4

type ConvertResult struct {
	Output     []byte
	Result     Result
	Iterations int
}

// Residual returns the issues remaining in r.Output that Convert was unable
// to fix automatically.
func (r ConvertResult) Residual() []PDFError {
	return r.Result.Issues
}

// Convert reads the PDF at path and attempts to produce a PDF/A-1b
// conformant rewrite. It always returns the best attempt it produced,
// even if some violations remain.
func Convert(path string) (ConvertResult, error) {
	doc, err := Open(path)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer doc.Close()
	return convertDocument(doc)
}

// ConvertBytes is Convert for an in-memory PDF.
func ConvertBytes(data []byte) (ConvertResult, error) {
	path, cleanup, err := writeTempFile("gopdfrab-convert-*.pdf", data)
	if err != nil {
		return ConvertResult{}, fmt.Errorf("convert: %w", err)
	}
	defer cleanup()
	return Convert(path)
}

func convertDocument(doc *Document) (ConvertResult, error) {
	graph, err := doc.ResolveGraph()
	if err != nil {
		res, verr := doc.Verify(A_1B)
		if verr != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		return ConvertResult{Result: res}, nil
	}
	trailer, ok := graph.(PDFDict)
	if !ok {
		return ConvertResult{}, fmt.Errorf("convert: resolved graph is not a dictionary")
	}

	if err := applyPreemptiveFixups(&trailer); err != nil {
		return ConvertResult{}, fmt.Errorf("convert: pre-emptive fixups: %w", err)
	}

	var (
		cr         ConvertResult
		prevCounts map[Check]int
	)

	for iter := 1; iter <= maxConvertIterations; iter++ {
		cr.Iterations = iter

		var buf bytes.Buffer
		if err := WriteDocument(&buf, trailer); err != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", err)
		}
		cr.Output = buf.Bytes()

		result, verr := verifyBytes(cr.Output)
		if verr != nil {
			return ConvertResult{}, fmt.Errorf("convert: %w", verr)
		}
		cr.Result = result

		if result.Valid {
			return cr, nil
		}

		counts := violationCounts(result.Issues)
		if sameMultiset(counts, prevCounts) {
			break // no progress since last iteration
		}
		prevCounts = counts

		changed := false
		for check := range counts {
			fixer, ok := fixerRegistry[check]
			if !ok {
				continue
			}
			ch, err := fixer.Fix(&trailer, result.IssuesForCheck(check))
			if err != nil {
				return ConvertResult{}, fmt.Errorf("convert: fixer for check %q: %w", check.Name(), err)
			}
			if ch {
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	return cr, nil
}

func verifyBytes(data []byte) (Result, error) {
	path, cleanup, err := writeTempFile("gopdfrab-verify-*.pdf", data)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	doc, err := Open(path)
	if err != nil {
		return Result{}, err
	}
	defer doc.Close()
	return doc.Verify(A_1B)
}

func writeTempFile(pattern string, data []byte) (path string, cleanup func(), err error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.Remove(tmp.Name()) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp.Name(), cleanup, nil
}

// violationCounts tallies how many times each Check is violated, used to
// detect whether a fixup pass made any progress.
func violationCounts(issues []PDFError) map[Check]int {
	counts := map[Check]int{}
	for _, iss := range issues {
		counts[iss.check]++
	}
	return counts
}

// sameMultiset reports whether a and b record exactly the same violation
// counts per Check.
func sameMultiset(a, b map[Check]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
