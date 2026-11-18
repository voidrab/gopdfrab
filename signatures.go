package gopdfrab

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/voidrab/gopdfrab/internal/cms"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// SignatureStatus is the verdict on a digital signature.
type SignatureStatus int

// Signature statuses, from the best to the worst. Status returns the worst
// that applies.
const (
	// SignatureValid is a signature that verifies, by a signer whose
	// certificate chains to a trusted root, over a document that has not
	// been changed since, other than by adding signatures and the data that
	// validates them.
	SignatureValid SignatureStatus = iota
	// SignatureUntrusted is a signature that verifies, by a signer whose
	// certificate does not chain to a trusted root, was not valid when it
	// signed, or may not sign documents, or with a timestamp that does not
	// verify.
	SignatureUntrusted
	// SignatureModified is a signature that verifies, over a document that
	// was changed after it was signed.
	SignatureModified
	// SignatureUnsupported is a signature that could not be checked: it uses
	// a format or an algorithm this package does not read, or lacks the
	// signer's certificate.
	SignatureUnsupported
	// SignatureInvalid is a signature that does not verify: the signed bytes
	// were changed, the signature value is wrong, or the signer's
	// certificate was revoked.
	SignatureInvalid
)

func (s SignatureStatus) String() string {
	switch s {
	case SignatureValid:
		return "valid"
	case SignatureUntrusted:
		return "untrusted"
	case SignatureModified:
		return "modified"
	case SignatureUnsupported:
		return "unsupported"
	case SignatureInvalid:
		return "invalid"
	}
	return "SignatureStatus(" + strconv.Itoa(int(s)) + ")"
}

// SignatureResult is the outcome of one SignatureCheck.
type SignatureResult int

// Signature check results.
const (
	SignaturePassed  SignatureResult = iota
	SignatureFailed                  // the check found a problem
	SignatureSkipped                 // the check could not be made, or did not apply
)

func (r SignatureResult) String() string {
	switch r {
	case SignaturePassed:
		return "passed"
	case SignatureFailed:
		return "failed"
	case SignatureSkipped:
		return "skipped"
	}
	return "SignatureResult(" + strconv.Itoa(int(r)) + ")"
}

// SignatureCheck is one of the checks a signature's status rests on.
type SignatureCheck struct {
	// Name says what was checked, one of the SignatureCheck names such as
	// "Integrity" or "Revocation".
	Name   string
	Result SignatureResult
	// Detail explains the result in a sentence.
	Detail string
}

// The names of the signature checks, in the order Signature.Checks lists
// them.
const (
	SignatureCheckIntegrity  = "Integrity"
	SignatureCheckValue      = "Signature"
	SignatureCheckTimestamp  = "Timestamp"
	SignatureCheckTrust      = "Certificate chain"
	SignatureCheckValidity   = "Validity at signing time"
	SignatureCheckKeyUsage   = "Key usage"
	SignatureCheckRevocation = "Revocation"
	SignatureCheckChanges    = "Changes after signing"
)

// SignatureWidget is where a signature field shows on a page.
type SignatureWidget struct {
	// Page is the zero-based index of the page.
	Page int
	// Box is the area, on the displayed page like Link.Box. It is empty for
	// an invisible signature.
	Box Rect
}

// SignatureTimestamp is an RFC 3161 timestamp: a time authority's signed
// statement of when it saw the signature, or for a document timestamp the
// document.
type SignatureTimestamp struct {
	// Time is when the authority saw it.
	Time time.Time
	// Authority is the authority's certificate, or nil when the token does
	// not carry it.
	Authority *x509.Certificate
	// Verified reports that the token's signature verifies and that it
	// stamps the signature it is attached to.
	Verified bool
	// Trusted reports that the authority's certificate chains to a trusted
	// root.
	Trusted bool
}

// SignatureTimeSource says where Signature.Time comes from.
type SignatureTimeSource string

// Time sources of Signature.Time.
const (
	SignatureTimeFromTimestamp SignatureTimeSource = "timestamp" // a timestamp token by a trusted authority, or a document timestamp's own
	SignatureTimeFromSigner    SignatureTimeSource = "signer"    // the signer's signing-time attribute
	SignatureTimeFromDocument  SignatureTimeSource = "document"  // the signature dictionary's M entry
)

// Signature is one signed signature field of a document and the verdict on
// it.
type Signature struct {
	// Field is the field's fully qualified name: the names of it and its
	// ancestors joined by periods. It is empty for a certification signature
	// that no field holds.
	Field string
	// Widgets are where the field shows; a field shows on no page, or with
	// an empty box, when it is invisible.
	Widgets []SignatureWidget
	// Name, Reason, Location and ContactInfo are what the signature
	// dictionary says of the signer and the signing.
	Name, Reason, Location, ContactInfo string
	// Time is when the document was signed, and TimeSource where that time
	// comes from; Time is zero when nothing says.
	Time       time.Time
	TimeSource SignatureTimeSource
	// SubFilter names the signature's encoding, such as
	// "adbe.pkcs7.detached" or "ETSI.CAdES.detached".
	SubFilter string
	// DocumentTimestamp reports a document timestamp (ETSI.RFC3161), which a
	// time authority rather than a person signs.
	DocumentTimestamp bool
	// Certifies is, for a certification signature, the changes its signer
	// permits after it: 1 none, 2 filling in forms and signing, 3 those and
	// annotating. It is 0 for an ordinary signature.
	Certifies int
	// ByteRange is the part of the file signed, as pairs of offset and
	// length.
	ByteRange [4]int64
	// Revision is the revision of the file the signature signs, counting
	// from 1, out of Revisions; 0 when no revision ends where the signed
	// bytes do.
	Revision, Revisions int
	// CoversDocument reports that the signed bytes reach the end of the
	// file: nothing was added after signing.
	CoversDocument bool
	// Signer is the signer's certificate, or nil when the signature does
	// not carry it.
	Signer *x509.Certificate
	// Chain runs from the signer's certificate towards its root: the chain
	// that was verified, or as much of one as the embedded certificates
	// give.
	Chain []*x509.Certificate
	// Timestamp is the signature's timestamp token, or nil.
	Timestamp *SignatureTimestamp
	// Status is the verdict, and Checks the checks it rests on.
	Status SignatureStatus
	Checks []SignatureCheck
}

// Check returns the check called name, and false if the signature has none.
func (s *Signature) Check(name string) (SignatureCheck, bool) {
	for _, c := range s.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return SignatureCheck{}, false
}

// systemCertPool and copyRange are x509.SystemCertPool and the Reader method,
// which tests replace to see the system pool missing and a read failing.
var (
	systemCertPool = x509.SystemCertPool
	copyRange      = (*pdf.Reader).CopyRange
)

// SignatureOptions tune Document.Signatures.
type SignatureOptions struct {
	// Roots are the trusted root certificates. nil means the system's; where
	// the system has none to offer, as in a browser, no signer is trusted.
	Roots *x509.CertPool
	// ExtraRoots, if not nil, are further trust anchors, tried for a chain
	// that does not reach Roots, such as the qualified authorities of a
	// trusted list. They need not be self-signed: a chain ends at the first
	// certificate in the pool. ExtraRootsName names them in a check's
	// detail, as in "the EU trusted lists".
	ExtraRoots     *x509.CertPool
	ExtraRootsName string
	// Now is the time to judge certificates by when a signature does not say
	// when it was made; zero means the current time.
	Now time.Time
}

// Signatures returns the document's signed signature fields, in the order of
// its form's field tree, each with the verdict on its signature. It works
// offline: revocation is checked only against the CRLs and OCSP responses the
// document carries, in its signatures and its document security store.
func (d *Document) Signatures(ctx context.Context, o SignatureOptions) ([]Signature, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.loadPages(); err != nil {
		return nil, err
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	roots, rootsNote := o.Roots, ""
	if roots == nil {
		var err error
		if roots, err = systemCertPool(); err != nil || roots == nil {
			roots, rootsNote = x509.NewCertPool(), "no trust store is available"
		}
	}
	acro, err := d.catalogEntry("AcroForm")
	if err != nil {
		return nil, err
	}
	form, _ := acro.(pdf.PDFDict)
	perms, _ := d.catalogEntry("Perms")
	permsDict, _ := perms.(pdf.PDFDict)
	fields := signatureFields(form, d.pages, permsDict)
	if len(fields) == 0 {
		return nil, nil
	}
	v := &sigVerifier{d: d, roots: roots, rootsNote: rootsNote, extra: o.ExtraRoots, extraName: o.ExtraRootsName,
		now: o.Now, ends: d.r.RevisionEnds(), form: form}
	v.loadDSS()
	// What a later revision may change rests on which later signatures
	// verify, so every signature is checked before any revision is judged.
	checks := make([]*sigCheck, len(fields))
	for i, f := range fields {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		checks[i] = v.signature(f)
	}
	v.checks = checks
	out := make([]Signature, len(checks))
	for i, c := range checks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.judgeChanges {
			c.changes()
		}
		c.s.Status = c.status()
		out[i] = *c.s
	}
	return out, nil
}

// maxFieldDepth bounds the walks of the field tree, which a crafted file may
// make deep or cyclic.
const maxFieldDepth = 32

// sigField is a signature field with its fully qualified name, or for a
// certification signature that no field holds, its signature dictionary
// alone.
type sigField struct {
	dict pdf.PDFDict // the field, or the zero dict
	sig  pdf.PDFDict // its value, the signature dictionary
	name string
	// certifies is the DocMDP access permissions (1 to 3) of a
	// certification signature, or 0.
	certifies int
}

// signatureFields returns the signed signature fields: those of the form's
// field tree, in tree order, whose field type, inherited or not, is Sig, and
// whose value is a signature dictionary; then those that pages show but the
// tree leaves out; then a certification signature in perms, the catalog's
// permissions, that no field holds.
func signatureFields(form pdf.PDFDict, pages []pageEntry, perms pdf.PDFDict) []sigField {
	var out []sigField
	seen := map[uintptr]bool{}    // fields
	sigSeen := map[uintptr]bool{} // signature dictionaries
	add := func(f pdf.PDFDict, name string) {
		v, ok := f.Entries.Get("V").(pdf.PDFDict)
		if !ok || sigSeen[pdf.ValuePointer(v.Entries)] {
			return
		}
		sigSeen[pdf.ValuePointer(v.Entries)] = true
		out = append(out, sigField{dict: f, sig: v, name: name})
	}
	var walk func(kids pdf.PDFValue, ft pdf.PDFValue, prefix string, depth int)
	walk = func(kids pdf.PDFValue, ft pdf.PDFValue, prefix string, depth int) {
		arr, _ := kids.(pdf.PDFArray)
		if depth > maxFieldDepth {
			return
		}
		for _, k := range arr {
			f, ok := k.(pdf.PDFDict)
			if !ok || seen[pdf.ValuePointer(f.Entries)] {
				continue
			}
			seen[pdf.ValuePointer(f.Entries)] = true
			fieldFT := ft
			if v := f.Entries.Get("FT"); v != nil {
				fieldFT = v
			}
			name := joinName(prefix, f)
			if fieldFT == (pdf.PDFName{Value: "Sig"}) {
				add(f, name)
			}
			walk(f.Entries.Get("Kids"), fieldFT, name, depth+1)
		}
	}
	walk(form.Entries.Get("Fields"), nil, "", 0)
	for _, p := range pages {
		annots, _ := p.dict.Entries.Get("Annots").(pdf.PDFArray)
		for _, a := range annots {
			w, ok := a.(pdf.PDFDict)
			if !ok || !isWidget(w) || seen[pdf.ValuePointer(w.Entries)] {
				continue
			}
			// The widget is the field, or a kid of it.
			f := w
			if _, ok := w.Entries.Get("T").(pdf.PDFString); !ok && w.Entries.Get("FT") == nil {
				if parent, ok := w.Entries.Get("Parent").(pdf.PDFDict); ok {
					f = parent
				}
			}
			if seen[pdf.ValuePointer(f.Entries)] || inheritedFT(f) != "Sig" {
				continue
			}
			seen[pdf.ValuePointer(f.Entries)] = true
			add(f, fullName(f))
		}
	}
	mdp, _ := perms.Entries.Get("DocMDP").(pdf.PDFDict)
	if mdp.Entries != nil {
		ptr := pdf.ValuePointer(mdp.Entries)
		found := false
		for i := range out {
			if pdf.ValuePointer(out[i].sig.Entries) == ptr {
				out[i].certifies, found = docMDP(mdp), true
			}
		}
		if !found {
			out = append(out, sigField{sig: mdp, certifies: docMDP(mdp)})
		}
	}
	return out
}

// docMDP returns the access permissions, 1 to 3, of the certification
// signature sig: its DocMDP transform's P, which defaults to 2.
func docMDP(sig pdf.PDFDict) int {
	refs, _ := sig.Entries.Get("Reference").(pdf.PDFArray)
	for _, r := range refs {
		ref, ok := r.(pdf.PDFDict)
		if m, _ := ref.Entries.Get("TransformMethod").(pdf.PDFName); !ok || m.Value != "DocMDP" {
			continue
		}
		params, _ := ref.Entries.Get("TransformParams").(pdf.PDFDict)
		if p, ok := params.Entries.Get("P").(pdf.PDFInteger); ok && p >= 1 && p <= 3 {
			return int(p)
		}
	}
	return 2
}

// joinName returns the name of field f, whose parent's name is prefix.
func joinName(prefix string, f pdf.PDFDict) string {
	t := pdf.DecodeInfoTextString(f.Entries.Get("T"))
	switch {
	case t == "":
		return prefix
	case prefix == "":
		return t
	}
	return prefix + "." + t
}

// fullName returns the fully qualified name of field f, from its ancestors.
func fullName(f pdf.PDFDict) string {
	var parts []string
	for depth := 0; f.Entries != nil && depth < maxFieldDepth; depth++ {
		if t := pdf.DecodeInfoTextString(f.Entries.Get("T")); t != "" {
			parts = append([]string{t}, parts...)
		}
		f, _ = f.Entries.Get("Parent").(pdf.PDFDict)
	}
	return strings.Join(parts, ".")
}

// inheritedFT returns the field type of f, its own or an ancestor's.
func inheritedFT(f pdf.PDFDict) string {
	for depth := 0; f.Entries != nil && depth < maxFieldDepth; depth++ {
		if ft, ok := f.Entries.Get("FT").(pdf.PDFName); ok {
			return ft.Value
		}
		f, _ = f.Entries.Get("Parent").(pdf.PDFDict)
	}
	return ""
}

// sigVerifier holds what checking a document's signatures shares.
type sigVerifier struct {
	d         *Document
	roots     *x509.CertPool
	rootsNote string // why no root is trusted, or empty
	extra     *x509.CertPool
	extraName string
	now       time.Time
	ends      []int64 // where the file's revisions end

	// From the document security store.
	dssCerts []*x509.Certificate
	dssPool  *x509.CertPool // dssCerts, for chain building
	dssCRLs  [][]byte
	dssOCSPs [][]byte

	form   pdf.PDFDict // the interactive form
	checks []*sigCheck // every signature's check, once all are made
}

// loadDSS reads the certificates and revocation data of the catalog's
// document security store (ISO 32000-2 12.8.4.3).
func (v *sigVerifier) loadDSS() {
	v.dssPool = x509.NewCertPool()
	dssVal, _ := v.d.catalogEntry("DSS")
	dss, ok := dssVal.(pdf.PDFDict)
	if !ok {
		return
	}
	streams := func(key string) [][]byte {
		arr, _ := dss.Entries.Get(key).(pdf.PDFArray)
		var out [][]byte
		for _, e := range arr {
			if s, ok := e.(pdf.PDFDict); ok && s.IsStream() {
				if b, err := v.d.r.DecodeStreamCached(s); err == nil {
					out = append(out, b)
				}
			}
		}
		return out
	}
	for _, b := range streams("Certs") {
		if c, err := x509.ParseCertificate(b); err == nil {
			v.dssCerts = append(v.dssCerts, c)
			v.dssPool.AddCert(c)
		}
	}
	v.dssCRLs, v.dssOCSPs = streams("CRLs"), streams("OCSPs")
}

// signature checks the signature of field f, all but the changes after it,
// which need every signature checked first.
func (v *sigVerifier) signature(f sigField) *sigCheck {
	sig := f.sig
	s := Signature{
		Field:       f.name,
		Certifies:   f.certifies,
		Name:        sigText(sig.Entries.Get("Name")),
		Reason:      sigText(sig.Entries.Get("Reason")),
		Location:    sigText(sig.Entries.Get("Location")),
		ContactInfo: sigText(sig.Entries.Get("ContactInfo")),
		Revisions:   len(v.ends),
	}
	if f.dict.Entries != nil {
		s.Widgets = v.d.widgetsOf(f.dict)
	}
	if sf, ok := sig.Entries.Get("SubFilter").(pdf.PDFName); ok {
		s.SubFilter = sf.Value
	}
	typ, _ := sig.Entries.Get("Type").(pdf.PDFName)
	s.DocumentTimestamp = typ.Value == "DocTimeStamp" || s.SubFilter == "ETSI.RFC3161"
	if m, ok := parsePDFDate(pdf.DecodeInfoTextString(sig.Entries.Get("M"))); ok {
		s.Time, s.TimeSource = m, SignatureTimeFromDocument
	}
	c := &sigCheck{v: v, s: &s, field: f}
	c.run(sig)
	return c
}

// sigCheck is the check of one signature under way.
type sigCheck struct {
	v     *sigVerifier
	s     *Signature
	field sigField

	// verified reports that the signature value verifies over the signed
	// bytes, and judgeChanges that the changes after them are to be judged.
	verified, judgeChanges bool

	invalid, unsupported, modified, untrusted bool
	trustedTime                               bool // s.Time comes from a timestamp by a trusted authority
	certs                                     []*x509.Certificate
	crls, ocsps                               [][]byte
}

func (c *sigCheck) add(name string, r SignatureResult, detail string) {
	c.s.Checks = append(c.s.Checks, SignatureCheck{Name: name, Result: r, Detail: detail})
}

// skip records a check that could not be made, which leaves the signature
// unsupported.
func (c *sigCheck) skip(name, detail string) {
	c.unsupported = true
	c.add(name, SignatureSkipped, detail)
}

// fail records a failed check, which makes the signature invalid.
func (c *sigCheck) fail(name, detail string) {
	c.invalid = true
	c.add(name, SignatureFailed, detail)
}

// status returns the worst verdict the checks found.
func (c *sigCheck) status() SignatureStatus {
	switch {
	case c.invalid:
		return SignatureInvalid
	case c.unsupported:
		return SignatureUnsupported
	case c.modified:
		return SignatureModified
	case c.untrusted:
		return SignatureUntrusted
	}
	return SignatureValid
}

// run makes the checks on the signature dictionary sig.
func (c *sigCheck) run(sig pdf.PDFDict) {
	s, d := c.s, c.v.d
	br, ok := pdf.ByteRangeOf(sig)
	if !ok || br[0] != 0 || br[1] > br[2] || br[2] > d.r.Size() || br[3] > d.r.Size()-br[2] {
		c.fail(SignatureCheckIntegrity, "The signature's byte range is missing or does not fit the file.")
		return
	}
	s.ByteRange = br
	contents, ok := c.contents(br)
	if !ok {
		c.fail(SignatureCheckIntegrity, "The bytes the signature leaves out are not just its own value.")
		return
	}
	c.coverage(br)
	if s.SubFilter == "adbe.x509.rsa_sha1" {
		c.skip(SignatureCheckValue, "Signatures in the adbe.x509.rsa_sha1 format are not supported.")
		return
	}
	if s.DocumentTimestamp {
		c.documentTimestamp(br, contents)
	} else if !c.cmsSignature(br, contents) {
		return
	}
	if s.Signer != nil {
		c.trust()
		c.revocation()
	}
	c.judgeChanges = true
}

// maxContentsGap bounds the bytes a byte range may leave out: a hex string of
// 16 MiB of signature value, far more than any signature with its revocation
// data takes, so that a crafted byte range cannot make contents read a file's
// worth of bytes into memory.
const maxContentsGap = 32 << 20

// contents returns the signature value, from the bytes the byte range leaves
// out, which must be a hex string and nothing more. Reading the file rather
// than the parsed dictionary keeps an encrypted file's value as it is
// written, since signature values are not encrypted.
func (c *sigCheck) contents(br [4]int64) ([]byte, bool) {
	if br[2]-br[1] > maxContentsGap {
		return nil, false
	}
	gap, err := c.v.d.r.ReadRange(br[1], br[2]-br[1])
	if err != nil || len(gap) < 2 || gap[0] != '<' || gap[len(gap)-1] != '>' {
		return nil, false
	}
	digits := gap[1 : len(gap)-1]
	if bytes.IndexByte(digits, '>') >= 0 {
		return nil, false
	}
	out, err := pdf.DecodeASCIIHex(digits)
	if err != nil {
		return nil, false
	}
	return out, true
}

// coverage finds the revision the byte range signs and whether it reaches
// the end of the file.
func (c *sigCheck) coverage(br [4]int64) {
	end := br[2] + br[3]
	last := int64(0)
	for i, e := range c.v.ends {
		// The signed bytes may leave out the EOL marker after %%EOF.
		if e >= end && e <= end+2 && c.s.Revision == 0 {
			c.s.Revision = i + 1
		}
		last = max(last, e)
	}
	c.s.CoversDocument = end >= max(last, c.v.d.r.Size())-2
}

// digest returns the hash under h of the bytes br signs.
func (c *sigCheck) digest(h crypto.Hash, br [4]int64) ([]byte, error) {
	w := h.New()
	if err := copyRange(c.v.d.r, w, br[0], br[1]); err != nil {
		return nil, err
	}
	if err := copyRange(c.v.d.r, w, br[2], br[3]); err != nil {
		return nil, err
	}
	return w.Sum(nil), nil
}

// cmsSignature checks a PKCS #7 or CAdES signature, and reports whether its
// signer could be found to check further.
func (c *sigCheck) cmsSignature(br [4]int64, contents []byte) bool {
	s := c.s
	sd, err := cms.Parse(contents)
	if err != nil {
		c.skip(SignatureCheckValue, "The signature value could not be read: "+cmsReason(err)+".")
		return false
	}
	c.certs, c.crls = sd.Certificates, sd.CRLs
	if len(sd.Signers) == 0 {
		c.fail(SignatureCheckValue, "The signature value names no signer.")
		return false
	}
	signer := sd.Signers[0]
	s.Signer = signer.Certificate
	c.ocsps, c.crls = append(c.ocsps, signer.OCSPs...), append(c.crls, signer.CRLs...)
	if !signer.SigningTime.IsZero() {
		s.Time, s.TimeSource = signer.SigningTime, SignatureTimeFromSigner
	}
	if signer.Hash == 0 || !signer.Hash.Available() {
		c.skip(SignatureCheckIntegrity, "The signature uses a digest algorithm that is not supported.")
		return false
	}
	sha1 := s.SubFilter == "adbe.pkcs7.sha1"
	if !sha1 && sd.Content != nil {
		c.skip(SignatureCheckValue, "The signature encapsulates content, which this format should not.")
		return false
	}
	// For adbe.pkcs7.sha1 the signed content is the SHA-1 digest of the
	// signed bytes, and the signature covers that content.
	h := signer.Hash
	if sha1 {
		h = crypto.SHA1
	}
	digest, err := c.digest(h, br)
	if err != nil {
		c.fail(SignatureCheckIntegrity, "The signed bytes could not be read.")
		return false
	}
	if sha1 {
		if !bytes.Equal(sd.Content, digest) {
			c.fail(SignatureCheckIntegrity, "The document was changed after it was signed: its digest does not match.")
			return false
		}
		digest = cms.Sum(signer.Hash, sd.Content)
	}
	err = signer.Verify(digest)
	switch {
	case errors.Is(err, cms.ErrDigestMismatch):
		c.fail(SignatureCheckIntegrity, "The signed bytes were changed: their digest does not match the signature's.")
		return false
	case errors.Is(err, cms.ErrNoCertificate):
		c.skip(SignatureCheckValue, "The signature does not carry the signer's certificate.")
		return false
	case errors.Is(err, cms.ErrUnsupported):
		c.skip(SignatureCheckValue, "The signature could not be checked: "+cmsReason(err)+".")
		return false
	case err != nil:
		c.invalid = true
		c.add(SignatureCheckIntegrity, SignaturePassed, "The signed bytes are unchanged.")
		c.add(SignatureCheckValue, SignatureFailed, "The signature value does not verify with the signer's key.")
		return false
	}
	c.verified = true
	c.add(SignatureCheckIntegrity, SignaturePassed, "The signed bytes are unchanged.")
	c.add(SignatureCheckValue, SignaturePassed, "The signature verifies with the signer's key ("+keyDescription(signer.Certificate)+").")
	if signer.TimestampToken != nil {
		c.timestamp(signer)
	}
	return true
}

// timestamp checks the timestamp token of signer. Its time is relied on only
// when its authority is trusted: anyone can make a token for any time.
func (c *sigCheck) timestamp(signer *cms.Signer) {
	ts, err := cms.ParseTimestamp(signer.TimestampToken)
	if err != nil {
		c.add(SignatureCheckTimestamp, SignatureSkipped, "The timestamp could not be read: "+cmsReason(err)+".")
		return
	}
	c.s.Timestamp = &SignatureTimestamp{Time: ts.Time, Authority: ts.Signer.Certificate}
	c.certs = append(c.certs, ts.Certificates...)
	if err := ts.Verify(signer.Signature); err != nil {
		c.untrusted = true
		c.add(SignatureCheckTimestamp, SignatureFailed, "The timestamp does not verify: "+cmsReason(err)+".")
		return
	}
	c.s.Timestamp.Verified = true
	who := "an unknown authority"
	if a := ts.Signer.Certificate; a != nil {
		who = displayName(a)
		_, _, err := c.chain(a, ts.Time, x509.ExtKeyUsageTimeStamping, ts.Certificates)
		c.s.Timestamp.Trusted = err == nil
	}
	if !c.s.Timestamp.Trusted {
		c.add(SignatureCheckTimestamp, SignaturePassed, "Timestamped by "+who+", which is not trusted, so the time it gives is not relied on.")
		return
	}
	c.s.Time, c.s.TimeSource, c.trustedTime = ts.Time, SignatureTimeFromTimestamp, true
	c.add(SignatureCheckTimestamp, SignaturePassed, "Timestamped by "+who+".")
}

// documentTimestamp checks a document timestamp: an RFC 3161 token over the
// signed bytes.
func (c *sigCheck) documentTimestamp(br [4]int64, contents []byte) {
	ts, err := cms.ParseTimestamp(contents)
	if err != nil {
		c.skip(SignatureCheckValue, "The timestamp could not be read: "+cmsReason(err)+".")
		return
	}
	c.certs = ts.Certificates
	c.s.Signer = ts.Signer.Certificate
	c.s.Timestamp = &SignatureTimestamp{Time: ts.Time, Authority: ts.Signer.Certificate}
	digest, err := c.digest(ts.ImprintHash, br)
	if err != nil {
		c.fail(SignatureCheckIntegrity, "The signed bytes could not be read.")
		return
	}
	err = ts.VerifyDigest(digest)
	switch {
	case errors.Is(err, cms.ErrDigestMismatch):
		c.fail(SignatureCheckIntegrity, "The timestamped bytes were changed: their digest does not match the timestamp's.")
		return
	case errors.Is(err, cms.ErrUnsupported), errors.Is(err, cms.ErrNoCertificate):
		c.unsupported = true
		c.s.Signer = nil
		c.add(SignatureCheckValue, SignatureSkipped, "The timestamp could not be checked: "+cmsReason(err)+".")
		return
	case err != nil:
		c.invalid = true
		c.add(SignatureCheckIntegrity, SignaturePassed, "The timestamped bytes are unchanged.")
		c.add(SignatureCheckValue, SignatureFailed, "The timestamp's signature does not verify with the authority's key.")
		return
	}
	// Its time is trusted once trust finds its authority's chain.
	c.s.Timestamp.Verified = true
	c.s.Time, c.s.TimeSource, c.verified = ts.Time, SignatureTimeFromTimestamp, true
	c.add(SignatureCheckIntegrity, SignaturePassed, "The timestamped bytes are unchanged.")
	c.add(SignatureCheckValue, SignaturePassed, "The timestamp verifies with the authority's key ("+keyDescription(c.s.Signer)+").")
}

// chain verifies cert's chain at t for usage, with extra certificates to
// build it from: to the trusted roots, or failing that to the extra roots,
// which fromExtra reports.
func (c *sigCheck) chain(cert *x509.Certificate, t time.Time, usage x509.ExtKeyUsage, extra []*x509.Certificate) (chain []*x509.Certificate, fromExtra bool, err error) {
	inter := c.v.dssPool.Clone()
	for _, list := range [][]*x509.Certificate{c.certs, extra} {
		for _, x := range list {
			inter.AddCert(x)
		}
	}
	opts := x509.VerifyOptions{Roots: c.v.roots, Intermediates: inter, CurrentTime: t, KeyUsages: []x509.ExtKeyUsage{usage}}
	chains, err := cert.Verify(opts)
	var unknown x509.UnknownAuthorityError
	if err != nil && errors.As(err, &unknown) && c.v.extra != nil {
		opts.Roots, fromExtra = c.v.extra, true
		chains, err = cert.Verify(opts)
	}
	if err != nil {
		return nil, false, err
	}
	return chains[0], fromExtra, nil
}

// when returns the time to judge the signer's certificate at.
func (c *sigCheck) when() time.Time {
	if c.s.Time.IsZero() {
		return c.v.now
	}
	return c.s.Time
}

// trust checks the signer's certificate: its chain to a trusted root, its
// validity when it signed and its key usage.
func (c *sigCheck) trust() {
	s := c.s
	t := c.when()
	usage := x509.ExtKeyUsageAny
	if s.DocumentTimestamp {
		usage = x509.ExtKeyUsageTimeStamping
	}
	chain, fromExtra, err := c.chain(s.Signer, t, usage, nil)
	if err == nil {
		anchor := chain[len(chain)-1]
		detail := "The certificate chains to the trusted root " + displayName(anchor) + "."
		if fromExtra {
			// An anchor of a trusted list is often an issuing CA: the
			// certificates the signature carries above it still show.
			if rest := c.naiveChain(anchor); len(rest) > 1 {
				chain = append(chain, rest[1:]...)
			}
			detail = "The certificate chains to " + displayName(anchor) + ", a trusted authority"
			if c.v.extraName != "" {
				detail += " of " + c.v.extraName
			}
			detail += "."
		}
		s.Chain = chain
		if s.DocumentTimestamp && s.Timestamp != nil {
			s.Timestamp.Trusted, c.trustedTime = true, true
		}
		c.add(SignatureCheckTrust, SignaturePassed, detail)
	} else {
		s.Chain = c.naiveChain(s.Signer)
		c.untrusted = true
		c.add(SignatureCheckTrust, SignatureFailed, c.trustProblem(err))
	}
	switch {
	case t.Before(s.Signer.NotBefore):
		c.untrusted = true
		c.add(SignatureCheckValidity, SignatureFailed, "The certificate was not yet valid when the document was signed.")
	case t.After(s.Signer.NotAfter):
		c.untrusted = true
		c.add(SignatureCheckValidity, SignatureFailed, "The certificate had expired when the document was signed.")
	case c.v.now.After(s.Signer.NotAfter):
		c.add(SignatureCheckValidity, SignaturePassed, "The certificate was valid when the document was signed. It has since expired.")
	default:
		c.add(SignatureCheckValidity, SignaturePassed, "The certificate was valid when the document was signed.")
	}
	if !s.DocumentTimestamp {
		ku := s.Signer.KeyUsage
		if ku != 0 && ku&(x509.KeyUsageDigitalSignature|x509.KeyUsageContentCommitment) == 0 {
			c.untrusted = true
			c.add(SignatureCheckKeyUsage, SignatureFailed, "The certificate may not be used to sign documents.")
		} else {
			c.add(SignatureCheckKeyUsage, SignaturePassed, "The certificate may be used to sign documents.")
		}
	}
}

// trustProblem explains why the signer's chain did not verify.
func (c *sigCheck) trustProblem(err error) string {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &unknown):
		note := ""
		if c.v.rootsNote != "" {
			note = " (" + c.v.rootsNote + ")"
		}
		if bytes.Equal(c.s.Signer.RawIssuer, c.s.Signer.RawSubject) {
			return "The certificate is self-signed and not trusted" + note + "."
		}
		return "The certificate is not issued by a trusted authority" + note + "."
	case errors.As(err, &invalid):
		switch invalid.Reason {
		case x509.Expired:
			return "A certificate in the chain was not valid when the document was signed."
		case x509.IncompatibleUsage:
			return "A certificate in the chain may not be used for this purpose."
		case x509.NotAuthorizedToSign:
			return "A certificate in the chain is not a certificate authority."
		}
	}
	return "The certificate chain does not verify: " + err.Error() + "."
}

// naiveChain follows issuers from cert through the certificates at hand, to
// show as much of a chain as there is when none verifies.
func (c *sigCheck) naiveChain(cert *x509.Certificate) []*x509.Certificate {
	pool := append(append([]*x509.Certificate{}, c.certs...), c.v.dssCerts...)
	chain := []*x509.Certificate{cert}
	for len(chain) < 10 {
		cur := chain[len(chain)-1]
		if bytes.Equal(cur.RawIssuer, cur.RawSubject) {
			break
		}
		var next *x509.Certificate
		for _, x := range pool {
			if bytes.Equal(x.RawSubject, cur.RawIssuer) && !slices.Contains(chain, x) && cur.CheckSignatureFrom(x) == nil {
				next = x
				break
			}
		}
		if next == nil {
			break
		}
		chain = append(chain, next)
	}
	return chain
}

// revocation checks each certificate of the chain below its root against
// the CRLs and OCSP responses the document carries.
func (c *sigCheck) revocation() {
	chain := c.s.Chain
	crls := append(append([][]byte{}, c.crls...), c.v.dssCRLs...)
	ocsps := append(append([][]byte{}, c.ocsps...), c.v.dssOCSPs...)
	checked, unchecked := 0, 0
	for i := 0; i+1 < len(chain); i++ {
		cert, issuer := chain[i], chain[i+1]
		revoked, at, found := revokedBy(cert, issuer, crls, ocsps)
		switch {
		case !found:
			unchecked++
		case revoked && (!c.trustedTime || !at.After(c.when())):
			c.fail(SignatureCheckRevocation, fmt.Sprintf("The certificate of %s was revoked on %s.",
				displayName(cert), at.UTC().Format("2 Jan 2006")))
			return
		default:
			checked++
		}
	}
	switch {
	case len(chain) < 2:
		c.add(SignatureCheckRevocation, SignatureSkipped, "Not checked: the certificate's issuer is not at hand.")
	case unchecked == 0:
		c.add(SignatureCheckRevocation, SignaturePassed, "Not revoked, by the revocation data the document carries.")
	case checked == 0:
		c.add(SignatureCheckRevocation, SignatureSkipped, "Not checked: the document carries no revocation data, and none is fetched.")
	default:
		c.add(SignatureCheckRevocation, SignatureSkipped, "Partly checked: the document carries revocation data for only some of the chain.")
	}
}

// revokedBy looks for cert, issued by issuer, in the CRLs and OCSP responses.
// found reports that one of them covers it.
func revokedBy(cert, issuer *x509.Certificate, crls, ocsps [][]byte) (revoked bool, at time.Time, found bool) {
	for _, b := range ocsps {
		r, err := cms.ParseOCSP(b)
		if err != nil {
			continue
		}
		one, ok, err := r.For(cert, issuer)
		if !ok || err != nil || one.Status == cms.OCSPUnknown {
			continue
		}
		if one.Status == cms.OCSPRevoked {
			return true, one.RevokedAt, true
		}
		found = true
	}
	for _, b := range crls {
		l, err := x509.ParseRevocationList(b)
		if err != nil || !bytes.Equal(l.RawIssuer, issuer.RawSubject) || l.CheckSignatureFrom(issuer) != nil {
			continue
		}
		for _, e := range l.RevokedCertificateEntries {
			if e.SerialNumber.Cmp(cert.SerialNumber) == 0 {
				return true, e.RevocationTime, true
			}
		}
		found = true
	}
	return false, time.Time{}, found
}

// changes judges what was added to the file after the signed bytes: nothing,
// only further signatures and the data that validates them, or other
// changes.
func (c *sigCheck) changes() {
	if c.s.CoversDocument {
		c.add(SignatureCheckChanges, SignaturePassed, "The signature covers the whole document.")
		return
	}
	// The signed bytes end a revision, which is read as a file of its own.
	// The document is read afresh too: its graph, resolved in place, no
	// longer shows which arrays are objects of their own.
	end := c.s.ByteRange[2] + c.s.ByteRange[3]
	old, err := c.v.d.r.Prefix(end)
	cur, errCur := c.v.d.r.Prefix(c.v.d.r.Size())
	for _, rd := range []*pdf.Reader{old, cur} {
		if rd != nil {
			defer rd.Close()
		}
	}
	if err != nil || errCur != nil {
		c.modified = true
		c.add(SignatureCheckChanges, SignatureFailed, "The document was changed after it was signed: the signed bytes are no revision that can be read.")
		return
	}
	other := c.v.newRevisionDiff(old, cur, end).changes()
	if other == 0 {
		c.add(SignatureCheckChanges, SignaturePassed, "Later revisions only add signatures and the data that validates them.")
		return
	}
	c.modified = true
	c.add(SignatureCheckChanges, SignatureFailed, fmt.Sprintf("The document was changed after it was signed: %s added or replaced.",
		plural(other, "object was", "objects were")))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// revisionDiff compares the document with an earlier revision of it, the one
// a signature signs, object by object.
type revisionDiff struct {
	v        *sigVerifier
	old, cur *pdf.Reader // the earlier revision, and the document
	// changed are the objects written after it, and allowed those of them
	// that may differ: the document security store and metadata, the
	// cross-reference and object streams, objects new to the form's default
	// resources, and the fields of later signatures that verify, as their own
	// revisions wrote them.
	changed, allowed map[int]bool
	// addable are the fields and widgets of those signatures, which the
	// form's field list and a page's annotations may gain.
	addable   map[int]bool
	root      pdf.PDFRef
	comparing map[int]bool // objects whose comparison is under way
}

func (v *sigVerifier) newRevisionDiff(old, cur *pdf.Reader, end int64) *revisionDiff {
	r := &revisionDiff{v: v, old: old, cur: cur, changed: map[int]bool{}, allowed: map[int]bool{},
		addable: map[int]bool{}, comparing: map[int]bool{}}
	for _, n := range cur.ObjectsFrom(end) {
		r.changed[n] = true
	}
	r.root, _ = cur.Trailer().Entries.Get("Root").(pdf.PDFRef)
	if n, ok := objNum(cur.Trailer().Entries.Get("Info")); ok {
		r.allowed[n] = true
	}
	for n := range r.changed {
		if d, ok := resolve(cur, pdf.PDFRef{ObjNum: n}).(pdf.PDFDict); ok {
			if t, _ := d.Entries.Get("Type").(pdf.PDFName); t.Value == "XRef" || t.Value == "ObjStm" {
				r.allowed[n] = true
			}
		}
	}
	catalog, _ := resolve(cur, r.root).(pdf.PDFDict)
	r.mark(catalog.Entries.Get("DSS"), nil)
	r.mark(catalog.Entries.Get("Metadata"), nil)
	// The form's default resources, and what a later signature's dictionary
	// and appearances reach, may gain objects but not change those the
	// earlier revision has: a font they share with the pages must stay.
	added := func(n int) bool { return !old.IsDefined(n) }
	form, _ := resolve(cur, catalog.Entries.Get("AcroForm")).(pdf.PDFDict)
	r.mark(form.Entries.Get("DR"), added)
	for _, c := range v.checks {
		fEnd := c.s.ByteRange[2] + c.s.ByteRange[3]
		num, ok := objNum(c.field.dict)
		if !c.verified || fEnd <= end || !ok {
			continue
		}
		// Only the objects the later signature signs: those written after
		// this signature and before the later one's end.
		later := map[int]bool{}
		for _, n := range cur.ObjectsFrom(fEnd) {
			later[n] = true
		}
		signed := func(n int) bool { return r.changed[n] && !later[n] }
		signedAdded := func(n int) bool { return signed(n) && added(n) }
		// The field and its widgets may be ones the earlier revision left
		// unsigned.
		one := func(val pdf.PDFValue) {
			if n, ok := objNum(val); ok && signed(n) {
				r.allowed[n], r.addable[n] = true, true
			}
		}
		ref := pdf.PDFRef{ObjNum: num}
		field, _ := resolve(cur, ref).(pdf.PDFDict)
		one(ref)
		r.mark(field.Entries.Get("V"), signedAdded)
		widgets := []pdf.PDFValue{ref}
		if !isWidget(field) {
			kids, _ := resolve(cur, field.Entries.Get("Kids")).(pdf.PDFArray)
			widgets = kids
		}
		for _, w := range widgets {
			wd, _ := resolve(cur, w).(pdf.PDFDict)
			if !isWidget(wd) {
				continue
			}
			one(w)
			r.mark(wd.Entries.Get("AP"), signedAdded)
			r.mark(wd.Entries.Get("MK"), signedAdded)
		}
	}
	return r
}

// mark allows the indirect objects of the document's subgraph from val, other
// than through the Parent and P entries that lead back up, that keep accepts,
// or all of them when keep is nil.
func (r *revisionDiff) mark(val pdf.PDFValue, keep func(int) bool) {
	seen := map[int]bool{}
	for todo := []pdf.PDFValue{val}; len(todo) > 0; {
		val, todo = todo[len(todo)-1], todo[:len(todo)-1]
		if ref, ok := val.(pdf.PDFRef); ok {
			if seen[ref.ObjNum] {
				continue
			}
			seen[ref.ObjNum] = true
			if keep == nil || keep(ref.ObjNum) {
				r.allowed[ref.ObjNum] = true
			}
		}
		switch x := resolve(r.cur, val).(type) {
		case pdf.PDFDict:
			for k, e := range x.Entries.All() {
				if k != "Parent" && k != "P" {
					todo = append(todo, e)
				}
			}
		case pdf.PDFArray:
			todo = append(todo, x...)
		}
	}
}

// changes returns how many of the objects written after the earlier revision
// change what it holds: those not allowed to differ that the earlier revision
// did not hold, or held differently. The catalog, the form and the pages may
// differ in the fields and annotations of later signatures alone.
func (r *revisionDiff) changes() int {
	n := 0
	done := map[int]bool{}
	// The catalog and the pages first, even when not written again: comparing
	// them allows their field and annotation lists the signatures' growth.
	nums := []int{r.root.ObjNum}
	for _, p := range r.v.d.pages {
		if num, ok := objNum(p.dict); ok {
			nums = append(nums, num)
		}
	}
	for _, num := range nums {
		if done[num] || !r.old.IsDefined(num) {
			continue
		}
		done[num] = true
		ref := pdf.PDFRef{ObjNum: num}
		da, okA := resolve(r.old, ref).(pdf.PDFDict)
		db, okB := resolve(r.cur, ref).(pdf.PDFDict)
		r.comparing[num] = true
		same := okA && okB && r.sameDict(da, db, num == r.root.ObjNum, 0)
		delete(r.comparing, num)
		if r.changed[num] && !r.allowed[num] && !same {
			n++
		}
	}
	for num := range r.changed {
		if done[num] || r.allowed[num] {
			continue
		}
		ref := pdf.PDFRef{ObjNum: num}
		if !r.old.IsDefined(num) || !r.same(ref, ref, 0) {
			n++
		}
	}
	return n
}

// resolve returns val, read from rd when it is a reference. An opened reader
// resolves an object it cannot read to null rather than fail.
func resolve(rd *pdf.Reader, val pdf.PDFValue) pdf.PDFValue {
	if ref, ok := val.(pdf.PDFRef); ok {
		val, _ = rd.ResolveReference(ref)
	}
	return val
}

// same reports whether a, a value of the earlier revision, and b, its
// counterpart in the document, hold the same. An object that was not
// written again, or may differ, is the same as itself.
func (r *revisionDiff) same(a, b pdf.PDFValue, depth int) bool {
	if depth > 64 {
		return false
	}
	an, aInd := objNum(a)
	bn, bInd := objNum(b)
	if aInd != bInd || aInd && an != bn {
		return false
	}
	if aInd {
		if !r.changed[an] || r.allowed[an] || r.comparing[an] {
			// Unchanged, allowed, or already being compared further up.
			return true
		}
		r.comparing[an] = true
		defer delete(r.comparing, an)
	}
	a, b = resolve(r.old, a), resolve(r.cur, b)
	switch x := a.(type) {
	case pdf.PDFDict:
		y, ok := b.(pdf.PDFDict)
		return ok && r.sameDict(x, y, aInd && an == r.root.ObjNum, depth)
	case pdf.PDFArray:
		y, ok := b.(pdf.PDFArray)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !r.same(x[i], y[i], depth+1) {
				return false
			}
		}
		return true
	}
	return pdf.EqualPDFValue(a, b)
}

// sameDict compares two dictionaries, or streams, as same does. A catalog,
// a form and a page are allowed what signing changes in them.
func (r *revisionDiff) sameDict(a, b pdf.PDFDict, catalog bool, depth int) bool {
	if a.IsStream() != b.IsStream() || !bytes.Equal(a.Stream(), b.Stream()) {
		return false
	}
	t, _ := a.Entries.Get("Type").(pdf.PDFName)
	keys := map[string]bool{}
	for _, d := range []pdf.PDFDict{a, b} {
		for k := range d.Entries.All() {
			keys[k] = true
		}
	}
	for k := range keys {
		x, y := a.Entries.Get(k), b.Entries.Get(k)
		var same bool
		switch {
		case catalog && (k == "DSS" || k == "Metadata" || k == "Extensions"):
			same = true
		case catalog && k == "AcroForm":
			same = r.sameForm(x, y, depth+1)
		case t.Value == "Page" && k == "Annots":
			same = r.sameList(x, y, depth+1)
		default:
			same = r.same(x, y, depth+1)
		}
		if !same {
			return false
		}
	}
	return true
}

// sameForm compares the form of the earlier revision, which may have had
// none, with the document's: its field list may gain the fields of later
// signatures, and its signature flags and default resources may differ.
func (r *revisionDiff) sameForm(a, b pdf.PDFValue, depth int) bool {
	fa, _ := resolve(r.old, a).(pdf.PDFDict)
	fb, ok := resolve(r.cur, b).(pdf.PDFDict)
	if !ok {
		return false
	}
	skip := func(k string) bool { return k == "SigFlags" || k == "DR" || k == "Fields" }
	for _, d := range []pdf.PDFDict{fa, fb} {
		for k := range d.Entries.All() {
			if !skip(k) && !r.same(fa.Entries.Get(k), fb.Entries.Get(k), depth+1) {
				return false
			}
		}
	}
	if !r.sameList(fa.Entries.Get("Fields"), fb.Entries.Get("Fields"), depth+1) {
		return false
	}
	r.allowObject(b)
	return true
}

// sameList compares a list of fields or annotations of the earlier revision
// with the document's: it may gain the fields and widgets of later
// signatures, and lose nothing.
func (r *revisionDiff) sameList(a, b pdf.PDFValue, depth int) bool {
	x, y := resolve(r.old, a), resolve(r.cur, b)
	la, okA := x.(pdf.PDFArray)
	lb, okB := y.(pdf.PDFArray)
	if x != nil && !okA || y != nil && !okB {
		return false
	}
	matched := make([]bool, len(lb))
	for _, e := range la {
		i := slices.IndexFunc(lb, func(f pdf.PDFValue) bool { return sameObject(e, f) })
		if i < 0 || matched[i] || !r.same(e, lb[i], depth+1) {
			return false
		}
		matched[i] = true
	}
	for i, f := range lb {
		if n, ok := objNum(f); !matched[i] && (!ok || !r.addable[n]) {
			return false
		}
	}
	r.allowObject(b)
	return true
}

// sameObject reports whether a and b are the same indirect object.
func sameObject(a, b pdf.PDFValue) bool {
	n, ok := objNum(a)
	m, ok2 := objNum(b)
	return ok && ok2 && n == m
}

// allowObject allows val, when it is an object written again, to differ: a
// form or a list that compared as the same apart from what it may gain.
func (r *revisionDiff) allowObject(val pdf.PDFValue) {
	if n, ok := objNum(val); ok && r.changed[n] {
		r.allowed[n] = true
	}
}

// objNum returns the object number of a reference, or of a dictionary that is
// an indirect object.
func objNum(v pdf.PDFValue) (int, bool) {
	switch x := v.(type) {
	case pdf.PDFRef:
		return x.ObjNum, true
	case pdf.PDFDict:
		if ref, ok := x.Ref(); ok {
			return ref.ObjNum, true
		}
	}
	return 0, false
}

// isWidget reports whether d is a widget annotation.
func isWidget(d pdf.PDFDict) bool {
	st, _ := d.Entries.Get("Subtype").(pdf.PDFName)
	return st.Value == "Widget"
}

// widgetDicts returns the widget annotations of field f: f itself when it is
// merged with its widget, otherwise its kids that are widgets.
func widgetDicts(f pdf.PDFDict) []pdf.PDFDict {
	if isWidget(f) {
		return []pdf.PDFDict{f}
	}
	kids, _ := f.Entries.Get("Kids").(pdf.PDFArray)
	var out []pdf.PDFDict
	for _, k := range kids {
		if w, ok := k.(pdf.PDFDict); ok && isWidget(w) {
			out = append(out, w)
		}
	}
	return out
}

// widgetsOf returns where the widgets of field f show: the pages whose
// annotations list them, and their boxes there. A hidden widget, or one
// with no area, has an empty box. The caller holds d.mu.
func (d *Document) widgetsOf(f pdf.PDFDict) []SignatureWidget {
	var out []SignatureWidget
	for _, w := range widgetDicts(f) {
		ptr := pdf.ValuePointer(w.Entries)
		for i, p := range d.pages {
			annots, _ := p.dict.Entries.Get("Annots").(pdf.PDFArray)
			if !slices.ContainsFunc(annots, func(a pdf.PDFValue) bool {
				ad, ok := a.(pdf.PDFDict)
				return ok && pdf.ValuePointer(ad.Entries) == ptr
			}) {
				continue
			}
			sw := SignatureWidget{Page: i}
			flags, _ := w.Entries.Get("F").(pdf.PDFInteger)
			if rect, err := pdf.FloatArray(w.Entries.Get("Rect")); err == nil && len(rect) == 4 && flags&(2|32) == 0 {
				g := pdf.Geometry(p.dict)
				box := annotRect(g, rect)
				if box.X1-box.X0 >= 1 && box.Y1-box.Y0 >= 1 {
					sw.Box = box
				}
			}
			out = append(out, sw)
			break
		}
	}
	return out
}

// annotRect maps an annotation's Rect on a page of geometry g to the
// displayed page, as Link.Box is.
func annotRect(g pdf.PageGeometry, rect []float64) Rect {
	w, h := g.CropBox[2]-g.CropBox[0], g.CropBox[3]-g.CropBox[1]
	// From PDF space, origin at the lower left of the crop box, to the
	// unrotated page with its origin at the upper left.
	x0, x1 := min(rect[0], rect[2])-g.CropBox[0], max(rect[0], rect[2])-g.CropBox[0]
	y0, y1 := g.CropBox[3]-max(rect[1], rect[3]), g.CropBox[3]-min(rect[1], rect[3])
	return displayRect([4]float64{x0, y0, x1, y1}, w, h, g.Rotate, g.UserUnit)
}

// displayName is how a certificate's subject is named to a person: its
// common name, else its organization, else the whole name.
func displayName(c *x509.Certificate) string {
	switch {
	case c.Subject.CommonName != "":
		return c.Subject.CommonName
	case len(c.Subject.Organization) > 0:
		return c.Subject.Organization[0]
	}
	return c.Subject.String()
}

// keyDescription names a certificate's key, such as "RSA 2048-bit" or
// "ECDSA P-256".
func keyDescription(c *x509.Certificate) string {
	if c == nil {
		return "unknown key"
	}
	switch k := c.PublicKey.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d-bit", k.Size()*8)
	case *ecdsa.PublicKey:
		return "ECDSA " + k.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	}
	return c.PublicKeyAlgorithm.String()
}

// cmsReason turns a cms error into the end of a sentence.
func cmsReason(err error) string {
	s := strings.TrimPrefix(err.Error(), "cms: ")
	return strings.TrimSuffix(s, ".")
}

// sigText decodes a text string of a signature dictionary. It is a PDF text
// string, but signers often write UTF-8 without the byte order mark that PDF
// 2.0 asks of it, so bytes that are valid UTF-8 and not plain ASCII, which
// PDFDocEncoding text seldom is, are read as UTF-8.
func sigText(v pdf.PDFValue) string {
	raw, ok := pdf.StringBytes(v)
	if !ok {
		return ""
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if utf8.Valid(raw) && slices.ContainsFunc(raw, func(b byte) bool { return b >= 0x80 }) && !bytes.HasPrefix(raw, []byte{0xfe, 0xff}) {
		return string(raw)
	}
	return pdf.DecodePDFTextString(raw)
}

// parsePDFDate reads a PDF date (ISO 32000-1 7.9.4), "D:YYYYMMDDHHmmSSOHH'mm",
// of which everything after the year is optional.
func parsePDFDate(s string) (time.Time, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "D:")
	if len(s) < 4 {
		return time.Time{}, false
	}
	num := func(from, n, def int) int {
		if len(s) < from+n {
			return def
		}
		v, err := strconv.Atoi(s[from : from+n])
		if err != nil {
			return def
		}
		return v
	}
	year := num(0, 4, -1)
	if year < 0 {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(num(4, 2, 1)), num(6, 2, 1), num(8, 2, 0), num(10, 2, 0), num(12, 2, 0), 0, time.UTC)
	if i := strings.IndexAny(s, "+-"); i >= 0 && i >= 4 {
		zone := strings.ReplaceAll(s[i+1:], "'", "")
		h, _ := strconv.Atoi(zone[:min(2, len(zone))])
		m := 0
		if len(zone) >= 4 {
			m, _ = strconv.Atoi(zone[2:4])
		}
		off := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
		if s[i] == '+' {
			off = -off
		}
		t = t.Add(off)
	}
	return t, true
}
