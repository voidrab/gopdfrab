package cms

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"time"
)

// Timestamp is an RFC 3161 timestamp token: a SignedData whose content is a
// TSTInfo.
type Timestamp struct {
	// Time is when the authority says it saw the imprint.
	Time time.Time
	// Imprint is the digest of the timestamped data under ImprintHash.
	Imprint     []byte
	ImprintHash crypto.Hash
	// Signer is the authority's signature over the token.
	Signer *Signer
	// Certificates are those the token carries.
	Certificates []*x509.Certificate

	content []byte // the TSTInfo, which the signer signs
}

type tstInfo struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint messageImprint
	Serial         *big.Int
	GenTime        time.Time `asn1:"generalized"`
	// The optional fields after genTime are not read; encoding/asn1 allows
	// trailing elements.
}

type messageImprint struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	HashedMessage []byte
}

// ParseTimestamp reads a timestamp token.
func ParseTimestamp(ber []byte) (*Timestamp, error) {
	sd, err := Parse(ber)
	if err != nil {
		return nil, err
	}
	if !sd.ContentType.Equal(OIDTSTInfo) || sd.Content == nil {
		return nil, fmt.Errorf("%w: timestamp token without TSTInfo", ErrUnsupported)
	}
	if len(sd.Signers) != 1 {
		return nil, fmt.Errorf("cms: timestamp token with %d signers", len(sd.Signers))
	}
	var info tstInfo
	if _, err := asn1.Unmarshal(sd.Content, &info); err != nil {
		return nil, fmt.Errorf("cms: TSTInfo: %w", err)
	}
	alg := info.MessageImprint.HashAlgorithm.Algorithm
	h := HashOf(alg)
	if h == 0 {
		return nil, fmt.Errorf("%w imprint digest %v", ErrUnsupported, alg)
	}
	return &Timestamp{Time: info.GenTime, Imprint: info.MessageImprint.HashedMessage, ImprintHash: h,
		Signer: sd.Signers[0], Certificates: sd.Certificates, content: sd.Content}, nil
}

// Verify checks that the token stamps data, the bytes timestamped, and that
// its authority's signature over it verifies.
func (t *Timestamp) Verify(data []byte) error {
	return t.VerifyDigest(Sum(t.ImprintHash, data))
}

// VerifyDigest is Verify for data already hashed under ImprintHash.
func (t *Timestamp) VerifyDigest(digest []byte) error {
	if !bytes.Equal(digest, t.Imprint) {
		return fmt.Errorf("%w: the timestamp is for other data", ErrDigestMismatch)
	}
	return t.verifyToken()
}

// verifyToken checks the authority's signature over the TSTInfo.
func (t *Timestamp) verifyToken() error {
	if t.Signer.Hash == 0 {
		return fmt.Errorf("%w timestamp digest", ErrUnsupported)
	}
	return t.Signer.Verify(Sum(t.Signer.Hash, t.content))
}
