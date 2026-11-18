package gopdfrab_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/internal/cms/cmstest"
)

// sigPDF writes PDF files revision by revision, with classic
// cross-reference tables, and signs signature dictionaries in them.
type sigPDF struct {
	buf      []byte
	offsets  map[int]int
	lastXref int
	size     int
	// trailer is added to each later trailer, such as an Info entry.
	trailer string
}

// contentsLen is the hex digits reserved for a signature value.
const contentsLen = 2 * 12000

func newSigPDF() *sigPDF {
	return &sigPDF{buf: []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n"), offsets: map[int]int{}, lastXref: -1}
}

// revision appends objects as a revision of the file: the whole file the
// first time, an incremental update after that.
func (p *sigPDF) revision(objs map[int]string) {
	nums := make([]int, 0, len(objs))
	for n := range objs {
		nums = append(nums, n)
	}
	slices.Sort(nums)
	for _, n := range nums {
		p.offsets[n] = len(p.buf)
		p.buf = fmt.Appendf(p.buf, "%d 0 obj\n%s\nendobj\n", n, objs[n])
		p.size = max(p.size, n+1)
	}
	xref := len(p.buf)
	p.buf = append(p.buf, "xref\n"...)
	if p.lastXref < 0 {
		p.buf = fmt.Appendf(p.buf, "0 %d\n0000000000 65535 f \n", p.size)
		for n := 1; n < p.size; n++ {
			p.buf = fmt.Appendf(p.buf, "%010d 00000 n \n", p.offsets[n])
		}
	} else {
		for _, n := range nums {
			p.buf = fmt.Appendf(p.buf, "%d 1\n%010d 00000 n \n", n, p.offsets[n])
		}
	}
	prev := ""
	if p.lastXref >= 0 {
		prev = fmt.Sprintf(" /Prev %d", p.lastXref)
	}
	p.buf = fmt.Appendf(p.buf, "trailer\n<< /Size %d /Root 1 0 R%s%s >>\nstartxref\n%d\n%%%%EOF\n", p.size, prev, p.trailer, xref)
	p.lastXref = xref
}

// objStmRevision appends an incremental update that keeps objs in an object
// stream, numbered stm, and indexes them, and itself, in a cross-reference
// stream numbered xrefNum, as writers that compress objects do. Objects in
// direct, which may hold streams, are written uncompressed before it.
func (p *sigPDF) objStmRevision(stm, xrefNum int, objs, direct map[int]string) {
	sorted := func(m map[int]string) []int {
		nums := make([]int, 0, len(m))
		for n := range m {
			nums = append(nums, n)
		}
		slices.Sort(nums)
		return nums
	}
	for _, n := range sorted(direct) {
		p.offsets[n] = len(p.buf)
		p.buf = fmt.Appendf(p.buf, "%d 0 obj\n%s\nendobj\n", n, direct[n])
		p.size = max(p.size, n+1)
	}
	var header, body []byte
	index := map[int]int{}
	for i, n := range sorted(objs) {
		header = fmt.Appendf(header, "%d %d ", n, len(body))
		body = fmt.Appendf(body, "%s\n", objs[n])
		index[n] = i
		p.size = max(p.size, n+1)
	}
	data := append(header, body...)
	p.offsets[stm] = len(p.buf)
	p.buf = fmt.Appendf(p.buf, "%d 0 obj\n<< /Type /ObjStm /N %d /First %d /Length %d >>\nstream\n%s\nendstream\nendobj\n",
		stm, len(objs), len(header), len(data), data)
	p.size = max(p.size, stm+1, xrefNum+1)
	xref := len(p.buf)
	p.offsets[xrefNum] = xref
	var rows []byte
	var idx []byte
	entry := func(typ byte, f2, f3 int) {
		rows = append(rows, typ, byte(f2>>24), byte(f2>>16), byte(f2>>8), byte(f2), byte(f3>>8), byte(f3))
	}
	all := map[int]bool{stm: true, xrefNum: true}
	for n := range objs {
		all[n] = true
	}
	for n := range direct {
		all[n] = true
	}
	nums := make([]int, 0, len(all))
	for n := range all {
		nums = append(nums, n)
	}
	slices.Sort(nums)
	for _, n := range nums {
		idx = fmt.Appendf(idx, "%d 1 ", n)
		if i, ok := index[n]; ok {
			entry(2, stm, i)
		} else {
			entry(1, p.offsets[n], 0)
		}
	}
	p.buf = fmt.Appendf(p.buf, "%d 0 obj\n<< /Type /XRef /Size %d /Root 1 0 R /Prev %d /W [1 4 2] /Index [%s] /Length %d >>\nstream\n%s\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n",
		xrefNum, p.size, p.lastXref, strings.TrimSpace(string(idx)), len(rows), rows, xref)
	p.lastXref = xref
}

// sigDict returns a signature dictionary with a placeholder for its value,
// to sign with sign.
func sigDict(subFilter, extra string) string {
	return fmt.Sprintf("<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /%s /Name (Alice) /Reason (Approved) "+
		"/Location (Vienna) /M (D:20260304050607+01'00') %s/ByteRange [0 %10d %10d %10d] /Contents <%s> >>",
		subFilter, extra, 0, 0, 0, strings.Repeat("0", contentsLen))
}

// sign fills in the last placeholder of the file: its byte range, up to the
// end of the file, and the value contents returns for the bytes it covers.
func (p *sigPDF) sign(t *testing.T, contents func(data []byte) []byte) {
	t.Helper()
	at := bytes.LastIndex(p.buf, []byte("/ByteRange [0 "))
	cs := bytes.Index(p.buf[at:], []byte("/Contents <")) + at + len("/Contents ")
	ce := bytes.IndexByte(p.buf[cs:], '>') + cs + 1
	br := fmt.Sprintf("/ByteRange [0 %10d %10d %10d]", cs, ce, len(p.buf)-ce)
	copy(p.buf[at:], br)
	data := append(append([]byte{}, p.buf[:cs]...), p.buf[ce:]...)
	value := hex.EncodeToString(contents(data))
	if len(value) > contentsLen {
		t.Fatalf("signature value of %d digits does not fit", len(value))
	}
	copy(p.buf[cs+1:], value)
}

// The objects of a one-page document with one signature field, whose value is
// object 5.
func baseObjects(sig string) map[int]string {
	return map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 >> >>",
		2: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots [4 0 R] >>",
		4: "<< /FT /Sig /T (Approval) /V 5 0 R /Type /Annot /Subtype /Widget /Rect [10 20 110 70] /P 3 0 R /F 4 >>",
		5: sig,
		6: "<< /Length 0 >>\nstream\n\nendstream",
	}
}

var (
	testRoot = cmstest.Root("Test Root")
	alice    = testRoot.Issue("Alice", cmstest.IssueOptions{})
	tsa      = testRoot.Issue("Test TSA", cmstest.IssueOptions{EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping}})
	signedAt = time.Date(2026, 3, 4, 4, 6, 7, 0, time.UTC)
)

func trusted() gopdfrab.SignatureOptions {
	pool := x509.NewCertPool()
	pool.AddCert(testRoot.Cert)
	return gopdfrab.SignatureOptions{Roots: pool, Now: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}
}

// detached signs as adbe.pkcs7.detached does.
func detached(id *cmstest.Identity, o cmstest.Options) func([]byte) []byte {
	return func(data []byte) []byte {
		if o.SigningTime.IsZero() {
			o.SigningTime = signedAt
		}
		return cmstest.Sign(id, data, o)
	}
}

// signedDoc returns a document signed by alice, as o says.
func signedDoc(t *testing.T, o cmstest.Options) *sigPDF {
	t.Helper()
	p := newSigPDF()
	p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
	p.sign(t, detached(alice, o))
	return p
}

func signaturesOf(t *testing.T, b []byte, o gopdfrab.SignatureOptions) []gopdfrab.Signature {
	t.Helper()
	d, err := gopdfrab.OpenBytes(b)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()
	sigs, err := d.Signatures(context.Background(), o)
	if err != nil {
		t.Fatalf("Signatures: %v", err)
	}
	return sigs
}

func onlySignature(t *testing.T, b []byte, o gopdfrab.SignatureOptions) gopdfrab.Signature {
	t.Helper()
	sigs := signaturesOf(t, b, o)
	if len(sigs) != 1 {
		t.Fatalf("%d signatures, want 1", len(sigs))
	}
	return sigs[0]
}

// wantCheck fails unless s has the check name with result r.
func wantCheck(t *testing.T, s gopdfrab.Signature, name string, r gopdfrab.SignatureResult) {
	t.Helper()
	c, ok := s.Check(name)
	if !ok {
		t.Errorf("no %q check in %+v", name, s.Checks)
		return
	}
	if c.Result != r {
		t.Errorf("%s: result %d (%s), want %d", name, c.Result, c.Detail, r)
	}
}

func TestSignatureValid(t *testing.T) {
	s := onlySignature(t, signedDoc(t, cmstest.Options{Chain: []*x509.Certificate{testRoot.Cert}}).buf, trusted())
	if s.Status != gopdfrab.SignatureValid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	if s.Field != "Approval" || s.Name != "Alice" || s.Reason != "Approved" || s.Location != "Vienna" {
		t.Errorf("fields: %+v", s)
	}
	if s.SubFilter != "adbe.pkcs7.detached" || s.DocumentTimestamp {
		t.Errorf("SubFilter %q, DocumentTimestamp %v", s.SubFilter, s.DocumentTimestamp)
	}
	if !s.Time.Equal(signedAt) || s.TimeSource != gopdfrab.SignatureTimeFromSigner {
		t.Errorf("Time %v from %q", s.Time, s.TimeSource)
	}
	if !s.CoversDocument || s.Revision != 1 || s.Revisions != 1 {
		t.Errorf("CoversDocument %v, revision %d of %d", s.CoversDocument, s.Revision, s.Revisions)
	}
	if s.Signer == nil || s.Signer.Subject.CommonName != "Alice" || len(s.Chain) != 2 {
		t.Errorf("signer %v, chain of %d", s.Signer, len(s.Chain))
	}
	// The widget's Rect [10 20 110 70] on a page 300 high.
	want := []gopdfrab.SignatureWidget{{Page: 0, Box: gopdfrab.Rect{X0: 10, Y0: 230, X1: 110, Y1: 280}}}
	if !slices.Equal(s.Widgets, want) {
		t.Errorf("Widgets = %+v, want %+v", s.Widgets, want)
	}
	for _, name := range []string{gopdfrab.SignatureCheckIntegrity, gopdfrab.SignatureCheckValue, gopdfrab.SignatureCheckTrust,
		gopdfrab.SignatureCheckValidity, gopdfrab.SignatureCheckKeyUsage, gopdfrab.SignatureCheckChanges} {
		wantCheck(t, s, name, gopdfrab.SignaturePassed)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckRevocation, gopdfrab.SignatureSkipped)
}

func TestSignatureUntrusted(t *testing.T) {
	self := cmstest.Root("Mallory")
	p := newSigPDF()
	p.revision(baseObjects(sigDict("ETSI.CAdES.detached", "")))
	p.sign(t, detached(self, cmstest.Options{}))
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureUntrusted {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckTrust, gopdfrab.SignatureFailed)
	if c, _ := s.Check(gopdfrab.SignatureCheckTrust); !strings.Contains(c.Detail, "self-signed") {
		t.Errorf("detail %q", c.Detail)
	}
}

func TestSignatureInvalid(t *testing.T) {
	t.Run("changed byte", func(t *testing.T) {
		p := signedDoc(t, cmstest.Options{})
		i := bytes.Index(p.buf, []byte("/MediaBox [0 0 200 300]"))
		p.buf[i+len("/MediaBox [0 0 2")] = '5' // 200 becomes 250
		s := onlySignature(t, p.buf, trusted())
		if s.Status != gopdfrab.SignatureInvalid {
			t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
		}
		wantCheck(t, s, gopdfrab.SignatureCheckIntegrity, gopdfrab.SignatureFailed)
	})
	t.Run("bad signature value", func(t *testing.T) {
		s := onlySignature(t, signedDoc(t, cmstest.Options{Tamper: true}).buf, trusted())
		if s.Status != gopdfrab.SignatureInvalid {
			t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
		}
		wantCheck(t, s, gopdfrab.SignatureCheckValue, gopdfrab.SignatureFailed)
	})
	t.Run("byte range leaves out more", func(t *testing.T) {
		p := signedDoc(t, cmstest.Options{})
		// Widen the gap to swallow the byte before the value.
		at := bytes.Index(p.buf, []byte("/ByteRange [0 "))
		var a, b, c int
		fmt.Sscanf(string(p.buf[at+len("/ByteRange [0 "):]), "%d %d %d", &a, &b, &c)
		copy(p.buf[at:], fmt.Sprintf("/ByteRange [0 %10d %10d %10d]", a-1, b, c))
		s := onlySignature(t, p.buf, trusted())
		if s.Status != gopdfrab.SignatureInvalid {
			t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
		}
	})
	t.Run("revoked", func(t *testing.T) {
		crl := cmstest.CRL(testRoot, signedAt.Add(-time.Hour), alice.Cert)
		s := onlySignature(t, signedDoc(t, cmstest.Options{CRLs: [][]byte{crl}}).buf, trusted())
		if s.Status != gopdfrab.SignatureInvalid {
			t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
		}
		wantCheck(t, s, gopdfrab.SignatureCheckRevocation, gopdfrab.SignatureFailed)
	})
}

func TestSignatureRevocationGood(t *testing.T) {
	ocsp := cmstest.OCSP(testRoot, alice.Cert, signedAt, time.Time{})
	s := onlySignature(t, signedDoc(t, cmstest.Options{OCSPs: [][]byte{ocsp}}).buf, trusted())
	if s.Status != gopdfrab.SignatureValid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckRevocation, gopdfrab.SignaturePassed)
}

func TestSignatureUnsupported(t *testing.T) {
	p := newSigPDF()
	p.revision(baseObjects(sigDict("adbe.x509.rsa_sha1", "")))
	p.sign(t, func([]byte) []byte { return []byte{0x04, 0x01, 0x00} })
	if s := onlySignature(t, p.buf, trusted()); s.Status != gopdfrab.SignatureUnsupported {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	p = newSigPDF()
	p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
	p.sign(t, func([]byte) []byte { return []byte("not a signature") })
	if s := onlySignature(t, p.buf, trusted()); s.Status != gopdfrab.SignatureUnsupported {
		t.Fatalf("garbage: status %v, checks %+v", s.Status, s.Checks)
	}
}

func TestSignatureBER(t *testing.T) {
	p := newSigPDF()
	p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
	p.sign(t, func(data []byte) []byte { return cmstest.Indefinite(cmstest.Sign(alice, data, cmstest.Options{})) })
	if s := onlySignature(t, p.buf, trusted()); s.Status != gopdfrab.SignatureValid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
}

func TestSignatureRSA(t *testing.T) {
	bob := testRoot.Issue("Bob", cmstest.IssueOptions{Key: cmstest.RSA})
	for _, pss := range []bool{false, true} {
		p := newSigPDF()
		p.revision(baseObjects(sigDict("ETSI.CAdES.detached", "")))
		p.sign(t, detached(bob, cmstest.Options{PSS: pss}))
		s := onlySignature(t, p.buf, trusted())
		if s.Status != gopdfrab.SignatureValid {
			t.Fatalf("pss %v: status %v, checks %+v", pss, s.Status, s.Checks)
		}
		if c, _ := s.Check(gopdfrab.SignatureCheckValue); !strings.Contains(c.Detail, "RSA 2048-bit") {
			t.Errorf("detail %q", c.Detail)
		}
	}
}

func TestSignatureTimestamp(t *testing.T) {
	stamped := time.Date(2026, 3, 4, 4, 10, 0, 0, time.UTC)
	s := onlySignature(t, signedDoc(t, cmstest.Options{TSA: tsa, TSATime: stamped}).buf, trusted())
	if s.Status != gopdfrab.SignatureValid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	if s.Timestamp == nil || !s.Timestamp.Verified || !s.Timestamp.Trusted || !s.Time.Equal(stamped) ||
		s.TimeSource != gopdfrab.SignatureTimeFromTimestamp {
		t.Errorf("timestamp %+v, time %v from %q", s.Timestamp, s.Time, s.TimeSource)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckTimestamp, gopdfrab.SignaturePassed)
}

func TestSignatureExpiredAtSigning(t *testing.T) {
	old := testRoot.Issue("Old", cmstest.IssueOptions{NotAfter: signedAt.Add(-24 * time.Hour)})
	p := newSigPDF()
	p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
	p.sign(t, detached(old, cmstest.Options{}))
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureUntrusted {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckValidity, gopdfrab.SignatureFailed)
}

func TestSignatureModified(t *testing.T) {
	p := signedDoc(t, cmstest.Options{})
	// A new content stream for the page.
	p.revision(map[int]string{
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 7 0 R /Annots [4 0 R] >>",
		7: "<< /Length 10 >>\nstream\n0 0 m 9 9 l\nendstream",
	})
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureModified || s.CoversDocument || s.Revision != 1 || s.Revisions != 2 {
		t.Fatalf("status %v, covers %v, revision %d of %d, checks %+v", s.Status, s.CoversDocument, s.Revision, s.Revisions, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckChanges, gopdfrab.SignatureFailed)
}

func TestSignatureLaterDSS(t *testing.T) {
	p := signedDoc(t, cmstest.Options{})
	ocsp := cmstest.OCSP(testRoot, alice.Cert, signedAt, time.Time{})
	p.revision(map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 >> /DSS 7 0 R >>",
		7: "<< /Type /DSS /OCSPs [8 0 R] >>",
		8: fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(ocsp), ocsp),
	})
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureValid || s.CoversDocument {
		t.Fatalf("status %v, covers %v, checks %+v", s.Status, s.CoversDocument, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckChanges, gopdfrab.SignaturePassed)
	wantCheck(t, s, gopdfrab.SignatureCheckRevocation, gopdfrab.SignaturePassed)
}

func TestTwoSignatures(t *testing.T) {
	bob := testRoot.Issue("Bob", cmstest.IssueOptions{})
	p := signedDoc(t, cmstest.Options{})
	p.revision(map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 7 0 R] /SigFlags 3 >> >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots [4 0 R 7 0 R] >>",
		7: "<< /FT /Sig /T (Review) /V 8 0 R /Type /Annot /Subtype /Widget /Rect [0 0 0 0] /P 3 0 R /F 4 >>",
		8: sigDict("adbe.pkcs7.detached", ""),
	})
	p.sign(t, detached(bob, cmstest.Options{}))
	sigs := signaturesOf(t, p.buf, trusted())
	if len(sigs) != 2 {
		t.Fatalf("%d signatures", len(sigs))
	}
	first, second := sigs[0], sigs[1]
	if first.Field != "Approval" || second.Field != "Review" {
		t.Errorf("fields %q, %q", first.Field, second.Field)
	}
	if first.Status != gopdfrab.SignatureValid || first.CoversDocument || first.Revision != 1 {
		t.Errorf("first: status %v, covers %v, revision %d, checks %+v", first.Status, first.CoversDocument, first.Revision, first.Checks)
	}
	if second.Status != gopdfrab.SignatureValid || !second.CoversDocument || second.Revision != 2 {
		t.Errorf("second: status %v, covers %v, revision %d, checks %+v", second.Status, second.CoversDocument, second.Revision, second.Checks)
	}
	// The second is invisible: on the page, with no box.
	if want := []gopdfrab.SignatureWidget{{Page: 0}}; !slices.Equal(second.Widgets, want) {
		t.Errorf("second widgets %+v", second.Widgets)
	}
}

func TestDocumentTimestamp(t *testing.T) {
	p := newSigPDF()
	objs := baseObjects(strings.Replace(sigDict("ETSI.RFC3161", ""), "/Type /Sig", "/Type /DocTimeStamp", 1))
	p.revision(objs)
	stamped := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	p.sign(t, func(data []byte) []byte { return cmstest.Timestamp(tsa, data, stamped) })
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureValid || !s.DocumentTimestamp || !s.Time.Equal(stamped) {
		t.Fatalf("status %v, timestamp %v, time %v, checks %+v", s.Status, s.DocumentTimestamp, s.Time, s.Checks)
	}
	if s.Signer == nil || s.Signer.Subject.CommonName != "Test TSA" {
		t.Errorf("signer %v", s.Signer)
	}
	if s.Timestamp == nil || !s.Timestamp.Verified || !s.Timestamp.Trusted {
		t.Errorf("timestamp %+v", s.Timestamp)
	}
}

func TestUnsignedDocument(t *testing.T) {
	p := newSigPDF()
	objs := baseObjects("<< >>")
	objs[4] = "<< /FT /Sig /T (Empty) /Type /Annot /Subtype /Widget /Rect [10 20 110 70] /P 3 0 R >>"
	p.revision(objs)
	if sigs := signaturesOf(t, p.buf, trusted()); len(sigs) != 0 {
		t.Errorf("%d signatures in a document with an unsigned field", len(sigs))
	}
}

func TestSignatureExtraRoots(t *testing.T) {
	// An issuing CA stands as the anchor, as a trusted list names it.
	ca := testRoot.Issue("Issuing CA", cmstest.IssueOptions{CA: true})
	bob := ca.Issue("Bob", cmstest.IssueOptions{})
	doc := func() []byte {
		p := newSigPDF()
		p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
		p.sign(t, detached(bob, cmstest.Options{Chain: []*x509.Certificate{ca.Cert, testRoot.Cert}}))
		return p.buf
	}()
	o := gopdfrab.SignatureOptions{Roots: x509.NewCertPool(), Now: trusted().Now}
	if s := onlySignature(t, doc, o); s.Status != gopdfrab.SignatureUntrusted {
		t.Fatalf("without extra roots: status %v", s.Status)
	}
	o.ExtraRoots, o.ExtraRootsName = x509.NewCertPool(), "the test list"
	o.ExtraRoots.AddCert(ca.Cert)
	s := onlySignature(t, doc, o)
	if s.Status != gopdfrab.SignatureValid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	if c, _ := s.Check(gopdfrab.SignatureCheckTrust); c.Detail != "The certificate chains to Issuing CA, a trusted authority of the test list." {
		t.Errorf("detail %q", c.Detail)
	}
	// The chain goes on past the anchor to the root the signature carries.
	if len(s.Chain) != 3 || s.Chain[2].Subject.CommonName != "Test Root" {
		t.Errorf("chain of %d", len(s.Chain))
	}
	// The trusted roots come first.
	o.Roots = trusted().Roots
	both := onlySignature(t, doc, o)
	if c, _ := both.Check(gopdfrab.SignatureCheckTrust); !strings.Contains(c.Detail, "trusted root Test Root") {
		t.Errorf("with both: detail %q", c.Detail)
	}
}

func TestSignatureNoTrustStore(t *testing.T) {
	// The system pool, which may or may not exist, never holds the test root.
	s := onlySignature(t, signedDoc(t, cmstest.Options{}).buf, gopdfrab.SignatureOptions{})
	if s.Status != gopdfrab.SignatureUntrusted {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
}

func TestSignaturesCancelled(t *testing.T) {
	d, err := gopdfrab.OpenBytes(signedDoc(t, cmstest.Options{}).buf)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Signatures(ctx, trusted()); err == nil {
		t.Error("Signatures with a cancelled context succeeded")
	}
}

func TestSignatureResultString(t *testing.T) {
	for r, want := range map[gopdfrab.SignatureResult]string{
		gopdfrab.SignaturePassed: "passed", gopdfrab.SignatureFailed: "failed", gopdfrab.SignatureSkipped: "skipped",
		7: "SignatureResult(7)",
	} {
		if got := r.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(r), got, want)
		}
	}
}

func TestSignatureStatusString(t *testing.T) {
	for s, want := range map[gopdfrab.SignatureStatus]string{
		gopdfrab.SignatureValid: "valid", gopdfrab.SignatureUntrusted: "untrusted", gopdfrab.SignatureModified: "modified",
		gopdfrab.SignatureUnsupported: "unsupported", gopdfrab.SignatureInvalid: "invalid", 99: "SignatureStatus(99)",
	} {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(s), got, want)
		}
	}
}

func TestSignatureTextEncodings(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{"(Ond\xc5\x99ej)", "Ondřej"},                 // UTF-8 without a BOM
		{"(\xef\xbb\xbfOnd\xc5\x99ej)", "Ondřej"},     // with the UTF-8 BOM
		{"<FEFF004F006E00640159 0065006A>", "Ondřej"}, // UTF-16BE
		{"(Andr\xe9)", "André"},                       // PDFDocEncoding
	} {
		p := newSigPDF()
		p.revision(baseObjects(strings.Replace(sigDict("adbe.pkcs7.detached", ""), "/Name (Alice)", "/Name "+c.raw, 1)))
		p.sign(t, detached(alice, cmstest.Options{}))
		if got := onlySignature(t, p.buf, trusted()).Name; got != c.want {
			t.Errorf("Name %s = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestSignatureFieldShapes(t *testing.T) {
	t.Run("kid widgets and a qualified name", func(t *testing.T) {
		p := newSigPDF()
		objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
		objs[1] = "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [7 0 R] /SigFlags 3 >> >>"
		objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots [8 0 R 9 0 R] /Rotate 90 >>"
		objs[4] = "<< /FT /Sig /T (Approval) /Parent 7 0 R /V 5 0 R /Kids [8 0 R 9 0 R] >>"
		objs[7] = "<< /T (Form) /Kids [4 0 R] >>"
		objs[8] = "<< /Type /Annot /Subtype /Widget /Parent 4 0 R /Rect [10 20 110 70] /P 3 0 R >>"
		objs[9] = "<< /Type /Annot /Subtype /Widget /Parent 4 0 R /Rect [10 100 110 150] /P 3 0 R /F 2 >>" // hidden
		p.revision(objs)
		p.sign(t, detached(alice, cmstest.Options{}))
		s := onlySignature(t, p.buf, trusted())
		if s.Field != "Form.Approval" || s.Status != gopdfrab.SignatureValid {
			t.Fatalf("field %q, status %v, checks %+v", s.Field, s.Status, s.Checks)
		}
		// Rotated a quarter turn, the 200 by 300 page shows 300 wide.
		want := []gopdfrab.SignatureWidget{{Page: 0, Box: gopdfrab.Rect{X0: 20, Y0: 10, X1: 70, Y1: 110}}, {Page: 0}}
		if !slices.Equal(s.Widgets, want) {
			t.Errorf("Widgets = %+v, want %+v", s.Widgets, want)
		}
	})
	t.Run("a field only a page shows", func(t *testing.T) {
		p := newSigPDF()
		objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
		objs[1] = "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [] >> >>"
		objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots [8 0 R] >>"
		objs[4] = "<< /FT /Sig /T (Hidden) /V 5 0 R /Kids [8 0 R] >>"
		objs[8] = "<< /Type /Annot /Subtype /Widget /Parent 4 0 R /Rect [0 0 0 0] /P 3 0 R >>"
		p.revision(objs)
		p.sign(t, detached(alice, cmstest.Options{}))
		if s := onlySignature(t, p.buf, trusted()); s.Field != "Hidden" || s.Status != gopdfrab.SignatureValid {
			t.Errorf("field %q, status %v", s.Field, s.Status)
		}
	})
}

func TestCertificationSignature(t *testing.T) {
	ref := "/Reference [<< /Type /SigRef /TransformMethod /DocMDP /TransformParams << /P 1 /V /1.2 >> >>] "
	t.Run("in a field", func(t *testing.T) {
		p := newSigPDF()
		objs := baseObjects(sigDict("adbe.pkcs7.detached", ref))
		objs[1] = "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 >> /Perms << /DocMDP 5 0 R >> >>"
		p.revision(objs)
		p.sign(t, detached(alice, cmstest.Options{}))
		if s := onlySignature(t, p.buf, trusted()); s.Certifies != 1 || s.Field != "Approval" {
			t.Errorf("Certifies %d, field %q", s.Certifies, s.Field)
		}
	})
	t.Run("in no field", func(t *testing.T) {
		p := newSigPDF()
		objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
		objs[1] = "<< /Type /Catalog /Pages 2 0 R /Perms << /DocMDP 5 0 R >> >>"
		objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R >>"
		delete(objs, 4)
		objs[4] = "<< >>"
		p.revision(objs)
		p.sign(t, detached(alice, cmstest.Options{}))
		s := onlySignature(t, p.buf, trusted())
		if s.Certifies != 2 || s.Field != "" || len(s.Widgets) != 0 || s.Status != gopdfrab.SignatureValid {
			t.Errorf("Certifies %d, field %q, widgets %v, status %v", s.Certifies, s.Field, s.Widgets, s.Status)
		}
	})
}

func TestSHA1SubFilter(t *testing.T) {
	sign := func(t *testing.T, digest func([]byte) []byte) gopdfrab.Signature {
		p := newSigPDF()
		p.revision(baseObjects(sigDict("adbe.pkcs7.sha1", "")))
		p.sign(t, func(data []byte) []byte {
			return cmstest.Sign(alice, nil, cmstest.Options{Content: digest(data)})
		})
		return onlySignature(t, p.buf, trusted())
	}
	if s := sign(t, func(b []byte) []byte { h := sha1.Sum(b); return h[:] }); s.Status != gopdfrab.SignatureValid {
		t.Errorf("status %v, checks %+v", s.Status, s.Checks)
	}
	if s := sign(t, func(b []byte) []byte { return make([]byte, 20) }); s.Status != gopdfrab.SignatureInvalid {
		t.Errorf("a wrong digest: status %v", s.Status)
	}
	// A detached format with encapsulated content is not read.
	p := newSigPDF()
	p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
	p.sign(t, func(data []byte) []byte { return cmstest.Sign(alice, nil, cmstest.Options{Content: data}) })
	if s := onlySignature(t, p.buf, trusted()); s.Status != gopdfrab.SignatureUnsupported {
		t.Errorf("encapsulated detached: status %v", s.Status)
	}
}

func TestDocumentTimestampFailures(t *testing.T) {
	stamp := func(t *testing.T, contents func([]byte) []byte) gopdfrab.Signature {
		p := newSigPDF()
		p.revision(baseObjects(sigDict("ETSI.RFC3161", "")))
		p.sign(t, contents)
		return onlySignature(t, p.buf, trusted())
	}
	if s := stamp(t, func([]byte) []byte { return cmstest.Timestamp(tsa, []byte("other"), signedAt) }); s.Status != gopdfrab.SignatureInvalid {
		t.Errorf("a timestamp of other bytes: status %v", s.Status)
	}
	if s := stamp(t, func(d []byte) []byte { return cmstest.Sign(tsa, d, cmstest.Options{}) }); s.Status != gopdfrab.SignatureUnsupported {
		t.Errorf("a signature for a timestamp: status %v", s.Status)
	}
}

func TestSignatureRevokedInTheDSS(t *testing.T) {
	p := signedDoc(t, cmstest.Options{})
	ocsp := cmstest.OCSP(testRoot, alice.Cert, signedAt, signedAt.Add(-time.Hour))
	cert := testRoot.Cert.Raw
	p.revision(map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 >> /DSS 7 0 R >>",
		7: "<< /Type /DSS /OCSPs [8 0 R] /Certs [9 0 R] >>",
		8: fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(ocsp), ocsp),
		9: fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cert), cert),
	})
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureInvalid || len(s.Chain) != 2 {
		t.Fatalf("status %v, chain of %d, checks %+v", s.Status, len(s.Chain), s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckRevocation, gopdfrab.SignatureFailed)
}

// TestSignatureUntrustedTimestamp checks that a timestamp whose authority is
// not trusted gives no time to rely on: a signer whose certificate was revoked
// cannot backdate a signature with a timestamp of their own making.
func TestSignatureUntrustedTimestamp(t *testing.T) {
	revokedAt := signedAt.Add(-time.Hour)
	crl := cmstest.CRL(testRoot, revokedAt, alice.Cert)
	ownTSA := cmstest.Root("Mallory TSA").Issue("Mallory TSA", cmstest.IssueOptions{EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping}})
	backdated := revokedAt.Add(-24 * time.Hour)
	s := onlySignature(t, signedDoc(t, cmstest.Options{TSA: ownTSA, TSATime: backdated, CRLs: [][]byte{crl}}).buf, trusted())
	if s.Status != gopdfrab.SignatureInvalid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckRevocation, gopdfrab.SignatureFailed)
	if s.Timestamp == nil || !s.Timestamp.Verified || s.Timestamp.Trusted {
		t.Errorf("timestamp %+v", s.Timestamp)
	}
	if s.TimeSource == gopdfrab.SignatureTimeFromTimestamp || s.Time.Equal(backdated) {
		t.Errorf("time %v from %q, taken from an untrusted timestamp", s.Time, s.TimeSource)
	}
}

// TestSignatureTimestampOfOtherBytes checks that a timestamp that does not
// stamp its signature makes the signature untrusted.
func TestSignatureTimestampOfOtherBytes(t *testing.T) {
	s := onlySignature(t, signedDoc(t, cmstest.Options{TSA: tsa, TSATime: signedAt, TSAStampsOther: true}).buf, trusted())
	if s.Status != gopdfrab.SignatureUntrusted {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckTimestamp, gopdfrab.SignatureFailed)
	if s.TimeSource == gopdfrab.SignatureTimeFromTimestamp {
		t.Errorf("time from a timestamp that does not verify")
	}
}

// TestDocumentTimestampUntrustedAuthority checks that a document timestamp
// by an authority that is not trusted is untrusted, and its time not relied
// on for revocation.
func TestDocumentTimestampUntrustedAuthority(t *testing.T) {
	own := cmstest.Root("Mallory TSA").Issue("Mallory TSA", cmstest.IssueOptions{EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping}})
	p := newSigPDF()
	p.revision(baseObjects(sigDict("ETSI.RFC3161", "")))
	p.sign(t, func(data []byte) []byte { return cmstest.Timestamp(own, data, signedAt) })
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureUntrusted {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	if s.Timestamp == nil || !s.Timestamp.Verified || s.Timestamp.Trusted {
		t.Errorf("timestamp %+v", s.Timestamp)
	}
}

// TestSignatureHugeByteRangeGap checks that a byte range leaving out far more
// than any signature value is rejected without reading the gap.
func TestSignatureHugeByteRangeGap(t *testing.T) {
	p := signedDoc(t, cmstest.Options{})
	// Pad the file after the signature, then widen the gap over the padding.
	at := bytes.Index(p.buf, []byte("/ByteRange [0 "))
	var a int
	fmt.Sscanf(string(p.buf[at+len("/ByteRange [0 "):]), "%d", &a)
	pad := 40 << 20
	p.buf = append(p.buf, bytes.Repeat([]byte(" "), pad)...)
	copy(p.buf[at:], fmt.Sprintf("/ByteRange [0 %10d %10d %10d]", a, len(p.buf)-1, 1))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	s := onlySignature(t, p.buf, trusted())
	runtime.ReadMemStats(&after)
	if s.Status != gopdfrab.SignatureInvalid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > uint64(pad)/2 {
		t.Errorf("checking allocated %d MB for a %d MB gap", grew>>20, pad>>20)
	}
}

// TestSignatureLaterSignatureInObjectStream checks that a second signature
// whose revision keeps its objects in an object stream leaves the first
// unchanged, when the objects it rewrites are the same as before.
func TestSignatureLaterSignatureInObjectStream(t *testing.T) {
	bob := testRoot.Issue("Bob", cmstest.IssueOptions{})
	p := signedDoc(t, cmstest.Options{})
	p.objStmRevision(9, 10, map[int]string{
		1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 7 0 R] /SigFlags 3 >> >>",
		2: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots [4 0 R 7 0 R] >>",
		7: "<< /FT /Sig /T (Review) /V 8 0 R /Type /Annot /Subtype /Widget /Rect [0 0 0 0] /P 3 0 R /F 4 >>",
	}, map[int]string{8: sigDict("adbe.pkcs7.detached", "")})
	p.sign(t, detached(bob, cmstest.Options{}))
	sigs := signaturesOf(t, p.buf, trusted())
	if len(sigs) != 2 {
		t.Fatalf("%d signatures", len(sigs))
	}
	for _, s := range sigs {
		if s.Status != gopdfrab.SignatureValid {
			t.Errorf("%s: status %v, checks %+v", s.Field, s.Status, s.Checks)
		}
	}
}

// TestSignatureChangesThatAreNotSignatures checks that later revisions which
// change what a page shows, through objects that signing also rewrites, make
// the signature modified.
func TestSignatureChangesThatAreNotSignatures(t *testing.T) {
	bob := testRoot.Issue("Bob", cmstest.IssueOptions{})
	// The first revision holds an unused stream 9, and page 3's contents are
	// the indirect array 8.
	base := func(t *testing.T) *sigPDF {
		objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
		objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots [4 0 R] >>"
		objs[8] = "[6 0 R]"
		objs[9] = "<< /Length 10 >>\nstream\n0 0 m 9 9 l\nendstream"
		// Font 10 serves both the page and the form.
		objs[1] = "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 /DR << /Font << /F1 10 0 R >> >> >> >>"
		objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots [4 0 R] /Resources << /Font << /F1 10 0 R >> >> >>"
		objs[10] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"
		p := newSigPDF()
		p.revision(objs)
		p.sign(t, detached(alice, cmstest.Options{}))
		return p
	}
	for _, tc := range []struct {
		name   string
		update func(t *testing.T, p *sigPDF)
	}{
		{"page points to another existing stream", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 9 0 R /Annots [4 0 R] >>"})
		}},
		{"content stream rewritten", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{6: "<< /Length 10 >>\nstream\n0 0 m 9 9 l\nendstream"})
		}},
		{"font shared with the form's resources rewritten", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{
				1:  "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 /DR << /Font << /F1 10 0 R >> >> >> >>",
				10: "<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>",
			})
		}},
		{"page added", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{
				2:  "<< /Type /Pages /Kids [3 0 R 12 0 R] /Count 2 >>",
				12: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] >>",
			})
		}},
		{"form removed", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{1: "<< /Type /Catalog /Pages 2 0 R >>"})
		}},
		{"form asks for new appearances", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{
				1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 /NeedAppearances true " +
					"/DR << /Font << /F1 10 0 R >> >> >> >>",
			})
		}},
		{"annotations no list", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots /None " +
				"/Resources << /Font << /F1 10 0 R >> >> >>"})
		}},
		{"signature widget removed", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots [] " +
				"/Resources << /Font << /F1 10 0 R >> >> >>"})
		}},
		{"page box changes", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 300] /Contents 8 0 R /Annots [4 0 R] >>"})
		}},
		{"indirect content array grows", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{8: "[6 0 R 9 0 R]"})
		}},
		{"page tree changes", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{2: "<< /Type /Pages /Kids [3 0 R] /Count 1 /Rotate 90 >>"})
		}},
		{"rewrite in an object stream", func(t *testing.T, p *sigPDF) {
			p.objStmRevision(10, 11, map[int]string{8: "[9 0 R]"}, nil)
		}},
		{"unsigned signature field with an appearance", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{
				1:  "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 10 0 R] /SigFlags 3 >> >>",
				3:  "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots [4 0 R 10 0 R] >>",
				10: "<< /FT /Sig /T (Cover) /V 11 0 R /Type /Annot /Subtype /Widget /Rect [0 0 200 300] /P 3 0 R /AP << /N 12 0 R >> >>",
				11: sigDict("adbe.pkcs7.detached", ""),
				12: "<< /Type /XObject /Subtype /Form /BBox [0 0 200 300] /Length 18 >>\nstream\n0 0 200 300 re f\nendstream",
			})
		}},
		{"a signature's appearance rewritten after it signed", func(t *testing.T, p *sigPDF) {
			p.revision(map[int]string{
				1:  "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 10 0 R] /SigFlags 3 >> >>",
				3:  "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots [4 0 R 10 0 R] >>",
				10: "<< /FT /Sig /T (Review) /V 11 0 R /Type /Annot /Subtype /Widget /Rect [0 0 200 300] /P 3 0 R /AP << /N 12 0 R >> >>",
				11: sigDict("adbe.pkcs7.detached", ""),
				12: "<< /Type /XObject /Subtype /Form /BBox [0 0 200 300] /Length 0 >>\nstream\n\nendstream",
			})
			p.sign(t, detached(bob, cmstest.Options{}))
			p.revision(map[int]string{
				12: "<< /Type /XObject /Subtype /Form /BBox [0 0 200 300] /Length 18 >>\nstream\n0 0 200 300 re f\nendstream",
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base(t)
			tc.update(t, p)
			first := signaturesOf(t, p.buf, trusted())[0]
			if first.Field != "Approval" || first.Status != gopdfrab.SignatureModified {
				t.Fatalf("%s: status %v, checks %+v", first.Field, first.Status, first.Checks)
			}
			wantCheck(t, first, gopdfrab.SignatureCheckChanges, gopdfrab.SignatureFailed)
		})
	}
}

// TestSignatureLaterSignatureRewritesAsBefore checks that a later signature
// whose revision writes the page, its content list and the page tree again,
// as they were, leaves the first signature unchanged.
func TestSignatureLaterSignatureRewritesAsBefore(t *testing.T) {
	bob := testRoot.Issue("Bob", cmstest.IssueOptions{})
	objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
	objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots [4 0 R] >>"
	objs[8] = "[6 0 R]"
	p := newSigPDF()
	p.revision(objs)
	p.sign(t, detached(alice, cmstest.Options{}))
	p.revision(map[int]string{
		1:  "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 10 0 R] /SigFlags 3 >> >>",
		2:  "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3:  "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 8 0 R /Annots [4 0 R 10 0 R] >>",
		8:  "[6 0 R]",
		10: "<< /FT /Sig /T (Review) /V 11 0 R /Type /Annot /Subtype /Widget /Rect [0 0 50 50] /P 3 0 R /AP << /N 12 0 R >> >>",
		11: sigDict("adbe.pkcs7.detached", ""),
		12: "<< /Type /XObject /Subtype /Form /BBox [0 0 50 50] /Length 13 0 R >>\nstream\n0 0 50 50 re S\nendstream",
		13: "14",
	})
	p.sign(t, detached(bob, cmstest.Options{}))
	for _, s := range signaturesOf(t, p.buf, trusted()) {
		if s.Status != gopdfrab.SignatureValid {
			t.Errorf("signing alone: %s: status %v, checks %+v", s.Field, s.Status, s.Checks)
		}
	}
}

// TestSignatureLaterSignatureIndirectLists checks that a later signature may
// grow a form and an annotation list that are objects of their own, and only
// with its own field.
func TestSignatureLaterSignatureIndirectLists(t *testing.T) {
	bob := testRoot.Issue("Bob", cmstest.IssueOptions{})
	for _, extra := range []bool{false, true} {
		objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
		objs[1] = "<< /Type /Catalog /Pages 2 0 R /AcroForm 9 0 R >>"
		objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots 7 0 R >>"
		objs[7] = "[4 0 R]"
		objs[9] = "<< /Fields [4 0 R] /SigFlags 3 >>"
		p := newSigPDF()
		p.revision(objs)
		p.sign(t, detached(alice, cmstest.Options{}))
		update := map[int]string{
			7:  "[4 0 R 10 0 R]",
			9:  "<< /Fields [4 0 R 10 0 R] /SigFlags 3 >>",
			10: "<< /FT /Sig /T (Review) /V 11 0 R /Type /Annot /Subtype /Widget /Rect [0 0 0 0] /P 3 0 R /F 4 >>",
			11: sigDict("adbe.pkcs7.detached", ""),
		}
		if extra {
			// An annotation that is no signature's widget.
			update[7] = "[4 0 R 10 0 R 12 0 R]"
			update[12] = "<< /Type /Annot /Subtype /Square /Rect [0 0 200 300] >>"
		}
		p.revision(update)
		p.sign(t, detached(bob, cmstest.Options{}))
		sigs := signaturesOf(t, p.buf, trusted())
		want := gopdfrab.SignatureValid
		if extra {
			want = gopdfrab.SignatureModified
		}
		if len(sigs) != 2 || sigs[0].Status != want || sigs[1].Status != gopdfrab.SignatureValid {
			for _, s := range sigs {
				t.Errorf("extra %v: %s: status %v, checks %+v", extra, s.Field, s.Status, s.Checks)
			}
		}
	}
}

// TestSignatureLaterInformationAndStore checks that later revisions may
// rewrite the document information and grow a document security store, even
// one whose arrays lead back to themselves.
func TestSignatureLaterInformationAndStore(t *testing.T) {
	p := signedDoc(t, cmstest.Options{})
	p.trailer = " /Info 7 0 R"
	p.revision(map[int]string{
		1:  "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 >> /DSS 8 0 R >>",
		7:  "<< /Producer (Later) >>",
		8:  "<< /Type /DSS /Certs 9 0 R /VRI << /A 10 0 R /B 10 0 R >> >>",
		9:  "[9 0 R]",
		10: "<< /TU (D:20260401000000Z) >>",
	})
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureValid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
}

// TestSignatureByteRangeEndsNoRevision checks that a signature whose signed
// bytes end where no readable revision does counts as changed after signing:
// here its value sits in the header's comment line, before any object.
func TestSignatureByteRangeEndsNoRevision(t *testing.T) {
	p := newSigPDF()
	p.buf = append(p.buf, '%', '<')
	cs := len(p.buf) - 1
	p.buf = append(p.buf, strings.Repeat("0", contentsLen)...)
	p.buf = append(p.buf, ">\n"...)
	ce := cs + contentsLen + 2
	p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
	at := bytes.LastIndex(p.buf, []byte("/ByteRange [0 "))
	copy(p.buf[at:], fmt.Sprintf("/ByteRange [0 %10d %10d %10d]", cs, ce, 0))
	value := hex.EncodeToString(cmstest.Sign(alice, p.buf[:cs], cmstest.Options{SigningTime: signedAt}))
	copy(p.buf[cs+1:], value)
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureModified {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	if c, _ := s.Check(gopdfrab.SignatureCheckChanges); !strings.Contains(c.Detail, "no revision") {
		t.Errorf("changes %+v", c)
	}
}

// TestSignatureGapNotHex checks that a byte range whose gap holds more than
// the signature value's hex string is rejected: one that runs past the value
// to the end of its dictionary, or starts at the dictionary.
func TestSignatureGapNotHex(t *testing.T) {
	for _, past := range []bool{true, false} {
		p := signedDoc(t, cmstest.Options{})
		at := bytes.Index(p.buf, []byte("/ByteRange [0 "))
		var a, b, c int
		fmt.Sscanf(string(p.buf[at+len("/ByteRange [0 "):]), "%d %d %d", &a, &b, &c)
		if past {
			end := bytes.Index(p.buf[b:], []byte(">>")) + b + 2
			c -= end - b
			b = end
		} else {
			a = bytes.LastIndex(p.buf[:a], []byte("<<"))
		}
		copy(p.buf[at:], fmt.Sprintf("/ByteRange [0 %10d %10d %10d]", a, b, c))
		s := onlySignature(t, p.buf, trusted())
		if s.Status != gopdfrab.SignatureInvalid {
			t.Errorf("past %v: status %v, checks %+v", past, s.Status, s.Checks)
		}
		wantCheck(t, s, gopdfrab.SignatureCheckIntegrity, gopdfrab.SignatureFailed)
	}
}

// TestSignatureTooDeepToCompare checks that objects written again as they
// were, but through a chain of objects too long to follow, count as changes.
func TestSignatureTooDeepToCompare(t *testing.T) {
	objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
	chain := map[int]string{}
	for i := range 70 {
		chain[20+i] = fmt.Sprintf("[%d 0 R]", 21+i)
	}
	chain[89] = "[6 0 R]"
	chain[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 20 0 R /Annots [4 0 R] >>"
	maps.Copy(objs, chain)
	p := newSigPDF()
	p.revision(objs)
	p.sign(t, detached(alice, cmstest.Options{}))
	p.revision(chain)
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureModified {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
}

// TestSignatureLaterFieldWithKids checks that a later signature field whose
// widget is a kid of it may be added, and that a kid of it that is no widget
// is a change.
func TestSignatureLaterFieldWithKids(t *testing.T) {
	bob := testRoot.Issue("Bob", cmstest.IssueOptions{})
	for _, other := range []bool{false, true} {
		kids := "[9 0 R]"
		update := map[int]string{
			1: "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 7 0 R] /SigFlags 3 >> >>",
			3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots [4 0 R 9 0 R] >>",
			8: sigDict("adbe.pkcs7.detached", ""),
			9: "<< /Type /Annot /Subtype /Widget /Parent 7 0 R /Rect [0 0 0 0] /P 3 0 R /F 4 >>",
		}
		if other {
			kids = "[9 0 R 10 0 R]"
			update[10] = "<< /Parent 7 0 R /T (Note) >>"
		}
		update[7] = "<< /FT /Sig /T (Review) /V 8 0 R /Kids " + kids + " >>"
		p := signedDoc(t, cmstest.Options{})
		p.revision(update)
		p.sign(t, detached(bob, cmstest.Options{}))
		sigs := signaturesOf(t, p.buf, trusted())
		want := gopdfrab.SignatureValid
		if other {
			want = gopdfrab.SignatureModified
		}
		if len(sigs) != 2 || sigs[0].Status != want || sigs[1].Status != gopdfrab.SignatureValid {
			for _, s := range sigs {
				t.Errorf("other kid %v: %s: status %v, checks %+v", other, s.Field, s.Status, s.Checks)
			}
		}
	}
}

// countdownCtx is a context whose Err reports cancellation after n calls.
type countdownCtx struct {
	context.Context
	n int
}

func (c *countdownCtx) Err() error {
	if c.n--; c.n < 0 {
		return context.Canceled
	}
	return nil
}

func TestSignaturesCancelledPartway(t *testing.T) {
	doc := signedDoc(t, cmstest.Options{}).buf
	for _, n := range []int{1, 2} {
		d, err := gopdfrab.OpenBytes(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Signatures(&countdownCtx{context.Background(), n}, trusted()); err == nil {
			t.Errorf("cancelled after %d checks: Signatures succeeded", n)
		}
		d.Close()
	}
}

func TestSignatureCheckMissing(t *testing.T) {
	s := onlySignature(t, signedDoc(t, cmstest.Options{}).buf, trusted())
	if _, ok := s.Check("no such check"); ok {
		t.Error("Check found a check the signature does not have")
	}
}

func TestSignatureNoSystemTrustStore(t *testing.T) {
	defer gopdfrab.NoSystemCertPool()()
	s := onlySignature(t, signedDoc(t, cmstest.Options{}).buf, gopdfrab.SignatureOptions{})
	if c, _ := s.Check(gopdfrab.SignatureCheckTrust); !strings.Contains(c.Detail, "no trust store is available") {
		t.Errorf("trust detail %q", c.Detail)
	}
}

func TestSignatureFieldTreeOddities(t *testing.T) {
	p := newSigPDF()
	objs := baseObjects(sigDict("adbe.pkcs7.detached", ""))
	// Fields lists the field twice, a number, and a tree too deep to follow.
	objs[1] = "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 4 0 R 7 20 0 R] /SigFlags 3 >> >>"
	for n := 20; n < 60; n++ {
		objs[n] = fmt.Sprintf("<< /T (L%d) /Kids [%d 0 R] >>", n, n+1)
	}
	objs[60] = "<< /FT /Sig /T (Deep) /V 5 0 R >>"
	// The page shows widgets of a text field and of no field.
	objs[3] = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 6 0 R /Annots [4 0 R 10 0 R 11 0 R] >>"
	objs[10] = "<< /Type /Annot /Subtype /Widget /T (Name) /Parent 12 0 R /Rect [0 0 1 1] >>"
	objs[11] = "<< /Type /Annot /Subtype /Widget /T (Loose) /Rect [0 0 1 1] >>"
	objs[12] = "<< /FT /Tx /T (Person) /Kids [10 0 R] >>"
	p.revision(objs)
	p.sign(t, detached(alice, cmstest.Options{}))
	if s := onlySignature(t, p.buf, trusted()); s.Field != "Approval" || s.Status != gopdfrab.SignatureValid {
		t.Errorf("field %q, status %v, checks %+v", s.Field, s.Status, s.Checks)
	}
}

func TestCertificationSignatureOtherTransform(t *testing.T) {
	// A FieldMDP reference is passed over; DocMDP's P defaults to 2.
	ref := "/Reference [<< /Type /SigRef /TransformMethod /FieldMDP >>] "
	p := newSigPDF()
	objs := baseObjects(sigDict("adbe.pkcs7.detached", ref))
	objs[1] = "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R] /SigFlags 3 >> /Perms << /DocMDP 5 0 R >> >>"
	p.revision(objs)
	p.sign(t, detached(alice, cmstest.Options{}))
	if s := onlySignature(t, p.buf, trusted()); s.Certifies != 2 {
		t.Errorf("Certifies %d, want 2", s.Certifies)
	}
}

func TestSignatureByteRangeNotFromStart(t *testing.T) {
	p := signedDoc(t, cmstest.Options{})
	at := bytes.Index(p.buf, []byte("/ByteRange [0 "))
	p.buf[at+len("/ByteRange [")] = '1'
	s := onlySignature(t, p.buf, trusted())
	if s.Status != gopdfrab.SignatureInvalid {
		t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
	}
	wantCheck(t, s, gopdfrab.SignatureCheckIntegrity, gopdfrab.SignatureFailed)
}

// The DER of the object identifiers the tests below edit in signatures.
var (
	oidSHA256DER      = []byte{0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01}
	oidECDSASHA256DER = []byte{0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x02}
	oidTSTInfoDER     = []byte{0x06, 0x0b, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x09, 0x10, 0x01, 0x04}
)

// unknownOID returns b with every occurrence of the object identifier oid,
// or only the last, made into another of the same length.
func unknownOID(b, oid []byte, onlyLast bool) []byte {
	other := bytes.Clone(oid)
	other[len(other)-1] = 0x7f
	if !onlyLast {
		return bytes.ReplaceAll(b, oid, other)
	}
	out := bytes.Clone(b)
	i := bytes.LastIndex(out, oid)
	copy(out[i:], other)
	return out
}

func TestSignatureValueProblems(t *testing.T) {
	noSigners := []byte{
		0x30, 0x23, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x02, // signedData
		0xa0, 0x16, 0x30, 0x14, 0x02, 0x01, 0x01, 0x31, 0x00, // version, digestAlgorithms
		0x30, 0x0b, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x01, // data
		0x31, 0x00, // signerInfos
	}
	for _, tc := range []struct {
		name   string
		value  func([]byte) []byte
		status gopdfrab.SignatureStatus
		check  string
		result gopdfrab.SignatureResult
	}{
		{"no signer", func([]byte) []byte { return noSigners },
			gopdfrab.SignatureInvalid, gopdfrab.SignatureCheckValue, gopdfrab.SignatureFailed},
		{"unknown digest", func(d []byte) []byte { return unknownOID(detached(alice, cmstest.Options{})(d), oidSHA256DER, false) },
			gopdfrab.SignatureUnsupported, gopdfrab.SignatureCheckIntegrity, gopdfrab.SignatureSkipped},
		{"no certificate", detached(alice, cmstest.Options{NoCert: true}),
			gopdfrab.SignatureUnsupported, gopdfrab.SignatureCheckValue, gopdfrab.SignatureSkipped},
		{"unknown signature algorithm", func(d []byte) []byte {
			return unknownOID(detached(alice, cmstest.Options{})(d), oidECDSASHA256DER, true)
		}, gopdfrab.SignatureUnsupported, gopdfrab.SignatureCheckValue, gopdfrab.SignatureSkipped},
		{"unreadable timestamp", func(d []byte) []byte {
			return unknownOID(detached(alice, cmstest.Options{TSA: tsa, TSATime: signedAt})(d), oidTSTInfoDER, false)
		}, gopdfrab.SignatureValid, gopdfrab.SignatureCheckTimestamp, gopdfrab.SignatureSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newSigPDF()
			p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
			p.sign(t, tc.value)
			s := onlySignature(t, p.buf, trusted())
			if s.Status != tc.status {
				t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
			}
			wantCheck(t, s, tc.check, tc.result)
		})
	}
}

func TestDocumentTimestampValueProblems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func([]byte) []byte
		status gopdfrab.SignatureStatus
	}{
		{"unknown signature algorithm", func(b []byte) []byte { return unknownOID(b, oidECDSASHA256DER, true) }, gopdfrab.SignatureUnsupported},
		{"bad signature", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }, gopdfrab.SignatureInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newSigPDF()
			p.revision(baseObjects(sigDict("ETSI.RFC3161", "")))
			p.sign(t, func(d []byte) []byte { return tc.edit(cmstest.Timestamp(tsa, d, signedAt)) })
			if s := onlySignature(t, p.buf, trusted()); s.Status != tc.status {
				t.Errorf("status %v, checks %+v", s.Status, s.Checks)
			}
		})
	}
}

func TestSignatureSignedBytesUnreadable(t *testing.T) {
	signature := signedDoc(t, cmstest.Options{}).buf
	stamp := newSigPDF()
	stamp.revision(baseObjects(sigDict("ETSI.RFC3161", "")))
	stamp.sign(t, func(d []byte) []byte { return cmstest.Timestamp(tsa, d, signedAt) })
	for _, tc := range []struct {
		name string
		doc  []byte
		read int
	}{
		{"first part", signature, 1},
		{"second part", signature, 2},
		{"document timestamp", stamp.buf, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer gopdfrab.FailSignedBytesRead(tc.read)()
			s := onlySignature(t, tc.doc, trusted())
			if s.Status != gopdfrab.SignatureInvalid {
				t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
			}
			wantCheck(t, s, gopdfrab.SignatureCheckIntegrity, gopdfrab.SignatureFailed)
		})
	}
}

func TestSignatureValidityPeriod(t *testing.T) {
	for _, tc := range []struct {
		name   string
		o      cmstest.IssueOptions
		status gopdfrab.SignatureStatus
		result gopdfrab.SignatureResult
		detail string
	}{
		{"not yet valid", cmstest.IssueOptions{NotBefore: signedAt.Add(24 * time.Hour)},
			gopdfrab.SignatureUntrusted, gopdfrab.SignatureFailed, "not yet valid"},
		{"expired since", cmstest.IssueOptions{NotAfter: signedAt.Add(24 * time.Hour)},
			gopdfrab.SignatureValid, gopdfrab.SignaturePassed, "has since expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := testRoot.Issue("Carol", tc.o)
			p := newSigPDF()
			p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
			p.sign(t, detached(id, cmstest.Options{}))
			s := onlySignature(t, p.buf, trusted())
			if s.Status != tc.status {
				t.Fatalf("status %v, checks %+v", s.Status, s.Checks)
			}
			wantCheck(t, s, gopdfrab.SignatureCheckValidity, tc.result)
			if c, _ := s.Check(gopdfrab.SignatureCheckValidity); !strings.Contains(c.Detail, tc.detail) {
				t.Errorf("detail %q", c.Detail)
			}
		})
	}
}

func TestSignatureRevocationPartlyChecked(t *testing.T) {
	ca := testRoot.Issue("Issuing CA", cmstest.IssueOptions{CA: true})
	bob := ca.Issue("Bob", cmstest.IssueOptions{})
	ocsp := cmstest.OCSP(ca, bob.Cert, signedAt, time.Time{})
	p := newSigPDF()
	p.revision(baseObjects(sigDict("adbe.pkcs7.detached", "")))
	p.sign(t, detached(bob, cmstest.Options{Chain: []*x509.Certificate{ca.Cert}, OCSPs: [][]byte{ocsp}}))
	s := onlySignature(t, p.buf, trusted())
	if c, _ := s.Check(gopdfrab.SignatureCheckRevocation); !strings.HasPrefix(c.Detail, "Partly checked") {
		t.Errorf("revocation %+v, checks %+v", c, s.Checks)
	}
}
