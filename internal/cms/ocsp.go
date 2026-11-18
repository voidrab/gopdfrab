package cms

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"
)

// OCSP response status (RFC 6960).
const (
	OCSPGood = iota
	OCSPRevoked
	OCSPUnknown
)

var oidOCSPBasic = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}

// OCSPResponse is a parsed basic OCSP response.
type OCSPResponse struct {
	Responses []OCSPSingle
	// Certificates are those the response carries, such as a delegated
	// responder's.
	Certificates []*x509.Certificate

	tbs       []byte
	alg       pkix.AlgorithmIdentifier
	signature []byte
	byName    []byte // the responder's name, DER, or nil
	byKey     []byte // the SHA-1 of the responder's key, or nil
}

// OCSPSingle is the status of one certificate.
type OCSPSingle struct {
	Status                int
	RevokedAt             time.Time
	hash                  crypto.Hash
	issuerName, issuerKey []byte
	serial                *big.Int
}

type ocspResponse struct {
	Status   asn1.Enumerated
	Response struct {
		Type  asn1.ObjectIdentifier
		Bytes []byte
	} `asn1:"explicit,optional,tag:0"`
}

type basicResponse struct {
	TBS       asn1.RawValue
	Algorithm pkix.AlgorithmIdentifier
	Signature asn1.BitString
	Certs     []asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type responseData struct {
	Version    int `asn1:"explicit,optional,default:0,tag:0"`
	Responder  asn1.RawValue
	ProducedAt time.Time `asn1:"generalized"`
	Responses  []singleResponse
}

type singleResponse struct {
	CertID     certID
	Status     asn1.RawValue
	ThisUpdate time.Time `asn1:"generalized"`
	NextUpdate time.Time `asn1:"generalized,explicit,optional,tag:0"`
}

type certID struct {
	Hash     pkix.AlgorithmIdentifier
	NameHash []byte
	KeyHash  []byte
	Serial   *big.Int
}

// ParseOCSP reads an OCSPResponse holding a basic response.
func ParseOCSP(der []byte) (*OCSPResponse, error) {
	var outer ocspResponse
	if _, err := asn1.Unmarshal(der, &outer); err != nil {
		return nil, fmt.Errorf("cms: OCSP response: %w", err)
	}
	if outer.Status != 0 || !outer.Response.Type.Equal(oidOCSPBasic) {
		return nil, fmt.Errorf("%w OCSP response (status %d)", ErrUnsupported, outer.Status)
	}
	return parseBasic(outer.Response.Bytes)
}

func parseBasic(der []byte) (*OCSPResponse, error) {
	var basic basicResponse
	if _, err := asn1.Unmarshal(der, &basic); err != nil {
		return nil, fmt.Errorf("cms: basic OCSP response: %w", err)
	}
	var data responseData
	if _, err := asn1.Unmarshal(basic.TBS.FullBytes, &data); err != nil {
		return nil, fmt.Errorf("cms: OCSP response data: %w", err)
	}
	r := &OCSPResponse{tbs: basic.TBS.FullBytes, alg: basic.Algorithm, signature: basic.Signature.RightAlign()}
	switch data.Responder.Tag {
	case 1:
		r.byName = data.Responder.Bytes
	case 2:
		_, _ = asn1.Unmarshal(data.Responder.Bytes, &r.byKey)
	}
	for _, c := range basic.Certs {
		if cert, err := x509.ParseCertificate(c.FullBytes); err == nil {
			r.Certificates = append(r.Certificates, cert)
		}
	}
	for _, s := range data.Responses {
		one := OCSPSingle{hash: HashOf(s.CertID.Hash.Algorithm), issuerName: s.CertID.NameHash,
			issuerKey: s.CertID.KeyHash, serial: s.CertID.Serial}
		switch s.Status.Tag {
		case 0:
			one.Status = OCSPGood
		case 1:
			one.Status = OCSPRevoked
			var at time.Time
			if _, err := asn1.UnmarshalWithParams(s.Status.Bytes, &at, "generalized"); err == nil {
				one.RevokedAt = at
			}
		default:
			one.Status = OCSPUnknown
		}
		r.Responses = append(r.Responses, one)
	}
	return r, nil
}

// errNoResponder is returned when no certificate signed the response.
var errNoResponder = errors.New("cms: OCSP responder not found")

// For returns the status the response gives cert, issued by issuer, and
// checks that the response is signed by issuer or by a responder issuer
// delegated to. ok is false when the response does not cover cert.
func (r *OCSPResponse) For(cert, issuer *x509.Certificate) (s OCSPSingle, ok bool, err error) {
	for _, one := range r.Responses {
		if one.hash == 0 || one.serial == nil || one.serial.Cmp(cert.SerialNumber) != 0 {
			continue
		}
		if !bytes.Equal(Sum(one.hash, issuer.RawSubject), one.issuerName) ||
			!bytes.Equal(Sum(one.hash, subjectKey(issuer)), one.issuerKey) {
			continue
		}
		return one, true, r.checkSigner(issuer)
	}
	return OCSPSingle{}, false, nil
}

// checkSigner checks the response's signature, by issuer or by a certificate
// it carries that issuer issued for OCSP signing.
func (r *OCSPResponse) checkSigner(issuer *x509.Certificate) error {
	candidates := append([]*x509.Certificate{issuer}, r.Certificates...)
	for _, c := range candidates {
		if !r.names(c) || c != issuer &&
			(c.CheckSignatureFrom(issuer) != nil || !slices.Contains(c.ExtKeyUsage, x509.ExtKeyUsageOCSPSigning)) {
			continue
		}
		return CheckSignature(c.PublicKey, r.alg, 0, r.tbs, r.signature, false)
	}
	return errNoResponder
}

// names reports whether the response's responder ID names c.
func (r *OCSPResponse) names(c *x509.Certificate) bool {
	if r.byName != nil {
		return bytes.Equal(r.byName, c.RawSubject)
	}
	return bytes.Equal(Sum(crypto.SHA1, subjectKey(c)), r.byKey)
}

// subjectKey returns the bits of c's public key, without the algorithm.
func subjectKey(c *x509.Certificate) []byte {
	var spki struct {
		Algorithm pkix.AlgorithmIdentifier
		Key       asn1.BitString
	}
	if _, err := asn1.Unmarshal(c.RawSubjectPublicKeyInfo, &spki); err != nil {
		return nil
	}
	return spki.Key.RightAlign()
}
