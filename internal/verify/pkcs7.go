package verify

import "github.com/voidrab/gopdfrab/internal/ber"

// The PDF Signature in a signature dictionary's Contents is a PKCS#7
// SignedData object (RFC 2315). PDF/A-2 6.4.3 asks only how many signers it
// has and whether it carries certificates, so it is read with package ber:
// Go's encoding/asn1 accepts DER alone, and signers commonly write indefinite
// lengths.

// oidSignedData is the DER content of id-signedData, 1.2.840.113549.1.7.2.
const oidSignedData = "\x2a\x86\x48\x86\xf7\x0d\x01\x07\x02"

// signedDataSummary is what 6.4.3 asks of a PDF Signature: the number of
// SignerInfo structures, and whether at least one certificate is present and
// every certificate is well formed.
type signedDataSummary struct {
	signers      int
	certificates bool
}

// readSignedData summarizes a PKCS#7 ContentInfo holding SignedData. Bytes
// after the ContentInfo, such as the zero padding of a Contents string, are
// ignored. Anything that is not SignedData summarizes to no signers and no
// certificates.
func readSignedData(der []byte) signedDataSummary {
	ci, _, ok := ber.ReadBER(der)
	if !ok || ci.Tag != ber.TagSequence {
		return signedDataSummary{}
	}
	parts, ok := ber.Children(ci.Content)
	if !ok || len(parts) < 2 || parts[0].Tag != ber.TagOID || string(parts[0].Content) != oidSignedData ||
		parts[1].Tag != ber.TagContext0 {
		return signedDataSummary{}
	}
	sd, _, ok := ber.ReadBER(parts[1].Content)
	if !ok || sd.Tag != ber.TagSequence {
		return signedDataSummary{}
	}
	// version, digestAlgorithms, contentInfo, [0] certificates, [1] crls,
	// signerInfos.
	fields, ok := ber.Children(sd.Content)
	if !ok || len(fields) < 4 || fields[0].Tag != ber.TagInteger {
		return signedDataSummary{}
	}
	var s signedDataSummary
	if last := fields[len(fields)-1]; last.Tag == ber.TagSet {
		if signers, ok := ber.Children(last.Content); ok {
			s.signers = len(signers)
		}
	}
	if certs := fields[3]; certs.Tag == ber.TagContext0 {
		n, ok := countCertificates(certs.Content)
		if !ok {
			// veraPDF's PKCS#7 reader rejects the whole object.
			return signedDataSummary{}
		}
		s.certificates = n > 0
	}
	return s
}

// countCertificates counts the X.509 certificates in the content of a
// SignedData certificates field. ok is false when one of them lacks the outer
// shape of a certificate: a SEQUENCE of tbsCertificate, signatureAlgorithm and
// a BIT STRING signature. Elements of other kinds, such as attribute
// certificates, are skipped.
func countCertificates(content []byte) (n int, ok bool) {
	elems, ok := ber.Children(content)
	if !ok {
		return 0, false
	}
	for _, e := range elems {
		if e.Tag != ber.TagSequence {
			continue
		}
		parts, ok := ber.Children(e.Content)
		if !ok || len(parts) != 3 || parts[0].Tag != ber.TagSequence || parts[1].Tag != ber.TagSequence ||
			parts[2].Tag != ber.TagBitString {
			return 0, false
		}
		n++
	}
	return n, true
}
