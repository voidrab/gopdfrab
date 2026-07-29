package pdf

// PDFValue is any PDF object. The null object (ISO 32000-1 7.3.10) is a nil
// PDFValue -- there is no PDFNull type. Three sites depend on it: a reference
// with no target resolves to nil, the 'null' keyword parses to nil, and the
// writer emits nil as "null".
//
// The trap this creates: a dictionary entry that is present but null is
// Entries[k] == nil, indistinguishable from an absent entry. That is correct
// PDF semantics -- the spec says the two are equivalent -- but it means
// `if _, ok := Entries[k]; ok` does not mean "has a value". Test the value, not
// the key's presence.
type PDFValue any

type PDFHexString struct{ Value string }
type PDFString struct{ Value string }
type PDFInteger int
type PDFReal float32
type PDFBoolean bool
type PDFName struct{ Value string }
type PDFArray []PDFValue

type PDFDict struct {
	Entries   Dict
	HasStream bool
	// RawStream holds the undecoded stream bytes. The slice may alias a
	// read-only memory-map; always assign a new slice, never mutate in place.
	RawStream []byte
}

func NewPDFDict() PDFDict {
	return PDFDict{
		Entries:   NewDict(),
		HasStream: false,
	}
}

type PDFRef struct {
	ObjNum int
	GenNum int
}

func EqualPDFValue(a, b PDFValue) bool {
	if a == nil || b == nil {
		return a == b
	}

	switch va := a.(type) {

	case PDFHexString:
		vb, ok := b.(PDFHexString)
		return ok && va.Value == vb.Value

	case PDFString:
		vb, ok := b.(PDFString)
		return ok && va.Value == vb.Value

	case PDFInteger:
		vb, ok := b.(PDFInteger)
		return ok && va == vb

	case PDFReal:
		vb, ok := b.(PDFReal)
		return ok && va == vb

	case PDFBoolean:
		vb, ok := b.(PDFBoolean)
		return ok && va == vb

	case PDFName:
		vb, ok := b.(PDFName)
		return ok && va.Value == vb.Value

	case PDFRef:
		vb, ok := b.(PDFRef)
		return ok &&
			va.ObjNum == vb.ObjNum &&
			va.GenNum == vb.GenNum

	case PDFArray:
		vb, ok := b.(PDFArray)
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !EqualPDFValue(va[i], vb[i]) {
				return false
			}
		}
		return true

	case PDFDict:
		vb, ok := b.(PDFDict)
		if !ok || va.Entries.Len() != vb.Entries.Len() || va.HasStream != vb.HasStream {
			return false
		}
		for k, vaVal := range va.Entries.All() {
			vbVal, ok := vb.Entries.Lookup(k)
			if !ok {
				return false
			}
			if !EqualPDFValue(vaVal, vbVal) {
				return false
			}
		}
		return true

	default:
		return false
	}
}
