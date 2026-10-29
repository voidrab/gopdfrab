package convert

import (
	"fmt"
	"sort"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// This file registers fixers for 6.1.12 architectural-limit
// checks.
func init() {
	registerFixer(pagesTreeArrayFixer{})
	registerFixer(resourceDictPruneFixer{})
	registerFixer(nameTooLongFixer{})
	registerFixer(cmapCIDClampFixer{})

	// The Kids rebalance joins the shared pre-emptive walk; the structure
	// drop runs after that walk, so it never sees an oversized Kids array
	// the rebalance could have split (the struct tree reaches Pages nodes
	// via Pg references).
	registerPreemptiveVisitor(func(trailer *pdf.PDFDict, _ *pdf.Reader, spec pdfa.Spec) func(pdf.PDFDict) {
		if spec.Limits.MaxArrayElements == 0 {
			return nil
		}
		return pagesKidsRebalanceVisitor(trailer, nil, spec.Limits.MaxArrayElements)
	})
	registerPreemptiveAfterFixup(func(trailer *pdf.PDFDict, _ *pdf.Reader, spec pdfa.Spec) error {
		if spec.Limits.MaxArrayElements != 0 {
			dropOversizedStructure(trailer, spec.Limits.MaxArrayElements)
		}
		return nil
	})
}

// nextAvailableObjNum scans the graph for the highest existing _ref object
// number and returns one past it, for synthesizing a fresh, guaranteed-
// unique indirect-object identity (isIndirectDict/identityOf, writer.go) for
// a brand-new dict that needs to be referenced from more than one place
// (the actual output object number WriteDocument assigns is unrelated --
// this only has to avoid colliding with any _ref already in the graph).
func nextAvailableObjNum(trailer pdf.PDFDict) int {
	max := 0
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if ref, ok := d.Entries.Get(pdf.RefKey).(pdf.PDFRef); ok && ref.ObjNum > max {
			max = ref.ObjNum
		}
	})
	return max + 1
}

// --- ArrayTooLarge: page-tree rebalancing ---

// pagesTreeChunkSize is how many kids each new intermediate /Pages node
// gets, well under PDF/A-1's array limit (and never more than the level's).
const pagesTreeChunkSize = 4096

// pagesTreeArrayFixer remediates Checks.Structure.ArrayTooLarge for an
// oversized /Pages node's /Kids array by splitting it into a tree of new
// intermediate /Pages nodes -- the standard technique real PDF writers use
// for documents with very many pages. Page discovery (buildPageIndex,
// document.go) and any other /Kids walker already recurse through
// arbitrary nesting depth, so this never changes page count, order, or any
// page's content; it only restructures the tree.
type pagesTreeArrayFixer struct{ spec *pdfa.Spec }

func (f pagesTreeArrayFixer) withSpec(s *pdfa.Spec) Fixer { f.spec = s; return f }

func (pagesTreeArrayFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.ArrayTooLarge
}

func (f pagesTreeArrayFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	limit := f.spec.Limits.MaxArrayElements
	if limit == 0 {
		return false, nil
	}
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, pagesKidsRebalanceVisitor(trailer, &changed, limit))
	// The logical structure tree's per-page parent arrays (positional MCID ->
	// element maps) can exceed the array limit and cannot be split. PDF/A-1b
	// (level B) does not require structure, so drop it rather than rasterize.
	if dropOversizedStructure(trailer, limit) {
		changed = true
	}
	return changed, nil
}

// pagesKidsRebalanceVisitor returns the per-dict rebalance of oversized
// Pages/Kids arrays, ones over limit elements. nextAvailableObjNum -- itself
// a full-graph scan -- is only computed once an oversized array actually turns
// up. changed may be nil when the caller doesn't need the signal.
func pagesKidsRebalanceVisitor(trailer *pdf.PDFDict, changed *bool, limit int) func(pdf.PDFDict) {
	nextObjNum := 0
	return func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Pages"}) {
			return
		}
		kids, ok := d.Entries.Get("Kids").(pdf.PDFArray)
		if !ok || len(kids) <= limit {
			return
		}
		if nextObjNum == 0 {
			nextObjNum = nextAvailableObjNum(*trailer)
		}
		d.Entries.Set("Kids", rebalancePagesKids(d, kids, &nextObjNum, min(pagesTreeChunkSize, limit)))
		if changed != nil {
			*changed = true
		}
	}
}

// dropOversizedStructure removes the document's logical structure tree when it
// holds an array over limit elements that no in-place split can repair,
// stripping the catalog's /StructTreeRoot and /MarkInfo and the now-orphaned
// /StructParent(s) links throughout the graph.
func dropOversizedStructure(trailer *pdf.PDFDict, limit int) bool {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return false
	}
	st, ok := root.Entries.Get("StructTreeRoot").(pdf.PDFDict)
	if !ok || !hasOversizedArray(st, map[uintptr]bool{}, limit) {
		return false
	}
	root.Entries.Del("StructTreeRoot")
	root.Entries.Del("MarkInfo")
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		d.Entries.Del("StructParents")
		d.Entries.Del("StructParent")
	})
	return true
}

// hasOversizedArray reports whether v, or anything reachable from it, is an
// array exceeding limit elements. /Parent and /P back-pointers are skipped
// to avoid walking back out of the subtree being inspected.
func hasOversizedArray(v pdf.PDFValue, visited map[uintptr]bool, limit int) bool {
	switch t := v.(type) {
	case pdf.PDFArray:
		if len(t) > limit {
			return true
		}
		ptr := pdf.ValuePointer(t)
		if visited[ptr] {
			return false
		}
		visited[ptr] = true
		for _, e := range t {
			if hasOversizedArray(e, visited, limit) {
				return true
			}
		}
	case pdf.PDFDict:
		ptr := pdf.ValuePointer(t.Entries)
		if visited[ptr] {
			return false
		}
		visited[ptr] = true
		for k, e := range t.Entries.All() {
			if k == pdf.RefKey || k == "Parent" || k == "P" {
				continue
			}
			if hasOversizedArray(e, visited, limit) {
				return true
			}
		}
	}
	return false
}

// rebalancePagesKids splits kids into chunks of chunkSize, each
// wrapped in a new intermediate /Pages node (re-pointing each kid's own
// /Parent to it, the immediate-parent requirement PDF 32000-1 7.7.3.2
// places on /Parent), and returns the new, much shorter top-level /Kids
// array. parent keeps its own object identity -- only its /Kids shrinks.
func rebalancePagesKids(parent pdf.PDFDict, kids pdf.PDFArray, nextObjNum *int, chunkSize int) pdf.PDFArray {
	var out pdf.PDFArray
	for i := 0; i < len(kids); i += chunkSize {
		chunk := append(pdf.PDFArray{}, kids[i:min(i+chunkSize, len(kids))]...)
		node := pdf.NewPDFDict()
		node.Entries.Set(pdf.RefKey, pdf.PDFRef{ObjNum: *nextObjNum})
		*nextObjNum++
		node.Entries.Set("Type", pdf.PDFName{Value: "Pages"})
		node.Entries.Set("Parent", parent)
		node.Entries.Set("Count", pdf.PDFInteger(countPageLeaves(chunk)))
		node.Entries.Set("Kids", chunk)
		for _, kid := range chunk {
			if kd, ok := kid.(pdf.PDFDict); ok {
				kd.Entries.Set("Parent", node)
			}
		}
		out = append(out, node)
	}
	return out
}

// countPageLeaves sums the leaf-page count of items: 1 per /Page, or a
// nested /Pages node's own /Count, so a chunk's new node reports the right
// total regardless of whether its kids are leaves or already-nested nodes.
func countPageLeaves(items pdf.PDFArray) int {
	total := 0
	for _, item := range items {
		d, ok := item.(pdf.PDFDict)
		if !ok {
			continue
		}
		if (d.Entries.Get("Type") == pdf.PDFName{Value: "Pages"}) {
			if c, ok := d.Entries.Get("Count").(pdf.PDFInteger); ok {
				total += int(c)
				continue
			}
		}
		total++
	}
	return total
}

// --- DictTooLarge: unused resource-entry pruning ---

// resourceCategories are the /Resources sub-dictionary keys whose entries
// are independently addressable by name from content-stream operators, and
// therefore safe to prune individually when unreferenced.
var resourceCategories = []string{"Font", "XObject", "ColorSpace", "Pattern", "Shading", "ExtGState", "Properties"}

// resourceDictPruneFixer remediates Checks.Structure.DictTooLarge for an
// oversized /Resources sub-dictionary by deleting entries no content stream
// reachable from it actually references (resourceUsage, below) -- safe by
// construction, since a dropped entry was never selected by anything. If
// pruning every unused entry still isn't enough to get under the limit (more
// used entries than the limit allows), it's left as residual rather than
// risk breaking a live reference.
type resourceDictPruneFixer struct{ spec *pdfa.Spec }

func (f resourceDictPruneFixer) withSpec(s *pdfa.Spec) Fixer { f.spec = s; return f }

func (resourceDictPruneFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.DictTooLarge
}

func (f resourceDictPruneFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	limit := f.spec.Limits.MaxDictEntries
	if limit == 0 {
		return false, nil
	}
	usage := computeResourceUsage(*trailer)
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		for _, cat := range resourceCategories {
			sub, ok := d.Entries.Get(cat).(pdf.PDFDict)
			if !ok {
				continue
			}
			if pruneUnusedResourceEntries(sub, usage[pdf.ValuePointer(sub.Entries)], limit) {
				changed = true
			}
		}
	})
	return changed, nil
}

// dictRealEntryCount mirrors validateArchitecturalLimits' own count
// (verifier.go): every entry except the synthetic pdf.RefKey bookkeeping key.
func dictRealEntryCount(d pdf.PDFDict) int {
	n := d.Entries.Len()
	if _, ok := d.Entries.Lookup(pdf.RefKey); ok {
		n--
	}
	return n
}

// pruneUnusedResourceEntries deletes entries from sub not named in used,
// down to at most limit, counted as validateArchitecturalLimits counts them
// (verifier.go): without the synthetic pdf.RefKey bookkeeping key.
//
// Candidates are taken in sorted order because the dictionary usually holds
// more unused entries than there is excess to shed: which ones go is a real
// choice, and map order would make it a different choice on every run -- a
// conversion that is not byte-reproducible.
func pruneUnusedResourceEntries(sub pdf.PDFDict, used map[string]bool, limit int) bool {
	excess := dictRealEntryCount(sub) - limit
	if excess <= 0 {
		return false
	}
	candidates := make([]string, 0, sub.Entries.Len())
	for k := range sub.Entries.All() {
		if k == pdf.RefKey || used[k] {
			continue
		}
		candidates = append(candidates, k)
	}
	sort.Strings(candidates)
	if len(candidates) > excess {
		candidates = candidates[:excess]
	}
	for _, k := range candidates {
		sub.Entries.Del(k)
	}
	return len(candidates) > 0
}

// resourceUsage maps a /Resources sub-dictionary's Entries-map pointer to
// the set of key names actually selected by a resource-referencing operator
// in some content stream reachable from it.
type resourceUsage struct {
	used        map[uintptr]map[string]bool
	visitedForm map[uintptr]bool
}

// computeResourceUsage walks every Page's content (and, recursively, any
// Form XObject it draws, using that Form's own Resources) and records which
// /Font, /XObject, /ColorSpace, /Pattern, /Shading, /ExtGState and
// /Properties entries are actually selected by a font, XObject, colour space,
// pattern, shading, graphics state or marked-content operator respectively
// (Tf, Do, cs/CS, scn/SCN, sh, gs, BDC/DP). Tiling patterns' own content
// isn't recursed into -- no corpus fixture needs that -- so usage inside a
// pattern's paint procedure isn't tracked here.
func computeResourceUsage(graph pdf.PDFValue) map[uintptr]map[string]bool {
	ru := &resourceUsage{used: map[uintptr]map[string]bool{}, visitedForm: map[uintptr]bool{}}
	walkGraph(graph, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if d.Entries.Get("Type") != (pdf.PDFName{Value: "Page"}) {
			return true
		}
		resources, _ := d.Entries.Get("Resources").(pdf.PDFDict)
		collectResourceUsageFromContents(d.Entries.Get("Contents"), resources, ru)
		return false
	}, nil)
	return ru.used
}

func collectResourceUsageFromContents(contents pdf.PDFValue, resources pdf.PDFDict, ru *resourceUsage) {
	switch v := contents.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			if data, err := pdf.DecodeStream(v); err == nil {
				collectResourceUsageFromBytes(data, resources, ru)
			}
		}
	case pdf.PDFArray:
		for _, item := range v {
			if d, ok := item.(pdf.PDFDict); ok && d.HasStream {
				if data, err := pdf.DecodeStream(d); err == nil {
					collectResourceUsageFromBytes(data, resources, ru)
				}
			}
		}
	}
}

// markResourceUsed records that name was selected from resources' category
// sub-dictionary.
func markResourceUsed(resources pdf.PDFDict, category, name string, ru *resourceUsage) {
	sub, ok := resources.Entries.Get(category).(pdf.PDFDict)
	if !ok || sub.Entries == nil {
		return
	}
	ptr := pdf.ValuePointer(sub.Entries)
	set := ru.used[ptr]
	if set == nil {
		set = map[string]bool{}
		ru.used[ptr] = set
	}
	set[name] = true
}

func collectResourceUsageFromBytes(data []byte, resources pdf.PDFDict, ru *resourceUsage) {
	cs := pdf.NewContentScanner(data)
	cs.Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "Do":
			if len(operands) == 0 {
				return
			}
			name, ok := operands[len(operands)-1].(pdf.PDFName)
			if !ok {
				return
			}
			markResourceUsed(resources, "XObject", name.Value, ru)
			xobjects, ok := resources.Entries.Get("XObject").(pdf.PDFDict)
			if !ok {
				return
			}
			xobj, ok := xobjects.Entries.Get(name.Value).(pdf.PDFDict)
			if !ok || xobj.Entries.Get("Subtype") != (pdf.PDFName{Value: "Form"}) || !xobj.HasStream {
				return
			}
			ptr := pdf.ValuePointer(xobj.Entries)
			if ru.visitedForm[ptr] {
				return
			}
			ru.visitedForm[ptr] = true
			subResources, _ := xobj.Entries.Get("Resources").(pdf.PDFDict)
			if subResources.Entries == nil {
				subResources = resources
			}
			if subData, err := pdf.DecodeStream(xobj); err == nil {
				collectResourceUsageFromBytes(subData, subResources, ru)
			}
		case "Tf":
			if len(operands) >= 2 {
				if name, ok := operands[len(operands)-2].(pdf.PDFName); ok {
					markResourceUsed(resources, "Font", name.Value, ru)
				}
			}
		case "gs":
			if len(operands) >= 1 {
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
					markResourceUsed(resources, "ExtGState", name.Value, ru)
				}
			}
		case "cs", "CS":
			if len(operands) >= 1 {
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
					markResourceUsed(resources, "ColorSpace", name.Value, ru)
				}
			}
		case "scn", "SCN":
			// The last operand is a Pattern name only when the current
			// colour space is /Pattern; otherwise it's a number, and the
			// type assertion below simply doesn't match.
			if len(operands) >= 1 {
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
					markResourceUsed(resources, "Pattern", name.Value, ru)
				}
			}
		case "sh":
			if len(operands) >= 1 {
				if name, ok := operands[0].(pdf.PDFName); ok {
					markResourceUsed(resources, "Shading", name.Value, ru)
				}
			}
		case "BDC", "DP":
			if len(operands) >= 2 {
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
					markResourceUsed(resources, "Properties", name.Value, ru)
				}
			}
		}
	})
}

// --- NameTooLong: name-value truncation and resource-key renaming ---

// nameTooLongFixer remediates Checks.Structure.NameTooLong for both flavours
// the check covers: a pdf.PDFName value over the limit is truncated in place
// (mirroring the scalar clamps contentLimitsFixer already applies to other
// types, fixups_content.go -- a name this long is already non-conformant,
// so shortening it is accepted), and an overlong dictionary key is renamed
// to a short, collision-free replacement, with every content-stream
// operator referencing the old name (in a /Resources category that key
// belongs to) rewritten to the new one so resource lookups still resolve.
type nameTooLongFixer struct{ spec *pdfa.Spec }

func (f nameTooLongFixer) withSpec(s *pdfa.Spec) Fixer { f.spec = s; return f }

// truncate is truncateOverlongName under the level's name limit.
func (f nameTooLongFixer) truncate(v pdf.PDFValue) (pdf.PDFValue, bool) {
	return truncateOverlongName(v, f.spec.Limits.MaxNameBytes)
}

func (nameTooLongFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.NameTooLong
}

func (f nameTooLongFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	limit := f.spec.Limits.MaxNameBytes
	if limit == 0 {
		return false, nil
	}
	changed := false

	walkScalars(*trailer, map[uintptr]bool{}, func(v pdf.PDFValue) (pdf.PDFValue, bool) {
		fixed, ok := f.truncate(v)
		if ok {
			changed = true
		}
		return fixed, ok
	})

	renames := map[uintptr]map[string]string{} // category-dict ptr -> old name -> new name
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if renameOverlongKeys(d, renames, limit) {
			changed = true
		}
	})
	if len(renames) > 0 {
		renameResourceReferences(trailer, renames)
	}

	return changed, nil
}

// fixTargeted remediates both NameTooLong flavours on just the dicts the
// issues reference: overlong keys are renamed, and overlong name values
// owned by the dict (entries and their arrays) are truncated.
func (f nameTooLongFixer) fixTargeted(p *fixPass, issues []pdf.PDFError) (changed, handled bool, err error) {
	targets, ok := p.dictsForIssues(issues)
	if !ok {
		return false, false, nil
	}
	renames := map[uintptr]map[string]string{}
	for _, d := range targets {
		if renameOverlongKeys(d, renames, f.spec.Limits.MaxNameBytes) {
			changed = true
		}
		if fixOwnedScalars(d, f.truncate) {
			changed = true
		}
	}
	if len(renames) > 0 {
		renameResourceReferences(p.trailer, renames)
	}
	return changed, true, nil
}

// truncateOverlongName shortens a pdf.PDFName value over limit bytes; a name
// this long is already non-conformant, so truncation is accepted.
func truncateOverlongName(v pdf.PDFValue, limit int) (pdf.PDFValue, bool) {
	n, ok := v.(pdf.PDFName)
	if !ok || len(n.Value) <= limit {
		return v, false
	}
	return pdf.PDFName{Value: n.Value[:limit]}, true
}

// renameOverlongKeys renames every key of d over limit bytes to a short,
// collision-free replacement, recording old->new per dict for the
// content-stream reference rewrite.
func renameOverlongKeys(d pdf.PDFDict, renames map[uintptr]map[string]string, limit int) bool {
	var overlong []string
	for k := range d.Entries.All() {
		if k != pdf.RefKey && len(k) > limit {
			overlong = append(overlong, k)
		}
	}
	sort.Strings(overlong) // deterministic collision suffixes
	for _, k := range overlong {
		newKey := shortenDictKey(d, k, limit)
		d.Entries.Set(newKey, d.Entries.Get(k))
		d.Entries.Del(k)
		ptr := pdf.ValuePointer(d.Entries)
		if renames[ptr] == nil {
			renames[ptr] = map[string]string{}
		}
		renames[ptr][k] = newKey
	}
	return len(overlong) > 0
}

// shortenDictKey returns a name under limit bytes that isn't already a key
// of d, truncating k and, on collision, appending a numeric suffix until
// unique.
func shortenDictKey(d pdf.PDFDict, k string, limit int) string {
	base := k
	if len(base) > limit-8 {
		base = base[:limit-8]
	}
	if _, exists := d.Entries.Lookup(base); !exists {
		return base
	}
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("%s~%d", base, i)
		if _, exists := d.Entries.Lookup(candidate); !exists {
			return candidate
		}
	}
}

// renameResourceReferences rewrites every content-stream operator selecting
// one of the renamed keys to use its replacement instead, via
// walkResourceAwareContent. The operators are the ones
// collectResourceUsageFromBytes recognises (Do, Tf, gs, cs/CS, scn/SCN, sh,
// BDC/DP).
func renameResourceReferences(trailer *pdf.PDFDict, renames map[uintptr]map[string]string) {
	walkResourceAwareContent(trailer, func(op string, operands []pdf.PDFValue, resources pdf.PDFDict, changed *bool) {
		category, fromEnd := resourceOperatorTarget(op)
		if category == "" || fromEnd >= len(operands) {
			return
		}
		idx := len(operands) - 1 - fromEnd
		name, ok := operands[idx].(pdf.PDFName)
		if !ok {
			return
		}
		sub, ok := resources.Entries.Get(category).(pdf.PDFDict)
		if !ok {
			return
		}
		byName, ok := renames[pdf.ValuePointer(sub.Entries)]
		if !ok {
			return
		}
		if newName, ok := byName[name.Value]; ok {
			operands[idx] = pdf.PDFName{Value: newName}
			*changed = true
		}
	})
}

// resourceOperatorTarget reports which /Resources category a resource-
// selecting operator names its resource in, and that name operand's
// position counted backward from the end of operands (0 = last) --
// mirroring collectResourceUsageFromBytes' own operator handling exactly
// (the font name of the Tf operator is its first of two operands, hence 1;
// the sh operator always has exactly one operand, so "last" and "first"
// coincide).
func resourceOperatorTarget(op string) (category string, fromEnd int) {
	switch op {
	case "Do":
		return "XObject", 0
	case "Tf":
		return "Font", 1
	case "gs":
		return "ExtGState", 0
	case "cs", "CS":
		return "ColorSpace", 0
	case "scn", "SCN":
		return "Pattern", 0
	case "sh":
		return "Shading", 0
	case "BDC", "DP":
		return "Properties", 0
	}
	return "", 0
}

// --- CMapCIDOutOfRange: clamping a CID value inside a CMap stream ---

// cmapCIDClampFixer remediates Checks.Structure.CMapCIDOutOfRange by
// clamping any cidrange/cidchar CID value over 65535 down to 65535 directly
// within the CMap's PostScript stream bytes, mirroring
// checkCMapCIDLimits' own token-position state machine so it only ever
// touches the exact values that check would flag.
type cmapCIDClampFixer struct{ spec *pdfa.Spec }

func (f cmapCIDClampFixer) withSpec(s *pdfa.Spec) Fixer { f.spec = s; return f }

func (cmapCIDClampFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.CMapCIDOutOfRange
}

func (f cmapCIDClampFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		updated, ok := clampCMapStreamDict(d, f.spec.Limits.MaxCID)
		if ok {
			changed = true
		}
		return updated, ok
	})
	return changed, nil
}

// fixTargeted clamps only the CMap streams the issues reference, writing the
// rewritten dict back into every referencing slot via the pass index.
func (f cmapCIDClampFixer) fixTargeted(p *fixPass, issues []pdf.PDFError) (changed, handled bool, err error) {
	targets, ok := p.dictsForIssues(issues)
	if !ok {
		return false, false, nil
	}
	for _, d := range targets {
		updated, ok := clampCMapStreamDict(d, f.spec.Limits.MaxCID)
		if !ok {
			continue
		}
		p.replaceObject(d, updated)
		changed = true
	}
	return changed, true, nil
}

// clampCMapStreamDict returns a copy of d with CIDs over limit clamped in its
// decoded stream, or ok=false when d is not a CMap stream or needs no fix. A
// zero limit sets none.
func clampCMapStreamDict(d pdf.PDFDict, limit int) (pdf.PDFDict, bool) {
	if (d.Entries.Get("Type") != pdf.PDFName{Value: "CMap"}) || !d.HasStream || limit == 0 {
		return d, false
	}
	data, err := pdf.DecodeStream(d)
	if err != nil {
		return d, false
	}
	clamped, ok := clampCMapCIDs(data, limit)
	if !ok {
		return d, false
	}
	writer.SetStreamFlate(&d, clamped)
	return d, true
}

// clampCMapCIDs rewrites every cidrange/cidchar CID token over limit in data
// to limit, splicing the replacement directly into the original
// bytes (preserving everything else byte-for-byte) so it never needs to
// re-serialize the surrounding PostScript.
func clampCMapCIDs(data []byte, limit int) ([]byte, bool) {
	tokens := fontprog.CmapTokenize(data)
	type span struct{ start, end int }
	var spans []span

	inCIDRange, inCIDChar, pos := false, false, 0
	for _, tok := range tokens {
		switch tok.Text {
		case "begincidrange":
			inCIDRange, inCIDChar, pos = true, false, 0
		case "endcidrange":
			inCIDRange, pos = false, 0
		case "begincidchar":
			inCIDChar, inCIDRange, pos = true, false, 0
		case "endcidchar":
			inCIDChar, pos = false, 0
		default:
			target := 0
			if inCIDRange {
				target = 3
			} else if inCIDChar {
				target = 2
			}
			if target == 0 {
				continue
			}
			pos++
			if pos == target {
				if cid, ok := fontprog.CmapParseInt(tok.Text); ok && cid > int64(limit) {
					spans = append(spans, span{tok.Start, tok.End})
				}
				pos = 0
			}
		}
	}
	if len(spans) == 0 {
		return nil, false
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	clampedText := []byte(fmt.Sprintf("%d", limit))
	var out []byte
	prev := 0
	for _, s := range spans {
		out = append(out, data[prev:s.start]...)
		out = append(out, clampedText...)
		prev = s.end
	}
	out = append(out, data[prev:]...)
	return out, true
}

// --- shared content-stream resource-aware rewriting ---

// resourceOpRewriter is offered each scanned content-stream operator
// together with the /Resources dict in effect for that content stream
// (which differs between a page and any Form XObject it invokes); it may
// rewrite operands in place and must report via *changed whether it did.
type resourceOpRewriter func(op string, operands []pdf.PDFValue, resources pdf.PDFDict, changed *bool)

// walkResourceAwareContent calls rewrite for every operator in every Page's
// content stream, and recursively for any Form XObject it draws (using that
// Form's own Resources), writing back any content stream
// rewrite actually changed. Unlike walkContentStreams (fixups_content.go),
// which has no Resources context, this exists specifically for rewrites
// that need to know which resource dictionary a name operand selects from
// (renameResourceReferences, above). Tiling patterns' own content isn't
// recursed into, matching computeResourceUsage's same scope.
func walkResourceAwareContent(trailer *pdf.PDFDict, rewrite resourceOpRewriter) bool {
	changed := false
	visitedForm := map[uintptr]bool{}
	walkGraph(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if d.Entries.Get("Type") != (pdf.PDFName{Value: "Page"}) {
			return true
		}
		resources, _ := d.Entries.Get("Resources").(pdf.PDFDict)
		rewritePageContents(d, resources, rewrite, visitedForm, &changed)
		return false
	}, nil)
	return changed
}

func rewritePageContents(page, resources pdf.PDFDict, rewrite resourceOpRewriter, visitedForm map[uintptr]bool, changed *bool) {
	switch v := page.Entries.Get("Contents").(type) {
	case pdf.PDFDict:
		if v.HasStream {
			if fixed, ok := rewriteResourceAwareStream(v, resources, rewrite, visitedForm); ok {
				page.Entries.Set("Contents", fixed)
				*changed = true
			}
		}
	case pdf.PDFArray:
		for i, item := range v {
			d, ok := item.(pdf.PDFDict)
			if !ok || !d.HasStream {
				continue
			}
			if fixed, ok := rewriteResourceAwareStream(d, resources, rewrite, visitedForm); ok {
				v[i] = fixed
				*changed = true
			}
		}
	}
}

func rewriteResourceAwareStream(dict, resources pdf.PDFDict, rewrite resourceOpRewriter, visitedForm map[uintptr]bool) (pdf.PDFDict, bool) {
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		return dict, false
	}

	// Emit while scanning: see rewriteContentStreamDict. rewrite edits operands
	// in place, so the write must follow it and precede the callback's return.
	var cw writer.ContentStreamWriter
	modified := false
	cs := pdf.NewContentScanner(data)
	cs.Scan(func(op string, operands []pdf.PDFValue) {
		rewrite(op, operands, resources, &modified)
		_ = cw.WriteOp(op, operands)

		if op != "Do" || len(operands) == 0 {
			return
		}
		name, ok := operands[len(operands)-1].(pdf.PDFName)
		if !ok {
			return
		}
		xobjects, ok := resources.Entries.Get("XObject").(pdf.PDFDict)
		if !ok {
			return
		}
		xobj, ok := xobjects.Entries.Get(name.Value).(pdf.PDFDict)
		if !ok || xobj.Entries.Get("Subtype") != (pdf.PDFName{Value: "Form"}) || !xobj.HasStream {
			return
		}
		ptr := pdf.ValuePointer(xobj.Entries)
		if visitedForm[ptr] {
			return
		}
		visitedForm[ptr] = true
		subResources, _ := xobj.Entries.Get("Resources").(pdf.PDFDict)
		if subResources.Entries == nil {
			subResources = resources
		}
		if fixed, ok := rewriteResourceAwareStream(xobj, subResources, rewrite, visitedForm); ok {
			xobjects.Entries.Set(name.Value, fixed)
			modified = true
		}
	})
	// A stream only part of which could be read: see rewriteContentStreamDict.
	if !modified || !cs.Complete() || cw.Err() != nil {
		return dict, false
	}

	writer.SetStreamFlate(&dict, cw.Bytes())
	return dict, true
}
