package convert

import (
	"math"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

func init() {
	registerFixer(indirectRequiredFixer{})
	registerFixer(disallowedValueFixer{})
	registerFixer(descriptorFlagsFixer{})
	registerFixer(missingRequiredKeyFixer{})
	registerFixer(wrongValueTypeFixer{})
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

// wrongValueTypeFixer remediates the object model's WrongValueType check: a mis-typed
// value is coerced when a lossless scalar conversion to a schema-allowed type exists;
// otherwise an unconditionally-optional key is deleted (absence is always conformant),
// and a required key stays a residual. Array-element findings are skipped -- the finding
// does not identify which of the owner's entries holds the array.
type wrongValueTypeFixer struct{}

func (wrongValueTypeFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.ObjectModel.WrongValueType
}

// Fix is targeted-only, like missingRequiredKeyFixer.
func (wrongValueTypeFixer) Fix(_ *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return false, nil
}

func (wrongValueTypeFixer) fixTargeted(p *fixPass, issues []pdf.PDFError) (bool, bool, error) {
	changed := false
	for _, iss := range issues {
		detail, ok := iss.ObjModelDetail()
		if !ok || isArrayIndexKey(detail.Key) {
			continue
		}
		ref, ok := iss.ObjectRef()
		if !ok {
			continue
		}
		d, ok := p.dictForRef(ref)
		if !ok {
			continue
		}
		kd := keyDefFor(detail.TypeName, detail.Key)
		if kd == nil || len(kd.Types) == 0 {
			continue
		}
		val, present := d.Entries[detail.Key]
		if !present || val == nil || verify.MatchesValueType(val, kd.Types) {
			continue // absent, null, or repaired earlier: stale finding
		}
		if nv, ok := coerceScalar(val, kd.Types); ok {
			d.Entries[detail.Key] = nv
			changed = true
			continue
		}
		if deletableKey(kd) {
			delete(d.Entries, detail.Key)
			changed = true
		}
	}
	return changed, true, nil
}

// keyDefFor returns the schema row governing key on typeName: the named row if one exists,
// else the type's wildcard row, else nil.
func keyDefFor(typeName, key string) *arlington.KeyDef {
	if kd := namedKeyDef(typeName, key); kd != nil {
		return kd
	}
	ot, ok := arlington.Type(typeName)
	if !ok {
		return nil
	}
	return ot.Wildcard
}

// deletableKey reports whether removing the key is unconditionally conformant: never
// required, under no runtime or predicated requirement condition.
func deletableKey(kd *arlington.KeyDef) bool {
	return !kd.Required && kd.RequiredWhen == nil && !kd.Predicated.Required
}

// coerceScalar converts val to the first schema-allowed type it can represent losslessly:
// integral real or numeric string to integer, numeric string to number, string to name,
// name to string, and true/false names or strings to boolean. Date strings are never
// synthesized from other types.
func coerceScalar(val pdf.PDFValue, types []arlington.ValueType) (pdf.PDFValue, bool) {
	for _, vt := range types {
		switch vt {
		case arlington.Integer, arlington.Bitmask:
			switch v := val.(type) {
			case pdf.PDFReal:
				if float64(v) == math.Trunc(float64(v)) {
					return pdf.PDFInteger(int(v)), true
				}
			case pdf.PDFString:
				if n, err := strconv.Atoi(strings.TrimSpace(v.Value)); err == nil {
					return pdf.PDFInteger(n), true
				}
			}
		case arlington.Number:
			if v, ok := val.(pdf.PDFString); ok {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v.Value), 32); err == nil {
					return pdf.PDFReal(f), true
				}
			}
		case arlington.Name:
			switch v := val.(type) {
			case pdf.PDFString:
				return pdf.PDFName{Value: v.Value}, true
			case pdf.PDFHexString:
				return pdf.PDFName{Value: v.Value}, true
			}
		case arlington.String, arlington.StringText, arlington.StringByte, arlington.StringASCII:
			if v, ok := val.(pdf.PDFName); ok {
				return pdf.PDFString{Value: v.Value}, true
			}
		case arlington.Boolean:
			s := ""
			switch v := val.(type) {
			case pdf.PDFName:
				s = v.Value
			case pdf.PDFString:
				s = v.Value
			}
			if s == "true" || s == "false" {
				return pdf.PDFBoolean(s == "true"), true
			}
		}
	}
	return nil, false
}

// disallowedValueFixer remediates the object model's DisallowedValue check. Font
// descriptors storing /Descent as a magnitude are negated first (whole-graph, needing no
// finding, since ISO 32000 requires a non-positive value). Targeted repairs then follow
// the schema: a single-entry enum or a definitely-true pinned value replaces the offending
// value, a compiled inclusive range clamps it, and an unconditionally-optional key with no
// repair is deleted.
type disallowedValueFixer struct{}

func (disallowedValueFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.ObjectModel.DisallowedValue
}

func (disallowedValueFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if t, ok := d.Entries["Type"].(pdf.PDFName); !ok || t.Value != "FontDescriptor" {
			return
		}
		if negateDescent(d) {
			changed = true
		}
	})
	return changed, nil
}

func (f disallowedValueFixer) fixTargeted(p *fixPass, issues []pdf.PDFError) (bool, bool, error) {
	// Ref-less descriptors keep their magnitude repair regardless of targeting.
	changed, _ := f.Fix(p.trailer, nil)
	for _, iss := range issues {
		detail, ok := iss.ObjModelDetail()
		if !ok || isArrayIndexKey(detail.Key) {
			continue
		}
		ref, ok := iss.ObjectRef()
		if !ok {
			continue
		}
		d, ok := p.dictForRef(ref)
		if !ok {
			continue
		}
		kd := keyDefFor(detail.TypeName, detail.Key)
		if kd == nil {
			continue
		}
		if val := d.Entries[detail.Key]; val == nil {
			continue
		}
		if repairDisallowedValue(d, detail.Key, kd) {
			changed = true
		}
	}
	return changed, true, nil
}

// negateDescent flips a positive /Descent in place, preserving its magnitude where a clamp
// to the range bound would zero it.
func negateDescent(d pdf.PDFDict) bool {
	switch v := d.Entries["Descent"].(type) {
	case pdf.PDFInteger:
		if v > 0 {
			d.Entries["Descent"] = -v
			return true
		}
	case pdf.PDFReal:
		if v > 0 {
			d.Entries["Descent"] = -v
			return true
		}
	}
	return false
}

// repairDisallowedValue applies the schema-derived repair for key's current value on d.
// Every step re-checks the live value, so stale findings never mis-repair.
func repairDisallowedValue(d pdf.PDFDict, key string, kd *arlington.KeyDef) bool {
	if key == "Descent" && negateDescent(d) {
		return true // a descriptor without /Type, missed by the whole-graph pass
	}
	val := d.Entries[key]
	violated := false

	if !kd.Predicated.Values && len(kd.PossibleValues) > 0 {
		if s, ok := verify.EnumString(val); ok && !stringInList(s, kd.PossibleValues) {
			if len(kd.PossibleValues) == 1 {
				if nv, ok := scalarFromEnum(kd.PossibleValues[0], kd.Types); ok {
					d.Entries[key] = nv
					return true
				}
			}
			violated = true
		}
	}

	for i := range kd.PinnedValues {
		pin := &kd.PinnedValues[i]
		if holds, ok := verify.EvalCond(pin.When, d); !ok || !holds {
			continue
		}
		if s, ok := verify.EnumString(val); ok && s != pin.Value {
			if nv, ok := scalarFromEnum(pin.Value, kd.Types); ok {
				d.Entries[key] = nv
				return true
			}
			violated = true
		}
	}

	if kd.ValueCond != nil {
		if legal, ok := verify.EvalCond(kd.ValueCond, d); ok && !legal {
			if nv, ok := clampToBounds(val, kd.ValueCond, key); ok {
				d.Entries[key] = nv
				return true
			}
			violated = true
		}
	}

	if violated && deletableKey(kd) {
		delete(d.Entries, key)
		return true
	}
	return false
}

// clampToBounds moves a numeric value inside the inclusive bounds the conjunctive leaves
// of cond impose on key's own value, keeping the value's integer/real kind. Strict bounds
// and derived operands never clamp (fail closed).
func clampToBounds(val pdf.PDFValue, cond *arlington.Cond, key string) (pdf.PDFValue, bool) {
	lo, hi, hasLo, hasHi := condBounds(cond, key)
	if !hasLo && !hasHi {
		return nil, false
	}
	var n float64
	switch v := val.(type) {
	case pdf.PDFInteger:
		n = float64(v)
	case pdf.PDFReal:
		n = float64(v)
	default:
		return nil, false
	}
	c := n
	if hasLo && c < lo {
		c = lo
	}
	if hasHi && c > hi {
		c = hi
	}
	if c == n {
		return nil, false // the violation is not a simple bounds excursion
	}
	if _, isInt := val.(pdf.PDFInteger); isInt {
		return pdf.PDFInteger(int(c)), true
	}
	return pdf.PDFReal(c), true
}

// condBounds extracts the inclusive bounds cond's conjunctive comparison leaves impose on
// key's plain value; Or/Not subtrees, derived operands, and strict comparisons contribute
// nothing.
func condBounds(c *arlington.Cond, key string) (lo, hi float64, hasLo, hasHi bool) {
	leafBound := func(c *arlington.Cond) (float64, bool) {
		if c.Key != key || c.Fn != arlington.FnValue || c.RHSKey != "" || c.Mod != 0 {
			return 0, false
		}
		v, err := strconv.ParseFloat(c.Value, 64)
		return v, err == nil
	}
	switch c.Op {
	case arlington.CondGe:
		if v, ok := leafBound(c); ok {
			lo, hasLo = v, true
		}
	case arlington.CondLe:
		if v, ok := leafBound(c); ok {
			hi, hasHi = v, true
		}
	case arlington.CondAnd:
		for i := range c.Kids {
			l, h, hl, hh := condBounds(&c.Kids[i], key)
			if hl && (!hasLo || l > lo) {
				lo, hasLo = l, true
			}
			if hh && (!hasHi || h < hi) {
				hi, hasHi = h, true
			}
		}
	}
	return lo, hi, hasLo, hasHi
}

// stringInList reports whether s appears in list.
func stringInList(s string, list []string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
