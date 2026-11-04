package convert

import (
	"fmt"
	"slices"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
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
		if !verify.IsAnnotationDict(d) || !oc.Hides(d.Entries.Get("OC")) {
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
// A nil tracker is the opacity pass: every method of it is a no-op.
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

// --- 6.9 Optional content (PDF/A-2) ---

// ocConfigFixer repairs the optional content configuration dictionaries of
// the catalog's OCProperties. Each repair runs only when its check is
// reported: it removes configurations that repeat D or an earlier entry,
// gives a fresh Name to configurations that lack one or repeat a name,
// gives a fresh Name to groups that lack one, appends the groups missing
// from each Order array, and deletes AS entries.
type ocConfigFixer struct{}

func (ocConfigFixer) Applies(c pdf.Check) bool {
	s := pdf.Checks.Structure
	switch c {
	case s.OCConfigMissingName, s.OCConfigDuplicateName, s.OCConfigOrderIncomplete, s.OCConfigAutoState:
		return true
	}
	return false
}

func (ocConfigFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	props, ok := root.Entries.Get("OCProperties").(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	reported := reportedChecks(issues)
	s := pdf.Checks.Structure
	missing, duplicate := reported[s.OCConfigMissingName], reported[s.OCConfigDuplicateName]
	changed := false
	if missing || duplicate {
		// A repeated entry is the same dictionary as an earlier one, so
		// renaming it would rename every place it is listed and could not
		// make the names unique. Drop the repeats first.
		changed = dropRepeatedOCConfigs(props)
	}
	configs := verify.OCConfigs(props)
	if missing || duplicate {
		changed = renameOCConfigs(configs, missing, duplicate) || changed
	}
	if reported[s.OCConfigOrderIncomplete] {
		changed = completeOCOrders(verify.OCGroups(props), configs) || changed
	}
	if reported[s.OCConfigAutoState] {
		for _, cfg := range configs {
			if _, ok := cfg.Entries.Lookup("AS"); ok {
				cfg.Entries.Del("AS")
				changed = true
			}
		}
	}
	return changed, nil
}

// dropRepeatedOCConfigs removes from the Configs array of props each
// dictionary that is D or an earlier entry. Such an entry is the same
// dictionary as one already listed, so it repeats that entry's name and
// renaming cannot make it unique.
func dropRepeatedOCConfigs(props pdf.PDFDict) bool {
	arr, ok := props.Entries.Get("Configs").(pdf.PDFArray)
	if !ok {
		return false
	}
	seen := map[pdf.Dict]bool{}
	if d, ok := props.Entries.Get("D").(pdf.PDFDict); ok {
		seen[d.Entries] = true
	}
	kept := make(pdf.PDFArray, 0, len(arr))
	for _, v := range arr {
		if cfg, ok := v.(pdf.PDFDict); ok {
			if seen[cfg.Entries] {
				continue
			}
			seen[cfg.Entries] = true
		}
		kept = append(kept, v)
	}
	if len(kept) == len(arr) {
		return false
	}
	props.Entries.Set("Configs", kept)
	return true
}

// renameOCConfigs gives a fresh name to each of configs that has no non-empty
// Name string, when missing is set, and to each that repeats an earlier
// configuration's name, when duplicate is set.
func renameOCConfigs(configs []pdf.PDFDict, missing, duplicate bool) bool {
	taken := map[string]bool{}
	for _, cfg := range configs {
		if name, ok := verify.OCConfigName(cfg); ok {
			taken[name] = true
		}
	}
	dup := verify.OCConfigDuplicates(configs)
	changed := false
	for i, cfg := range configs {
		_, named := verify.OCConfigName(cfg)
		if rename := missing && !named || duplicate && dup[i]; !rename {
			continue
		}
		fallback := "Configuration"
		if i == 0 {
			fallback = "Default"
		}
		setFreshName(cfg, taken, fallback)
		changed = true
	}
	return changed
}

// completeOCOrders appends to the Order array of each of configs the groups
// it does not list. As the Order check matches groups by name, a group
// without a Name string first gets one, by setFreshName.
func completeOCOrders(groups []pdf.PDFDict, configs []pdf.PDFDict) bool {
	taken := map[string]bool{}
	for _, name := range verify.OCGroupNames(groups) {
		taken[name] = true
	}
	changed := false
	for _, g := range groups {
		if _, ok := pdf.StringBytes(g.Entries.Get("Name")); !ok {
			setFreshName(g, taken, "Layer")
			changed = true
		}
	}
	names := verify.OCGroupNames(groups)
	for _, cfg := range configs {
		missing := verify.OCGroupsMissingFromOrder(cfg, names)
		if len(missing) == 0 {
			continue
		}
		// Clip, so that an Order array other configurations share is not
		// extended in place.
		order := slices.Grow(slices.Clip(cfg.Entries.Get("Order").(pdf.PDFArray)), len(missing))
		for _, i := range missing {
			order = append(order, groups[i])
		}
		cfg.Entries.Set("Order", order)
		changed = true
	}
	return changed
}

// setFreshName sets the Name of d to a text string that is not in taken, and
// adds it to taken. The base is the value of d's Name, whether a text string
// or a name object, or fallback when that value is empty. uniqueName numbers
// the base when it is taken.
func setFreshName(d pdf.PDFDict, taken map[string]bool, fallback string) {
	name := pdf.DecodeInfoTextString(d.Entries.Get("Name"))
	if n, ok := d.Entries.Get("Name").(pdf.PDFName); ok {
		name = n.Value
	}
	if name == "" {
		name = fallback
	}
	d.Entries.Set("Name", pdf.PDFString{Value: string(pdf.EncodePDFTextString(uniqueName(taken, name)))})
}

// uniqueName returns base, or base followed by the smallest number from 2 up
// that makes it so, when base is taken, and marks the result taken.
func uniqueName(taken map[string]bool, base string) string {
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s %d", base, n)
	}
	taken[name] = true
	return name
}
