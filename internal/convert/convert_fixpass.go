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

	// parents maps a dict's Entries identity to every graph slot holding it,
	// lazily built on first replaceObject call and shared for the iteration.
	parents map[uintptr][]parentSlot
}

// parentSlot writes a replacement dict value into one graph location that
// held it; stream-field edits don't propagate through the shared Entries map,
// so every referencing slot must be rewritten (see walkStreamDicts).
type parentSlot func(pdf.PDFDict)

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

// replaceObject writes updated into every graph slot currently holding a dict
// with old's Entries identity, and into the iteration index, so stream-field
// changes take effect. Dicts synthesized after the index was built have no
// recorded slots; callers only replace dicts resolved via dictsForIssues.
func (p *fixPass) replaceObject(old, updated pdf.PDFDict) {
	if p.parents == nil {
		p.parents = map[uintptr][]parentSlot{}
		collectParentSlots(*p.trailer, map[uintptr]bool{}, p.parents)
	}
	ptr := pdf.ValuePointer(old.Entries)
	for _, set := range p.parents[ptr] {
		set(updated)
	}
	if ref, ok := old.Entries["_ref"].(pdf.PDFRef); ok {
		if _, exists := p.objs[ref.ObjNum]; exists {
			p.objs[ref.ObjNum] = updated
		}
	}
}

// collectParentSlots records, for every dict reachable from v, a setter per
// graph slot holding it, mirroring walkStreamDicts' traversal.
func collectParentSlots(v pdf.PDFValue, visited map[uintptr]bool, out map[uintptr][]parentSlot) {
	switch val := v.(type) {
	case pdf.PDFDict:
		ptr := pdf.ValuePointer(val.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for k, child := range val.Entries {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			if cd, ok := child.(pdf.PDFDict); ok {
				entries, key := val.Entries, k
				out[pdf.ValuePointer(cd.Entries)] = append(out[pdf.ValuePointer(cd.Entries)],
					func(nd pdf.PDFDict) { entries[key] = nd })
			}
			collectParentSlots(child, visited, out)
		}

	case pdf.PDFArray:
		ptr := pdf.ValuePointer(val)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for i, child := range val {
			if cd, ok := child.(pdf.PDFDict); ok {
				arr, idx := val, i
				out[pdf.ValuePointer(cd.Entries)] = append(out[pdf.ValuePointer(cd.Entries)],
					func(nd pdf.PDFDict) { arr[idx] = nd })
			}
			collectParentSlots(child, visited, out)
		}
	}
}
