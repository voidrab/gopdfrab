package verify

import (
	"fmt"
	"strconv"

	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// validateAgainstSchema checks v's keys against the Arlington object-model schema for typeName:
// a required, non-inheritable key must be present (MissingRequiredKey); a present key's value
// must be one of its schema-allowed types (WrongValueType); a present key with an enumerated
// PossibleValues list must use one of them (DisallowedValue); a key requiring an indirect
// reference must not be inlined (IndirectRequired); and a key the model says was introduced
// after PDF 1.4 must not appear on a non-wildcard type (KeyIntroducedAfterPDF14). A predicated
// column -- one whose fn: condition the Arlington generator could not fold -- suppresses only
// its own check, erring toward false negatives over false positives. Keys absent from both the
// 1.4 and post-1.4 schema (custom/private keys) are never flagged.
func validateAgainstSchema(v pdf.PDFDict, typeName string, ctx *ValidationContext) {
	ot, ok := arlington.Type(typeName)
	if !ok {
		return
	}

	for _, kd := range ot.Keys {
		if kd.Name == "Length" && v.HasStream {
			// The writer always recomputes Length from RawStream at
			// serialization time (internal/writer/writer.go), overwriting
			// whatever is here, so its in-memory presence/value is never a
			// meaningful conformance signal.
			continue
		}

		val, present := v.Entries[kd.Name]

		required := kd.Required
		if !required && kd.RequiredWhen != nil {
			req, ok := evalCond(kd.RequiredWhen, v)
			required = ok && req
		}
		if required && !kd.Predicated.Required && !kd.Inheritable && !present {
			ctx.Report(
				pdf.Checks.ObjectModel.MissingRequiredKey,
				v,
				fmt.Sprintf("%s is missing required key %q", typeName, kd.Name),
			)
			continue
		}

		if present && !kd.Predicated.Types && len(kd.Types) > 0 && !matchesValueType(val, kd.Types) {
			ctx.Report(
				pdf.Checks.ObjectModel.WrongValueType,
				v,
				fmt.Sprintf("%s key %q has an unexpected value type", typeName, kd.Name),
			)
			continue
		}

		if present && !kd.Predicated.Values && len(kd.PossibleValues) > 0 {
			if s, ok := scalarEnumString(val); ok && !stringInList(s, kd.PossibleValues) {
				ctx.Report(
					pdf.Checks.ObjectModel.DisallowedValue,
					v,
					fmt.Sprintf("%s key %q has a value not in its enumerated legal values", typeName, kd.Name),
				)
			}
		}

		if present && val != nil && kd.ValueCond != nil {
			if legal, ok := evalCond(kd.ValueCond, v); ok && !legal {
				ctx.Report(
					pdf.Checks.ObjectModel.DisallowedValue,
					v,
					fmt.Sprintf("%s key %q has a value outside its legal range", typeName, kd.Name),
				)
			}
		}

		if present && val != nil {
			for i := range kd.PinnedValues {
				pin := &kd.PinnedValues[i]
				pinned, ok := evalCond(pin.When, v)
				if !ok || !pinned {
					continue
				}
				if s, sok := scalarEnumString(val); sok && s != pin.Value {
					ctx.Report(
						pdf.Checks.ObjectModel.DisallowedValue,
						v,
						fmt.Sprintf("%s key %q must be %s under its current sibling entries", typeName, kd.Name, pin.Value),
					)
				}
			}
		}

		if present && val != nil && kd.SpecialCase != nil {
			if holds, ok := evalCond(kd.SpecialCase, v); ok && !holds {
				ctx.Report(
					pdf.Checks.ObjectModel.ConstraintViolated,
					v,
					fmt.Sprintf("%s key %q violates an object-model consistency constraint", typeName, kd.Name),
				)
			}
		}

		if present && !kd.Predicated.Indirect && kd.IndirectReference == arlington.IndirectRequired && !isIndirect(val) {
			ctx.Report(
				pdf.Checks.ObjectModel.IndirectRequired,
				v,
				fmt.Sprintf("%s key %q must be an indirect reference", typeName, kd.Name),
			)
		}
	}

	// The wildcard row governs every key without an explicit row: its type, enumerated
	// values, and indirect-reference constraints apply to each such entry (e.g. XObject
	// resource-map values must be indirect streams). Same Length exemption as above.
	if wc := ot.Wildcard; wc != nil {
		for _, k := range sortedKeys(v.Entries) {
			if k == "_ref" || hasNamedKey(ot, k) || (k == "Length" && v.HasStream) {
				continue
			}
			val := v.Entries[k]
			if !wc.Predicated.Types && len(wc.Types) > 0 && !matchesValueType(val, wc.Types) {
				ctx.Report(
					pdf.Checks.ObjectModel.WrongValueType,
					v,
					fmt.Sprintf("%s key %q has an unexpected value type", typeName, k),
				)
				continue
			}
			if !wc.Predicated.Values && len(wc.PossibleValues) > 0 {
				if s, ok := scalarEnumString(val); ok && !stringInList(s, wc.PossibleValues) {
					ctx.Report(
						pdf.Checks.ObjectModel.DisallowedValue,
						v,
						fmt.Sprintf("%s key %q has a value not in its enumerated legal values", typeName, k),
					)
				}
			}
			if !wc.Predicated.Indirect && wc.IndirectReference == arlington.IndirectRequired && !isIndirect(val) {
				ctx.Report(
					pdf.Checks.ObjectModel.IndirectRequired,
					v,
					fmt.Sprintf("%s key %q must be an indirect reference", typeName, k),
				)
			}
		}
	}

	// A wildcard type allows arbitrary keys, so there is nothing "introduced after PDF 1.4"
	// to flag there; a custom/private key on a non-wildcard type is likewise never in
	// Post14Keys, so it is never flagged either.
	if ot.Wildcard == nil {
		for k := range v.Entries {
			if k != "_ref" && stringInList(k, ot.Post14Keys) {
				ctx.Report(
					pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14,
					v,
					fmt.Sprintf("%s key %q was introduced after PDF 1.4", typeName, k),
				)
			}
		}
	}
}

// validateArrayAgainstSchema checks array v against the Arlington object-model schema for
// typeName: a required fixed index must exist (MissingRequiredKey), and every element --
// fixed-index rows first, the wildcard row for the rest -- must satisfy its allowed types
// (WrongValueType), enumerated values or compiled value range (DisallowedValue), and
// indirect-reference requirement (IndirectRequired). Violations are reported against owner,
// the nearest enclosing dict, so
// fixers can resolve them by ref. A predicated column suppresses only its own check, as in
// validateAgainstSchema.
func validateArrayAgainstSchema(v pdf.PDFArray, typeName string, owner pdf.PDFValue, ctx *ValidationContext) {
	ot, ok := arlington.Type(typeName)
	if !ok {
		return
	}

	var fixed map[int]bool
	for i := range ot.Keys {
		kd := &ot.Keys[i]
		idx, err := strconv.Atoi(kd.Name)
		if err != nil || idx < 0 {
			continue
		}
		if fixed == nil {
			fixed = make(map[int]bool, len(ot.Keys))
		}
		fixed[idx] = true
		if idx >= len(v) {
			if kd.Required && !kd.Predicated.Required {
				ctx.Report(
					pdf.Checks.ObjectModel.MissingRequiredKey,
					owner,
					fmt.Sprintf("%s is missing required element %d", typeName, idx),
				)
			}
			continue
		}
		validateArrayElement(v[idx], kd, idx, typeName, owner, ctx)
		if v[idx] != nil && kd.ValueCond != nil {
			if legal, ok := evalCondArray(kd.ValueCond, v); ok && !legal {
				ctx.Report(
					pdf.Checks.ObjectModel.DisallowedValue,
					owner,
					fmt.Sprintf("%s element %d has a value outside its legal range", typeName, idx),
				)
			}
		}
		if v[idx] != nil && kd.SpecialCase != nil {
			if holds, ok := evalCondArray(kd.SpecialCase, v); ok && !holds {
				ctx.Report(
					pdf.Checks.ObjectModel.ConstraintViolated,
					owner,
					fmt.Sprintf("%s element %d violates an object-model consistency constraint", typeName, idx),
				)
			}
		}
	}

	if ot.Wildcard != nil {
		for i, item := range v {
			if !fixed[i] {
				validateArrayElement(item, ot.Wildcard, i, typeName, owner, ctx)
			}
		}
	}
}

// validateArrayElement applies kd's type/value/indirect constraints to element idx of a
// typeName-typed array, mirroring validateAgainstSchema's per-key logic.
func validateArrayElement(val pdf.PDFValue, kd *arlington.KeyDef, idx int, typeName string, owner pdf.PDFValue, ctx *ValidationContext) {
	if !kd.Predicated.Types && len(kd.Types) > 0 && !matchesValueType(val, kd.Types) {
		ctx.Report(
			pdf.Checks.ObjectModel.WrongValueType,
			owner,
			fmt.Sprintf("%s element %d has an unexpected value type", typeName, idx),
		)
		return
	}
	if !kd.Predicated.Values && len(kd.PossibleValues) > 0 {
		if s, ok := scalarEnumString(val); ok && !stringInList(s, kd.PossibleValues) {
			ctx.Report(
				pdf.Checks.ObjectModel.DisallowedValue,
				owner,
				fmt.Sprintf("%s element %d has a value not in its enumerated legal values", typeName, idx),
			)
		}
	}
	if !kd.Predicated.Indirect && kd.IndirectReference == arlington.IndirectRequired && !isIndirect(val) {
		ctx.Report(
			pdf.Checks.ObjectModel.IndirectRequired,
			owner,
			fmt.Sprintf("%s element %d must be an indirect reference", typeName, idx),
		)
	}
}

// condOperands resolves a compiled condition's leaf operands against its owning container:
// sibling keys of a dict, or elements of an array (Key is a decimal index there). Implemented
// as value types over a generic evaluator so both paths share one set of semantics without
// interface boxing.
type condOperands interface {
	lookup(key string) (pdf.PDFValue, bool)
}

type dictOperands struct{ v pdf.PDFDict }

func (d dictOperands) lookup(key string) (pdf.PDFValue, bool) {
	val, present := d.v.Entries[key]
	return val, present
}

type arrayOperands struct{ v pdf.PDFArray }

func (a arrayOperands) lookup(key string) (pdf.PDFValue, bool) {
	idx, err := strconv.Atoi(key)
	if err != nil || idx < 0 || idx >= len(a.v) {
		return nil, false
	}
	return a.v[idx], true
}

// evalCond evaluates a compiled Arlington condition against v's own entries. ok is false when
// an operand is unresolvable (a present comparison value that is not a name/integer scalar);
// callers must then skip the dependent check, never flag. An absent or null sibling is a
// definite state: CondPresent is false, CondEq false, CondNe true. Boolean operands
// short-circuit on a decisive kid (true for Or, false for And) even when a sibling is not ok.
func evalCond(c *arlington.Cond, v pdf.PDFDict) (val, ok bool) {
	return evalCondOn(c, dictOperands{v})
}

// evalCondArray evaluates a fixed-index row's compiled condition against the owning array,
// with the same tri-state semantics as evalCond; an out-of-range index is a definite absence.
func evalCondArray(c *arlington.Cond, v pdf.PDFArray) (val, ok bool) {
	return evalCondOn(c, arrayOperands{v})
}

func evalCondOn[S condOperands](c *arlington.Cond, src S) (val, ok bool) {
	switch c.Op {
	case arlington.CondPresent:
		sib, present := src.lookup(c.Key)
		return present && sib != nil, true
	case arlington.CondEq, arlington.CondNe:
		if c.Mod != 0 {
			lhs, lok := operandInt(src, c.Key, c.Fn)
			rhs, err := strconv.ParseInt(c.Value, 10, 64)
			if !lok || err != nil {
				return false, false
			}
			eq := lhs%int64(c.Mod) == rhs
			return eq == (c.Op == arlington.CondEq), true
		}
		if c.Fn != arlington.FnValue || c.RHSKey != "" {
			lhs, rhs, ok := comparisonOperands(src, c)
			if !ok {
				return false, false
			}
			eq := lhs == rhs
			return eq == (c.Op == arlington.CondEq), true
		}
		sib, present := src.lookup(c.Key)
		eq := false
		if present && sib != nil {
			s, scalar := scalarEnumString(sib)
			if !scalar {
				return false, false
			}
			eq = s == c.Value
		}
		return eq == (c.Op == arlington.CondEq), true
	case arlington.CondLt, arlington.CondLe, arlington.CondGt, arlington.CondGe:
		n, bound, ok := comparisonOperands(src, c)
		if !ok {
			return false, false
		}
		// Values within 1e-5 of the bound compare equal, matching the tolerance the
		// hand-written checks use for rounded reals (e.g. /CA 1.0000001 passes CA <= 1).
		const eps = 1e-5
		switch c.Op {
		case arlington.CondLt:
			return n < bound-eps, true
		case arlington.CondLe:
			return n <= bound+eps, true
		case arlington.CondGt:
			return n > bound+eps, true
		default:
			return n >= bound-eps, true
		}
	case arlington.CondContains:
		sib, present := src.lookup(c.Key)
		if !present || sib == nil {
			return false, true // a definite state, like CondEq on an absent sibling
		}
		arr, isArr := sib.(pdf.PDFArray)
		if !isArr {
			arr = pdf.PDFArray{sib}
		}
		unresolvable := false
		for _, item := range arr {
			if item == nil {
				continue
			}
			s, scalar := scalarEnumString(item)
			if !scalar {
				unresolvable = true
				continue
			}
			if s == c.Value {
				return true, true
			}
		}
		return false, !unresolvable
	case arlington.CondUnknown:
		// An extension gate: unresolvable by design, so only a decisive sibling operand
		// can settle the enclosing And/Or.
		return false, false
	case arlington.CondNotStd14:
		sib, present := src.lookup(c.Key)
		name, isName := sib.(pdf.PDFName)
		if !present || !isName {
			return false, false
		}
		return !arlington.IsStandard14(name.Value), true
	case arlington.CondBitsClear, arlington.CondBitsSet:
		sib, present := src.lookup(c.Key)
		iv, isInt := sib.(pdf.PDFInteger)
		if !present || !isInt || c.BitLo < 1 || c.BitHi < c.BitLo || c.BitHi > 64 {
			return false, false
		}
		// Bits are 1-based; the uint64 conversion keeps the two's-complement pattern of
		// negative flag words (e.g. encryption /P).
		var mask uint64 = (1<<uint(c.BitHi) - 1) &^ (1<<uint(c.BitLo-1) - 1)
		bits := uint64(iv) & mask
		if c.Op == arlington.CondBitsClear {
			return bits == 0, true
		}
		return bits == mask, true
	case arlington.CondNot:
		if len(c.Kids) != 1 {
			return false, false
		}
		kv, kok := evalCondOn(&c.Kids[0], src)
		return !kv, kok
	case arlington.CondAnd, arlington.CondOr:
		decisive := c.Op == arlington.CondOr
		allOK := true
		for i := range c.Kids {
			kv, kok := evalCondOn(&c.Kids[i], src)
			if kok && kv == decisive {
				return decisive, true
			}
			if !kok {
				allOK = false
			}
		}
		if !allOK {
			return false, false
		}
		return !decisive, true
	}
	return false, false
}

// operandNumber resolves one comparison operand to a number: the entry's numeric value, or
// its array/string length per fn. ok is false when the entry is absent, null, or the wrong
// shape for fn -- the enclosing condition is then unknown.
func operandNumber[S condOperands](src S, key string, fn arlington.CondFn) (float64, bool) {
	val, present := src.lookup(key)
	if !present || val == nil {
		return 0, false
	}
	switch fn {
	case arlington.FnArrayLength:
		arr, ok := val.(pdf.PDFArray)
		if !ok {
			return 0, false
		}
		return float64(len(arr)), true
	case arlington.FnStringLength:
		s, ok := val.(pdf.PDFString)
		if !ok {
			return 0, false
		}
		return float64(len(s.Value)), true
	default:
		return numericValue(val)
	}
}

// operandInt is operandNumber for modulo comparisons, which are integer-only: a plain value
// operand must be a PDF integer (a real never satisfies "mod N"), lengths are integral.
func operandInt[S condOperands](src S, key string, fn arlington.CondFn) (int64, bool) {
	if fn == arlington.FnValue {
		val, present := src.lookup(key)
		iv, isInt := val.(pdf.PDFInteger)
		if !present || !isInt {
			return 0, false
		}
		return int64(iv), true
	}
	n, ok := operandNumber(src, key, fn)
	return int64(n), ok
}

// comparisonOperands resolves both sides of a compiled comparison: the left operand, and a
// literal or second-entry right operand.
func comparisonOperands[S condOperands](src S, c *arlington.Cond) (lhs, rhs float64, ok bool) {
	lhs, lok := operandNumber(src, c.Key, c.Fn)
	if !lok {
		return 0, 0, false
	}
	if c.RHSKey != "" {
		rhs, rok := operandNumber(src, c.RHSKey, c.RHSFn)
		return lhs, rhs, rok
	}
	rhs, err := strconv.ParseFloat(c.Value, 64)
	if err != nil {
		return 0, 0, false
	}
	return lhs, rhs, true
}

// selfIdentifiedType re-anchors an untyped dict by its own /Type (+/Subtype) names via the
// generated unambiguous-identification table; "" when they do not identify exactly one type.
func selfIdentifiedType(v pdf.PDFDict) string {
	t, ok := v.Entries["Type"].(pdf.PDFName)
	if !ok {
		return ""
	}
	sub := ""
	if s, ok := v.Entries["Subtype"].(pdf.PDFName); ok {
		sub = s.Value
	}
	return arlington.SelfIdentified(t.Value, sub)
}

// numericValue converts a PDF integer or real to float64 for range conditions.
func numericValue(val pdf.PDFValue) (float64, bool) {
	switch n := val.(type) {
	case pdf.PDFInteger:
		return float64(n), true
	case pdf.PDFReal:
		return float64(n), true
	}
	return 0, false
}

// isIndirect reports whether val was reached through an indirect reference. Only dicts and
// streams carry the resolver's "_ref" marker (internal/pdf/resolver.go); arrays required to be
// indirect (3 rows in the model) have no such marker and are always treated as satisfied,
// erring toward false negatives.
func isIndirect(val pdf.PDFValue) bool {
	d, ok := val.(pdf.PDFDict)
	if !ok {
		return true
	}
	_, ok = d.Entries["_ref"]
	return ok
}

// scalarEnumString returns val's string form for PossibleValues membership testing, for the
// name, integer, and boolean types only: string/real matching against Arlington's enum column
// is format-fragile (quoting, precision) and covers very few keys, so it is left unenforced.
func scalarEnumString(val pdf.PDFValue) (string, bool) {
	switch v := val.(type) {
	case pdf.PDFName:
		return v.Value, true
	case pdf.PDFInteger:
		return strconv.Itoa(int(v)), true
	case pdf.PDFBoolean:
		if v {
			return "true", true
		}
		return "false", true
	default:
		return "", false
	}
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

// matchesValueType reports whether val's PDF type is one of allowed. An empty allowed list
// means unconstrained (always matches). PDF null (Go nil) always matches any key: ISO 32000
// 7.3.9 treats a null value as equivalent to the key being absent, regardless of what type the
// key otherwise expects. Unresolved references and Go types this function doesn't recognize are
// also treated as a match, so callers skip rather than flag them.
func matchesValueType(val pdf.PDFValue, allowed []arlington.ValueType) bool {
	if len(allowed) == 0 || val == nil {
		return true
	}
	for _, vt := range allowed {
		if valueTypeAllowed(val, vt) {
			return true
		}
	}
	return false
}

// valueTypeAllowed reports whether val's Go representation can stand in for the PDF type vt.
// An unrecognized Go type (e.g. an unresolved pdf.PDFRef) always matches, since this function
// has nothing to say about it.
func valueTypeAllowed(val pdf.PDFValue, vt arlington.ValueType) bool {
	switch v := val.(type) {
	case pdf.PDFName:
		return vt == arlington.Name
	case pdf.PDFInteger:
		return vt == arlington.Integer || vt == arlington.Number || vt == arlington.Bitmask
	case pdf.PDFReal:
		return vt == arlington.Number
	case pdf.PDFBoolean:
		return vt == arlington.Boolean
	case pdf.PDFString, pdf.PDFHexString:
		return vt == arlington.String || vt == arlington.StringText ||
			vt == arlington.StringByte || vt == arlington.StringASCII || vt == arlington.Date
	case pdf.PDFArray:
		return vt == arlington.Array || vt == arlington.Rectangle || vt == arlington.Matrix
	case pdf.PDFDict:
		if v.HasStream {
			return vt == arlington.Stream
		}
		return vt == arlington.Dictionary || vt == arlington.NameTree || vt == arlington.NumberTree
	default:
		return true
	}
}

// arlingtonChildType returns the Arlington type name that val (key's value inside a dict of type
// parentType) should conform to, or "" if parentType is unknown, key has no schema entry, or
// val doesn't resolve to a single candidate type. Deliberately conservative: propagating a
// wrong guessed type would misvalidate an entire subtree.
func arlingtonChildType(parentType, key string, val pdf.PDFValue) string {
	ot, ok := arlington.Type(parentType)
	if !ok {
		return ""
	}
	kd := findArlingtonKey(ot, key)
	if kd == nil {
		return ""
	}
	return resolveLinkGroups(kd.LinkGroups, kd.Types, val)
}

// arlingtonElementType returns the Arlington type name that item (an element of an array of
// type arrayType) should conform to, or "" if unknown or unresolved.
func arlingtonElementType(arrayType string, item pdf.PDFValue) string {
	ot, ok := arlington.Type(arrayType)
	if !ok || ot.Wildcard == nil {
		return ""
	}
	return resolveLinkGroups(ot.Wildcard.LinkGroups, ot.Wildcard.Types, item)
}

// resolveLinkGroups picks the LinkGroup matching val's Go-level kind, then resolves it to a
// single Arlington candidate: directly if there is exactly one, or via the group's
// Discriminator otherwise -- a key name on dict values (e.g. Subtype, S, FunctionType), a
// fixed element index on array values (e.g. "0" for colour-space arrays like [/ICCBased ...]).
// Any step that doesn't produce an exact match -- no group matches val's kind, no
// discriminator was found at generation time, the discriminator is absent, or its value is
// unrecognized -- returns "": never propagate a guessed type. A group with nil ValueTypes
// (the key's only Type alternative) still requires val's kind to match the key's declared
// keyTypes, so a mis-shaped value (e.g. a stream where a dictionary is declared) never
// inherits a wrong-shaped schema.
func resolveLinkGroups(groups []arlington.LinkGroup, keyTypes []arlington.ValueType, val pdf.PDFValue) string {
	if val == nil {
		return ""
	}
	for _, g := range groups {
		kinds := g.ValueTypes
		if kinds == nil {
			kinds = keyTypes
		}
		if !linkGroupMatchesKind(val, kinds) {
			continue
		}
		switch len(g.Candidates) {
		case 0:
			return ""
		case 1:
			return g.Candidates[0]
		default:
			if g.Discriminator == "" {
				return ""
			}
			var dv pdf.PDFValue
			switch tv := val.(type) {
			case pdf.PDFDict:
				dv = tv.Entries[g.Discriminator]
			case pdf.PDFArray:
				idx, err := strconv.Atoi(g.Discriminator)
				if err != nil || idx < 0 || idx >= len(tv) {
					return ""
				}
				dv = tv[idx]
			default:
				return ""
			}
			s, ok := scalarEnumString(dv)
			if !ok {
				return ""
			}
			return g.ByValue[s] // zero value "" if s isn't a known discriminator value
		}
	}
	return ""
}

// linkGroupMatchesKind reports whether val's Go-level kind matches one of valueTypes. nil
// valueTypes means no type constraint is known at all, so it always matches. Unlike
// valueTypeAllowed (which defaults permissively -- there, false means "flag a violation"), an
// unrecognized or non-matching kind here always fails closed: picking the wrong LinkGroup risks
// propagating the wrong schema to an entire subtree, a worse outcome than leaving it unresolved.
func linkGroupMatchesKind(val pdf.PDFValue, valueTypes []arlington.ValueType) bool {
	if valueTypes == nil {
		return true
	}
	for _, vt := range valueTypes {
		switch vt {
		case arlington.Array, arlington.Rectangle, arlington.Matrix:
			if _, ok := val.(pdf.PDFArray); ok {
				return true
			}
		case arlington.Dictionary, arlington.NameTree, arlington.NumberTree:
			if d, ok := val.(pdf.PDFDict); ok && !d.HasStream {
				return true
			}
		case arlington.Stream:
			if d, ok := val.(pdf.PDFDict); ok && d.HasStream {
				return true
			}
		case arlington.Name:
			if _, ok := val.(pdf.PDFName); ok {
				return true
			}
		case arlington.Integer, arlington.Number, arlington.Bitmask:
			switch val.(type) {
			case pdf.PDFInteger, pdf.PDFReal:
				return true
			}
		case arlington.Boolean:
			if _, ok := val.(pdf.PDFBoolean); ok {
				return true
			}
		case arlington.String, arlington.StringText, arlington.StringByte, arlington.StringASCII, arlington.Date:
			switch val.(type) {
			case pdf.PDFString, pdf.PDFHexString:
				return true
			}
		}
	}
	return false
}

// hasNamedKey reports whether ot declares an explicit (non-wildcard) row for key.
func hasNamedKey(ot arlington.ObjectType, key string) bool {
	for i := range ot.Keys {
		if ot.Keys[i].Name == key {
			return true
		}
	}
	return false
}

// findArlingtonKey returns the KeyDef governing key within ot: an explicit named entry if
// present, else the wildcard entry, else nil.
func findArlingtonKey(ot arlington.ObjectType, key string) *arlington.KeyDef {
	for i := range ot.Keys {
		if ot.Keys[i].Name == key {
			return &ot.Keys[i]
		}
	}
	return ot.Wildcard
}
