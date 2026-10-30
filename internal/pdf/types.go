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

// PDFDict is a dictionary or a stream. Its body, Entries, holds the entries,
// the object's own reference and the stream, so every copy of a PDFDict sees
// every edit made through another.
type PDFDict struct {
	Entries Dict
}

func NewPDFDict() PDFDict {
	return PDFDict{Entries: NewDict()}
}

// NewStreamDict makes e a stream holding raw, its undecoded bytes, and returns
// it as a PDFDict. A nil e starts an empty dictionary. Every other copy of e
// becomes the stream as well.
func NewStreamDict(e Dict, raw []byte) PDFDict {
	if e == nil {
		e = NewDict()
	}
	d := PDFDict{Entries: e}
	d.SetStream(raw)
	return d
}

// IsStream reports whether the dictionary is a stream's. Safe on the zero
// PDFDict.
func (d PDFDict) IsStream() bool {
	return d.Entries != nil && d.Entries.isStream
}

// Stream returns the stream's undecoded bytes, nil for a plain dictionary. The
// slice may alias a read-only memory map: never write to it. Safe on the zero
// PDFDict.
func (d PDFDict) Stream() []byte {
	if d.Entries == nil {
		return nil
	}
	return d.Entries.stream
}

// SetStream makes the dictionary a stream holding raw, its undecoded bytes. The
// stream lives in the shared body, so every copy of d sees it. SetStream
// panics on a nil Entries, as Dict.Set does; NewStreamDict starts from
// nothing.
func (d PDFDict) SetStream(raw []byte) {
	if d.Entries == nil {
		panic("pdf: SetStream on a nil Dict")
	}
	d.Entries.stream, d.Entries.isStream = raw, true
}

// Ref returns the reference the dictionary was read or numbered under. The
// reader sets it on every indirect object, which marks the dictionary as one
// for the writer and fixers; ok is false on a direct dictionary. Safe on the
// zero PDFDict.
func (d PDFDict) Ref() (ref PDFRef, ok bool) {
	if d.Entries == nil {
		return PDFRef{}, false
	}
	return d.Entries.ref, d.Entries.hasRef
}

// SetRef records r as the dictionary's own reference. The reference lives in
// the shared body, so every copy of d sees it. SetRef panics on a nil Entries,
// as Dict.Set does.
func (d PDFDict) SetRef(r PDFRef) {
	if d.Entries == nil {
		panic("pdf: SetRef on a nil Dict")
	}
	d.Entries.ref, d.Entries.hasRef = r, true
}

// PDFRef is an indirect object reference: an object number and a generation
// number.
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
		if !ok || va.Entries.Len() != vb.Entries.Len() || va.IsStream() != vb.IsStream() {
			return false
		}
		ra, oka := va.Ref()
		rb, okb := vb.Ref()
		if oka != okb || ra != rb {
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
