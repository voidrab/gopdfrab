package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestDropInvalidLang verifies that a string /Lang that is not a language tag
// is removed, while a language tag, the empty (unknown language) string and a
// non-string value are kept.
func TestDropInvalidLang(t *testing.T) {
	cases := []struct {
		lang pdf.PDFValue
		keep bool
	}{
		{pdf.PDFString{Value: "en-GB"}, true},
		{pdf.PDFString{Value: "x-klingon"}, true},
		{pdf.PDFString{Value: "zh-Hant-TW"}, true},
		{pdf.PDFString{Value: ""}, true},
		{pdf.PDFString{Value: "\xfe\x10\x8f\xcd\xb4"}, false},
		{pdf.PDFString{Value: "en_GB"}, false},
		{pdf.PDFString{Value: "englishlanguage"}, false},
		{pdf.PDFName{Value: "en"}, true}, // not a string: a type check's concern
	}
	for _, tc := range cases {
		d := pdf.NewPDFDict()
		d.Entries.Set("Lang", tc.lang)
		dropInvalidLang(d)
		if got := d.Entries.Get("Lang") != nil; got != tc.keep {
			t.Errorf("Lang %q kept = %v, want %v", tc.lang, got, tc.keep)
		}
	}
}
