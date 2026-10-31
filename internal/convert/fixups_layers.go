package convert

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// --- 6.1.13 Optional content ---

// optionalContentFixer remediates Checks.Structure.OptionalContent by
// deleting the catalog's /OCProperties entry, mirroring
// (*Document).verifyOptionalContent in verifier.go.
//
// Without /OCProperties every layer is visible, so a layer the document hid
// by default would appear in the output. Before the entry goes, the content
// of the hidden layers is removed: the drawing inside the marked-content
// sequences they govern, the XObjects and annotations they govern, by the
// same means as content drawn at zero opacity. The BDC and EMC operators
// themselves stay; once /OCProperties is gone they mark nothing. Each hidden
// group whose content was removed is reported to lost, when non-nil.
type optionalContentFixer struct {
	lost *[]pdf.PDFError
}

func (optionalContentFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.OptionalContent
}

func (f optionalContentFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	if _, ok := root.Entries.Lookup("OCProperties"); !ok {
		return false, nil
	}
	if oc := content.NewOptionalContent(root); oc != nil {
		f.removeHiddenLayers(trailer, oc)
	}
	root.Entries.Del("OCProperties")
	return true, nil
}

// removeHiddenLayers removes everything oc hides from the document.
func (f optionalContentFixer) removeHiddenLayers(trailer *pdf.PDFDict, oc *content.OptionalContent) {
	removed := &hiddenGroups{seen: map[pdf.Dict]bool{}}
	removeInvisible(trailer, func() *alphaState {
		s := newAlphaState()
		s.layers = &layerTracker{oc: oc, marked: content.NewMarkedContent(oc), removed: removed}
		return s
	})
	for _, g := range removed.order {
		reportLost(f.lost, pdf.Checks.Structure.OptionalContent, g, hiddenGroupMessage(g))
	}

	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Annot"}) || !oc.Hides(d.Entries.Get("OC")) {
			return
		}
		msg := "annotation in hidden optional content removed"
		if subtype, ok := d.Entries.Get("Subtype").(pdf.PDFName); ok && subtype.Value != "" {
			msg = subtype.Value + " " + msg
		}
		clearDict(d)
		reportLost(f.lost, pdf.Checks.Structure.OptionalContent, d, msg)
	})
}

// hiddenGroupMessage describes the removal of the content that the hidden
// group or membership dictionary g governs.
func hiddenGroupMessage(g pdf.PDFDict) string {
	if (g.Entries.Get("Type") == pdf.PDFName{Value: "OCMD"}) {
		return "content of hidden optional content membership removed"
	}
	if name := pdf.DecodeInfoTextString(g.Entries.Get("Name")); name != "" {
		return fmt.Sprintf("content of hidden optional content group %q removed", name)
	}
	return "content of hidden optional content group removed"
}

// hiddenGroups collects, in the order first met, the hidden group and
// membership dictionaries whose content was removed.
type hiddenGroups struct {
	seen  map[pdf.Dict]bool
	order []pdf.PDFDict
}

func (h *hiddenGroups) add(g pdf.PDFDict) {
	if h.seen[g.Entries] {
		return
	}
	h.seen[g.Entries] = true
	h.order = append(h.order, g)
}

// layerTracker is what the hidden-layer pass adds to an alphaState: which
// marked content of the stream is hidden, and where removals are recorded.
// Every method of a nil tracker is a no-op, which is the opacity pass.
type layerTracker struct {
	oc      *content.OptionalContent
	marked  *content.MarkedContent
	removed *hiddenGroups
}

func (l *layerTracker) step(op string, operands []pdf.PDFValue, resources pdf.PDFDict) {
	if l != nil {
		l.marked.Step(op, operands, resources)
	}
}

func (l *layerTracker) hidden() bool {
	return l != nil && l.marked.Hidden()
}

// noteRemoved records that drawing inside a hidden sequence was removed.
func (l *layerTracker) noteRemoved() {
	if l == nil {
		return
	}
	if by, ok := l.marked.HiddenBy(); ok {
		l.removed.add(by)
	}
}

// hidesXObject reports whether the XObject x belongs to a hidden layer
// itself, through its /OC entry, and records the removal if so.
func (l *layerTracker) hidesXObject(x pdf.PDFDict) bool {
	if l == nil {
		return false
	}
	g, ok := x.Entries.Get("OC").(pdf.PDFDict)
	if !ok || !l.oc.Hides(g) {
		return false
	}
	l.removed.add(g)
	return true
}
