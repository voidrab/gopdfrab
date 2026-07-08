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
	// IndirectForbidden means the value must be a direct object. Never generated today --
	// all fn:MustBeDirect rows are predicated; reserved for the predicate evaluator.
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

// KeyDef is one row of an Arlington type: a single named key (or a fixed array index, which
// Arlington also expresses as a Key), or the wildcard entry (Name == "*") that governs
// arbitrary dictionary keys or repeating array elements.
type KeyDef struct {
	Name              string
	Types             []ValueType
	Required          bool
	IndirectReference IndirectRule
	// SinceVersion/DeprecatedIn are unread at runtime (the 1.4 TSV set is pre-filtered);
	// kept as groundwork for the predicate evaluator's version-gate families.
	SinceVersion   string
	DeprecatedIn   string      // empty if never deprecated
	PossibleValues []string    // enumerated legal values, when constrained; predicate-only entries are dropped
	LinkGroups     []LinkGroup // per-Type-alternative candidate Arlington type(s) the value should itself conform to
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

// Type looks up a vendored PDF 1.4 Arlington type by name (e.g. "Catalog", "ExtGState").
func Type(name string) (ObjectType, bool) {
	t, ok := Types[name]
	return t, ok
}
