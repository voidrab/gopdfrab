package pdfrab

type PDFValue interface{}

type PDFHexString struct{ Value string }
type PDFString struct{ Value string }
type PDFInteger int
type PDFReal float32
type PDFBoolean bool
type PDFName struct{ Value string }
type PDFArray []PDFValue
type PDFDict map[string]PDFValue
type PDFStreamDict = PDFDict
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
		if !ok || len(va) != len(vb) {
			return false
		}
		for k, vaVal := range va {
			vbVal, ok := vb[k]
			if !ok {
				return false
			}
			if !EqualPDFValue(vaVal, vbVal) {
				return false
			}
		}
		return true

	default:
		// Unknown or unsupported PDFValue
		return false
	}
}
