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

	// contentStreams holds the Entries identities of content-bearing streams,
	// lazily built on first isContentStream call.
	contentStreams map[uintptr]bool
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

// isContentStream reports whether d is one of the content-bearing streams
// walkContentStreams would rewrite; running the content rewriter over any
// other stream's bytes would corrupt it (e.g. an image flagged for an
// out-of-range entry value).
func (p *fixPass) isContentStream(d pdf.PDFDict) bool {
	if p.contentStreams == nil {
		p.contentStreams = map[uintptr]bool{}
		collectContentStreamPtrs(*p.trailer, p.contentStreams)
	}
	return p.contentStreams[pdf.ValuePointer(d.Entries)]
}

// collectContentStreamPtrs records the Entries identity of every content-
// bearing stream, mirroring walkContentStreams' positional dispatch (page
// Contents, tiling patterns, form XObjects, Type3 CharProcs) without
// decoding anything.
func collectContentStreamPtrs(trailer pdf.PDFDict, out map[uintptr]bool) {
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		switch {
		case (d.Entries["Type"] == pdf.PDFName{Value: "Page"}):
			switch contents := d.Entries["Contents"].(type) {
			case pdf.PDFDict:
				if contents.HasStream {
					out[pdf.ValuePointer(contents.Entries)] = true
				}
			case pdf.PDFArray:
				for _, item := range contents {
					if cd, ok := item.(pdf.PDFDict); ok && cd.HasStream {
						out[pdf.ValuePointer(cd.Entries)] = true
					}
				}
			}
		case d.Entries["PatternType"] == pdf.PDFInteger(1) && d.HasStream,
			(d.Entries["Subtype"] == pdf.PDFName{Value: "Form"}) && d.HasStream:
			out[pdf.ValuePointer(d.Entries)] = true
		case (d.Entries["Subtype"] == pdf.PDFName{Value: "Type3"}):
			if procs, ok := d.Entries["CharProcs"].(pdf.PDFDict); ok {
				for _, v := range procs.Entries {
					if pd, ok := v.(pdf.PDFDict); ok && pd.HasStream {
						out[pdf.ValuePointer(pd.Entries)] = true
					}
				}
			}
		}
	})
}

// fixOwnedScalars applies fix to every scalar d owns -- its entry values
// and, transitively, elements of arrays -- without descending into child
// dicts, mirroring the verifier's owner threading: a child dict's scalars
// are reported against the child, which is its own fix target.
func fixOwnedScalars(d pdf.PDFDict, fix func(pdf.PDFValue) (pdf.PDFValue, bool)) bool {
	changed := false
	visited := map[uintptr]bool{}
	var fixArray func(a pdf.PDFArray)
	fixArray = func(a pdf.PDFArray) {
		ptr := pdf.ValuePointer(a)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for i, item := range a {
			switch v := item.(type) {
			case pdf.PDFDict:
			case pdf.PDFArray:
				fixArray(v)
			default:
				if nv, ok := fix(item); ok {
					a[i] = nv
					changed = true
				}
			}
		}
	}
	for k, val := range d.Entries {
		if k == "_ref" || k == "_dirty" {
			continue
		}
		switch v := val.(type) {
		case pdf.PDFDict:
		case pdf.PDFArray:
			fixArray(v)
		default:
			if nv, ok := fix(val); ok {
				d.Entries[k] = nv
				changed = true
			}
		}
	}
	return changed
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
