package pdf

import "testing"

func TestHasFileIdentifier(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   PDFValue
		want bool
	}{
		{"nil", nil, false},
		{"not an array", PDFString{Value: "id"}, false},
		{"empty array", PDFArray{}, false},
		{"empty strings", PDFArray{PDFHexString{}, PDFString{}}, false},
		{"no strings", PDFArray{PDFInteger(1)}, false},
		{"hex bytes", PDFArray{PDFHexString{Value: "AB"}, PDFHexString{Value: "AB"}}, true},
		{"literal bytes in the second string", PDFArray{PDFString{}, PDFString{Value: "x"}}, true},
	} {
		if got := HasFileIdentifier(tc.id); got != tc.want {
			t.Errorf("%s: HasFileIdentifier = %v, want %v", tc.name, got, tc.want)
		}
	}
}
