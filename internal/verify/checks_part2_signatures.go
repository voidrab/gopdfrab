package verify

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// signatureRule checks the digital signature of every signature field
// (PDF/A-2 6.4.3): its ByteRange must cover the revision it signs, less the
// signature value, and its PKCS#7 object must carry the signer's certificate
// and exactly one signer.
func signatureRule(d *pdf.Reader, _ pdf.PDFValue, _ *ValidationContext) []pdf.PDFError {
	sigs := signatureDicts(catalog(d))
	if len(sigs) == 0 {
		return nil
	}
	sections := d.XRefSectionOffsets()
	var errs []pdf.PDFError
	for _, sig := range sigs {
		errs = append(errs, checkSignature(d, sig, sections)...)
	}
	return errs
}

// checkSignature checks one signature dictionary. sections holds the file's
// cross-reference sections.
func checkSignature(d *pdf.Reader, sig pdf.PDFDict, sections []int64) []pdf.PDFError {
	ref := refOf(sig)
	report := func(c pdf.Check, format string, args ...any) pdf.PDFError {
		return pdf.NewError(c, []error{fmt.Errorf(format, args...)}, 0, ref)
	}
	var errs []pdf.PDFError
	if br, ok := byteRangeOf(sig); !ok || ref == nil || !d.ByteRangeCoversRevision(*ref, br, sections) {
		errs = append(errs, report(pdf.Checks.Form.SignatureByteRange,
			"signature ByteRange does not cover the entire file except the signature value"))
	}
	contents, ok := pdf.StringBytes(sig.Entries.Get("Contents"))
	if !ok {
		return errs
	}
	sd := readSignedData(contents)
	if !sd.certificates {
		errs = append(errs, report(pdf.Checks.Form.SignatureCertificate,
			"signature PKCS#7 object does not include the signer's X.509 certificate"))
	}
	if sd.signers != 1 {
		errs = append(errs, report(pdf.Checks.Form.SignatureSigners,
			"signature PKCS#7 object has %d signers instead of one", sd.signers))
	}
	return errs
}

// byteRangeOf returns a signature dictionary's ByteRange, which must be an
// array of four integers.
func byteRangeOf(sig pdf.PDFDict) (br [4]int64, ok bool) {
	arr, ok := sig.Entries.Get("ByteRange").(pdf.PDFArray)
	if !ok || len(arr) != 4 {
		return br, false
	}
	for i, v := range arr {
		n, ok := v.(pdf.PDFInteger)
		if !ok {
			return br, false
		}
		br[i] = int64(n)
	}
	return br, true
}

// SignatureFields returns the signature fields of the interactive form of
// root, the document catalog: the fields of its field tree whose field type,
// inherited or not, is Sig.
func SignatureFields(root pdf.PDFValue) []pdf.PDFDict {
	r, _ := root.(pdf.PDFDict)
	form, _ := r.Entries.Get("AcroForm").(pdf.PDFDict)
	fields, _ := form.Entries.Get("Fields").(pdf.PDFArray)
	var out []pdf.PDFDict
	seen := map[uintptr]bool{}
	var walk func(kids pdf.PDFArray, ft pdf.PDFValue)
	walk = func(kids pdf.PDFArray, ft pdf.PDFValue) {
		for _, k := range kids {
			f, ok := k.(pdf.PDFDict)
			if !ok || f.Entries == nil || seen[pdf.ValuePointer(f.Entries)] {
				continue
			}
			seen[pdf.ValuePointer(f.Entries)] = true
			fieldFT := ft
			if v := f.Entries.Get("FT"); v != nil {
				fieldFT = v
			}
			if fieldFT == (pdf.PDFName{Value: "Sig"}) {
				out = append(out, f)
			}
			if sub, ok := f.Entries.Get("Kids").(pdf.PDFArray); ok {
				walk(sub, fieldFT)
			}
		}
	}
	walk(fields, nil)
	return out
}

// signatureDicts returns the signature dictionaries, the V entries, of the
// signature fields of root.
func signatureDicts(root pdf.PDFValue) []pdf.PDFDict {
	var out []pdf.PDFDict
	for _, f := range SignatureFields(root) {
		if v, ok := f.Entries.Get("V").(pdf.PDFDict); ok && v.Entries != nil {
			out = append(out, v)
		}
	}
	return out
}
