package verify

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// signedDoc describes a synthetic signed document for buildSignedPDF.
type signedDoc struct {
	signers    int  // SignerInfo structures in the PKCS#7 object
	noCert     bool // leave the certificates field out
	badCert    bool // put a malformed certificate in it
	indefinite bool // encode the outer ContentInfo with indefinite lengths
	shiftRange int64
	update     bool // append an incremental update after the signed revision
	contentRef bool // hold the Contents string in an object of its own
}

// testCertificate is a self-signed certificate, created once.
var testCertificate = func() *x509.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "gopdfrab test signer"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1<<31, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}()

// algorithmIdentifier is an AlgorithmIdentifier with no parameters.
type algorithmIdentifier struct {
	Algorithm asn1.ObjectIdentifier
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type signerInfo struct {
	Version         int
	IssuerAndSerial issuerAndSerial
	DigestAlgorithm algorithmIdentifier
	SignatureAlg    algorithmIdentifier
	Signature       []byte
}

// pkcs7SignedData assembles a ContentInfo holding SignedData as doc asks. The
// signature values are dummies: 6.4.3 does not ask whether they verify.
func pkcs7SignedData(doc signedDoc) []byte {
	sha256 := asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	ecdsaSHA256 := asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	mustMarshal := func(v any, params string) []byte {
		b, err := asn1.MarshalWithParams(v, params)
		if err != nil {
			panic(err)
		}
		return b
	}
	tlv := func(tag byte, content ...[]byte) []byte {
		body := bytes.Join(content, nil)
		if doc.indefinite && tag&0x20 != 0 {
			return append(append([]byte{tag, 0x80}, body...), 0, 0)
		}
		b := mustMarshal(asn1.RawValue{FullBytes: nil, Class: int(tag >> 6), Tag: int(tag & 0x1f),
			IsCompound: tag&0x20 != 0, Bytes: body}, "")
		return b
	}

	var signers [][]byte
	for range doc.signers {
		signers = append(signers, mustMarshal(signerInfo{
			Version:         1,
			IssuerAndSerial: issuerAndSerial{asn1.RawValue{FullBytes: testCertificate.RawIssuer}, testCertificate.SerialNumber},
			DigestAlgorithm: algorithmIdentifier{sha256},
			SignatureAlg:    algorithmIdentifier{ecdsaSHA256},
			Signature:       []byte{1, 2, 3, 4},
		}, ""))
	}
	fields := [][]byte{
		mustMarshal(1, ""),
		tlv(0x31, mustMarshal(algorithmIdentifier{sha256}, "")),
		tlv(0x30, mustMarshal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}, "")),
	}
	switch {
	case doc.badCert:
		fields = append(fields, tlv(0xa0, tlv(0x30, mustMarshal(3, ""))))
	case !doc.noCert:
		fields = append(fields, tlv(0xa0, testCertificate.Raw))
	}
	fields = append(fields, tlv(0x31, signers...))
	return tlv(0x30,
		mustMarshal(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}, ""),
		tlv(0xa0, tlv(0x30, fields...)))
}

// buildSignedPDF returns a one-page document with one signed signature field
// whose ByteRange covers the signed revision, then shifted by doc.shiftRange.
func buildSignedPDF(doc signedDoc) []byte {
	const placeholder = "[0 0000000000 0000000000 0000000000]"
	contents := "<" + hex.EncodeToString(pkcs7SignedData(doc)) + "0000>"
	b := pdfgen.NewBuilder("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 >> >>")
	b.Obj(2, "<< /Type /Pages /Count 1 /Kids [3 0 R] >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Annots [4 0 R] >>")
	b.Obj(4, "<< /FT /Sig /T (Sig1) /Type /Annot /Subtype /Widget /F 132 /Rect [0 0 0 0] /P 3 0 R /V 5 0 R >>")
	if doc.contentRef {
		b.Obj(5, "<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached /ByteRange "+placeholder+" /Contents 6 0 R >>")
		b.Obj(6, contents)
	} else {
		b.Obj(5, "<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached /ByteRange "+placeholder+" /Contents "+contents+" >>")
	}
	size := 6
	if doc.contentRef {
		size = 7
	}
	data := bytes.Clone(b.FinishClassic(fmt.Sprintf("<< /Size %d /Root 1 0 R >>", size)))
	data = append(data, '\n')

	start := int64(bytes.Index(data, []byte(contents)))
	end := start + int64(len(contents))
	br := fmt.Sprintf("[0 %010d %010d %010d]", start, end, int64(len(data))-end+doc.shiftRange)
	data = bytes.Replace(data, []byte(placeholder), []byte(br), 1)

	if doc.update {
		xref := len(data)
		data = fmt.Appendf(data, "xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size %d /Root 1 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n",
			size, bytes.LastIndex(data, []byte("\nxref\n"))+1, xref)
	}
	return data
}

func TestSignatureChecks(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  signedDoc
		want []pdf.Check
	}{
		{"valid", signedDoc{signers: 1}, nil},
		{"indefinite lengths", signedDoc{signers: 1, indefinite: true}, nil},
		{"incremental update", signedDoc{signers: 1, update: true}, nil},
		// veraPDF cannot follow a Contents reference.
		{"contents by reference", signedDoc{signers: 1, contentRef: true},
			[]pdf.Check{pdf.Checks.Form.SignatureByteRange}},
		// The file ends with the EOL after %%EOF, so the range must include it.
		{"range one byte short", signedDoc{signers: 1, shiftRange: -1},
			[]pdf.Check{pdf.Checks.Form.SignatureByteRange}},
		{"range one byte long", signedDoc{signers: 1, shiftRange: 1},
			[]pdf.Check{pdf.Checks.Form.SignatureByteRange}},
		{"no certificate", signedDoc{signers: 1, noCert: true},
			[]pdf.Check{pdf.Checks.Form.SignatureCertificate}},
		{"malformed certificate", signedDoc{signers: 1, badCert: true},
			[]pdf.Check{pdf.Checks.Form.SignatureCertificate, pdf.Checks.Form.SignatureSigners}},
		{"two signers", signedDoc{signers: 2}, []pdf.Check{pdf.Checks.Form.SignatureSigners}},
		{"no signers", signedDoc{signers: 0}, []pdf.Check{pdf.Checks.Form.SignatureSigners}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := pdf.OpenBytes(buildSignedPDF(tc.doc))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			var got []pdf.Check
			for _, e := range signatureRule(r, nil, nil) {
				got = append(got, e.Check())
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("reported %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSignatureFieldsWalksTheFieldTree(t *testing.T) {
	sig := pdf.NewPDFDict()
	leaf := pdf.NewPDFDict() // inherits FT Sig
	leaf.Entries.Set("V", sig)
	text := pdf.NewPDFDict()
	text.Entries.Set("FT", pdf.PDFName{Value: "Tx"})
	parent := pdf.NewPDFDict()
	parent.Entries.Set("FT", pdf.PDFName{Value: "Sig"})
	parent.Entries.Set("Kids", pdf.PDFArray{leaf, leaf, text, pdf.PDFInteger(1)})
	form := pdf.NewPDFDict()
	form.Entries.Set("Fields", pdf.PDFArray{parent})
	root := pdf.NewPDFDict()
	root.Entries.Set("AcroForm", form)

	if got := SignatureFields(root); len(got) != 2 {
		t.Errorf("SignatureFields found %d fields, want the parent and the leaf once", len(got))
	}
	if got := signatureDicts(root); len(got) != 1 {
		t.Errorf("signatureDicts found %d signatures, want 1", len(got))
	}
}

func TestCheckSignatureMalformedEntries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		byteRange pdf.PDFValue
	}{
		{"three elements", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(2)}},
		{"real element", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(2), pdf.PDFReal(3)}},
	} {
		sig := pdf.NewPDFDict()
		sig.Entries.Set("ByteRange", tc.byteRange)
		errs := checkSignature(nil, sig, nil)
		if len(errs) != 1 || errs[0].Check() != pdf.Checks.Form.SignatureByteRange {
			t.Errorf("%s: reported %v, want only SignatureByteRange (no Contents to read)", tc.name, errs)
		}
	}
}

// sigRevisionDoc returns a document in which object 2 is sigBody and tail
// follows the final %%EOF. It also returns the ByteRange that covers the
// document's single revision, where the Contents string is the first "<abcd>"
// in sigBody.
func sigRevisionDoc(t *testing.T, sigBody, tail string) (*pdf.Reader, [4]int64) {
	t.Helper()
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 3 0 R >>")
	b.Obj(2, sigBody)
	b.Obj(3, "<< /Type /Pages /Count 0 /Kids [] >>")
	data := append(bytes.Clone(b.FinishClassic("<< /Size 4 /Root 1 0 R >>")), tail...)
	start := int64(bytes.Index(data, []byte("<abcd>")))
	end := start + int64(len("<abcd>"))
	eof := int64(bytes.LastIndex(data, []byte("%%EOF"))) + int64(len("%%EOF"))
	r, err := pdf.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, [4]int64{0, start, end, eof - end}
}

func TestByteRangeCoversRevision(t *testing.T) {
	for _, tc := range []struct {
		name, body, tail string
		shift            int64 // added to the last ByteRange element
		want             bool
	}{
		{"plain", "<< /Contents <abcd> >>", "", 0, true},
		{"every value kind before Contents",
			"<< /A [1 2] /B << /C 1 >> /D 1 0 R /E 1 /F 1.5 /G true /H (s) /I <00> /J /N /K null /Contents <abcd> >>",
			"", 0, true},
		{"literal Contents", "<< /Contents (abcd) >>", "", 0, false},
		{"Contents too short", "<< /Contents <abcd> >>", "", -1, false},
		{"LF after %%EOF", "<< /Contents <abcd> >>", "\n", 1, true},
		{"LF left out", "<< /Contents <abcd> >>", "\n", 0, false},
		{"CR LF after %%EOF", "<< /Contents <abcd> >>", "\r\n", 2, true},
		{"CR after %%EOF", "<< /Contents <abcd> >>", "\r", 1, true},
		{"CR LF partly left out before more bytes", "<< /Contents <abcd> >>", "\r\njunk", 1, true},
		{"too long before more bytes", "<< /Contents <abcd> >>", "\njunk", 2, false},
		{"Contents by reference", "<< /Contents 4 0 R >>", "", 0, false},
		{"not a dictionary", "42", "", 0, false},
		{"key not a name", "<< 1 /Contents <abcd> >>", "", 0, false},
		{"no Contents", "<< /A 1 >>", "", 0, false},
		{"bad array value", "<< /A [ /Contents <abcd> >>", "", 0, false},
		{"bad dictionary value", "<< /A << 1 >> /Contents <abcd> >>", "", 0, false},
		{"keyword value", "<< /A foo /Contents <abcd> >>", "", 0, true},
		{"stray delimiter", "<< /A ] /Contents <abcd> >>", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, br := sigRevisionDoc(t, tc.body, tc.tail)
			br[3] += tc.shift
			if got := byteRangeCoversRevision(r, pdf.PDFRef{ObjNum: 2}, br, r.XRefSectionOffsets()); got != tc.want {
				t.Errorf("byteRangeCoversRevision(%v) = %v, want %v", br, got, tc.want)
			}
		})
	}
}

func TestByteRangeRejectsNonZeroStart(t *testing.T) {
	r, br := sigRevisionDoc(t, "<< /Contents <abcd> >>", "")
	br[0] = 1
	if byteRangeCoversRevision(r, pdf.PDFRef{ObjNum: 2}, br, r.XRefSectionOffsets()) {
		t.Error("a range not starting at 0 covers its revision")
	}
}

func TestNextXRefSection(t *testing.T) {
	for _, tc := range []struct {
		sections      []int64
		pdfStart, off int64
		want          int64
	}{
		{nil, 0, 10, 10},
		{[]int64{5, 40, 20}, 0, 10, 20},
		{[]int64{5, 40, 20}, 3, 30, 43},
		{[]int64{5}, 0, 10, 10},
	} {
		if got := nextXRefSection(tc.sections, tc.pdfStart, tc.off); got != tc.want {
			t.Errorf("nextXRefSection(%v, %d, %d) = %d, want %d", tc.sections, tc.pdfStart, tc.off, got, tc.want)
		}
	}
}
