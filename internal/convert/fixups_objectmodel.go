package convert

import (
	"strconv"

	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

func init() {
	registerFixer(indirectRequiredFixer{})
	registerFixer(descentSignFixer{})
	registerFixer(descriptorFlagsFixer{})
	registerFixer(missingRequiredKeyFixer{})
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

// missingRequiredKeyFixer remediates the object model's MissingRequiredKey check for keys
// with exactly one legal value: a single-entry enum (e.g. /Type on most types) or a pinned
// value whose condition definitely holds on the dict. Anything else has no synthesizable
// value and stays a residual.
type missingRequiredKeyFixer struct{}

func (missingRequiredKeyFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.ObjectModel.MissingRequiredKey
}

// Fix is targeted-only: without the walk's type propagation a full-graph pass
// cannot know which schema each dict was validated under.
func (missingRequiredKeyFixer) Fix(_ *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return false, nil
}

func (missingRequiredKeyFixer) fixTargeted(p *fixPass, issues []pdf.PDFError) (bool, bool, error) {
	changed := false
	for _, iss := range issues {
		detail, ok := iss.ObjModelDetail()
		if !ok || isArrayIndexKey(detail.Key) {
			continue // untargetable, or a missing array element (never synthesized)
		}
		ref, ok := iss.ObjectRef()
		if !ok {
			continue
		}
		d, ok := p.dictForRef(ref)
		if !ok {
			continue
		}
		kd := namedKeyDef(detail.TypeName, detail.Key)
		if kd == nil {
			continue
		}
		if v, present := d.Entries[detail.Key]; present && v != nil {
			continue // repaired by an earlier pass; stale finding
		}
		val, ok := synthesizedValue(kd, d)
		if !ok {
			continue
		}
		d.Entries[detail.Key] = val
		changed = true
	}
	return changed, true, nil
}

// isArrayIndexKey reports whether an ObjModelDetail key addresses an array element rather
// than a dict entry: array findings carry the decimal element index, and no Arlington dict
// type names a key with digits.
func isArrayIndexKey(key string) bool {
	n, err := strconv.Atoi(key)
	return err == nil && n >= 0
}

// namedKeyDef returns typeName's schema row for key, or nil when either is unknown.
func namedKeyDef(typeName, key string) *arlington.KeyDef {
	ot, ok := arlington.Type(typeName)
	if !ok {
		return nil
	}
	for i := range ot.Keys {
		if ot.Keys[i].Name == key {
			return &ot.Keys[i]
		}
	}
	return nil
}

// synthesizedValue returns the single value the schema permits for kd on d, if one exists:
// a one-entry unpredicated enum, or the first pinned value whose condition definitely holds.
func synthesizedValue(kd *arlington.KeyDef, d pdf.PDFDict) (pdf.PDFValue, bool) {
	if !kd.Predicated.Values && len(kd.PossibleValues) == 1 {
		if v, ok := scalarFromEnum(kd.PossibleValues[0], kd.Types); ok {
			return v, true
		}
	}
	for i := range kd.PinnedValues {
		pin := &kd.PinnedValues[i]
		if holds, ok := verify.EvalCond(pin.When, d); ok && holds {
			return scalarFromEnum(pin.Value, kd.Types)
		}
	}
	return nil, false
}

// scalarFromEnum converts an Arlington enum literal to the PDF scalar the key's types
// allow, mirroring the verifier's enum formats (name, integer, boolean).
func scalarFromEnum(s string, types []arlington.ValueType) (pdf.PDFValue, bool) {
	for _, vt := range types {
		switch vt {
		case arlington.Integer, arlington.Bitmask:
			if n, err := strconv.Atoi(s); err == nil {
				return pdf.PDFInteger(n), true
			}
		case arlington.Boolean:
			if s == "true" || s == "false" {
				return pdf.PDFBoolean(s == "true"), true
			}
		case arlington.Name:
			return pdf.PDFName{Value: s}, true
		}
	}
	return nil, false
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
