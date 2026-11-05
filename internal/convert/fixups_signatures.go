package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// signaturePerms are the catalog Perms entries that hold signature
// dictionaries.
var signaturePerms = []string{"DocMDP", "UR", "UR3"}

// dropSignatures removes the digital signatures of trailer's document when p
// checks that a signature's ByteRange covers its revision (PDF/A-2 6.4.3).
// Converting rewrites the file, so no ByteRange can cover the output, and the
// signature would not verify in the output either. Each signature field loses
// its value and stays as an unsigned field, its widget untouched; the
// signatures of the catalog's Perms go too. Each signature removed is reported
// once in lost, also when both a field and Perms hold it.
//
// It runs before the fix loop, whose verify reads the input's bytes, where
// the signatures are still intact.
func dropSignatures(trailer pdf.PDFDict, p *pdf.Profile, lost *[]pdf.PDFError) {
	if !p.Has(pdf.Checks.Form.SignatureByteRange) {
		return
	}
	const msg = "digital signature removed: converting the file invalidates it"
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	dropped := map[uintptr]bool{}
	for _, f := range verify.SignatureFields(root) {
		if v, ok := f.Entries.Lookup("V"); ok && v != nil {
			if sig, ok := v.(pdf.PDFDict); ok {
				dropped[pdf.ValuePointer(sig.Entries)] = true
			}
			f.Entries.Del("V")
			reportLost(lost, pdf.Checks.Form.SignatureByteRange, f, msg)
		}
	}
	perms, ok := root.Entries.Get("Perms").(pdf.PDFDict)
	if !ok || perms.Entries == nil {
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
