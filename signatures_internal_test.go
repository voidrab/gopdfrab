package gopdfrab

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab/internal/cms/cmstest"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestSignaturesCatalogErrors(t *testing.T) {
	broken := errors.New("unreadable")
	for _, entry := range []string{"Pages", "AcroForm"} {
		d := &Document{resolvePath: func(path []string) (pdf.PDFValue, error) {
			if path[len(path)-1] == entry {
				return nil, broken
			}
			return nil, pdf.ErrNoEntry
		}}
		if _, err := d.Signatures(context.Background(), SignatureOptions{}); !errors.Is(err, broken) {
			t.Errorf("%s unreadable: err %v", entry, err)
		}
	}
}

func TestDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name pkix.Name
		want string
	}{
		{pkix.Name{CommonName: "Alice", Organization: []string{"Org"}}, "Alice"},
		{pkix.Name{Organization: []string{"Org"}}, "Org"},
		{pkix.Name{Country: []string{"AT"}}, "C=AT"},
	} {
		if got := displayName(&x509.Certificate{Subject: tc.name}); got != tc.want {
			t.Errorf("displayName(%v) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestKeyDescription(t *testing.T) {
	for _, tc := range []struct {
		cert *x509.Certificate
		want string
	}{
		{nil, "unknown key"},
		{&x509.Certificate{PublicKey: ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))}, "Ed25519"},
		{&x509.Certificate{PublicKeyAlgorithm: x509.DSA}, "DSA"},
	} {
		if got := keyDescription(tc.cert); got != tc.want {
			t.Errorf("keyDescription = %q, want %q", got, tc.want)
		}
	}
}

func TestParsePDFDate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"D:20", time.Time{}, false},
		{"D:abcd", time.Time{}, false},
		{"D:2026", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"D:2026xx15", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), true},
		{"D:20260304050607-02", time.Date(2026, 3, 4, 7, 6, 7, 0, time.UTC), true},
	} {
		got, ok := parsePDFDate(tc.in)
		if ok != tc.ok || !got.Equal(tc.want) {
			t.Errorf("parsePDFDate(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTrustProblem(t *testing.T) {
	root := cmstest.Root("Root")
	leaf := root.Issue("Leaf", cmstest.IssueOptions{})
	c := &sigCheck{s: &Signature{Signer: leaf.Cert}, v: &sigVerifier{rootsNote: "no trust store is available"}}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{x509.UnknownAuthorityError{}, "not issued by a trusted authority (no trust store is available)."},
		{x509.CertificateInvalidError{Reason: x509.IncompatibleUsage}, "may not be used for this purpose."},
		{x509.CertificateInvalidError{Reason: x509.NotAuthorizedToSign}, "is not a certificate authority."},
		{x509.CertificateInvalidError{Reason: x509.TooManyIntermediates}, "The certificate chain does not verify: "},
		{errors.New("odd"), "The certificate chain does not verify: odd."},
	} {
		if got := c.trustProblem(tc.err); !strings.Contains(got, tc.want) {
			t.Errorf("trustProblem(%v) = %q, want it to contain %q", tc.err, got, tc.want)
		}
	}
}

func TestWhenWithoutSigningTime(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c := &sigCheck{s: &Signature{}, v: &sigVerifier{now: now}}
	if got := c.when(); !got.Equal(now) {
		t.Errorf("when() = %v, want the verifier's now %v", got, now)
	}
}

func TestRevokedBy(t *testing.T) {
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	root := cmstest.Root("Root")
	leaf := root.Issue("Leaf", cmstest.IssueOptions{})
	other := cmstest.Root("Other")
	otherLeaf := root.Issue("Other leaf", cmstest.IssueOptions{})
	// An impostor with the root's name but another key.
	impostor := cmstest.Root("Root")
	for _, tc := range []struct {
		name         string
		crls, ocsps  [][]byte
		revoked, hit bool
	}{
		{name: "unreadable OCSP", ocsps: [][]byte{{0x30, 0x00}}},
		{name: "OCSP for another certificate", ocsps: [][]byte{cmstest.OCSP(root, otherLeaf.Cert, at, time.Time{})}},
		{name: "OCSP status unknown", ocsps: [][]byte{cmstest.OCSPWith(root, leaf.Cert, at, cmstest.OCSPOptions{Unknown: true})}},
		{name: "unreadable CRL", crls: [][]byte{{0x30, 0x00}}},
		{name: "CRL of another issuer", crls: [][]byte{cmstest.CRL(other, at, leaf.Cert)}},
		{name: "CRL not signed by the issuer", crls: [][]byte{cmstest.CRL(impostor, at, leaf.Cert)}},
		{name: "CRL that does not list it", crls: [][]byte{cmstest.CRL(root, at, otherLeaf.Cert)}, hit: true},
		{name: "CRL that lists it", crls: [][]byte{cmstest.CRL(root, at, leaf.Cert)}, revoked: true, hit: true},
	} {
		revoked, _, found := revokedBy(leaf.Cert, root.Cert, tc.crls, tc.ocsps)
		if revoked != tc.revoked || found != tc.hit {
			t.Errorf("%s: revoked %v, found %v; want %v, %v", tc.name, revoked, found, tc.revoked, tc.hit)
		}
	}
}
