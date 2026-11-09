package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// signaturePerms are the catalog Perms entries that hold signature
// dictionaries.
var signaturePerms = []string{"DocMDP", "UR", "UR3"}

// dropSignatures removes the digital signatures of trailer's document.
// Converting rewrites the file, so no signature's ByteRange can cover the
// output and none would verify there, at any level. Each signature field loses
// its value and stays as an unsigned field, its widget untouched; the
// signatures of the catalog's Perms go too. Each signature removed is reported
// once in lost, also when both a field and Perms hold it. Under a level without
// the check, such as PDF/A-1b, the record has no clause.
//
// It runs before the fix loop, so the loop verifies the graph without the
// removed signatures. It is not a pre-emptive repair: those steps do not
// receive lost, which this function needs.
func dropSignatures(trailer pdf.PDFDict, lost *[]pdf.PDFError) {
	const msg = "digital signature removed: converting the file invalidates it"
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	dropped := map[uintptr]bool{}
	for _, f := range verify.SignatureFields(root) {
		if v := f.Entries.Get("V"); v != nil {
			if sig, ok := v.(pdf.PDFDict); ok {
				dropped[pdf.ValuePointer(sig.Entries)] = true
			}
			f.Entries.Del("V")
			reportLost(lost, pdf.Checks.Form.SignatureByteRange, f, msg)
		}
	}
	perms, ok := root.Entries.Get("Perms").(pdf.PDFDict)
	if !ok {
		return
	}
	for _, k := range signaturePerms {
		if sig, ok := perms.Entries.Get(k).(pdf.PDFDict); ok {
			perms.Entries.Del(k)
			if ptr := pdf.ValuePointer(sig.Entries); !dropped[ptr] {
				dropped[ptr] = true
				reportLost(lost, pdf.Checks.Form.SignatureByteRange, sig, msg)
			}
		}
	}
	if perms.Entries.Len() == 0 {
		root.Entries.Del("Perms")
	}
}
