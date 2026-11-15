package convert

import (
	"regexp"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// languageTagRe matches the syntax of an RFC 3066 language tag: a primary
// subtag of one to eight letters, then subtags of one to eight letters or
// digits.
var languageTagRe = regexp.MustCompile(`^[A-Za-z]{1,8}(-[A-Za-z0-9]{1,8})*$`)

// dropInvalidLang removes the /Lang entry of d when it is a string that does
// not match languageTagRe. An empty string is kept, since ISO 32000-1 14.9.2.1
// defines it as an unknown language.
func dropInvalidLang(d pdf.PDFDict) {
	s, ok := d.Entries.Get("Lang").(pdf.PDFString)
	if !ok || s.Value == "" || languageTagRe.MatchString(s.Value) {
		return
	}
	d.Entries.Del("Lang")
}
