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
// after PDF 1.4 must not appear on a non-wildcard type (KeyIntroducedAfterPDF14). Predicated
// schema rows -- those with an fn: condition the Arlington generator does not evaluate -- are
// never enforced, erring toward false negatives over false positives. Keys absent from both the
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

		if kd.Required && !kd.Predicated && !kd.Inheritable && !present {
			ctx.Report(
				pdf.Checks.ObjectModel.MissingRequiredKey,
				v,
				fmt.Sprintf("%s is missing required key %q", typeName, kd.Name),
			)
			continue
		}

		if present && !kd.Predicated && len(kd.Types) > 0 && !matchesValueType(val, kd.Types) {
			ctx.Report(
				pdf.Checks.ObjectModel.WrongValueType,
				v,
				fmt.Sprintf("%s key %q has an unexpected value type", typeName, kd.Name),
			)
			continue
		}

		if present && !kd.Predicated && len(kd.PossibleValues) > 0 {
			if s, ok := scalarEnumString(val); ok && !stringInList(s, kd.PossibleValues) {
				ctx.Report(
					pdf.Checks.ObjectModel.DisallowedValue,
					v,
					fmt.Sprintf("%s key %q has a value not in its enumerated legal values", typeName, kd.Name),
				)
			}
		}

		if present && !kd.Predicated && kd.IndirectReference == arlington.IndirectRequired && !isIndirect(val) {
			ctx.Report(
				pdf.Checks.ObjectModel.IndirectRequired,
				v,
				fmt.Sprintf("%s key %q must be an indirect reference", typeName, kd.Name),
			)
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
// name and integer types only: string/real matching against Arlington's enum column is
// format-fragile (quoting, precision) and covers very few keys, so it is left unenforced.
func scalarEnumString(val pdf.PDFValue) (string, bool) {
	switch v := val.(type) {
	case pdf.PDFName:
		return v.Value, true
	case pdf.PDFInteger:
		return strconv.Itoa(int(v)), true
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
	return resolveLinkGroups(kd.LinkGroups, val)
}

// arlingtonElementType returns the Arlington type name that item (an element of an array of
// type arrayType) should conform to, or "" if unknown or unresolved.
func arlingtonElementType(arrayType string, item pdf.PDFValue) string {
	ot, ok := arlington.Type(arrayType)
	if !ok || ot.Wildcard == nil {
		return ""
	}
	return resolveLinkGroups(ot.Wildcard.LinkGroups, item)
}

// resolveLinkGroups picks the LinkGroup matching val's Go-level kind, then resolves it to a
// single Arlington candidate: directly if there is exactly one, or via the group's
// Discriminator key (e.g. Subtype, S, FunctionType) otherwise. Any step that doesn't produce an
// exact match -- no group matches val's kind, no discriminator was found at generation time, the
// discriminator key is absent, or its value is unrecognized -- returns "": never propagate a
// guessed type.
func resolveLinkGroups(groups []arlington.LinkGroup, val pdf.PDFValue) string {
	if val == nil {
		return ""
	}
	for _, g := range groups {
		if !linkGroupMatchesKind(val, g.ValueTypes) {
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
			d, ok := val.(pdf.PDFDict)
			if !ok {
				return ""
			}
			s, ok := scalarEnumString(d.Entries[g.Discriminator])
			if !ok {
				return ""
			}
			return g.ByValue[s] // zero value "" if s isn't a known discriminator value
		}
	}
	return ""
}

// linkGroupMatchesKind reports whether val's Go-level kind matches one of valueTypes. nil
// valueTypes means the group is the key's only Type alternative, so it always matches. Unlike
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
