// Package arlington exposes the Arlington PDF Model
// (https://github.com/pdf-association/arlington-pdf-model) as a compiled-in Go table,
// generated from the vendored PDF 1.4 TSV set under testdata/tsv/1.4.
//
// Arlington describes what ISO 32000 permits for every dictionary/array/stream type in the
// spec; it does not encode PDF/A's narrower restrictions, and this package has no opinion on
// those — it is a lookup table only. See arlington.md at the repo root for the intended use.
package arlington

//go:generate go run gen.go

// ValueType is one of the plain PDF value types Arlington's Type column enumerates.
type ValueType int

// The set of value types Arlington's TSV Type column uses, in the order they were first
// observed in the vendored corpus. Keep in sync with valueTypeIdent in gen.go.
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

// String returns the Arlington TSV token for t (e.g. "string-text"), or "unknown" if t is not
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
	// IndirectForbidden means the value must be a direct object.
	IndirectForbidden
)

// KeyDef is one row of an Arlington type: a single named key (or a fixed array index, which
// Arlington also expresses as a Key), or the wildcard entry (Name == "*") that governs
// arbitrary dictionary keys or repeating array elements.
type KeyDef struct {
	Name              string
	Types             []ValueType
	Required          bool
	IndirectReference IndirectRule
	SinceVersion      string
	DeprecatedIn      string   // empty if never deprecated
	PossibleValues    []string // enumerated legal values, when constrained; predicate-only entries are dropped
	Link              []string // Arlington type name(s) the value should itself conform to
	// Predicated marks a row whose Required, IndirectReference, or PossibleValues carried an
	// fn: predicate this generator does not evaluate. Consumers should skip predicated rows
	// rather than flag them, erring toward false negatives over false positives.
	Predicated bool
}

// ObjectType is one Arlington-defined dictionary/array/stream schema (one TSV file).
type ObjectType struct {
	Name     string
	Keys     []KeyDef // named keys and fixed array indices, in TSV row order
	Wildcard *KeyDef  // the "*" row, if this type has one; nil otherwise
}

// Type looks up a vendored PDF 1.4 Arlington type by name (e.g. "Catalog", "ExtGState").
func Type(name string) (ObjectType, bool) {
	t, ok := Types[name]
	return t, ok
}
