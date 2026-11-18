package cms_test

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab/internal/cms"
	"github.com/voidrab/gopdfrab/internal/cms/cmstest"
)

var (
	root   = cmstest.Root("Test Root")
	leaf   = root.Issue("Alice", cmstest.IssueOptions{})
	rsaKey = root.Issue("Bob", cmstest.IssueOptions{Key: cmstest.RSA})
)

func digest(h crypto.Hash, data []byte) []byte {
	d := h.New()
	d.Write(data)
	return d.Sum(nil)
}

func parseOne(t *testing.T, der []byte) *cms.Signer {
	t.Helper()
	sd, err := cms.Parse(der)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sd.Signers) != 1 {
		t.Fatalf("%d signers, want 1", len(sd.Signers))
	}
	return sd.Signers[0]
}

func TestVerify(t *testing.T) {
	data := []byte("the signed bytes")
	signedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	tests := []struct {
		name string
		id   *cmstest.Identity
		o    cmstest.Options
	}{
		{"ecdsa", leaf, cmstest.Options{SigningTime: signedAt}},
		{"ecdsa sha512", leaf, cmstest.Options{Hash: crypto.SHA512}},
		{"rsa", rsaKey, cmstest.Options{}},
		{"rsa pss", rsaKey, cmstest.Options{PSS: true}},
		{"rsa no attributes", rsaKey, cmstest.Options{NoAttrs: true}},
		{"rsa sha1", rsaKey, cmstest.Options{Hash: crypto.SHA1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := parseOne(t, cmstest.Sign(tt.id, data, tt.o))
			if s.Certificate == nil || s.Certificate.Subject.CommonName != tt.id.Cert.Subject.CommonName {
				t.Fatalf("signer certificate %v", s.Certificate)
			}
			if err := s.Verify(digest(s.Hash, data)); err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if err := s.Verify(digest(s.Hash, []byte("other bytes"))); err == nil {
				t.Fatal("Verify accepted other content")
			}
			if !s.SigningTime.Equal(tt.o.SigningTime) {
				t.Errorf("SigningTime = %v, want %v", s.SigningTime, tt.o.SigningTime)
			}
		})
	}
}

func TestVerifyFailures(t *testing.T) {
	data := []byte("data")
	s := parseOne(t, cmstest.Sign(leaf, data, cmstest.Options{Tamper: true}))
	if err := s.Verify(digest(s.Hash, data)); !errors.Is(err, cms.ErrBadSignature) {
		t.Errorf("tampered: %v, want ErrBadSignature", err)
	}
	s = parseOne(t, cmstest.Sign(leaf, data, cmstest.Options{}))
	if err := s.Verify(digest(s.Hash, []byte("x"))); !errors.Is(err, cms.ErrDigestMismatch) {
		t.Errorf("other data: %v, want ErrDigestMismatch", err)
	}
	s = parseOne(t, cmstest.Sign(leaf, data, cmstest.Options{NoCert: true}))
	if err := s.Verify(digest(s.Hash, data)); !errors.Is(err, cms.ErrNoCertificate) {
		t.Errorf("no certificate: %v, want ErrNoCertificate", err)
	}
}

func TestParseBER(t *testing.T) {
	data := []byte("data")
	der := cmstest.Sign(leaf, data, cmstest.Options{})
	padded := append(cmstest.Indefinite(der), make([]byte, 100)...)
	s := parseOne(t, padded)
	if err := s.Verify(digest(s.Hash, data)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	for _, b := range [][]byte{nil, {0x30, 0x00}, {0x04, 0x01, 0x00}, make([]byte, 64)} {
		if _, err := cms.Parse(b); err == nil {
			t.Errorf("Parse(% x) succeeded", b)
		}
	}
}

func TestTimestamp(t *testing.T) {
	tsa := root.Issue("TSA", cmstest.IssueOptions{})
	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	data := []byte("signature value")
	s := parseOne(t, cmstest.Sign(leaf, []byte("doc"), cmstest.Options{TSA: tsa, TSATime: at}))
	if s.TimestampToken == nil {
		t.Fatal("no timestamp token")
	}
	ts, err := cms.ParseTimestamp(s.TimestampToken)
	if err != nil {
		t.Fatal(err)
	}
	if !ts.Time.Equal(at) {
		t.Errorf("Time = %v, want %v", ts.Time, at)
	}
	if err := ts.Verify(s.Signature); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if err := ts.Verify(data); err == nil {
		t.Error("Verify accepted other data")
	}
	sum := sha256.Sum256(s.Signature)
	if err := ts.VerifyDigest(sum[:]); err != nil {
		t.Errorf("VerifyDigest: %v", err)
	}
}

func TestOCSP(t *testing.T) {
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, revoked := range []bool{false, true} {
		var revokedAt time.Time
		if revoked {
			revokedAt = at.Add(-time.Hour)
		}
		r, err := cms.ParseOCSP(cmstest.OCSP(root, leaf.Cert, at, revokedAt))
		if err != nil {
			t.Fatal(err)
		}
		s, ok, err := r.For(leaf.Cert, root.Cert)
		if !ok || err != nil {
			t.Fatalf("For: ok %v, err %v", ok, err)
		}
		want := cms.OCSPGood
		if revoked {
			want = cms.OCSPRevoked
		}
		if s.Status != want || !s.RevokedAt.Equal(revokedAt) {
			t.Errorf("revoked %v: status %d at %v", revoked, s.Status, s.RevokedAt)
		}
		if _, ok, _ := r.For(rsaKey.Cert, root.Cert); ok {
			t.Error("the response covers another certificate")
		}
	}
	other := cmstest.Root("Other")
	r, _ := cms.ParseOCSP(cmstest.OCSP(other, leaf.Cert, at, time.Time{}))
	if _, ok, _ := r.For(leaf.Cert, root.Cert); ok {
		t.Error("a response from another issuer covers the certificate")
	}
}

func TestRevocationArchive(t *testing.T) {
	crl := cmstest.CRL(root, time.Now(), leaf.Cert)
	ocsp := cmstest.OCSP(root, leaf.Cert, time.Now(), time.Time{})
	s := parseOne(t, cmstest.Sign(leaf, []byte("d"), cmstest.Options{CRLs: [][]byte{crl}, OCSPs: [][]byte{ocsp}}))
	if len(s.CRLs) != 1 || string(s.CRLs[0]) != string(crl) || len(s.OCSPs) != 1 || string(s.OCSPs[0]) != string(ocsp) {
		t.Errorf("revocation data: %d CRLs, %d OCSPs", len(s.CRLs), len(s.OCSPs))
	}
}

func FuzzParse(f *testing.F) {
	f.Add(cmstest.Sign(leaf, []byte("x"), cmstest.Options{}))
	f.Add(cmstest.Indefinite(cmstest.Sign(leaf, []byte("x"), cmstest.Options{})))
	f.Add(cmstest.OCSP(root, leaf.Cert, time.Now(), time.Time{}))
	f.Fuzz(func(t *testing.T, b []byte) {
		if sd, err := cms.Parse(b); err == nil {
			for _, s := range sd.Signers {
				_ = s.Verify(make([]byte, 32))
				if s.TimestampToken != nil {
					_, _ = cms.ParseTimestamp(s.TimestampToken)
				}
			}
		}
		if r, err := cms.ParseOCSP(b); err == nil {
			_, _, _ = r.For(leaf.Cert, root.Cert)
		}
	})
}

// The DER builders below make the malformed structures signers never write.

func tlv(class, tag int, compound bool, parts ...[]byte) []byte {
	b, err := asn1.Marshal(asn1.RawValue{Class: class, Tag: tag, IsCompound: compound, Bytes: bytes.Join(parts, nil)})
	if err != nil {
		panic(err)
	}
	return b
}

func seq(p ...[]byte) []byte         { return tlv(asn1.ClassUniversal, asn1.TagSequence, true, p...) }
func set(p ...[]byte) []byte         { return tlv(asn1.ClassUniversal, asn1.TagSet, true, p...) }
func ctx(n int, p ...[]byte) []byte  { return tlv(asn1.ClassContextSpecific, n, true, p...) }
func prim(n int, b ...[]byte) []byte { return tlv(asn1.ClassContextSpecific, n, false, b...) }

func der(v any) []byte {
	b, err := asn1.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidRevocation    = asn1.ObjectIdentifier{1, 2, 840, 113583, 1, 1, 8}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidUnknown       = asn1.ObjectIdentifier{1, 2, 3, 4}
	oidRSA           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidRSASHA256     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidRSAPSS        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidECDSASHA256   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidEd25519       = asn1.ObjectIdentifier{1, 3, 101, 112}
	oidDSA           = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
)

func contentInfo(sd []byte) []byte { return seq(der(cms.OIDSignedData), ctx(0, sd)) }

// signedData returns a ContentInfo holding SignedData with certificates
// certs, when not nil, and one SignerInfo of sid, digest algorithm digestAlg
// and signed attributes attrs, when not nil.
func signedData(certs, sid []byte, digestAlg asn1.ObjectIdentifier, attrs []byte) []byte {
	var fields [][]byte
	fields = append(fields, der(1), set(), seq(der(oidData)))
	if certs != nil {
		fields = append(fields, certs)
	}
	si := [][]byte{der(1), sid, seq(der(digestAlg))}
	if attrs != nil {
		si = append(si, attrs)
	}
	si = append(si, seq(der(oidECDSASHA256)), der([]byte("sig")))
	fields = append(fields, set(seq(si...)))
	return contentInfo(seq(fields...))
}

func attr(typ asn1.ObjectIdentifier, values ...[]byte) []byte { return seq(der(typ), set(values...)) }

// bySerial is a signer identifier by issuer and serial number of c.
func bySerial(c *x509.Certificate) []byte { return seq(c.RawIssuer, der(c.SerialNumber)) }

func TestHashOfUnknown(t *testing.T) {
	if h := cms.HashOf(oidUnknown); h != 0 {
		t.Errorf("HashOf(unknown) = %v", h)
	}
}

func TestParseMalformed(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"not signed data", seq(der(oidData), ctx(0, seq()))},
		{"signed data not a sequence", contentInfo(der(1))},
		{"encapsulated content not octets", contentInfo(seq(der(1), set(), seq(der(oidData), ctx(0, der(5))), set()))},
		{"signed attributes not attributes", signedData(nil, bySerial(leaf.Cert), oidSHA256, ctx(0, der(1)))},
	} {
		if _, err := cms.Parse(tc.in); err == nil {
			t.Errorf("%s: Parse succeeded", tc.name)
		}
	}
}

func TestParseTolerates(t *testing.T) {
	sd, err := cms.Parse(signedData(prim(0, []byte{0x30, 0x05}), der(5), oidSHA256, ctx(0,
		attr(oidSigningTime, der(7)),
		attr(oidRevocation),
		attr(oidRevocation, seq(prim(0))),
	)))
	if err != nil {
		t.Fatal(err)
	}
	s := sd.Signers[0]
	if len(sd.Certificates) != 0 || s.Certificate != nil || !s.SigningTime.IsZero() || s.CRLs != nil || s.OCSPs != nil {
		t.Errorf("certificates %d, signer %v, time %v, CRLs %v, OCSPs %v",
			len(sd.Certificates), s.Certificate, s.SigningTime, s.CRLs, s.OCSPs)
	}
}

func TestSignerBySubjectKeyID(t *testing.T) {
	certs := ctx(0, root.Cert.Raw)
	sd, err := cms.Parse(signedData(certs, prim(0, root.Cert.SubjectKeyId), oidSHA256, nil))
	if err != nil {
		t.Fatal(err)
	}
	if c := sd.Signers[0].Certificate; c != root.Cert && (c == nil || !c.Equal(root.Cert)) {
		t.Errorf("signer certificate %v, want the root", c)
	}
	sd, _ = cms.Parse(signedData(certs, prim(0, []byte("other")), oidSHA256, nil))
	if c := sd.Signers[0].Certificate; c != nil {
		t.Errorf("an unknown key identifier found %v", c.Subject)
	}
}

func TestVerifyRejectsAttributes(t *testing.T) {
	certs := ctx(0, root.Cert.Raw)
	sid := prim(0, root.Cert.SubjectKeyId)
	d := digest(crypto.SHA256, []byte("data"))
	s := parseOne(t, signedData(certs, sid, oidUnknown, nil))
	if err := s.Verify(d); !errors.Is(err, cms.ErrUnsupported) {
		t.Errorf("unknown digest: %v, want ErrUnsupported", err)
	}
	s = parseOne(t, signedData(certs, sid, oidSHA256, ctx(0,
		attr(oidMessageDigest, der(d)),
		attr(oidContentType, der(oidUnknown)),
	)))
	if err := s.Verify(d); !errors.Is(err, cms.ErrBadSignature) {
		t.Errorf("content type of other content: %v, want ErrBadSignature", err)
	}
}

func TestCheckSignatureAlgorithms(t *testing.T) {
	rsaPub := rsaKey.Cert.PublicKey
	ecPub := leaf.Cert.PublicKey
	edPub, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("message")
	alg := func(o asn1.ObjectIdentifier, params ...[]byte) pkix.AlgorithmIdentifier {
		a := pkix.AlgorithmIdentifier{Algorithm: o}
		if len(params) > 0 {
			a.Parameters = asn1.RawValue{FullBytes: params[0]}
		}
		return a
	}
	otherMGF := seq(ctx(1, seq(der(oidUnknown))))
	for _, tc := range []struct {
		name      string
		key       any
		alg       pkix.AlgorithmIdentifier
		hash      crypto.Hash
		msg, sig  []byte
		prehashed bool
		want      error
	}{
		{"DSA", rsaPub, alg(oidDSA), 0, msg, nil, false, cms.ErrUnsupported},
		{"unknown algorithm", rsaPub, alg(oidUnknown), 0, msg, nil, false, cms.ErrUnsupported},
		{"PSS parameters malformed", rsaPub, alg(oidRSAPSS, der(1)), 0, msg, nil, false, cms.ErrUnsupported},
		{"PSS mask other than MGF1", rsaPub, alg(oidRSAPSS, otherMGF), 0, msg, nil, false, cms.ErrUnsupported},
		{"PSS default digest", rsaPub, alg(oidRSAPSS), 0, msg, []byte("sig"), false, cms.ErrBadSignature},
		{"PSS digest other than the signer's", rsaPub, alg(oidRSAPSS), crypto.SHA256, msg, nil, true, cms.ErrUnsupported},
		{"Ed25519 with an RSA key", rsaPub, alg(oidEd25519), 0, msg, nil, false, cms.ErrBadSignature},
		{"Ed25519 prehashed", edPub, alg(oidEd25519), crypto.SHA256, msg, nil, true, cms.ErrUnsupported},
		{"Ed25519 bad signature", edPub, alg(oidEd25519), 0, msg, make([]byte, ed25519.SignatureSize), false, cms.ErrBadSignature},
		{"Ed25519", edPub, alg(oidEd25519), 0, msg, ed25519.Sign(edKey, msg), false, nil},
		{"bare RSA without a digest", rsaPub, alg(oidRSA), 0, msg, nil, false, cms.ErrUnsupported},
		{"RSA with an ECDSA key", ecPub, alg(oidRSASHA256), 0, msg, nil, false, cms.ErrBadSignature},
		{"ECDSA with an RSA key", rsaPub, alg(oidECDSASHA256), 0, msg, nil, false, cms.ErrBadSignature},
	} {
		err := cms.CheckSignature(tc.key, tc.alg, tc.hash, tc.msg, tc.sig, tc.prehashed)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestParseTimestampRejects(t *testing.T) {
	tsa := root.Issue("TSA", cmstest.IssueOptions{})
	badImprint := der(struct {
		Version int
		Policy  asn1.ObjectIdentifier
		Imprint struct {
			Alg  pkix.AlgorithmIdentifier
			Hash []byte
		}
		Serial  int
		GenTime time.Time `asn1:"generalized"`
	}{Version: 1, Policy: oidUnknown, Imprint: struct {
		Alg  pkix.AlgorithmIdentifier
		Hash []byte
	}{pkix.AlgorithmIdentifier{Algorithm: oidUnknown}, []byte{1}}, Serial: 1, GenTime: time.Now().UTC()})
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"not CMS", []byte("x")},
		{"no signer", contentInfo(seq(der(1), set(), seq(der(cms.OIDTSTInfo), ctx(0, der([]byte("x")))), set()))},
		{"content not a TSTInfo", cmstest.Sign(tsa, nil, cmstest.Options{Content: []byte("x"), ContentType: cms.OIDTSTInfo})},
		{"imprint digest unknown", cmstest.Sign(tsa, nil, cmstest.Options{Content: badImprint, ContentType: cms.OIDTSTInfo})},
	} {
		if _, err := cms.ParseTimestamp(tc.in); err == nil {
			t.Errorf("%s: ParseTimestamp succeeded", tc.name)
		}
	}
}

func TestTimestampUnknownSignerDigest(t *testing.T) {
	tsa := root.Issue("TSA", cmstest.IssueOptions{})
	data := []byte("data")
	tok := cmstest.Timestamp(tsa, data, time.Now())
	// The signer's digest algorithm is the last SHA-256 identifier.
	sha := der(oidSHA256)
	i := bytes.LastIndex(tok, sha)
	tok[i+len(sha)-1] = 0x7f
	ts, err := cms.ParseTimestamp(tok)
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Verify(data); !errors.Is(err, cms.ErrUnsupported) {
		t.Errorf("Verify: %v, want ErrUnsupported", err)
	}
}

func TestParseOCSPRejects(t *testing.T) {
	basic := func(b []byte) []byte {
		return der(struct {
			Status   asn1.Enumerated
			Response struct {
				Type  asn1.ObjectIdentifier
				Bytes []byte
			} `asn1:"explicit,tag:0"`
		}{0, struct {
			Type  asn1.ObjectIdentifier
			Bytes []byte
		}{asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}, b}})
	}
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"not a response", []byte("x")},
		{"unsuccessful", der(struct{ Status asn1.Enumerated }{1})},
		{"basic response malformed", basic(der(1))},
		{"response data malformed", basic(seq(der(1), seq(der(oidECDSASHA256)), der(asn1.BitString{Bytes: []byte{1}, BitLength: 8})))},
	} {
		if _, err := cms.ParseOCSP(tc.in); err == nil {
			t.Errorf("%s: ParseOCSP succeeded", tc.name)
		}
	}
}

func TestOCSPResponders(t *testing.T) {
	at := time.Now()
	delegate := root.Issue("Responder", cmstest.IssueOptions{EKU: []x509.ExtKeyUsage{x509.ExtKeyUsageOCSPSigning}})
	notDelegated := root.Issue("Not a responder", cmstest.IssueOptions{})
	for _, tc := range []struct {
		name   string
		o      cmstest.OCSPOptions
		status int
		valid  bool
	}{
		{"by key", cmstest.OCSPOptions{ByKey: true}, cms.OCSPGood, true},
		{"unknown", cmstest.OCSPOptions{Unknown: true}, cms.OCSPUnknown, true},
		{"delegated", cmstest.OCSPOptions{Responder: delegate}, cms.OCSPGood, true},
		{"not delegated", cmstest.OCSPOptions{Responder: notDelegated}, cms.OCSPGood, false},
	} {
		r, err := cms.ParseOCSP(cmstest.OCSPWith(root, leaf.Cert, at, tc.o))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		s, ok, err := r.For(leaf.Cert, root.Cert)
		if !ok || s.Status != tc.status || (err == nil) != tc.valid {
			t.Errorf("%s: ok %v, status %d, err %v", tc.name, ok, s.Status, err)
		}
	}
}

func TestOCSPIssuerKeyUnreadable(t *testing.T) {
	r, err := cms.ParseOCSP(cmstest.OCSP(root, leaf.Cert, time.Now(), time.Time{}))
	if err != nil {
		t.Fatal(err)
	}
	issuer := *root.Cert
	issuer.RawSubjectPublicKeyInfo = []byte{1}
	if _, ok, _ := r.For(leaf.Cert, &issuer); ok {
		t.Error("the response covers a certificate of an issuer whose key is unreadable")
	}
}

func TestVerifySHA384(t *testing.T) {
	data := []byte("data")
	s := parseOne(t, cmstest.Sign(leaf, data, cmstest.Options{Hash: crypto.SHA384}))
	if err := s.Verify(digest(crypto.SHA384, data)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}
