package pdf

// HasFileIdentifier reports whether a trailer ID value is an array with at
// least one non-empty string element.
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
