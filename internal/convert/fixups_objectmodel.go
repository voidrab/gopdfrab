package convert

import (
	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func init() {
	registerFixer(indirectRequiredFixer{})
	registerFixer(descentSignFixer{})
	registerFixer(descriptorFlagsFixer{})
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

// mustBeClearMask collects the bits an Arlington type's SpecialCase requires clear on the
// given key, from the constraint's conjunctive structure only (an Or/Not subtree could make
// clearing wrong, so it contributes nothing). Zero means no clearable rule.
func mustBeClearMask(typeName, key string) int64 {
	ot, ok := arlington.Type(typeName)
	if !ok {
		return 0
	}
	var mask int64
	for _, kd := range ot.Keys {
		if kd.Name != key || kd.SpecialCase == nil {
			continue
		}
		var collect func(c *arlington.Cond)
		collect = func(c *arlington.Cond) {
			switch c.Op {
			case arlington.CondBitsClear:
				if c.Key == key {
					mask |= (1<<uint(c.BitHi) - 1) &^ (1<<uint(c.BitLo-1) - 1)
				}
			case arlington.CondAnd:
				for i := range c.Kids {
					collect(&c.Kids[i])
				}
			}
		}
		collect(kd.SpecialCase)
	}
	return mask
}

// descriptorFlagsFixer remediates the object model's ConstraintViolated check on flag words:
// real files carry junk in reserved bits (FontDescriptor /Flags, text field /Ff), which
// readers must ignore anyway, so masking off the bits the model requires clear is
// conformance-neutral. The masks come from the compiled Arlington SpecialCase constraints.
type descriptorFlagsFixer struct{}

func (descriptorFlagsFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.ObjectModel.ConstraintViolated
}

var reservedFlagBits = map[string]struct {
	discKey, discVal, flagKey string
	clear                     int64
}{
	"FontDescriptor": {"Type", "FontDescriptor", "Flags", mustBeClearMask("FontDescriptorType1", "Flags")},
	"FieldTx":        {"FT", "Tx", "Ff", mustBeClearMask("FieldTx", "Ff")},
	"FieldCh":        {"FT", "Ch", "Ff", mustBeClearMask("FieldCh", "Ff")},
}

func (descriptorFlagsFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		for _, r := range reservedFlagBits {
			if t, ok := d.Entries[r.discKey].(pdf.PDFName); !ok || t.Value != r.discVal {
				continue
			}
			if v, ok := d.Entries[r.flagKey].(pdf.PDFInteger); ok && int64(v)&r.clear != 0 {
				d.Entries[r.flagKey] = v &^ pdf.PDFInteger(r.clear)
				changed = true
			}
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
