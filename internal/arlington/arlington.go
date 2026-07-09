// Package arlington exposes the Arlington PDF Model
// (https://github.com/pdf-association/arlington-pdf-model) as a compiled-in Go table,
// generated from the vendored PDF 1.4 TSV set under testdata/tsv/1.4.
//
// Arlington describes what ISO 32000 permits for every dictionary/array/stream type in the
// spec; it does not encode PDF/A's narrower restrictions, and this package has no opinion on
// those — it is a lookup table only.
package arlington

//go:generate go run gen.go

// ValueType is one of the plain PDF value types Arlington's Type column enumerates.
type ValueType int

// The set of value types Arlington's TSV Type column uses.
const (
	Array ValueType = iota
	Bitmask
	Boolean
	Date
	Dictionary
	Integer
	Matrix
	Name
	NameTree
	Null
	Number
	NumberTree
	Rectangle
	Stream
	String
	StringASCII
	StringByte
	StringText
)

// String returns the Arlington TSV token for t, or "unknown" if t is not
// one of the declared constants.
func (t ValueType) String() string {
	switch t {
	case Array:
		return "array"
	case Bitmask:
		return "bitmask"
	case Boolean:
		return "boolean"
	case Date:
		return "date"
	case Dictionary:
		return "dictionary"
	case Integer:
		return "integer"
	case Matrix:
		return "matrix"
	case Name:
		return "name"
	case NameTree:
		return "name-tree"
	case Null:
		return "null"
	case Number:
		return "number"
	case NumberTree:
		return "number-tree"
	case Rectangle:
		return "rectangle"
	case Stream:
		return "stream"
	case String:
		return "string"
	case StringASCII:
		return "string-ascii"
	case StringByte:
		return "string-byte"
	case StringText:
		return "string-text"
	default:
		return "unknown"
	}
}

// IndirectRule states whether a key's value must, must not, or may be an indirect reference.
type IndirectRule int

const (
	// IndirectEither means both direct and indirect values are allowed, the requirement is
	// unconstrained, or it varies across this key's type alternatives.
	IndirectEither IndirectRule = iota
	// IndirectRequired means the value must be an indirect reference.
	IndirectRequired
	// IndirectForbidden means the value must be a direct object. Never generated: every
	// fn:MustBeDirect row in the model has a scalar or array value type, and the resolver
	// only marks indirection on dicts/streams (_ref), so the constraint is unenforceable.
	// Kept so the disposition is representable if the resolver ever learns scalar marking.
	IndirectForbidden
)

// LinkGroup is one Type alternative's set of candidate Arlington types for a key's value: which
// of the key's declared Types this alternative applies to (nil if it is the key's only
// alternative, so no kind check is needed), the candidate type name(s) a matching value should
// itself conform to, and -- when more than one candidate exists -- the discriminator key (e.g.
// Subtype, S, FunctionType) each candidate declares as Required with a single PossibleValues
// entry, and the map from that value to the one matching candidate. Discriminator/ByValue are
// zero if no such key was found (the group stays ambiguous) or if there is only one candidate.
type LinkGroup struct {
	ValueTypes    []ValueType
	Candidates    []string
	Discriminator string
	ByValue       map[string]string
}

// Predication marks the columns of a row whose constraint carried an fn: predicate the
// generator could not fold. Each check must skip exactly its own column's flag -- the other
// columns' constraints remain fully known -- erring toward false negatives over false
// positives.
type Predication struct {
	Required bool // the Required column is unresolved
	Types    bool // a Type column token was unrecognized
	Values   bool // PossibleValues carries an unfoldable entry
	Indirect bool // the IndirectReference column is unresolved
}

// Any reports whether any column of the row is predicated.
func (p Predication) Any() bool { return p.Required || p.Types || p.Values || p.Indirect }

// CondOp enumerates the operators a compiled runtime condition can use.
type CondOp int

const (
	CondPresent CondOp = iota // the sibling Key exists and is not null
	CondEq                    // the sibling Key's scalar value equals Value
	CondNe                    // the sibling Key's scalar value differs from Value
	CondLt                    // the sibling Key's numeric value is < Value
	CondLe                    // the sibling Key's numeric value is <= Value
	CondGt                    // the sibling Key's numeric value is > Value
	CondGe                    // the sibling Key's numeric value is >= Value
	CondAnd
	CondOr
	CondNot
	// CondUnknown is a subexpression the generator recognized as boolean but cannot evaluate
	// (an fn:Extension gate). It always evaluates as unresolvable, so a decisive sibling
	// operand can still settle the enclosing And/Or; otherwise the dependent check is skipped.
	CondUnknown
	// CondContains is true when Key's value equals Value or is an array with an element equal
	// to Value (fn:Contains(@Filter,JPXDecode) -- /Filter may be a name or a name array).
	CondContains
	// CondNotStd14 is true when Key (always BaseFont) names a font outside the standard 14
	// (fn:NotStandard14Font). A subset-tagged name (ABCDEF+Helvetica) is not a standard font;
	// an absent or non-name value leaves the condition unknown.
	CondNotStd14
	// CondBitsClear / CondBitsSet test bits BitLo..BitHi (1-based, inclusive) of Key's
	// integer value: all clear, or all set (fn:BitsClear/fn:BitClear/fn:BitsSet/fn:BitSet --
	// the single-bit forms compile with BitLo == BitHi). A non-integer value is unknown.
	CondBitsClear
	CondBitsSet
)

// CondFn transforms a comparison operand: the key's value itself, or a derived quantity.
type CondFn int

const (
	FnValue        CondFn = iota // the operand is the key's value
	FnArrayLength                // fn:ArrayLength(Key): element count of an array value
	FnStringLength               // fn:StringLength(Key): byte length of a string value
)

// Cond is a compiled fn: condition over the owning container's own entries -- sibling keys of
// a dict, or elements of an array when Key is a decimal index -- produced at generation time
// from predicate forms with no cross-object paths. Leaf ops use Key (and Value for
// comparisons); CondAnd/CondOr/CondNot use Kids. Consumers evaluate it fail-closed: an
// unresolvable operand must skip the dependent check, never flag.
type Cond struct {
	Op    CondOp
	Key   string
	Value string
	// Fn derives the left operand from Key's value (array/string length instead of the value
	// itself); comparisons with a derived operand are always numeric.
	Fn CondFn
	// RHSKey, when set, makes the right operand another entry's value (with RHSFn) instead of
	// the Value literal ("@0<=@1", "@TI<fn:ArrayLength(Opt)").
	RHSKey string
	RHSFn  CondFn
	// RHSAdd, RHSMul and RHSKey2 extend the second-entry right operand to the affine form
	// RHSAdd + RHSMul*op(RHSKey,RHSFn) - value(RHSKey2), compiled from arithmetic
	// expressions like "1+(@LastChar - @FirstChar)" or "2 * @N". RHSMul 0 means 1.
	RHSAdd  int
	RHSMul  int
	RHSKey2 string
	// Mod, when nonzero, compares the (integer) left operand modulo Mod against Value
	// ("(@Rotate mod 90)==0"); only generated with CondEq/CondNe and a literal right side.
	Mod int
	// BitLo/BitHi delimit the 1-based bit range CondBitsClear/CondBitsSet test.
	BitLo, BitHi int
	Kids         []Cond
}

// PinnedValue pins a key to one specific value whenever its condition holds
// (fn:RequiredValue -- e.g. EncryptionStandard.R must be 3 when V is 2 or 3). The pinned
// value is also an ordinary member of the key's PossibleValues.
type PinnedValue struct {
	When  *Cond
	Value string
}

// KeyDef is one row of an Arlington type: a single named key (or a fixed array index, which
// Arlington also expresses as a Key), or the wildcard entry (Name == "*") that governs
// arbitrary dictionary keys or repeating array elements.
type KeyDef struct {
	Name     string
	Types    []ValueType
	Required bool
	// RequiredWhen makes the key conditionally required: it must be present whenever the
	// condition holds. Mutually exclusive with Required and Predicated.Required.
	RequiredWhen *Cond
	// ValueCond constrains the key's present value (a compiled whole-column fn:Eval range,
	// e.g. 0 <= CA <= 1). On a named dict key its operands are sibling key names, evaluated
	// over the owning dict like RequiredWhen; on a fixed array-index row they are element
	// indices, evaluated over the owning array. Mutually exclusive with PossibleValues and
	// Predicated.Values.
	ValueCond         *Cond
	IndirectReference IndirectRule
	// SinceVersion/DeprecatedIn are unread at runtime (the 1.4 TSV set is pre-filtered);
	// kept as groundwork for the predicate evaluator's version-gate families.
	SinceVersion   string
	DeprecatedIn   string      // empty if never deprecated
	PossibleValues []string // enumerated legal values, when constrained; predicate-only entries are dropped
	// PinnedValues narrows PossibleValues conditionally: whenever a pin's condition holds,
	// the key must have exactly that pin's value.
	PinnedValues []PinnedValue
	LinkGroups   []LinkGroup // per-Type-alternative candidate Arlington type(s) the value should itself conform to
	// SpecialCase is a compiled consistency constraint from the TSV SpecialCase column
	// (e.g. fn:ArrayLength(DecodeParms)==fn:ArrayLength(Filter)); it must hold whenever the
	// key is present. Only constraints over the owning container's own entries are compiled;
	// the rest of the column (cross-object paths, fn:Ignore advisories, domain predicates)
	// is not represented.
	SpecialCase *Cond
	// Inheritable means an ancestor node (e.g. a Page's Pages ancestor) may supply this key
	// instead, so its absence here is not itself a violation.
	Inheritable bool
	// Predicated marks the columns whose fn: predicate could not be folded at generation time.
	Predicated Predication
}

// ObjectType is one Arlington-defined dictionary/array/stream schema (one TSV file).
type ObjectType struct {
	Name     string
	Keys     []KeyDef // named keys and fixed array indices, in TSV row order
	Wildcard *KeyDef  // the "*" row, if this type has one; nil otherwise
	// Post14Keys lists keys this type gained after PDF 1.4, computed by diffing the vendored
	// tsv/latest set against tsv/1.4. Empty if the type itself did not exist in tsv/latest
	// (e.g. renamed across versions), which is a safe (false-negative) default.
	Post14Keys []string
}

// standard14Fonts is the set of base font names every conforming reader must provide
// (ISO 32000-1 §9.6.2.2); hand-maintained, not TSV data.
var standard14Fonts = map[string]bool{
	"Times-Roman": true, "Times-Bold": true, "Times-Italic": true, "Times-BoldItalic": true,
	"Helvetica": true, "Helvetica-Bold": true, "Helvetica-Oblique": true, "Helvetica-BoldOblique": true,
	"Courier": true, "Courier-Bold": true, "Courier-Oblique": true, "Courier-BoldOblique": true,
	"Symbol": true, "ZapfDingbats": true,
}

// IsStandard14 reports whether name is exactly one of the 14 standard font names. A
// subset-tagged name (ABCDEF+Helvetica) refers to an embedded subset, not a standard font.
func IsStandard14(name string) bool { return standard14Fonts[name] }

// Type looks up a vendored PDF 1.4 Arlington type by name (e.g. "Catalog", "ExtGState").
func Type(name string) (ObjectType, bool) {
	t, ok := Types[name]
	return t, ok
}

// SelfIdentified returns the one Arlington type an untyped dictionary's own /Type (and
// /Subtype) names unambiguously identify, or "" when the pair is unknown or ambiguous.
// The generated table only contains pairs exactly one type in the model is consistent with,
// so re-anchoring on the result never guesses.
func SelfIdentified(typeVal, subtypeVal string) string {
	if subtypeVal != "" {
		if n, ok := selfIdentified[[2]string{typeVal, subtypeVal}]; ok {
			return n
		}
	}
	return selfIdentified[[2]string{typeVal, ""}]
}
