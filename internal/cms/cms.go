// Package cms reads the Cryptographic Message Syntax (RFC 5652) SignedData
// objects that PDF signatures hold, checks their signatures, and reads the
// RFC 3161 timestamp tokens and the OCSP responses that travel with them.
//
// Signers commonly write BER, with indefinite lengths, which Go's
// encoding/asn1 rejects; such input is rewritten as DER first.
package cms

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/voidrab/gopdfrab/internal/ber"

	// The digests a signature may name.
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
)

// Object identifiers of the content types, attributes and algorithms read.
var (
	OIDSignedData            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	OIDTSTInfo               = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	oidContentType           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest         = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidTimeStampToken        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
	oidRevocationInfoArchive = asn1.ObjectIdentifier{1, 2, 840, 113583, 1, 1, 8}

	oidRSA        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidRSAPSS     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidECPublic   = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidEd25519    = asn1.ObjectIdentifier{1, 3, 101, 112}
	oidMGF1       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
	oidDSAWithSHA = asn1.ObjectIdentifier{1, 2, 840, 10040, 4, 3}
)

// digests maps digest algorithm identifiers to their hashes.
var digests = []struct {
	oid  asn1.ObjectIdentifier
	hash crypto.Hash
}{
	{asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}, crypto.SHA1},
	{asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 4}, crypto.SHA224},
	{asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}, crypto.SHA256},
	{asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}, crypto.SHA384},
	{asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}, crypto.SHA512},
}

// signatureAlgorithms maps signature algorithms to the digest they name, or 0
// for none, and the key they need.
var signatureAlgorithms = []struct {
	oid  asn1.ObjectIdentifier
	hash crypto.Hash
	key  keyKind
}{
	{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}, crypto.SHA1, keyRSA},
	{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 14}, crypto.SHA224, keyRSA},
	{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}, crypto.SHA256, keyRSA},
	{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}, crypto.SHA384, keyRSA},
	{asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}, crypto.SHA512, keyRSA},
	{asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}, crypto.SHA1, keyECDSA},
	{asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 1}, crypto.SHA224, keyECDSA},
	{asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}, crypto.SHA256, keyECDSA},
	{asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}, crypto.SHA384, keyECDSA},
	{asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}, crypto.SHA512, keyECDSA},
	// Bare key algorithms, which take the signer's digest.
	{oidRSA, 0, keyRSA},
	{oidECPublic, 0, keyECDSA},
	{oidEd25519, 0, keyEd25519},
	{oidRSAPSS, 0, keyRSAPSS},
}

type keyKind uint8

const (
	keyAny keyKind = iota
	keyRSA
	keyECDSA
	keyEd25519
	keyRSAPSS // an RSA key used with PSS padding
)

// Errors a check returns, wrapped with detail.
var (
	// ErrUnsupported is a structure or algorithm this package does not read.
	ErrUnsupported = errors.New("cms: unsupported")
	// ErrDigestMismatch is a message digest that does not match the content.
	ErrDigestMismatch = errors.New("cms: message digest does not match")
	// ErrBadSignature is a signature that does not verify.
	ErrBadSignature = errors.New("cms: signature does not verify")
	// ErrNoCertificate is a signer whose certificate is not in the object.
	ErrNoCertificate = errors.New("cms: signer certificate not found")
)

// Sum returns the digest of b under h.
func Sum(h crypto.Hash, b []byte) []byte {
	d := h.New()
	d.Write(b)
	return d.Sum(nil)
}

// HashOf returns the hash a digest algorithm identifier names, or 0.
func HashOf(oid asn1.ObjectIdentifier) crypto.Hash {
	for _, d := range digests {
		if d.oid.Equal(oid) {
			return d.hash
		}
	}
	return 0
}

// The ASN.1 shapes of RFC 5652.
type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type signedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContentInfo encapContentInfo
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue `asn1:"optional,tag:1"`
	SignerInfos      []signerInfo  `asn1:"set"`
}

type encapContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type signerInfo struct {
	Version            int
	SID                asn1.RawValue
	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignedAttrs        asn1.RawValue `asn1:"optional,tag:0"`
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          []byte
	UnsignedAttrs      asn1.RawValue `asn1:"optional,tag:1"`
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

// SignedData is a parsed SignedData object.
type SignedData struct {
	// ContentType is the type of the encapsulated content, and Content that
	// content, or nil when the signature is detached.
	ContentType asn1.ObjectIdentifier
	Content     []byte
	// Certificates are those the object carries that parse.
	Certificates []*x509.Certificate
	// CRLs are the revocation lists the object carries, DER-encoded.
	CRLs    [][]byte
	Signers []*Signer
}

// Signer is one SignerInfo of a SignedData.
type Signer struct {
	// Hash is the digest of the content, or 0 for an unknown digest.
	Hash crypto.Hash
	// Certificate is the signer's, or nil when the object does not carry it.
	Certificate *x509.Certificate
	// SigningTime is the signer's claim of when it signed, or zero.
	SigningTime time.Time
	// Signature is the signature value.
	Signature []byte
	// TimestampToken is the RFC 3161 token over Signature, DER-encoded, or
	// nil.
	TimestampToken []byte
	// CRLs and OCSPs are the revocation data in an Adobe
	// revocationInfoArchival signed attribute, DER-encoded.
	CRLs, OCSPs [][]byte

	digestAlg     asn1.ObjectIdentifier
	sigAlg        pkix.AlgorithmIdentifier
	messageDigest []byte
	contentType   asn1.ObjectIdentifier // the content type attribute
	wantType      asn1.ObjectIdentifier // the content type the object encapsulates
	attrsDER      []byte                // the signed attributes as the SET the signature covers, or nil
}

// Parse reads a ContentInfo holding SignedData, in BER or DER. Bytes after it
// are ignored.
func Parse(in []byte) (*SignedData, error) {
	der, err := ber.ToDER(in)
	if err != nil {
		return nil, err
	}
	var ci contentInfo
	if _, err := asn1.Unmarshal(der, &ci); err != nil {
		return nil, fmt.Errorf("cms: content info: %w", err)
	}
	if !ci.ContentType.Equal(OIDSignedData) {
		return nil, fmt.Errorf("%w content type %v", ErrUnsupported, ci.ContentType)
	}
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("cms: signed data: %w", err)
	}
	out := &SignedData{ContentType: sd.EncapContentInfo.EContentType}
	if c := sd.EncapContentInfo.EContent; len(c.Bytes) > 0 {
		var octets []byte
		if _, err := asn1.Unmarshal(c.Bytes, &octets); err != nil {
			return nil, fmt.Errorf("cms: encapsulated content: %w", err)
		}
		out.Content = octets
	}
	// Other elements of the certificates SET are attribute or other
	// certificates.
	for _, raw := range sequences(sd.Certificates.Bytes) {
		if c, err := x509.ParseCertificate(raw); err == nil {
			out.Certificates = append(out.Certificates, c)
		}
	}
	out.CRLs = sequences(sd.CRLs.Bytes)
	for _, si := range sd.SignerInfos {
		s, err := out.signer(si)
		if err != nil {
			return nil, err
		}
		out.Signers = append(out.Signers, s)
	}
	return out, nil
}

// signer reads one SignerInfo.
func (sd *SignedData) signer(si signerInfo) (*Signer, error) {
	s := &Signer{
		Hash:        HashOf(si.DigestAlgorithm.Algorithm),
		Certificate: sd.findCertificate(si.SID),
		Signature:   si.Signature,
		digestAlg:   si.DigestAlgorithm.Algorithm,
		sigAlg:      si.SignatureAlgorithm,
		wantType:    sd.ContentType,
	}
	if len(si.SignedAttrs.FullBytes) > 0 {
		// The signature covers the attributes' DER with the SET tag in place
		// of the implicit [0].
		s.attrsDER = append([]byte{ber.TagSet}, si.SignedAttrs.FullBytes[1:]...)
		attrs, err := attributes(si.SignedAttrs.Bytes)
		if err != nil {
			return nil, fmt.Errorf("cms: signed attributes: %w", err)
		}
		for _, a := range attrs {
			switch {
			case a.Type.Equal(oidMessageDigest):
				_, _ = asn1.Unmarshal(a.Values.Bytes, &s.messageDigest)
			case a.Type.Equal(oidContentType):
				_, _ = asn1.Unmarshal(a.Values.Bytes, &s.contentType)
			case a.Type.Equal(oidSigningTime):
				s.SigningTime = parseTime(a.Values.Bytes)
			case a.Type.Equal(oidRevocationInfoArchive):
				s.CRLs, s.OCSPs = revocationArchive(a.Values.Bytes)
			}
		}
	}
	if len(si.UnsignedAttrs.Bytes) > 0 {
		attrs, _ := attributes(si.UnsignedAttrs.Bytes)
		for _, a := range attrs {
			if a.Type.Equal(oidTimeStampToken) {
				var raw asn1.RawValue
				if _, err := asn1.Unmarshal(a.Values.Bytes, &raw); err == nil {
					s.TimestampToken = raw.FullBytes
				}
			}
		}
	}
	return s, nil
}

// attributes reads the content of a SET OF Attribute.
func attributes(b []byte) ([]attribute, error) {
	var out []attribute
	for len(b) > 0 {
		var a attribute
		var err error
		if b, err = asn1.Unmarshal(b, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// elements splits the content of a constructed value into its elements,
// stopping at the first that does not parse.
func elements(b []byte) []asn1.RawValue {
	var out []asn1.RawValue
	for len(b) > 0 {
		var raw asn1.RawValue
		var err error
		if b, err = asn1.Unmarshal(b, &raw); err != nil {
			break
		}
		out = append(out, raw)
	}
	return out
}

// sequences returns the encodings of the universal SEQUENCE elements of a
// constructed value's content.
func sequences(b []byte) [][]byte {
	var out [][]byte
	for _, raw := range elements(b) {
		if raw.Class == asn1.ClassUniversal && raw.Tag == asn1.TagSequence {
			out = append(out, raw.FullBytes)
		}
	}
	return out
}

// parseTime reads a UTCTime or GeneralizedTime, or returns the zero time.
func parseTime(b []byte) time.Time {
	var t time.Time
	if _, err := asn1.Unmarshal(b, &t); err != nil {
		return time.Time{}
	}
	return t
}

// revocationArchive reads Adobe's RevocationInfoArchival: [0] CRLs, [1] OCSP
// responses, each a SEQUENCE OF.
func revocationArchive(b []byte) (crls, ocsps [][]byte) {
	var seq asn1.RawValue
	if _, err := asn1.Unmarshal(b, &seq); err != nil {
		return nil, nil
	}
	for _, field := range elements(seq.Bytes) {
		var list asn1.RawValue
		if _, err := asn1.Unmarshal(field.Bytes, &list); err != nil {
			continue
		}
		for _, item := range elements(list.Bytes) {
			switch field.Tag {
			case 0:
				crls = append(crls, item.FullBytes)
			case 1:
				ocsps = append(ocsps, item.FullBytes)
			}
		}
	}
	return crls, ocsps
}

// findCertificate returns the certificate sid names, by issuer and serial
// number or by subject key identifier, or nil.
func (sd *SignedData) findCertificate(sid asn1.RawValue) *x509.Certificate {
	if sid.Class == asn1.ClassContextSpecific && sid.Tag == 0 {
		for _, c := range sd.Certificates {
			if bytes.Equal(c.SubjectKeyId, sid.Bytes) {
				return c
			}
		}
		return nil
	}
	var ias issuerAndSerial
	if _, err := asn1.Unmarshal(sid.FullBytes, &ias); err != nil {
		return nil
	}
	for _, c := range sd.Certificates {
		if c.SerialNumber.Cmp(ias.Serial) == 0 && bytes.Equal(c.RawIssuer, ias.Issuer.FullBytes) {
			return c
		}
	}
	return nil
}

// Verify checks the signer's signature given digest, the hash of the content
// it signs under s.Hash: the message digest attribute must equal it, and the
// signature over the signed attributes must verify with the signer's key. A
// signer with no signed attributes signs digest itself.
func (s *Signer) Verify(digest []byte) error {
	if s.Certificate == nil {
		return ErrNoCertificate
	}
	if s.Hash == 0 {
		return fmt.Errorf("%w digest algorithm %v", ErrUnsupported, s.digestAlg)
	}
	if s.attrsDER == nil {
		return CheckSignature(s.Certificate.PublicKey, s.sigAlg, s.Hash, digest, s.Signature, true)
	}
	if !bytes.Equal(s.messageDigest, digest) {
		return ErrDigestMismatch
	}
	if len(s.contentType) > 0 && len(s.wantType) > 0 && !s.contentType.Equal(s.wantType) {
		return fmt.Errorf("%w: content type attribute %v names other content", ErrBadSignature, s.contentType)
	}
	return CheckSignature(s.Certificate.PublicKey, s.sigAlg, s.Hash, s.attrsDER, s.Signature, false)
}

type pssParams struct {
	Hash       pkix.AlgorithmIdentifier `asn1:"explicit,optional,tag:0"`
	MGF        pkix.AlgorithmIdentifier `asn1:"explicit,optional,tag:1"`
	SaltLength int                      `asn1:"explicit,optional,tag:2,default:20"`
	Trailer    int                      `asn1:"explicit,optional,tag:3,default:1"`
}

// pssOptions reads the digest and salt length of RSASSA-PSS parameters.
func pssOptions(alg pkix.AlgorithmIdentifier) (crypto.Hash, int, error) {
	p := pssParams{SaltLength: 20}
	if len(alg.Parameters.FullBytes) > 0 {
		if _, err := asn1.Unmarshal(alg.Parameters.FullBytes, &p); err != nil {
			return 0, 0, fmt.Errorf("%w RSASSA-PSS parameters: %v", ErrUnsupported, err)
		}
	}
	if p.MGF.Algorithm != nil && !p.MGF.Algorithm.Equal(oidMGF1) {
		return 0, 0, fmt.Errorf("%w mask generation %v", ErrUnsupported, p.MGF.Algorithm)
	}
	if p.Hash.Algorithm == nil {
		return crypto.SHA1, p.SaltLength, nil
	}
	return HashOf(p.Hash.Algorithm), p.SaltLength, nil
}

// CheckSignature checks sig over msg with key, under the signature algorithm
// alg. hash is the digest to use when alg does not name one. When prehashed,
// msg is already the digest under hash.
func CheckSignature(key any, alg pkix.AlgorithmIdentifier, hash crypto.Hash, msg, sig []byte, prehashed bool) error {
	if alg.Algorithm.Equal(oidDSAWithSHA) {
		return fmt.Errorf("%w signature algorithm DSA", ErrUnsupported)
	}
	kind := keyAny
	for _, a := range signatureAlgorithms {
		if a.oid.Equal(alg.Algorithm) {
			kind = a.key
			if a.hash != 0 && !prehashed {
				hash = a.hash
			}
		}
	}
	salt := rsa.PSSSaltLengthAuto
	switch kind {
	case keyAny:
		return fmt.Errorf("%w signature algorithm %v", ErrUnsupported, alg.Algorithm)
	case keyRSAPSS:
		pssHash, pssSalt, err := pssOptions(alg)
		if err != nil {
			return err
		}
		if prehashed && pssHash != hash {
			return fmt.Errorf("%w: RSASSA-PSS digest differs from the signer's", ErrUnsupported)
		}
		hash, salt = pssHash, pssSalt
	}
	if kind == keyEd25519 {
		pub, ok := key.(ed25519.PublicKey)
		switch {
		case !ok:
			return fmt.Errorf("%w: Ed25519 signature with a %T key", ErrBadSignature, key)
		case prehashed:
			return fmt.Errorf("%w Ed25519 without signed attributes", ErrUnsupported)
		case !ed25519.Verify(pub, msg, sig):
			return ErrBadSignature
		}
		return nil
	}
	if hash == 0 || !hash.Available() {
		return fmt.Errorf("%w digest for %v", ErrUnsupported, alg.Algorithm)
	}
	digest := msg
	if !prehashed {
		digest = Sum(hash, msg)
	}
	var err error
	switch kind {
	case keyRSA, keyRSAPSS:
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: RSA signature with a %T key", ErrBadSignature, key)
		}
		if kind == keyRSA {
			err = rsa.VerifyPKCS1v15(pub, hash, digest, sig)
		} else {
			err = rsa.VerifyPSS(pub, hash, digest, sig, &rsa.PSSOptions{SaltLength: salt, Hash: hash})
		}
	case keyECDSA:
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: ECDSA signature with a %T key", ErrBadSignature, key)
		}
		if !ecdsa.VerifyASN1(pub, digest, sig) {
			err = ErrBadSignature
		}
	}
	if err != nil {
		return ErrBadSignature
	}
	return nil
}
