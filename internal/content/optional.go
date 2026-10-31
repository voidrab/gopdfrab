package content

import "github.com/voidrab/gopdfrab/internal/pdf"

// OptionalContent is the visibility that a document's default optional
// content configuration (the /D entry of /OCProperties) gives its optional
// content groups. A nil *OptionalContent hides nothing.
type OptionalContent struct {
	on, off map[pdf.Dict]bool
	// baseOff is a /BaseState of OFF: a group in neither list is hidden.
	baseOff bool
}

// maxVisibilityDepth bounds the nesting of a visibility expression, so a
// cyclic or pathologically deep /VE array cannot exhaust the stack.
const maxVisibilityDepth = 64

// NewOptionalContent reads the default configuration of catalog's
// /OCProperties. It returns nil when that configuration turns no group off,
// so a document whose layers are all visible pays for no tracking.
func NewOptionalContent(catalog pdf.PDFDict) *OptionalContent {
	props, ok := catalog.Entries.Get("OCProperties").(pdf.PDFDict)
	if !ok {
		return nil
	}
	config, ok := props.Entries.Get("D").(pdf.PDFDict)
	if !ok {
		return nil
	}
	oc := &OptionalContent{
		on:      groupSet(config.Entries.Get("ON")),
		off:     groupSet(config.Entries.Get("OFF")),
		baseOff: config.Entries.Get("BaseState") == pdf.PDFName{Value: "OFF"},
	}
	if !oc.baseOff && len(oc.off) == 0 {
		return nil
	}
	return oc
}

// groupSet collects the group dictionaries an /ON or /OFF array lists.
func groupSet(v pdf.PDFValue) map[pdf.Dict]bool {
	arr, _ := v.(pdf.PDFArray)
	set := make(map[pdf.Dict]bool, len(arr))
	for _, item := range arr {
		if d, ok := item.(pdf.PDFDict); ok && d.Entries != nil {
			set[d.Entries] = true
		}
	}
	return set
}

// Hides reports whether v, an optional content group or membership
// dictionary, makes the content it governs invisible. Any other value hides
// nothing.
func (oc *OptionalContent) Hides(v pdf.PDFValue) bool {
	if oc == nil {
		return false
	}
	d, ok := v.(pdf.PDFDict)
	if !ok || d.Entries == nil {
		return false
	}
	switch d.Entries.Get("Type") {
	case pdf.PDFName{Value: "OCG"}:
		return !oc.groupOn(d)
	case pdf.PDFName{Value: "OCMD"}:
		return !oc.membershipVisible(d)
	}
	return false
}

// groupOn reports whether the configuration turns group g on.
func (oc *OptionalContent) groupOn(g pdf.PDFDict) bool {
	switch {
	case oc.off[g.Entries]:
		return false
	case oc.on[g.Entries]:
		return true
	}
	return !oc.baseOff
}

// membershipVisible evaluates an optional content membership dictionary
// (ISO 32000-1, 8.11.2.2). A visibility expression takes precedence over the
// group list; a dictionary that names no group leaves its content visible.
func (oc *OptionalContent) membershipVisible(md pdf.PDFDict) bool {
	if ve, ok := md.Entries.Get("VE").(pdf.PDFArray); ok {
		if visible, valid := oc.expression(ve, 0); valid {
			return visible
		}
	}
	var groups []pdf.PDFDict
	switch v := md.Entries.Get("OCGs").(type) {
	case pdf.PDFDict:
		groups = append(groups, v)
	case pdf.PDFArray:
		for _, item := range v {
			if g, ok := item.(pdf.PDFDict); ok && g.Entries != nil {
				groups = append(groups, g)
			}
		}
	}
	if len(groups) == 0 {
		return true
	}
	on := 0
	for _, g := range groups {
		if oc.groupOn(g) {
			on++
		}
	}
	switch md.Entries.Get("P") {
	case pdf.PDFName{Value: "AllOn"}:
		return on == len(groups)
	case pdf.PDFName{Value: "AnyOff"}:
		return on < len(groups)
	case pdf.PDFName{Value: "AllOff"}:
		return on == 0
	}
	return on > 0 // AnyOn, the default
}

// expression evaluates a visibility expression. valid is false for an
// expression that is malformed, so the caller can fall back to the group
// list.
func (oc *OptionalContent) expression(ve pdf.PDFArray, depth int) (visible, valid bool) {
	if len(ve) == 0 || depth > maxVisibilityDepth {
		return false, false
	}
	op, _ := ve[0].(pdf.PDFName)
	var operands []bool
	for _, item := range ve[1:] {
		switch v := item.(type) {
		case pdf.PDFDict:
			operands = append(operands, oc.groupOn(v))
		case pdf.PDFArray:
			sub, ok := oc.expression(v, depth+1)
			if !ok {
				return false, false
			}
			operands = append(operands, sub)
		default:
			return false, false
		}
	}
	switch op.Value {
	case "Not":
		if len(operands) != 1 {
			return false, false
		}
		return !operands[0], true
	case "And", "Or":
		if len(operands) == 0 {
			return false, false
		}
		for _, o := range operands {
			if o == (op.Value == "Or") {
				return o, true
			}
		}
		return op.Value == "And", true
	}
	return false, false
}

// MarkedContent follows a content stream's marked-content sequences to say
// whether the content at the current operator lies in hidden optional
// content.
type MarkedContent struct {
	oc *OptionalContent
	// stack holds, per open sequence, the dictionary that hides it, or a
	// zero PDFDict for a sequence that hides nothing.
	stack []pdf.PDFDict
	// hidden counts the open sequences that hide their content.
	hidden int
}

// NewMarkedContent starts tracking a content stream against oc. The tracker
// of a nil oc never reports hidden content.
func NewMarkedContent(oc *OptionalContent) *MarkedContent {
	return &MarkedContent{oc: oc}
}

// Step follows one operator. A BDC sequence tagged /OC is hidden when the
// group or membership dictionary its property list names, through the
// /Properties resources or inline, is hidden.
func (m *MarkedContent) Step(op string, operands []pdf.PDFValue, resources pdf.PDFDict) {
	switch op {
	case "BMC":
		m.stack = append(m.stack, pdf.PDFDict{})
	case "BDC":
		var by pdf.PDFDict
		if len(operands) == 2 && operands[0] == (pdf.PDFName{Value: "OC"}) {
			if d, ok := propertyList(operands[1], resources).(pdf.PDFDict); ok && m.oc.Hides(d) {
				by = d
				m.hidden++
			}
		}
		m.stack = append(m.stack, by)
	case "EMC":
		n := len(m.stack)
		if n == 0 {
			return
		}
		if m.stack[n-1].Entries != nil {
			m.hidden--
		}
		m.stack = m.stack[:n-1]
	}
}

// propertyList resolves a BDC property-list operand: a name in the
// /Properties resources, or the dictionary itself.
func propertyList(v pdf.PDFValue, resources pdf.PDFDict) pdf.PDFValue {
	name, ok := v.(pdf.PDFName)
	if !ok {
		return v
	}
	props, _ := resources.Entries.Get("Properties").(pdf.PDFDict)
	return props.Entries.Get(name.Value)
}

// Hidden reports whether the content at the current operator is hidden.
func (m *MarkedContent) Hidden() bool { return m.hidden > 0 }

// HiddenBy returns the dictionary of the outermost open sequence that hides
// the current content, and false when the content is visible.
func (m *MarkedContent) HiddenBy() (pdf.PDFDict, bool) {
	for _, d := range m.stack {
		if d.Entries != nil {
			return d, true
		}
	}
	return pdf.PDFDict{}, false
}
