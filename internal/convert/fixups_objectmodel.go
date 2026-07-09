package convert

import (
	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func init() {
	registerFixer(indirectRequiredFixer{})
	registerFixer(descentSignFixer{})
}

// indirectRequiredKeys holds every key name some Arlington row requires to be an indirect
// reference (unpredicated rows only), computed once from the compiled-in model.
var indirectRequiredKeys = func() map[string]bool {
	keys := map[string]bool{}
	for _, ot := range arlington.Types {
		for _, kd := range ot.Keys {
			if kd.IndirectReference == arlington.IndirectRequired && !kd.Predicated.Indirect {
				keys[kd.Name] = true
			}
		}
	}
	return keys
}()

// indirectRequiredFixer remediates the object model's IndirectRequired check: a direct
// dictionary under an indirect-required key gets its own object number, so the writer
// serializes it as an indirect object. No enforced row demands directness, so promoting by
// key name across all types is a safe overshoot.
type indirectRequiredFixer struct{}

func (indirectRequiredFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.ObjectModel.IndirectRequired
}

func (indirectRequiredFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	next := nextAvailableObjNum(*trailer)
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		for k, v := range d.Entries {
			if k == "_ref" || !indirectRequiredKeys[k] {
				continue
			}
			child, ok := v.(pdf.PDFDict)
			if !ok || child.Entries == nil || child.Entries["_ref"] != nil {
				continue
			}
			child.Entries["_ref"] = pdf.PDFRef{ObjNum: next}
			next++
			changed = true
		}
	})
	return changed, nil
}

// descentSignFixer remediates the object model's DisallowedValue check on font descriptors:
// ISO 32000 requires /Descent to be non-positive, but some fonts store its magnitude, so a
// positive value is negated in place.
type descentSignFixer struct{}

func (descentSignFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.ObjectModel.DisallowedValue
}

func (descentSignFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if t, ok := d.Entries["Type"].(pdf.PDFName); !ok || t.Value != "FontDescriptor" {
			return
		}
		switch v := d.Entries["Descent"].(type) {
		case pdf.PDFInteger:
			if v > 0 {
				d.Entries["Descent"] = -v
				changed = true
			}
		case pdf.PDFReal:
			if v > 0 {
				d.Entries["Descent"] = -v
				changed = true
			}
		}
	})
	return changed, nil
}
