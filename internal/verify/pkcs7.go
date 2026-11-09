package verify

// The PDF Signature in a signature dictionary's Contents is a PKCS#7
// SignedData object (RFC 2315). PDF/A-2 6.4.3 asks only how many signers it
// has and whether it carries certificates, so it is read with a minimal BER
// reader: Go's encoding/asn1 accepts DER alone, and signers commonly write
// indefinite lengths.

// ASN.1 universal tags and the context-specific [0] tag the reader needs.
const (
	tagInteger   = 0x02
	tagBitString = 0x03
	tagOID       = 0x06
	tagSequence  = 0x30
	tagSet       = 0x31
	tagContext0  = 0xa0
)

// oidSignedData is the DER content of id-signedData, 1.2.840.113549.1.7.2.
const oidSignedData = "\x2a\x86\x48\x86\xf7\x0d\x01\x07\x02"

// berElement is one BER-encoded value: its identifier octet and its content.
// A tag number above 30, which needs more identifier octets, is not supported.
type berElement struct {
	tag     byte
	content []byte
}

// maxBERDepth bounds the nesting of indefinite-length values readBER follows.
const maxBERDepth = 32

// readBER splits the first BER value off b. ok is false for a truncated or
// malformed value.
func readBER(b []byte) (e berElement, rest []byte, ok bool) {
	return readBERDepth(b, 0)
}

func readBERDepth(b []byte, depth int) (e berElement, rest []byte, ok bool) {
	if len(b) < 2 || b[0]&0x1f == 0x1f || depth > maxBERDepth {
		return berElement{}, nil, false
	}
	e.tag = b[0]
	n := int(b[1])
	b = b[2:]
	switch {
	case n == 0x80: // indefinite length: constructed content up to 00 00
		if e.tag&0x20 == 0 {
			return berElement{}, nil, false
		}
		body := b
		for {
			if len(b) >= 2 && b[0] == 0 && b[1] == 0 {
				e.content = body[:len(body)-len(b)]
				return e, b[2:], true
			}
			if _, b, ok = readBERDepth(b, depth+1); !ok {
				return berElement{}, nil, false
			}
		}
	case n > 0x80:
		size := n & 0x7f
		if size > 4 || len(b) < size {
			return berElement{}, nil, false
		}
		n = 0
		for _, c := range b[:size] {
			n = n<<8 | int(c)
		}
		b = b[size:]
	}
	if n < 0 || n > len(b) {
		return berElement{}, nil, false
	}
	e.content = b[:n]
	return e, b[n:], true
}

// berChildren splits a constructed value's content into its elements.
func berChildren(content []byte) ([]berElement, bool) {
	var kids []berElement
	for len(content) > 0 {
		var e berElement
		var ok bool
		if e, content, ok = readBER(content); !ok {
			return nil, false
		}
		kids = append(kids, e)
	}
	return kids, true
}

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
	ci, _, ok := readBER(der)
	if !ok || ci.tag != tagSequence {
		return signedDataSummary{}
	}
	parts, ok := berChildren(ci.content)
	if !ok || len(parts) < 2 || parts[0].tag != tagOID || string(parts[0].content) != oidSignedData ||
		parts[1].tag != tagContext0 {
		return signedDataSummary{}
	}
	sd, _, ok := readBER(parts[1].content)
	if !ok || sd.tag != tagSequence {
		return signedDataSummary{}
	}
	// version, digestAlgorithms, contentInfo, [0] certificates, [1] crls,
	// signerInfos.
	fields, ok := berChildren(sd.content)
	if !ok || len(fields) < 4 || fields[0].tag != tagInteger {
		return signedDataSummary{}
	}
	var s signedDataSummary
	if last := fields[len(fields)-1]; last.tag == tagSet {
		if signers, ok := berChildren(last.content); ok {
			s.signers = len(signers)
		}
	}
	if certs := fields[3]; certs.tag == tagContext0 {
		n, ok := countCertificates(certs.content)
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
	elems, ok := berChildren(content)
	if !ok {
		return 0, false
	}
	for _, e := range elems {
		if e.tag != tagSequence {
			continue
		}
		parts, ok := berChildren(e.content)
		if !ok || len(parts) != 3 || parts[0].tag != tagSequence || parts[1].tag != tagSequence ||
			parts[2].tag != tagBitString {
			return 0, false
		}
		n++
	}
	return n, true
}
