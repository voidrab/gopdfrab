package convert

import (
	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// deviceNColorantsFixer remediates Checks.Structure.DeviceNColorants, which
// reports a DeviceN colour-space array that lists more colorants than the
// level allows (validateColourSpaceArray in internal/verify/checks_colour.go).
// The report is against the array itself, so the fix replaces every use of
// the array with a literal RGB colour and deletes the resource entries that
// name it, making the array unreachable from the trailer. Truncating the
// colorant list would leave fewer names than the tint transform's declared
// input arity, so it is not used. Colours are resolved with ResolveColor,
// which evaluates the DeviceN tint transform through resolveSeparation in
// internal/pdf/colorspace.go.
//
// limit is the most colorants the level allows. Spaces within it stay.
type deviceNColorantsFixer struct{ limit int }

func (deviceNColorantsFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.DeviceNColorants
}

func (f deviceNColorantsFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	if f.rewriteDeviceNContentUsage(trailer) {
		changed = true
	}
	if f.rewriteDeviceNImageDicts(trailer) {
		changed = true
	}
	if f.pruneDeadDeviceNColorSpaceEntries(trailer) {
		changed = true
	}
	return changed, nil
}

// oversized reports whether v is a [/DeviceN names alternateSpace
// tintTransform] array listing more than limit colorant names. It mirrors the
// DeviceN-colorant trigger of validateColourSpaceArray exactly.
func (f deviceNColorantsFixer) oversized(v pdf.PDFValue) bool {
	arr, ok := v.(pdf.PDFArray)
	if !ok || len(arr) < 3 {
		return false
	}
	head, ok := arr[0].(pdf.PDFName)
	if !ok || head.Value != "DeviceN" {
		return false
	}
	names, ok := arr[1].(pdf.PDFArray)
	return ok && len(names) > f.limit
}

// rewriteDeviceNContentUsage walks every Page's content (and, recursively,
// any Form XObject it draws, using that Form's own /Resources) and
// rewrites cs/CS+scn/SCN usage of an oversized DeviceN space into a literal
// rg/RG, mirroring the Form-recursion computeResourceUsage (fixups_limits.go)
// already uses.
func (f deviceNColorantsFixer) rewriteDeviceNContentUsage(trailer *pdf.PDFDict) bool {
	changed := false
	visitedForm := map[uintptr]bool{}
	walkGraph(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if d.Entries.Get("Type") != (pdf.PDFName{Value: "Page"}) {
			return true
		}
		resources := pdf.PageResources(d)
		f.rewriteDeviceNPageContents(d, resources, visitedForm, &changed)
		return false
	}, nil)
	return changed
}

func (f deviceNColorantsFixer) rewriteDeviceNPageContents(page, resources pdf.PDFDict, visitedForm map[uintptr]bool, changed *bool) {
	switch v := page.Entries.Get("Contents").(type) {
	case pdf.PDFDict:
		if v.IsStream() {
			if fixed, ok := f.rewriteDeviceNStream(v, resources, visitedForm); ok {
				page.Entries.Set("Contents", fixed)
				*changed = true
			}
		}
	case pdf.PDFArray:
		for i, item := range v {
			d, ok := item.(pdf.PDFDict)
			if !ok || !d.IsStream() {
				continue
			}
			if fixed, ok := f.rewriteDeviceNStream(d, resources, visitedForm); ok {
				v[i] = fixed
				*changed = true
			}
		}
	}
}

// rewriteDeviceNStream rewrites dict's content stream, following the
// graphics state through it. A cs or CS selecting an oversized DeviceN space
// is dropped, and each scn or SCN that sets a colour in it is replaced with
// the rg or RG of the same colour. Recurses into any Form XObject that the
// stream draws, using that Form's own /Resources.
func (f deviceNColorantsFixer) rewriteDeviceNStream(dict, resources pdf.PDFDict, visitedForm map[uintptr]bool) (pdf.PDFDict, bool) {
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		return dict, false
	}

	// Emit while scanning: see rewriteContentStreamDict. Retaining operands
	// here would alias the scanner's reused stack.
	var cw writer.ContentStreamWriter
	modified := false
	gs := content.NewTracker()
	cs := pdf.NewContentScanner(data)
	cs.Scan(func(op string, operands []pdf.PDFValue) {
		gs.Step(op, operands, resources)
		switch op {
		case "cs":
			if f.oversized(gs.Fill.Space) {
				modified = true
				return
			}
		case "CS":
			if f.oversized(gs.Stroke.Space) {
				modified = true
				return
			}
		case "scn":
			if f.oversized(gs.Fill.Space) {
				r, g, b := pdf.ResolveColor(gs.Fill.Space, pdf.NumericOperands(operands), resources)
				_ = cw.WriteOp("rg", []pdf.PDFValue{pdf.PDFReal(r), pdf.PDFReal(g), pdf.PDFReal(b)})
				modified = true
				return
			}
		case "SCN":
			if f.oversized(gs.Stroke.Space) {
				r, g, b := pdf.ResolveColor(gs.Stroke.Space, pdf.NumericOperands(operands), resources)
				_ = cw.WriteOp("RG", []pdf.PDFValue{pdf.PDFReal(r), pdf.PDFReal(g), pdf.PDFReal(b)})
				modified = true
				return
			}
		case "Do":
			if _, ok := f.recurseDeviceNForm(operands, resources, visitedForm); ok {
				modified = true
			}
		}
		_ = cw.WriteOp(op, operands)
	})
	// A stream only part of which could be read: see rewriteContentStreamDict.
	if !modified || !cs.Complete() || cw.Err() != nil {
		return dict, false
	}

	writer.SetStreamFlate(&dict, cw.Bytes())
	return dict, true
}

// recurseDeviceNForm follows the Form XObject that an operator draws, if any,
// and rewrites its content in place via rewriteDeviceNStream. visitedForm
// guards against revisiting a Form that is drawn more than once.
func (f deviceNColorantsFixer) recurseDeviceNForm(operands []pdf.PDFValue, resources pdf.PDFDict, visitedForm map[uintptr]bool) (pdf.PDFDict, bool) {
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
	if !ok || (xobj.Entries.Get("Subtype") != pdf.PDFName{Value: "Form"}) || !xobj.IsStream() {
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
	fixed, ok := f.rewriteDeviceNStream(xobj, subResources, visitedForm)
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
func (f deviceNColorantsFixer) rewriteDeviceNImageDicts(trailer *pdf.PDFDict) bool {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Image"}) {
			return false
		}
		if !f.oversized(d.Entries.Get("ColorSpace")) {
			return false
		}
		img, err := raster.DecodeImageRGBA(d, pdf.PDFDict{})
		if err != nil {
			return false
		}
		d.Entries.Set("BitsPerComponent", pdf.PDFInteger(8))
		d.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
		d.Entries.Del("Decode")
		writer.SetStreamFlate(&d, packRGBSamples(img))
		changed = true
		return true
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
func (f deviceNColorantsFixer) pruneDeadDeviceNColorSpaceEntries(trailer *pdf.PDFDict) bool {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		cs, ok := d.Entries.Get("ColorSpace").(pdf.PDFDict)
		if !ok {
			return
		}
		cs.Entries.DeleteFunc(func(_ string, v pdf.PDFValue) bool {
			if !f.oversized(v) {
				return false
			}
			changed = true
			return true
		})
	})
	return changed
}
