package pdf

// HasFileIdentifier reports whether a trailer ID value is an array with at
// least one string element that holds bytes.
func HasFileIdentifier(id PDFValue) bool {
	arr, ok := id.(PDFArray)
	if !ok {
		return false
	}
	for _, v := range arr {
		if len(stringBytes(v)) > 0 {
			return true
		}
	}
	return false
}
