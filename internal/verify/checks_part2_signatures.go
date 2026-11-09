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
	if br, ok := byteRangeOf(sig); !ok || ref == nil || !byteRangeCoversRevision(d, *ref, br, sections) {
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

// byteRangeCoversRevision reports whether byteRange, the ByteRange of the
// signature dictionary that is object sig, covers the revision that dictionary
// was written in, apart from the signature value itself.
//
// The expected range is derived from the file's bytes, as veraPDF does: it
// starts at offset 0, skips exactly the signature dictionary's Contents
// string, and ends after the first %%EOF that follows the next
// cross-reference section (or the Contents string, when no section follows),
// plus at most one EOL marker unless that marker ends the file. A signature
// dictionary whose Contents cannot be located covers nothing. sections holds
// the file's cross-reference sections, as pdf.Reader.XRefSectionOffsets returns
// them.
func byteRangeCoversRevision(d *pdf.Reader, sig pdf.PDFRef, byteRange [4]int64, sections []int64) bool {
	start, end, ok := d.ContentsSpan(sig)
	if !ok || byteRange[0] != 0 || byteRange[1] != start || byteRange[2] != end {
		return false
	}
	eof, slack := d.RevisionEnd(nextXRefSection(sections, d.PDFStart(), end))
	want := eof - end
	return byteRange[3] >= want-slack && byteRange[3] <= want
}

// nextXRefSection returns the smallest of sections, each shifted by pdfStart,
// that lies after off, or off when none does.
func nextXRefSection(sections []int64, pdfStart, off int64) int64 {
	next := off
	for _, o := range sections {
		if o += pdfStart; o > off && (next == off || o < next) {
			next = o
		}
	}
	return next
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
	var out []pdf.PDFDict
	seen := map[uintptr]bool{}
	var walk func(kids []pdf.PDFDict, ft pdf.PDFValue)
	walk = func(kids []pdf.PDFDict, ft pdf.PDFValue) {
		for _, f := range kids {
			if seen[pdf.ValuePointer(f.Entries)] {
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
			walk(dictsOf(f.Entries.Get("Kids")), fieldFT)
		}
	}
	walk(dictsOf(form.Entries.Get("Fields")), nil)
	return out
}

// signatureDicts returns the signature dictionaries, the V entries, of the
// signature fields of root.
func signatureDicts(root pdf.PDFValue) []pdf.PDFDict {
	var out []pdf.PDFDict
	for _, f := range SignatureFields(root) {
		if v, ok := f.Entries.Get("V").(pdf.PDFDict); ok {
			out = append(out, v)
		}
	}
	return out
}
