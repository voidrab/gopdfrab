package convert

import (
	"sort"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// targetedFixer is an optional capability for a Fixer that can remediate just
// the objects its issues point at instead of walking the whole graph.
// handled=false means the batch could not be targeted (e.g. a ref-less issue)
// and the caller falls back to the ordinary Fix.
type targetedFixer interface {
	Fixer
	fixTargeted(p *fixPass, issues []pdf.PDFError) (changed, handled bool, err error)
}

// fixPass carries per-iteration fixer state: the trailer and the ObjNum ->
// object index built by writer.NumberObjects for this iteration's in-heap
// verify. Object numbers are only stable within one iteration, so a fixPass
// must never outlive the iteration whose index it was built from.
type fixPass struct {
	trailer *pdf.PDFDict
	objs    map[int]pdf.PDFValue
}

// dictForRef resolves ref against the iteration index. ok is false unless the
// numbered object exists, is a dict, and still carries the matching _ref.
func (p *fixPass) dictForRef(ref pdf.PDFRef) (pdf.PDFDict, bool) {
	if p == nil || p.objs == nil {
		return pdf.PDFDict{}, false
	}
	d, ok := p.objs[ref.ObjNum].(pdf.PDFDict)
	if !ok {
		return pdf.PDFDict{}, false
	}
	r, ok := d.Entries["_ref"].(pdf.PDFRef)
	if !ok || r.ObjNum != ref.ObjNum {
		return pdf.PDFDict{}, false
	}
	return d, true
}

// dictsForIssues returns the distinct dicts the issues point at, sorted by
// object number so targeted fixing stays deterministic despite nondeterministic
// issue order. ok is false -- and the caller must fall back to its full-graph
// walk -- when there is no index, no issues, or any issue lacks a resolvable
// dict ref.
func (p *fixPass) dictsForIssues(issues []pdf.PDFError) ([]pdf.PDFDict, bool) {
	if p == nil || p.objs == nil || len(issues) == 0 {
		return nil, false
	}
	seen := map[uintptr]bool{}
	type target struct {
		num int
		d   pdf.PDFDict
	}
	var targets []target
	for _, iss := range issues {
		ref, ok := iss.ObjectRef()
		if !ok {
			return nil, false
		}
		d, ok := p.dictForRef(ref)
		if !ok {
			return nil, false
		}
		ptr := pdf.ValuePointer(d.Entries)
		if seen[ptr] {
			continue
		}
		seen[ptr] = true
		targets = append(targets, target{num: ref.ObjNum, d: d})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].num < targets[j].num })
	out := make([]pdf.PDFDict, len(targets))
	for i, t := range targets {
		out[i] = t.d
	}
	return out, true
}
