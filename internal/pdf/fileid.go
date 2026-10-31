package pdf

// HasFileIdentifier reports whether a trailer ID value is an array whose
// string elements hold at least one byte between them, the least a file
// identifier must be.
func HasFileIdentifier(id PDFValue) bool {
	arr, ok := id.(PDFArray)
	if !ok {
		return false
	}
	for _, v := range arr {
		switch s := v.(type) {
		case PDFString:
			if len(s.Value) > 0 {
				return true
			}
		case PDFHexString:
			if len(s.Value) > 0 {
				return true
			}
		}
	}
	return false
}
