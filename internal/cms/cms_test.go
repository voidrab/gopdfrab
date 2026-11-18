package cms_test

import (
	"crypto"
	"crypto/sha256"
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
