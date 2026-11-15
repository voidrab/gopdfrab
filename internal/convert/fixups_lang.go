package convert

import (
	"regexp"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// languageTagRe matches the syntax of an RFC 3066 language tag: a primary
// subtag of one to eight letters, then subtags of one to eight letters or
// digits. BCP 47 tags, which RFC 3066 tags are a subset of, share the syntax.
var languageTagRe = regexp.MustCompile(`^[A-Za-z]{1,8}(-[A-Za-z0-9]{1,8})*$`)

// dropInvalidLang removes a /Lang entry of d that is not a language tag. A
// tag that is not one names no language, so dropping it loses nothing; an
// empty string, which ISO 32000-1 14.9.2.1 defines as an unknown language, is
// kept.
func dropInvalidLang(d pdf.PDFDict) {
	s, ok := d.Entries.Get("Lang").(pdf.PDFString)
	if !ok || s.Value == "" || languageTagRe.MatchString(s.Value) {
		return
	}
	d.Entries.Del("Lang")
}
