package verify

import (
	"bytes"
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// 6.2.2 Content streams (ISO 19005-2)

// inheritance is what one content stream may and may not take its named
// resources from: own is the Resources dictionary associated with the stream,
// and from is the dictionary it would otherwise inherit from. A stream with
// nothing to inherit has a nil from.
type inheritance struct {
	own, from pdf.PDFDict
}

// inheritanceOf returns the inheritance of a content root while the walk is
// at its holder. A page uses its own Resources dictionary only; the page
// tree's are inherited. A form XObject, tiling pattern or Type 3 font inherits
// from the scope its holder sits in.
func inheritanceOf(r pdf.ContentRoot, holder pdf.PDFDict, ctx *ValidationContext) inheritance {
	own, hasOwn := holder.Entries.Get("Resources").(pdf.PDFDict)
	switch {
	case r.Kind == pdf.ContentPage && hasOwn:
		return inheritance{own: own}
	case r.Kind == pdf.ContentPage:
		return inheritance{from: r.Resources}
	case hasOwn:
		return inheritance{own: own, from: ctx.parentResourceScope}
	}
	return inheritance{from: ctx.resourceScope}
}

// check reports name when the stream finds it in the category of resources
// it inherits rather than in its own.
func (in inheritance) check(category, name string, obj pdf.PDFValue, ctx *ValidationContext) {
	if hasResource(in.own, category, name) || !hasResource(in.from, category, name) {
		return
	}
	ctx.Report(pdf.Checks.Colour.InheritedResources, obj,
		fmt.Sprintf("content stream uses /%s from inherited %s resources instead of its own", name, category))
}

// checkOperands reports the operands of op that name a resource the stream
// inherits.
func (in inheritance) checkOperands(op string, operands []pdf.PDFValue, obj pdf.PDFValue, ctx *ValidationContext) {
	if in.from.Entries == nil {
		return
	}
	ResourceNames(op, operands, func(category, name string) {
		in.check(category, name, obj, ctx)
	})
}

// hasResource reports whether resources lists name in the given category.
func hasResource(resources pdf.PDFDict, category, name string) bool {
	sub, ok := resources.Entries.Get(category).(pdf.PDFDict)
	return ok && sub.Entries.Get(name) != nil
}

// deviceSpaceNames are the colour space names that need no resource.
var deviceSpaceNames = map[string]bool{
	"DeviceGray": true, "DeviceRGB": true, "DeviceCMYK": true, "Pattern": true,
	"G": true, "RGB": true, "CMYK": true,
}

// ResourceNames calls fn with the resource category and name of each operand
// of a content stream operator that names a resource.
func ResourceNames(op string, operands []pdf.PDFValue, fn func(category, name string)) {
	if len(operands) == 0 {
		return
	}
	named := func(category string, v pdf.PDFValue) {
		if n, ok := v.(pdf.PDFName); ok {
			fn(category, n.Value)
		}
	}
	last := operands[len(operands)-1]
	switch op {
	case "Tf":
		named("Font", operands[0])
	case "cs", "CS":
		if n, ok := last.(pdf.PDFName); ok && !deviceSpaceNames[n.Value] {
			fn("ColorSpace", n.Value)
		}
	case "scn", "SCN":
		named("Pattern", last)
	case "sh":
		named("Shading", last)
	case "Do":
		named("XObject", last)
	case "gs":
		named("ExtGState", last)
	case "BDC", "DP":
		if len(operands) == 2 {
			named("Properties", operands[1])
		}
	case "INLINEIMAGE":
		for i := 0; i+1 < len(operands); i += 2 {
			if k, ok := operands[i].(pdf.PDFName); ok && (k.Value == "CS" || k.Value == "ColorSpace") {
				if n, ok := operands[i+1].(pdf.PDFName); ok && !deviceSpaceNames[n.Value] && InlineCSAbbrev[n.Value] == "" {
					fn("ColorSpace", n.Value)
				}
			}
		}
	}
}

// 6.2.3 Output intent (ISO 19005-2)

// deviceColourWithoutPDFAIntentRule reports, once, a file whose device colour
// goes uncovered because its output intents are all of other kinds than the
// PDF/A one, such as PDF/X ones.
func deviceColourWithoutPDFAIntentRule(d *pdf.Reader, _ pdf.PDFValue, ctx *ValidationContext) []pdf.PDFError {
	if ctx.hasOutputIntent || !ctx.uncoveredDeviceColour {
		return nil
	}
	value, err := d.ResolveGraphByPath([]string{"Root", "OutputIntents"})
	intents, ok := value.(pdf.PDFArray)
	if err != nil || !ok || len(intents) == 0 {
		return nil
	}
	return []pdf.PDFError{pdf.NewError(
		pdf.Checks.Colour.DeviceColourWithoutPDFAIntent,
		[]error{fmt.Errorf("device colour is used, but no output intent has /S /%s", ctx.Spec.OutputIntentSubtype)},
		0,
		refOf(catalog(d)),
	)}
}

// 6.2.4.2 ICCBased colour spaces (ISO 19005-2)

// overprint is the part of the graphics state the ICCBased CMYK overprint
// rule reads: whether the stroking and nonstroking colour spaces are ICCBased
// CMYK, the two overprint flags and the overprint mode.
type overprint struct {
	strokeCMYK, fillCMYK bool
	op, opFill           bool
	mode                 int
}

// overprintTracker follows the overprint state through one content stream.
type overprintTracker struct {
	state overprint
	saved []overprint
}

// fillOps and strokeOps are the painting operators that use the nonstroking
// and the stroking colour.
var (
	fillOps   = map[string]bool{"f": true, "F": true, "f*": true, "B": true, "B*": true, "b": true, "b*": true, "Tj": true, "TJ": true, "'": true, "\"": true}
	strokeOps = map[string]bool{"S": true, "s": true, "B": true, "B*": true, "b": true, "b*": true}
)

// step applies op and reports a painting operator that overprints an
// ICCBased CMYK colour with overprint mode 1.
func (t *overprintTracker) step(op string, operands []pdf.PDFValue, resources pdf.PDFDict, obj pdf.PDFValue, ctx *ValidationContext) {
	s := &t.state
	switch op {
	case "q":
		t.saved = append(t.saved, *s)
	case "Q":
		if n := len(t.saved); n > 0 {
			*s, t.saved = t.saved[n-1], t.saved[:n-1]
		}
	case "CS":
		s.strokeCMYK = len(operands) > 0 && isICCBasedCMYK(operands[len(operands)-1], resources)
	case "cs":
		s.fillCMYK = len(operands) > 0 && isICCBasedCMYK(operands[len(operands)-1], resources)
	case "gs":
		if len(operands) == 0 {
			return
		}
		name, _ := operands[len(operands)-1].(pdf.PDFName)
		states, _ := resources.Entries.Get("ExtGState").(pdf.PDFDict)
		gs, _ := states.Entries.Get(name.Value).(pdf.PDFDict)
		if b, ok := gs.Entries.Get("OP").(pdf.PDFBoolean); ok {
			s.op = bool(b)
			if gs.Entries.Get("op") == nil {
				s.opFill = bool(b)
			}
		}
		if b, ok := gs.Entries.Get("op").(pdf.PDFBoolean); ok {
			s.opFill = bool(b)
		}
		if m, ok := gs.Entries.Get("OPM").(pdf.PDFInteger); ok {
			s.mode = int(m)
		}
	}
	if s.mode != 1 {
		return
	}
	if (fillOps[op] && s.fillCMYK && s.opFill) || (strokeOps[op] && s.strokeCMYK && s.op) {
		ctx.Report(pdf.Checks.Colour.ICCBasedCMYKOverprint, obj,
			"an ICCBased CMYK colour is painted with overprinting on and overprint mode 1")
	}
}

// isICCBasedCMYK reports whether a colour space operand names an ICCBased
// colour space of four components.
func isICCBasedCMYK(name pdf.PDFValue, resources pdf.PDFDict) bool {
	n, ok := name.(pdf.PDFName)
	if !ok {
		return false
	}
	spaces, _ := resources.Entries.Get("ColorSpace").(pdf.PDFDict)
	arr, ok := spaces.Entries.Get(n.Value).(pdf.PDFArray)
	if !ok || len(arr) < 2 || arr[0] != (pdf.PDFName{Value: "ICCBased"}) {
		return false
	}
	stream, _ := arr[1].(pdf.PDFDict)
	return stream.Entries.Get("N") == pdf.PDFInteger(4)
}

// 6.2.4.4 Separation and DeviceN colour spaces (ISO 19005-2)

// processColorants are the colorant names of DeviceCMYK, which a DeviceN
// colour space need not describe in its Colorants dictionary.
var processColorants = map[string]bool{"Cyan": true, "Magenta": true, "Yellow": true, "Black": true}

// ProcessColorant reports whether name is a colorant of DeviceCMYK.
func ProcessColorant(name string) bool { return processColorants[name] }

// checkDeviceNColorants reports a DeviceN colour space with a spot colorant
// its attributes dictionary describes neither in Colorants nor among the
// process components.
func checkDeviceNColorants(arr pdf.PDFArray, ctx *ValidationContext) {
	if missing := MissingColorants(arr); len(missing) > 0 {
		ctx.Report(pdf.Checks.Colour.DeviceNColorantsMissing, arr,
			fmt.Sprintf("DeviceN spot colorant /%s has no entry in the Colorants dictionary", missing[0]))
	}
}

// MissingColorants returns the spot colorants of a DeviceN colour space array
// that its attributes dictionary describes neither in Colorants nor among the
// process components, in the order the array names them.
func MissingColorants(arr pdf.PDFArray) []string {
	if len(arr) < 2 {
		return nil
	}
	names, ok := arr[1].(pdf.PDFArray)
	if !ok {
		return nil
	}
	var attrs pdf.PDFDict
	if len(arr) > 4 {
		attrs, _ = arr[4].(pdf.PDFDict)
	}
	colorants, _ := attrs.Entries.Get("Colorants").(pdf.PDFDict)
	process, _ := attrs.Entries.Get("Process").(pdf.PDFDict)
	components, _ := process.Entries.Get("Components").(pdf.PDFArray)
	var missing []string
	for _, v := range names {
		n, ok := v.(pdf.PDFName)
		if !ok || n.Value == "None" || processColorants[n.Value] || colorants.Entries.Get(n.Value) != nil || containsName(components, n.Value) {
			continue
		}
		missing = append(missing, n.Value)
	}
	return missing
}

// containsName reports whether arr holds the name.
func containsName(arr pdf.PDFArray, name string) bool {
	for _, v := range arr {
		if v == (pdf.PDFName{Value: name}) {
			return true
		}
	}
	return false
}

// separation is the alternate space and tint transform the first Separation
// colour space of a colorant name had.
type separation struct {
	alternate, tint pdf.PDFValue
	reported        bool
}

// checkSeparationConsistency reports a Separation colour space whose
// alternate space or tint transform differs from those of an earlier one of
// the same colorant name. Each name is reported once.
func checkSeparationConsistency(arr pdf.PDFArray, ctx *ValidationContext) {
	if len(arr) < 4 {
		return
	}
	name, ok := arr[1].(pdf.PDFName)
	if !ok {
		return
	}
	if ctx.separations == nil {
		ctx.separations = map[string]*separation{}
	}
	first, seen := ctx.separations[name.Value]
	if !seen {
		ctx.separations[name.Value] = &separation{alternate: arr[2], tint: arr[3]}
		return
	}
	if first.reported {
		return
	}
	var differs string
	switch {
	case !ctx.equivalent(first.alternate, arr[2], 0):
		differs = "alternate space"
	case !ctx.equivalent(first.tint, arr[3], 0):
		differs = "tint transform"
	default:
		return
	}
	first.reported = true
	ctx.Report(pdf.Checks.Colour.SeparationInconsistent, arr,
		fmt.Sprintf("Separation colour spaces named /%s have different %ss", name.Value, differs))
}

// maxEquivalenceDepth bounds how deep equivalent compares nested objects.
const maxEquivalenceDepth = 32

// equivalent reports whether two objects have the same content: the same
// object, or values whose entries, elements and stream data are equivalent.
// Objects nested deeper than maxEquivalenceDepth count as equivalent, so a
// cyclic structure is not reported as a difference.
func (ctx *ValidationContext) equivalent(a, b pdf.PDFValue, depth int) bool {
	if depth > maxEquivalenceDepth {
		return true
	}
	switch va := a.(type) {
	case pdf.PDFArray:
		vb, ok := b.(pdf.PDFArray)
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !ctx.equivalent(va[i], vb[i], depth+1) {
				return false
			}
		}
		return true
	case pdf.PDFDict:
		vb, ok := b.(pdf.PDFDict)
		if !ok {
			return false
		}
		if ra, ok := va.Ref(); ok {
			if rb, ok := vb.Ref(); ok && ra == rb {
				return true
			}
		}
		if va.IsStream() != vb.IsStream() || !ctx.equivalentEntries(va, vb, depth) {
			return false
		}
		if !va.IsStream() {
			return true
		}
		da, errA := ctx.decodeStreamCached(va)
		db, errB := ctx.decodeStreamCached(vb)
		return errA == nil && errB == nil && bytes.Equal(da, db)
	}
	return pdf.EqualPDFValue(a, b)
}

// equivalentEntries compares two dictionaries' entries, leaving out a
// stream's Length, which depends on its encoding rather than its content.
func (ctx *ValidationContext) equivalentEntries(a, b pdf.PDFDict, depth int) bool {
	count := func(d pdf.PDFDict) int {
		n := d.Entries.Len()
		if d.IsStream() && d.Entries.Get("Length") != nil {
			n--
		}
		return n
	}
	if count(a) != count(b) {
		return false
	}
	for k, va := range a.Entries.All() {
		if k == "Length" && a.IsStream() {
			continue
		}
		vb, ok := b.Entries.Lookup(k)
		if !ok || !ctx.equivalent(va, vb, depth+1) {
			return false
		}
	}
	return true
}

// 6.2.4.5 Indexed and Pattern colour spaces (ISO 19005-2)

// baseColourModel returns the device colour model an Indexed or Pattern
// colour space is based on, or "".
func baseColourModel(cs pdf.PDFValue) string {
	arr, ok := cs.(pdf.PDFArray)
	if !ok || len(arr) < 2 {
		return ""
	}
	switch arr[0] {
	case pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: "I"}, pdf.PDFName{Value: "Pattern"}:
		return DeviceColourModel(arr[1])
	}
	return ""
}

// checkBaseColour reports an Indexed or Pattern colour space based on a
// device colour space that no output intent and no Default colour space in
// resources or the enclosing scope covers.
func checkBaseColour(cs pdf.PDFValue, resources pdf.PDFDict, obj pdf.PDFValue, ctx *ValidationContext) {
	model := baseColourModel(cs)
	if model == "" || ctx.deviceColourAllowed(model) ||
		DefaultColorSpaceDefined(model, resources) || DefaultColorSpaceDefined(model, ctx.resourceScope) {
		return
	}
	head, _ := cs.(pdf.PDFArray)[0].(pdf.PDFName)
	ctx.uncoveredDeviceColour = true
	ctx.Report(pdf.Checks.Colour.DeviceColourBase, obj,
		fmt.Sprintf("%s colour space is based on device colour (%s) without a matching output intent", head.Value, model))
}

// namedColourSpace returns the colour space a cs or CS operand selects: the
// resource it names, or nil for a device or unknown name.
func namedColourSpace(name pdf.PDFValue, resources pdf.PDFDict) pdf.PDFValue {
	n, ok := name.(pdf.PDFName)
	if !ok {
		return nil
	}
	spaces, _ := resources.Entries.Get("ColorSpace").(pdf.PDFDict)
	return spaces.Entries.Get(n.Value)
}

// 6.2.5 Extended graphics state (ISO 19005-2)

// validateHalftones checks the HTP entry and the halftone of an ExtGState
// dictionary.
func validateHalftones(v pdf.PDFDict, ctx *ValidationContext) {
	if v.Entries.Get("HTP") != nil {
		ctx.Report(pdf.Checks.Transparency.HalftoneExtGState, v, "ExtGState shall not contain an HTP key")
	}
	if ht, ok := v.Entries.Get("HT").(pdf.PDFDict); ok {
		checkHalftone(ht, "", ctx)
	}
}

// checkHalftone checks one halftone. colorant is the key a type 5 halftone
// holds it under, or "" for a halftone an ExtGState names directly.
func checkHalftone(ht pdf.PDFDict, colorant string, ctx *ValidationContext) {
	kind := ht.Entries.Get("HalftoneType")
	if kind != pdf.PDFInteger(1) && kind != pdf.PDFInteger(5) {
		ctx.Report(pdf.Checks.Transparency.HalftoneType, ht, fmt.Sprintf("halftone type is %v, not 1 or 5", kind))
	}
	if ht.Entries.Get("HalftoneName") != nil {
		ctx.Report(pdf.Checks.Transparency.HalftoneName, ht, "halftone shall not contain a HalftoneName key")
	}
	if colorant != "Default" {
		hasTF := ht.Entries.Get("TransferFunction") != nil
		primary := colorant == "" || processColorants[colorant]
		switch {
		case primary && hasTF:
			ctx.Report(pdf.Checks.Transparency.HalftoneTransferFunction, ht, "halftone for a primary colorant shall not contain a TransferFunction key")
		case !primary && !hasTF:
			ctx.Report(pdf.Checks.Transparency.HalftoneTransferFunction, ht,
				fmt.Sprintf("halftone for the colorant /%s needs a TransferFunction key", colorant))
		}
	}
	if kind != pdf.PDFInteger(5) {
		return
	}
	for k, sub := range ht.Entries.All() {
		switch k {
		case "Type", "HalftoneType", "HalftoneName":
			continue
		}
		if d, ok := sub.(pdf.PDFDict); ok {
			checkHalftone(d, k, ctx)
		}
	}
}
