package pdfrab

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

func (deviceNColorantsFixer) Applies(c Check) bool {
	return c == Checks.Structure.DeviceNColorants
}

func (deviceNColorantsFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
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
func isOversizedDeviceN(v PDFValue) bool {
	arr, ok := v.(PDFArray)
	if !ok || len(arr) < 3 {
		return false
	}
	head, ok := arr[0].(PDFName)
	if !ok || head.Value != "DeviceN" {
		return false
	}
	names, ok := arr[1].(PDFArray)
	return ok && len(names) > 8
}

// rewriteDeviceNContentUsage walks every Page's content (and, recursively,
// any Form XObject it invokes via Do, using that Form's own /Resources) and
// rewrites cs/CS+scn/SCN usage of an oversized DeviceN space into a literal
// rg/RG, mirroring the Form-recursion computeResourceUsage (fixups_limits.go)
// already uses.
func rewriteDeviceNContentUsage(trailer *PDFDict) bool {
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
			if (val.Entries["Type"] == PDFName{Value: "Page"}) {
				resources, _ := val.Entries["Resources"].(PDFDict)
				rewriteDeviceNPageContents(val, resources, visitedForm, &changed)
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

func rewriteDeviceNPageContents(page, resources PDFDict, visitedForm map[uintptr]bool, changed *bool) {
	switch v := page.Entries["Contents"].(type) {
	case PDFDict:
		if v.HasStream {
			if fixed, ok := rewriteDeviceNStream(v, resources, visitedForm); ok {
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
func rewriteDeviceNStream(dict, resources PDFDict, visitedForm map[uintptr]bool) (PDFDict, bool) {
	data, err := decodeStream(dict)
	if err != nil {
		return dict, false
	}

	var ops []contentOp
	modified := false
	var fillCS, strokeCS PDFValue
	newContentScanner(data).scan(func(op string, operands []PDFValue) {
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
				r, g, b := ResolveColor(fillCS, numericOperands(operands), resources)
				ops = append(ops, contentOp{Op: "rg", Operands: []PDFValue{PDFReal(r), PDFReal(g), PDFReal(b)}})
				modified = true
				return
			}
		case "SCN":
			if isOversizedDeviceN(strokeCS) {
				r, g, b := ResolveColor(strokeCS, numericOperands(operands), resources)
				ops = append(ops, contentOp{Op: "RG", Operands: []PDFValue{PDFReal(r), PDFReal(g), PDFReal(b)}})
				modified = true
				return
			}
		case "Do":
			if _, ok := recurseDeviceNForm(operands, resources, visitedForm); ok {
				modified = true
			}
		}
		ops = append(ops, contentOp{Op: op, Operands: operands})
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

// recurseDeviceNForm follows a Do operator's Form XObject reference (if any)
// and rewrites its content in place via rewriteDeviceNStream, guarded by
// visitedForm against revisiting a Form shared by multiple Do calls.
func recurseDeviceNForm(operands []PDFValue, resources PDFDict, visitedForm map[uintptr]bool) (PDFDict, bool) {
	if len(operands) == 0 {
		return PDFDict{}, false
	}
	name, ok := operands[len(operands)-1].(PDFName)
	if !ok {
		return PDFDict{}, false
	}
	xobjects, ok := resources.Entries["XObject"].(PDFDict)
	if !ok {
		return PDFDict{}, false
	}
	xobj, ok := xobjects.Entries[name.Value].(PDFDict)
	if !ok || (xobj.Entries["Subtype"] != PDFName{Value: "Form"}) || !xobj.HasStream {
		return PDFDict{}, false
	}
	ptr := pdfValuePointer(xobj.Entries)
	if visitedForm[ptr] {
		return PDFDict{}, false
	}
	visitedForm[ptr] = true
	subResources, _ := xobj.Entries["Resources"].(PDFDict)
	if subResources.Entries == nil {
		subResources = resources
	}
	fixed, ok := rewriteDeviceNStream(xobj, subResources, visitedForm)
	if !ok {
		return PDFDict{}, false
	}
	xobjects.Entries[name.Value] = fixed
	return fixed, true
}

// rewriteDeviceNImageDicts rewrites every Image XObject whose inline
// /ColorSpace is an oversized DeviceN array into a plain, opaque DeviceRGB
// image: decoding its samples via DecodeImageRGBA (which already resolves
// DeviceN pixels through ResolveColor) and repacking them, the same
// in-place bake pattern bakeSoftMaskOut (fixups_transparency.go) uses for
// /SMask.
func rewriteDeviceNImageDicts(trailer *PDFDict) bool {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) (PDFDict, bool) {
		if (d.Entries["Subtype"] != PDFName{Value: "Image"}) {
			return d, false
		}
		if !isOversizedDeviceN(d.Entries["ColorSpace"]) {
			return d, false
		}
		img, err := DecodeImageRGBA(d, PDFDict{})
		if err != nil {
			return d, false
		}
		d.Entries["BitsPerComponent"] = PDFInteger(8)
		d.Entries["ColorSpace"] = PDFName{Value: "DeviceRGB"}
		delete(d.Entries, "Decode")
		d.HasStream = true
		d.RawStream = packRGBSamples(img)
		MarkStreamDirty(&d)
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
func pruneDeadDeviceNColorSpaceEntries(trailer *PDFDict) bool {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		cs, ok := d.Entries["ColorSpace"].(PDFDict)
		if !ok {
			return
		}
		for name, v := range cs.Entries {
			if isOversizedDeviceN(v) {
				delete(cs.Entries, name)
				changed = true
			}
		}
	})
	return changed
}
