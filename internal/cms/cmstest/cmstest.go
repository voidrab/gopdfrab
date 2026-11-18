// Package cmstest makes certificates and CMS SignedData objects for tests: a
// small certificate authority, detached signatures as PDF signers write them,
// RFC 3161 timestamp tokens, revocation lists and OCSP responses.
package cmstest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"time"
)

// Identity is a certificate and its private key.
type Identity struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

var serial = big.NewInt(1000)

func nextSerial() *big.Int {
	serial = new(big.Int).Add(serial, big.NewInt(1))
	return new(big.Int).Set(serial)
}

// Epoch is the time test certificates are issued at; they are valid for ten
// years from it.
var Epoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// KeyType chooses a test key.
type KeyType int

// Key types.
const (
	ECDSA KeyType = iota
	RSA
)

// must returns v, or panics with err: the builders here fail only on a
// broken test.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func newKey(kt KeyType) crypto.Signer {
	if kt == RSA {
		return must(rsa.GenerateKey(rand.Reader, 2048))
	}
	return must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
}

// Root returns a self-signed certificate authority called name.
func Root(name string) *Identity {
	key := newKey(ECDSA)
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(), Subject: pkix.Name{CommonName: name, Organization: []string{"Test"}},
		NotBefore: Epoch, NotAfter: Epoch.AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der := must(x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key))
	cert, _ := x509.ParseCertificate(der)
	return &Identity{Cert: cert, Key: key}
}

// IssueOptions tune Issue.
type IssueOptions struct {
	Key       KeyType
	CA        bool
	NotBefore time.Time // zero means Epoch
	NotAfter  time.Time // zero means ten years after Epoch
	EKU       []x509.ExtKeyUsage
	KeyUsage  x509.KeyUsage // zero means digital signature and non-repudiation
}

// Issue returns a certificate called name that ca signs.
func (ca *Identity) Issue(name string, o IssueOptions) *Identity {
	key := newKey(o.Key)
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(), Subject: pkix.Name{CommonName: name, Organization: []string{"Test"}},
		NotBefore: o.NotBefore, NotAfter: o.NotAfter, ExtKeyUsage: o.EKU, KeyUsage: o.KeyUsage,
	}
	if tmpl.NotBefore.IsZero() {
		tmpl.NotBefore = Epoch
	}
	if tmpl.NotAfter.IsZero() {
		tmpl.NotAfter = Epoch.AddDate(10, 0, 0)
	}
	if tmpl.KeyUsage == 0 {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment
	}
	if o.CA {
		tmpl.IsCA, tmpl.BasicConstraintsValid = true, true
		tmpl.KeyUsage |= x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	}
	der := must(x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, key.Public(), ca.Key))
	cert, _ := x509.ParseCertificate(der)
	return &Identity{Cert: cert, Key: key}
}

// Object identifiers.
var (
	oidData           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidTSTInfo        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	oidContentType    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidTimeStampToken = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
	oidRevocationInfo = asn1.ObjectIdentifier{1, 2, 840, 113583, 1, 1, 8}
	oidSHA1           = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA256         = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384         = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512         = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	oidRSA            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidRSAPSS         = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidMGF1           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 8}
	oidECDSASHA256    = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidECDSASHA384    = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidECDSASHA512    = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidOCSPBasic      = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}
)

func digestOID(h crypto.Hash) asn1.ObjectIdentifier {
	switch h {
	case crypto.SHA1:
		return oidSHA1
	case crypto.SHA384:
		return oidSHA384
	case crypto.SHA512:
		return oidSHA512
	}
	return oidSHA256
}

// Options tune Sign.
type Options struct {
	// Hash is the digest; zero means SHA-256.
	Hash crypto.Hash
	// PSS signs an RSA key with PSS padding rather than PKCS #1 v1.5.
	PSS bool
	// Chain are certificates carried besides the signer's.
	Chain []*x509.Certificate
	// SigningTime, if set, goes in a signing-time attribute.
	SigningTime time.Time
	// NoAttrs signs the content digest itself, with no signed attributes.
	NoAttrs bool
	// Content, if set, is encapsulated: the signature covers it rather than
	// the data passed to Sign. ContentType is its type; nil means data.
	Content     []byte
	ContentType asn1.ObjectIdentifier
	// TSA, if set, timestamps the signature value at TSATime.
	TSA     *Identity
	TSATime time.Time
	// TSAStampsOther makes the timestamp stamp other bytes than the
	// signature value, as a token moved from another signature does.
	TSAStampsOther bool
	// CRLs and OCSPs go in an Adobe revocation-information attribute.
	CRLs, OCSPs [][]byte
	// NoCert leaves the signer's certificate out.
	NoCert bool
	// Tamper flips a bit of the signature value after signing.
	Tamper bool
}

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type signedData struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier `asn1:"set"`
	EncapContentInfo encapContentInfo
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	SignerInfos      []signerInfo  `asn1:"set"`
}

type encapContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type signerInfo struct {
	Version            int
	SID                issuerAndSerial
	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignedAttrs        asn1.RawValue `asn1:"optional"`
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          []byte
	UnsignedAttrs      asn1.RawValue `asn1:"optional"`
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue `asn1:"set"`
}

func mustMarshal(v any, params ...string) []byte {
	if len(params) > 0 {
		return must(asn1.MarshalWithParams(v, params[0]))
	}
	return must(asn1.Marshal(v))
}

func raw(b []byte) asn1.RawValue { return asn1.RawValue{FullBytes: b} }

// Sign returns a ContentInfo holding SignedData by id over data, detached,
// unless o.Content encapsulates other content.
func Sign(id *Identity, data []byte, o Options) []byte {
	if o.Hash == 0 {
		o.Hash = crypto.SHA256
	}
	content := data
	ctype := oidData
	if o.Content != nil {
		content = o.Content
		if o.ContentType != nil {
			ctype = o.ContentType
		}
	}
	h := o.Hash.New()
	h.Write(content)
	digest := h.Sum(nil)

	si := signerInfo{
		Version:         1,
		SID:             issuerAndSerial{Issuer: raw(id.Cert.RawIssuer), Serial: id.Cert.SerialNumber},
		DigestAlgorithm: pkix.AlgorithmIdentifier{Algorithm: digestOID(o.Hash), Parameters: asn1.NullRawValue},
	}
	signed := digest
	prehashed := true
	if !o.NoAttrs {
		attrs := []attribute{
			{oidContentType, []asn1.RawValue{raw(mustMarshal(ctype))}},
			{oidMessageDigest, []asn1.RawValue{raw(mustMarshal(digest))}},
		}
		if !o.SigningTime.IsZero() {
			attrs = append(attrs, attribute{oidSigningTime, []asn1.RawValue{raw(mustMarshal(o.SigningTime.UTC()))}})
		}
		if len(o.CRLs) > 0 || len(o.OCSPs) > 0 {
			attrs = append(attrs, attribute{oidRevocationInfo, []asn1.RawValue{raw(revocationInfo(o.CRLs, o.OCSPs))}})
		}
		set := mustMarshal(attrs, "set")
		signed, prehashed = set, false
		si.SignedAttrs = raw(append([]byte{0xa0}, set[1:]...))
	}
	si.SignatureAlgorithm, si.Signature = sign(id, o, signed, prehashed)
	if o.Tamper {
		si.Signature[len(si.Signature)/2] ^= 1
	}
	if o.TSA != nil {
		stamped := si.Signature
		if o.TSAStampsOther {
			stamped = []byte("another signature")
		}
		tok := Timestamp(o.TSA, stamped, o.TSATime)
		set := mustMarshal([]attribute{{oidTimeStampToken, []asn1.RawValue{raw(tok)}}}, "set")
		si.UnsignedAttrs = raw(append([]byte{0xa1}, set[1:]...))
	}

	var certs []byte
	if !o.NoCert {
		certs = append(certs, id.Cert.Raw...)
	}
	for _, c := range o.Chain {
		certs = append(certs, c.Raw...)
	}
	sd := signedData{
		Version:          1,
		DigestAlgorithms: []pkix.AlgorithmIdentifier{si.DigestAlgorithm},
		EncapContentInfo: encapContentInfo{EContentType: ctype},
		SignerInfos:      []signerInfo{si},
	}
	if len(certs) > 0 {
		sd.Certificates = asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: certs}
	}
	if o.Content != nil {
		sd.EncapContentInfo.EContent = raw(mustMarshal(asn1.RawValue{
			Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: mustMarshal(o.Content)}))
	}
	return mustMarshal(contentInfo{ContentType: oidSignedData, Content: raw(mustMarshal(asn1.RawValue{
		Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: mustMarshal(sd)}))})
}

// sign signs msg, or the digest msg when prehashed, with id's key.
func sign(id *Identity, o Options, msg []byte, prehashed bool) (pkix.AlgorithmIdentifier, []byte) {
	digest := msg
	if !prehashed {
		h := o.Hash.New()
		h.Write(msg)
		digest = h.Sum(nil)
	}
	var alg pkix.AlgorithmIdentifier
	var opts crypto.SignerOpts = o.Hash
	switch id.Key.(type) {
	case *rsa.PrivateKey:
		alg = pkix.AlgorithmIdentifier{Algorithm: oidRSA, Parameters: asn1.NullRawValue}
		if o.PSS {
			hashAlg := pkix.AlgorithmIdentifier{Algorithm: digestOID(o.Hash), Parameters: asn1.NullRawValue}
			params := struct {
				Hash pkix.AlgorithmIdentifier `asn1:"explicit,tag:0"`
				MGF  pkix.AlgorithmIdentifier `asn1:"explicit,tag:1"`
				Salt int                      `asn1:"explicit,tag:2"`
			}{hashAlg, pkix.AlgorithmIdentifier{Algorithm: oidMGF1, Parameters: raw(mustMarshal(hashAlg))}, o.Hash.Size()}
			alg = pkix.AlgorithmIdentifier{Algorithm: oidRSAPSS, Parameters: raw(mustMarshal(params))}
			opts = &rsa.PSSOptions{SaltLength: o.Hash.Size(), Hash: o.Hash}
		}
	default:
		switch o.Hash {
		case crypto.SHA384:
			alg.Algorithm = oidECDSASHA384
		case crypto.SHA512:
			alg.Algorithm = oidECDSASHA512
		default:
			alg.Algorithm = oidECDSASHA256
		}
	}
	return alg, must(id.Key.Sign(rand.Reader, digest, opts))
}

func revocationInfo(crls, ocsps [][]byte) []byte {
	var fields []byte
	seqOf := func(items [][]byte) []byte {
		var b []byte
		for _, it := range items {
			b = append(b, it...)
		}
		return mustMarshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: b})
	}
	if len(crls) > 0 {
		fields = append(fields, mustMarshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: seqOf(crls)})...)
	}
	if len(ocsps) > 0 {
		fields = append(fields, mustMarshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: seqOf(ocsps)})...)
	}
	return mustMarshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: fields})
}

// Timestamp returns an RFC 3161 timestamp token by tsa over data at t.
func Timestamp(tsa *Identity, data []byte, t time.Time) []byte {
	h := crypto.SHA256.New()
	h.Write(data)
	return TimestampDigest(tsa, h.Sum(nil), t)
}

// TimestampDigest is Timestamp for a SHA-256 digest of the data.
func TimestampDigest(tsa *Identity, digest []byte, t time.Time) []byte {
	info := mustMarshal(struct {
		Version int
		Policy  asn1.ObjectIdentifier
		Imprint struct {
			Alg  pkix.AlgorithmIdentifier
			Hash []byte
		}
		Serial  *big.Int
		GenTime time.Time `asn1:"generalized"`
	}{1, asn1.ObjectIdentifier{1, 2, 3, 4}, struct {
		Alg  pkix.AlgorithmIdentifier
		Hash []byte
	}{pkix.AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.NullRawValue}, digest}, nextSerial(), t.UTC()})
	return Sign(tsa, nil, Options{Content: info, ContentType: oidTSTInfo})
}

// CRL returns a revocation list by ca that revokes the serial numbers of
// revoked at at.
func CRL(ca *Identity, at time.Time, revoked ...*x509.Certificate) []byte {
	tmpl := &x509.RevocationList{Number: nextSerial(), ThisUpdate: at, NextUpdate: at.AddDate(0, 1, 0)}
	for _, c := range revoked {
		tmpl.RevokedCertificateEntries = append(tmpl.RevokedCertificateEntries,
			x509.RevocationListEntry{SerialNumber: c.SerialNumber, RevocationTime: at})
	}
	return must(x509.CreateRevocationList(rand.Reader, tmpl, ca.Cert, ca.Key))
}

// OCSP returns an OCSP response by issuer that says cert is good, or revoked
// at revokedAt when that is not zero.
func OCSP(issuer *Identity, cert *x509.Certificate, at, revokedAt time.Time) []byte {
	return OCSPWith(issuer, cert, at, OCSPOptions{RevokedAt: revokedAt})
}

// OCSPOptions tune OCSPWith.
type OCSPOptions struct {
	// RevokedAt, if set, says the certificate was revoked then.
	RevokedAt time.Time
	// Unknown says the responder does not know the certificate.
	Unknown bool
	// ByKey names the responder by the hash of its key, not its name.
	ByKey bool
	// Responder, if set, signs the response in the issuer's place and is
	// carried in it, as a delegated responder is.
	Responder *Identity
}

// OCSPWith returns an OCSP response about cert, issued by issuer, as o says.
func OCSPWith(issuer *Identity, cert *x509.Certificate, at time.Time, o OCSPOptions) []byte {
	signer := issuer
	if o.Responder != nil {
		signer = o.Responder
	}
	status := asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0}
	switch {
	case o.Unknown:
		status.Tag = 2
	case !o.RevokedAt.IsZero():
		status = asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true,
			Bytes: mustMarshal(o.RevokedAt.UTC(), "generalized")}
	}
	responder := asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: signer.Cert.RawSubject}
	if o.ByKey {
		responder = asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, IsCompound: true, Bytes: mustMarshal(keyHash(signer.Cert))}
	}
	var certs []asn1.RawValue
	if o.Responder != nil {
		certs = []asn1.RawValue{raw(o.Responder.Cert.Raw)}
	}
	type certID struct {
		Alg      pkix.AlgorithmIdentifier
		NameHash []byte
		KeyHash  []byte
		Serial   *big.Int
	}
	type single struct {
		ID         certID
		Status     asn1.RawValue
		ThisUpdate time.Time `asn1:"generalized"`
	}
	tbs := mustMarshal(struct {
		Responder  asn1.RawValue
		ProducedAt time.Time `asn1:"generalized"`
		Responses  []single
	}{
		responder,
		at.UTC(),
		[]single{{certID{pkix.AlgorithmIdentifier{Algorithm: oidSHA1, Parameters: asn1.NullRawValue},
			sha1Sum(issuer.Cert.RawSubject), keyHash(issuer.Cert), cert.SerialNumber}, status, at.UTC()}},
	})
	alg, sig := sign(signer, Options{Hash: crypto.SHA256}, tbs, false)
	basic := mustMarshal(struct {
		TBS   asn1.RawValue
		Alg   pkix.AlgorithmIdentifier
		Sig   asn1.BitString
		Certs []asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}{raw(tbs), alg, asn1.BitString{Bytes: sig, BitLength: 8 * len(sig)}, certs})
	return mustMarshal(struct {
		Status   asn1.Enumerated
		Response struct {
			Type  asn1.ObjectIdentifier
			Bytes []byte
		} `asn1:"explicit,tag:0"`
	}{0, struct {
		Type  asn1.ObjectIdentifier
		Bytes []byte
	}{oidOCSPBasic, basic}})
}

func sha1Sum(b []byte) []byte {
	h := sha1.Sum(b)
	return h[:]
}

// keyHash returns the SHA-1 hash of c's public key bits, as OCSP names keys.
func keyHash(c *x509.Certificate) []byte {
	var spki struct {
		Alg pkix.AlgorithmIdentifier
		Key asn1.BitString
	}
	must(asn1.Unmarshal(c.RawSubjectPublicKeyInfo, &spki))
	return sha1Sum(spki.Key.RightAlign())
}

// Indefinite rewrites the outermost SEQUENCE of a DER value with an
// indefinite length, as some signers write, to test BER input.
func Indefinite(der []byte) []byte {
	var v asn1.RawValue
	must(asn1.Unmarshal(der, &v))
	out := []byte{der[0], 0x80}
	out = append(out, v.Bytes...)
	return append(out, 0, 0)
}
