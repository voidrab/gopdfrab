package pdfrab

import (
	"fmt"
	"sort"
)

// This file registers fixers for the remaining 6.1.12 architectural-limit
// checks Phase 11 left out of scope (conversion-plan.md): ArrayTooLarge,
// DictTooLarge, NameTooLong and CMapCIDOutOfRange. Unlike the scalar clamps
// contentLimitsFixer already applies (fixups_content.go), each of these
// needs a different repair shape -- splitting an oversized page tree,
// pruning resource entries nothing references, renaming an overlong
// resource key (and keeping content-stream references to it in sync), and
// clamping a value inside a CMap's PostScript stream -- so each gets its own
// fixer here rather than a single scalar-clamp pass.
//
// DeviceNColorants is deliberately NOT claimed: the one corpus fixture that
// violates it lists 12 colorants, 3 of which are the spec's /None
// placeholder (no visual effect) -- but the remaining 9 real colorants
// still exceed the 8-colorant maximum, and reducing them further would
// require rewriting the tint-transform function's input arity to match,
// which is out of scope here. It stays residual.

func init() {
	registerFixer(pagesTreeArrayFixer{})
	registerFixer(resourceDictPruneFixer{})
	registerFixer(nameTooLongFixer{})
	registerFixer(cmapCIDClampFixer{})
}

// nextAvailableObjNum scans the graph for the highest existing _ref object
// number and returns one past it, for synthesizing a fresh, guaranteed-
// unique indirect-object identity (isIndirectDict/identityOf, writer.go) for
// a brand-new dict that needs to be referenced from more than one place
// (the actual output object number WriteDocument assigns is unrelated --
// this only has to avoid colliding with any _ref already in the graph).
func nextAvailableObjNum(trailer PDFDict) int {
	max := 0
	walkDicts(trailer, map[uintptr]bool{}, func(d PDFDict) {
		if ref, ok := d.Entries["_ref"].(PDFRef); ok && ref.ObjNum > max {
			max = ref.ObjNum
		}
	})
	return max + 1
}

// --- ArrayTooLarge: page-tree rebalancing ---

// maxPDFArrayElements mirrors the 8191-element ceiling validateArchitecturalLimits
// (verifier.go) enforces for any PDF array (6.1.12/3).
const maxPDFArrayElements = 8191

// pagesTreeChunkSize is how many kids each new intermediate /Pages node
// gets, well under maxPDFArrayElements.
const pagesTreeChunkSize = 4096

// pagesTreeArrayFixer remediates Checks.Structure.ArrayTooLarge for an
// oversized /Pages node's /Kids array by splitting it into a tree of new
// intermediate /Pages nodes -- the standard technique real PDF writers use
// for documents with very many pages. Page discovery (buildPageIndex,
// document.go) and any other /Kids walker already recurse through
// arbitrary nesting depth, so this never changes page count, order, or any
// page's content; it only restructures the tree.
type pagesTreeArrayFixer struct{}

func (pagesTreeArrayFixer) Applies(c Check) bool {
	return c == Checks.Structure.ArrayTooLarge
}

func (pagesTreeArrayFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	nextObjNum := nextAvailableObjNum(*trailer)
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		if (d.Entries["Type"] != PDFName{Value: "Pages"}) {
			return
		}
		kids, ok := d.Entries["Kids"].(PDFArray)
		if !ok || len(kids) <= maxPDFArrayElements {
			return
		}
		d.Entries["Kids"] = rebalancePagesKids(d, kids, &nextObjNum)
		changed = true
	})
	return changed, nil
}

// rebalancePagesKids splits kids into chunks of pagesTreeChunkSize, each
// wrapped in a new intermediate /Pages node (re-pointing each kid's own
// /Parent to it, the immediate-parent requirement PDF 32000-1 7.7.3.2
// places on /Parent), and returns the new, much shorter top-level /Kids
// array. parent keeps its own object identity -- only its /Kids shrinks.
func rebalancePagesKids(parent PDFDict, kids PDFArray, nextObjNum *int) PDFArray {
	var out PDFArray
	for i := 0; i < len(kids); i += pagesTreeChunkSize {
		chunk := append(PDFArray{}, kids[i:min(i+pagesTreeChunkSize, len(kids))]...)
		node := NewPDFDict()
		node.Entries["_ref"] = PDFRef{ObjNum: *nextObjNum}
		*nextObjNum++
		node.Entries["Type"] = PDFName{Value: "Pages"}
		node.Entries["Parent"] = parent
		node.Entries["Count"] = PDFInteger(countPageLeaves(chunk))
		node.Entries["Kids"] = chunk
		for _, kid := range chunk {
			if kd, ok := kid.(PDFDict); ok {
				kd.Entries["Parent"] = node
			}
		}
		out = append(out, node)
	}
	return out
}

// countPageLeaves sums the leaf-page count of items: 1 per /Page, or a
// nested /Pages node's own /Count, so a chunk's new node reports the right
// total regardless of whether its kids are leaves or already-nested nodes.
func countPageLeaves(items PDFArray) int {
	total := 0
	for _, item := range items {
		d, ok := item.(PDFDict)
		if !ok {
			continue
		}
		if (d.Entries["Type"] == PDFName{Value: "Pages"}) {
			if c, ok := d.Entries["Count"].(PDFInteger); ok {
				total += int(c)
				continue
			}
		}
		total++
	}
	return total
}

// --- DictTooLarge: unused resource-entry pruning ---

// maxDictEntries mirrors the 4096-entry ceiling validateArchitecturalLimits
// (verifier.go) enforces for any PDF dictionary (6.1.12/4), excluding the
// synthetic "_ref" bookkeeping key the same way that check does.
const maxDictEntries = 4096

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
type resourceDictPruneFixer struct{}

func (resourceDictPruneFixer) Applies(c Check) bool {
	return c == Checks.Structure.DictTooLarge
}

func (resourceDictPruneFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	usage := computeResourceUsage(*trailer)
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		for _, cat := range resourceCategories {
			sub, ok := d.Entries[cat].(PDFDict)
			if !ok {
				continue
			}
			if pruneUnusedResourceEntries(sub, usage[pdfValuePointer(sub.Entries)]) {
				changed = true
			}
		}
	})
	return changed, nil
}

// dictRealEntryCount mirrors validateArchitecturalLimits' own count
// (verifier.go): every entry except the synthetic "_ref" bookkeeping key.
func dictRealEntryCount(d PDFDict) int {
	n := len(d.Entries)
	if _, ok := d.Entries["_ref"]; ok {
		n--
	}
	return n
}

// pruneUnusedResourceEntries deletes entries from sub not named in used,
// down to at most maxDictEntries.
func pruneUnusedResourceEntries(sub PDFDict, used map[string]bool) bool {
	excess := dictRealEntryCount(sub) - maxDictEntries
	if excess <= 0 {
		return false
	}
	changed := false
	for k := range sub.Entries {
		if excess <= 0 {
			break
		}
		if k == "_ref" || k == "_dirty" || used[k] {
			continue
		}
		delete(sub.Entries, k)
		excess--
		changed = true
	}
	return changed
}

// resourceUsage maps a /Resources sub-dictionary's Entries-map pointer to
// the set of key names actually selected by a resource-referencing operator
// in some content stream reachable from it.
type resourceUsage struct {
	used        map[uintptr]map[string]bool
	visitedForm map[uintptr]bool
}

// computeResourceUsage walks every Page's content (and, recursively, any
// Form XObject it invokes via Do, using that Form's own Resources) and
// records which /Font, /XObject, /ColorSpace, /Pattern, /Shading,
// /ExtGState and /Properties entries are actually used by a Tf, Do, cs/CS,
// scn/SCN, sh, gs or BDC/DP operator respectively. Tiling patterns' own
// content isn't recursed into -- no corpus fixture needs that -- so usage
// inside a pattern's paint procedure isn't tracked here.
func computeResourceUsage(graph PDFValue) map[uintptr]map[string]bool {
	ru := &resourceUsage{used: map[uintptr]map[string]bool{}, visitedForm: map[uintptr]bool{}}
	visited := map[uintptr]bool{}

	var walk func(v PDFValue)
	walk = func(v PDFValue) {
		switch val := v.(type) {
		case PDFDict:
			ptr := pdfValuePointer(val.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			if val.Entries["Type"] == (PDFName{Value: "Page"}) {
				resources, _ := val.Entries["Resources"].(PDFDict)
				collectResourceUsageFromContents(val.Entries["Contents"], resources, ru)
				return
			}
			for _, child := range val.Entries {
				walk(child)
			}
		case PDFArray:
			ptr := pdfValuePointer(val)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			for _, item := range val {
				walk(item)
			}
		}
	}
	walk(graph)
	return ru.used
}

func collectResourceUsageFromContents(contents PDFValue, resources PDFDict, ru *resourceUsage) {
	switch v := contents.(type) {
	case PDFDict:
		if v.HasStream {
			if data, err := decodeStream(v); err == nil {
				collectResourceUsageFromBytes(data, resources, ru)
			}
		}
	case PDFArray:
		for _, item := range v {
			if d, ok := item.(PDFDict); ok && d.HasStream {
				if data, err := decodeStream(d); err == nil {
					collectResourceUsageFromBytes(data, resources, ru)
				}
			}
		}
	}
}

// markResourceUsed records that name was selected from resources' category
// sub-dictionary.
func markResourceUsed(resources PDFDict, category, name string, ru *resourceUsage) {
	sub, ok := resources.Entries[category].(PDFDict)
	if !ok || sub.Entries == nil {
		return
	}
	ptr := pdfValuePointer(sub.Entries)
	set := ru.used[ptr]
	if set == nil {
		set = map[string]bool{}
		ru.used[ptr] = set
	}
	set[name] = true
}

func collectResourceUsageFromBytes(data []byte, resources PDFDict, ru *resourceUsage) {
	cs := newContentScanner(data)
	cs.scan(func(op string, operands []PDFValue) {
		switch op {
		case "Do":
			if len(operands) == 0 {
				return
			}
			name, ok := operands[len(operands)-1].(PDFName)
			if !ok {
				return
			}
			markResourceUsed(resources, "XObject", name.Value, ru)
			xobjects, ok := resources.Entries["XObject"].(PDFDict)
			if !ok {
				return
			}
			xobj, ok := xobjects.Entries[name.Value].(PDFDict)
			if !ok || xobj.Entries["Subtype"] != (PDFName{Value: "Form"}) || !xobj.HasStream {
				return
			}
			ptr := pdfValuePointer(xobj.Entries)
			if ru.visitedForm[ptr] {
				return
			}
			ru.visitedForm[ptr] = true
			subResources, _ := xobj.Entries["Resources"].(PDFDict)
			if subResources.Entries == nil {
				subResources = resources
			}
			if subData, err := decodeStream(xobj); err == nil {
				collectResourceUsageFromBytes(subData, subResources, ru)
			}
		case "Tf":
			if len(operands) >= 2 {
				if name, ok := operands[len(operands)-2].(PDFName); ok {
					markResourceUsed(resources, "Font", name.Value, ru)
				}
			}
		case "gs":
			if len(operands) >= 1 {
				if name, ok := operands[len(operands)-1].(PDFName); ok {
					markResourceUsed(resources, "ExtGState", name.Value, ru)
				}
			}
		case "cs", "CS":
			if len(operands) >= 1 {
				if name, ok := operands[len(operands)-1].(PDFName); ok {
					markResourceUsed(resources, "ColorSpace", name.Value, ru)
				}
			}
		case "scn", "SCN":
			// The last operand is a Pattern name only when the current
			// colour space is /Pattern; otherwise it's a number, and the
			// type assertion below simply doesn't match.
			if len(operands) >= 1 {
				if name, ok := operands[len(operands)-1].(PDFName); ok {
					markResourceUsed(resources, "Pattern", name.Value, ru)
				}
			}
		case "sh":
			if len(operands) >= 1 {
				if name, ok := operands[0].(PDFName); ok {
					markResourceUsed(resources, "Shading", name.Value, ru)
				}
			}
		case "BDC", "DP":
			if len(operands) >= 2 {
				if name, ok := operands[len(operands)-1].(PDFName); ok {
					markResourceUsed(resources, "Properties", name.Value, ru)
				}
			}
		}
	})
}

// --- NameTooLong: name-value truncation and resource-key renaming ---

// maxNameLength mirrors the 127-byte ceiling validateArchitecturalLimits
// (verifier.go) enforces for both PDFName values and dictionary keys
// (6.1.12/1).
const maxNameLength = 127

// nameTooLongFixer remediates Checks.Structure.NameTooLong for both flavours
// the check covers: a PDFName value over the limit is truncated in place
// (mirroring the scalar clamps contentLimitsFixer already applies to other
// types, fixups_content.go -- a name this long is already non-conformant,
// so shortening it is accepted), and an overlong dictionary key is renamed
// to a short, collision-free replacement, with every content-stream
// operator referencing the old name (in a /Resources category that key
// belongs to) rewritten to the new one so resource lookups still resolve.
type nameTooLongFixer struct{}

func (nameTooLongFixer) Applies(c Check) bool {
	return c == Checks.Structure.NameTooLong
}

func (nameTooLongFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false

	walkScalars(*trailer, map[uintptr]bool{}, func(v PDFValue) (PDFValue, bool) {
		n, ok := v.(PDFName)
		if !ok || len(n.Value) <= maxNameLength {
			return v, false
		}
		changed = true
		return PDFName{Value: n.Value[:maxNameLength]}, true
	})

	renames := map[uintptr]map[string]string{} // category-dict ptr -> old name -> new name
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		var overlong []string
		for k := range d.Entries {
			if k != "_ref" && k != "_dirty" && len(k) > maxNameLength {
				overlong = append(overlong, k)
			}
		}
		for _, k := range overlong {
			newKey := shortenDictKey(d, k)
			d.Entries[newKey] = d.Entries[k]
			delete(d.Entries, k)
			changed = true
			ptr := pdfValuePointer(d.Entries)
			if renames[ptr] == nil {
				renames[ptr] = map[string]string{}
			}
			renames[ptr][k] = newKey
		}
	})
	if len(renames) > 0 {
		renameResourceReferences(trailer, renames)
	}

	return changed, nil
}

// shortenDictKey returns a name under maxNameLength bytes that isn't
// already a key of d, truncating k and, on collision, appending a numeric
// suffix until unique.
func shortenDictKey(d PDFDict, k string) string {
	base := k
	if len(base) > maxNameLength-8 {
		base = base[:maxNameLength-8]
	}
	if _, exists := d.Entries[base]; !exists {
		return base
	}
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("%s~%d", base, i)
		if _, exists := d.Entries[candidate]; !exists {
			return candidate
		}
	}
}

// renameResourceReferences rewrites every content-stream operator (Do, Tf,
// gs, cs/CS, scn/SCN, sh, BDC/DP -- the same operators
// collectResourceUsageFromBytes recognises) selecting one of the renamed
// keys to use its replacement instead, via walkResourceAwareContent.
func renameResourceReferences(trailer *PDFDict, renames map[uintptr]map[string]string) {
	walkResourceAwareContent(trailer, func(op string, operands []PDFValue, resources PDFDict, changed *bool) {
		category, fromEnd := resourceOperatorTarget(op)
		if category == "" || fromEnd >= len(operands) {
			return
		}
		idx := len(operands) - 1 - fromEnd
		name, ok := operands[idx].(PDFName)
		if !ok {
			return
		}
		sub, ok := resources.Entries[category].(PDFDict)
		if !ok {
			return
		}
		byName, ok := renames[pdfValuePointer(sub.Entries)]
		if !ok {
			return
		}
		if newName, ok := byName[name.Value]; ok {
			operands[idx] = PDFName{Value: newName}
			*changed = true
		}
	})
}

// resourceOperatorTarget reports which /Resources category a resource-
// selecting operator names its resource in, and that name operand's
// position counted backward from the end of operands (0 = last) --
// mirroring collectResourceUsageFromBytes' own operator handling exactly
// (Tf's font name is its first of two operands, hence 1; sh always has
// exactly one operand, so "last" and "first" coincide).
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

// maxCMapCID mirrors checkCMapCIDLimits' own ceiling (checks_font.go).
const maxCMapCID = 65535

// cmapCIDClampFixer remediates Checks.Structure.CMapCIDOutOfRange by
// clamping any cidrange/cidchar CID value over 65535 down to 65535 directly
// within the CMap's PostScript stream bytes, mirroring
// checkCMapCIDLimits' own token-position state machine so it only ever
// touches the exact values that check would flag.
type cmapCIDClampFixer struct{}

func (cmapCIDClampFixer) Applies(c Check) bool {
	return c == Checks.Structure.CMapCIDOutOfRange
}

func (cmapCIDClampFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) (PDFDict, bool) {
		if (d.Entries["Type"] != PDFName{Value: "CMap"}) || !d.HasStream {
			return d, false
		}
		data, err := decodeStream(d)
		if err != nil {
			return d, false
		}
		clamped, ok := clampCMapCIDs(data)
		if !ok {
			return d, false
		}
		d.RawStream = clamped
		MarkStreamDirty(&d)
		changed = true
		return d, true
	})
	return changed, nil
}

// clampCMapCIDs rewrites every cidrange/cidchar CID token over maxCMapCID in
// data to maxCMapCID, splicing the replacement directly into the original
// bytes (preserving everything else byte-for-byte) so it never needs to
// re-serialize the surrounding PostScript.
func clampCMapCIDs(data []byte) ([]byte, bool) {
	tokens := cmapTokenize(data)
	type span struct{ start, end int }
	var spans []span

	inCIDRange, inCIDChar, pos := false, false, 0
	for _, tok := range tokens {
		switch tok.text {
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
				if cid, ok := cmapParseInt(tok.text); ok && cid > maxCMapCID {
					spans = append(spans, span{tok.start, tok.end})
				}
				pos = 0
			}
		}
	}
	if len(spans) == 0 {
		return nil, false
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	clampedText := []byte(fmt.Sprintf("%d", maxCMapCID))
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
type resourceOpRewriter func(op string, operands []PDFValue, resources PDFDict, changed *bool)

// walkResourceAwareContent calls rewrite for every operator in every Page's
// content stream, and recursively for any Form XObject invoked via Do
// (using that Form's own Resources), writing back any content stream
// rewrite actually changed. Unlike walkContentStreams (fixups_content.go),
// which has no Resources context, this exists specifically for rewrites
// that need to know which resource dictionary a name operand selects from
// (renameResourceReferences, above). Tiling patterns' own content isn't
// recursed into, matching computeResourceUsage's same scope.
func walkResourceAwareContent(trailer *PDFDict, rewrite resourceOpRewriter) bool {
	changed := false
	visited := map[uintptr]bool{}
	visitedForm := map[uintptr]bool{}

	var walk func(v PDFValue)
	walk = func(v PDFValue) {
		switch val := v.(type) {
		case PDFDict:
			ptr := pdfValuePointer(val.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			if val.Entries["Type"] == (PDFName{Value: "Page"}) {
				resources, _ := val.Entries["Resources"].(PDFDict)
				rewritePageContents(val, resources, rewrite, visitedForm, &changed)
				return
			}
			for _, child := range val.Entries {
				walk(child)
			}
		case PDFArray:
			ptr := pdfValuePointer(val)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			for _, item := range val {
				walk(item)
			}
		}
	}
	walk(*trailer)
	return changed
}

func rewritePageContents(page, resources PDFDict, rewrite resourceOpRewriter, visitedForm map[uintptr]bool, changed *bool) {
	switch v := page.Entries["Contents"].(type) {
	case PDFDict:
		if v.HasStream {
			if fixed, ok := rewriteResourceAwareStream(v, resources, rewrite, visitedForm); ok {
				page.Entries["Contents"] = fixed
				*changed = true
			}
		}
	case PDFArray:
		for i, item := range v {
			d, ok := item.(PDFDict)
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

func rewriteResourceAwareStream(dict, resources PDFDict, rewrite resourceOpRewriter, visitedForm map[uintptr]bool) (PDFDict, bool) {
	data, err := decodeStream(dict)
	if err != nil {
		return dict, false
	}

	var ops []contentOp
	modified := false
	newContentScanner(data).scan(func(op string, operands []PDFValue) {
		rewrite(op, operands, resources, &modified)
		ops = append(ops, contentOp{Op: op, Operands: operands})

		if op != "Do" || len(operands) == 0 {
			return
		}
		name, ok := operands[len(operands)-1].(PDFName)
		if !ok {
			return
		}
		xobjects, ok := resources.Entries["XObject"].(PDFDict)
		if !ok {
			return
		}
		xobj, ok := xobjects.Entries[name.Value].(PDFDict)
		if !ok || xobj.Entries["Subtype"] != (PDFName{Value: "Form"}) || !xobj.HasStream {
			return
		}
		ptr := pdfValuePointer(xobj.Entries)
		if visitedForm[ptr] {
			return
		}
		visitedForm[ptr] = true
		subResources, _ := xobj.Entries["Resources"].(PDFDict)
		if subResources.Entries == nil {
			subResources = resources
		}
		if fixed, ok := rewriteResourceAwareStream(xobj, subResources, rewrite, visitedForm); ok {
			xobjects.Entries[name.Value] = fixed
			modified = true
		}
	})
	if !modified {
		return dict, false
	}

	out, err := writeContentStream(ops)
	if err != nil {
		return dict, false
	}
	delete(dict.Entries, "Filter")
	delete(dict.Entries, "DecodeParms")
	delete(dict.Entries, "DP")
	dict.RawStream = out
	MarkStreamDirty(&dict)
	return dict, true
}
