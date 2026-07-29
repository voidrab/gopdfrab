package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// deviceNColorantsFixer remediates Checks.Structure.DeviceNColorants: a
// DeviceN colour-space array listing more than 8 colorants
// (validateColourSpaceArray, checks_colour.go), reported against the array
// itself rather than any particular use of it. Truncating the colorant list
// would leave it shorter than its own tint-transform function's declared
// input arity (fixups_limits.go's earlier rejection of that approach), so
// the only lossless fix is to make the array unreachable from the trailer
// instead: resolve every use of it to a literal RGB colour -- reusing
// ResolveColor/resolveSeparation (colorspace.go), which already evaluates a
// DeviceN space's tint transform -- and delete the resource entries that
// named it.
type deviceNColorantsFixer struct{}

func init() {
	registerFixer(deviceNColorantsFixer{})
}

func (deviceNColorantsFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.DeviceNColorants
}

func (deviceNColorantsFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	if rewriteDeviceNContentUsage(trailer) {
		changed = true
	}
	if rewriteDeviceNImageDicts(trailer) {
		changed = true
	}
	if pruneDeadDeviceNColorSpaceEntries(trailer) {
		changed = true
	}
	return changed, nil
}

// isOversizedDeviceN mirrors validateColourSpaceArray's own DeviceN-colorant
// trigger exactly: a [/DeviceN names alternateSpace tintTransform] array
// listing more than 8 colorant names.
func isOversizedDeviceN(v pdf.PDFValue) bool {
	arr, ok := v.(pdf.PDFArray)
	if !ok || len(arr) < 3 {
		return false
	}
	head, ok := arr[0].(pdf.PDFName)
	if !ok || head.Value != "DeviceN" {
		return false
	}
	names, ok := arr[1].(pdf.PDFArray)
	return ok && len(names) > 8
}

// rewriteDeviceNContentUsage walks every Page's content (and, recursively,
// any Form XObject it invokes via Do, using that Form's own /Resources) and
// rewrites cs/CS+scn/SCN usage of an oversized DeviceN space into a literal
// rg/RG, mirroring the Form-recursion computeResourceUsage (fixups_limits.go)
// already uses.
func rewriteDeviceNContentUsage(trailer *pdf.PDFDict) bool {
	changed := false
	visited := map[uintptr]bool{}
	visitedForm := map[uintptr]bool{}

	var walk func(v pdf.PDFValue)
	walk = func(v pdf.PDFValue) {
		switch val := v.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(val.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			if (val.Entries.Get("Type") == pdf.PDFName{Value: "Page"}) {
				resources, _ := val.Entries.Get("Resources").(pdf.PDFDict)
				rewriteDeviceNPageContents(val, resources, visitedForm, &changed)
				return
			}
			for _, child := range val.Entries.All() {
				walk(child)
			}
		case pdf.PDFArray:
			ptr := pdf.ArrayPointer(val)
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

func rewriteDeviceNPageContents(page, resources pdf.PDFDict, visitedForm map[uintptr]bool, changed *bool) {
	switch v := page.Entries.Get("Contents").(type) {
	case pdf.PDFDict:
		if v.HasStream {
			if fixed, ok := rewriteDeviceNStream(v, resources, visitedForm); ok {
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
			if fixed, ok := rewriteDeviceNStream(d, resources, visitedForm); ok {
				v[i] = fixed
				*changed = true
			}
		}
	}
}

// rewriteDeviceNStream decodes dict's content stream into its full op
// sequence, tracking the currently cs/CS-selected fill/stroke colour space
// across it (an approximation of q/Q-scoped graphics state -- adequate here
// since the Check this fixer clears cares only about the array's
// reachability from the resource graph, not about exactly which q/Q scope
// every scn/SCN call falls in). A cs/CS selecting an oversized DeviceN space
// is dropped, and the scn/SCN call(s) that use it are replaced with a
// literal rg/RG resolved via ResolveColor. Recurses into any Form XObject
// invoked via Do, using that Form's own /Resources.
func rewriteDeviceNStream(dict, resources pdf.PDFDict, visitedForm map[uintptr]bool) (pdf.PDFDict, bool) {
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		return dict, false
	}

	// Emit while scanning: see rewriteContentStreamDict. Retaining operands
	// here would alias the scanner's reused stack.
	var cw writer.ContentStreamWriter
	modified := false
	var fillCS, strokeCS pdf.PDFValue
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "cs":
			fillCS = resolveOperandColorSpace(operands, resources)
			if isOversizedDeviceN(fillCS) {
				modified = true
				return
			}
		case "CS":
			strokeCS = resolveOperandColorSpace(operands, resources)
			if isOversizedDeviceN(strokeCS) {
				modified = true
				return
			}
		case "scn":
			if isOversizedDeviceN(fillCS) {
				r, g, b := pdf.ResolveColor(fillCS, numericOperands(operands), resources)
				_ = cw.WriteOp("rg", []pdf.PDFValue{pdf.PDFReal(r), pdf.PDFReal(g), pdf.PDFReal(b)})
				modified = true
				return
			}
		case "SCN":
			if isOversizedDeviceN(strokeCS) {
				r, g, b := pdf.ResolveColor(strokeCS, numericOperands(operands), resources)
				_ = cw.WriteOp("RG", []pdf.PDFValue{pdf.PDFReal(r), pdf.PDFReal(g), pdf.PDFReal(b)})
				modified = true
				return
			}
		case "Do":
			if _, ok := recurseDeviceNForm(operands, resources, visitedForm); ok {
				modified = true
			}
		}
		_ = cw.WriteOp(op, operands)
	})
	if !modified || cw.Err() != nil {
		return dict, false
	}

	if err := writer.SetStreamFlate(&dict, cw.Bytes()); err != nil {
		return dict, false
	}
	return dict, true
}

// recurseDeviceNForm follows a Do operator's Form XObject reference (if any)
// and rewrites its content in place via rewriteDeviceNStream, guarded by
// visitedForm against revisiting a Form shared by multiple Do calls.
func recurseDeviceNForm(operands []pdf.PDFValue, resources pdf.PDFDict, visitedForm map[uintptr]bool) (pdf.PDFDict, bool) {
	if len(operands) == 0 {
		return pdf.PDFDict{}, false
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		return pdf.PDFDict{}, false
	}
	xobjects, ok := resources.Entries.Get("XObject").(pdf.PDFDict)
	if !ok {
		return pdf.PDFDict{}, false
	}
	xobj, ok := xobjects.Entries.Get(name.Value).(pdf.PDFDict)
	if !ok || (xobj.Entries.Get("Subtype") != pdf.PDFName{Value: "Form"}) || !xobj.HasStream {
		return pdf.PDFDict{}, false
	}
	ptr := pdf.ValuePointer(xobj.Entries)
	if visitedForm[ptr] {
		return pdf.PDFDict{}, false
	}
	visitedForm[ptr] = true
	subResources, _ := xobj.Entries.Get("Resources").(pdf.PDFDict)
	if subResources.Entries == nil {
		subResources = resources
	}
	fixed, ok := rewriteDeviceNStream(xobj, subResources, visitedForm)
	if !ok {
		return pdf.PDFDict{}, false
	}
	xobjects.Entries.Set(name.Value, fixed)
	return fixed, true
}

// rewriteDeviceNImageDicts rewrites every Image XObject whose inline
// /ColorSpace is an oversized DeviceN array into a plain, opaque DeviceRGB
// image: decoding its samples via DecodeImageRGBA (which already resolves
// DeviceN pixels through ResolveColor) and repacking them, the same
// in-place bake pattern bakeSoftMaskOut (fixups_transparency.go) uses for
// /SMask.
func rewriteDeviceNImageDicts(trailer *pdf.PDFDict) bool {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Image"}) {
			return d, false
		}
		if !isOversizedDeviceN(d.Entries.Get("ColorSpace")) {
			return d, false
		}
		img, err := DecodeImageRGBA(d, pdf.PDFDict{})
		if err != nil {
			return d, false
		}
		d.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
		d.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
		d.Entries.Del("Decode")
		if err := writer.SetStreamFlate(&d, packRGBSamples(img)); err != nil {
			return d, false
		}
		changed = true
		return d, true
	})
	return changed
}

// pruneDeadDeviceNColorSpaceEntries deletes every /Resources /ColorSpace
// entry naming an oversized DeviceN array. Once
// rewriteDeviceNContentUsage/rewriteDeviceNImageDicts have replaced every
// use of it, the entry is the array's only remaining path of reachability
// from the trailer, so dropping it makes the array unreachable -- the
// document-wide validator walk that reported the Check never visits it
// again, and WriteDocument never re-emits it.
func pruneDeadDeviceNColorSpaceEntries(trailer *pdf.PDFDict) bool {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		cs, ok := d.Entries.Get("ColorSpace").(pdf.PDFDict)
		if !ok {
			return
		}
		cs.Entries.DeleteFunc(func(_ string, v pdf.PDFValue) bool {
			if !isOversizedDeviceN(v) {
				return false
			}
			changed = true
			return true
		})
	})
	return changed
}
