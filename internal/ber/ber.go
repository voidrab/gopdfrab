// Package ber reads ASN.1 values in the Basic Encoding Rules (X.690), which
// PDF signers commonly write with indefinite lengths that Go's encoding/asn1
// rejects. It depends on nothing beyond the standard library's errors package,
// so the light verify package can read a signature's structure without the
// cryptography that checks it.
package ber

import "errors"

// ASN.1 universal tags and the context-specific [0] tag the readers need.
const (
	TagInteger   = 0x02
	TagBitString = 0x03
	TagOID       = 0x06
	TagSequence  = 0x30
	TagSet       = 0x31
	TagContext0  = 0xa0
)

// Element is one BER-encoded value: its identifier octet and its content.
// A tag number above 30, which needs more identifier octets, is not supported.
type Element struct {
	Tag     byte
	Content []byte
}

// maxDepth bounds the nesting the readers follow.
const maxDepth = 32

// ReadBER splits the first BER value off b. ok is false for a truncated or
// malformed value.
func ReadBER(b []byte) (e Element, rest []byte, ok bool) {
	return readBER(b, 0)
}

func readBER(b []byte, depth int) (e Element, rest []byte, ok bool) {
	if len(b) < 2 || b[0]&0x1f == 0x1f || depth > maxDepth {
		return Element{}, nil, false
	}
	e.Tag = b[0]
	n := int(b[1])
	b = b[2:]
	switch {
	case n == 0x80: // indefinite length: constructed content up to 00 00
		if e.Tag&0x20 == 0 {
			return Element{}, nil, false
		}
		body := b
		for {
			if len(b) >= 2 && b[0] == 0 && b[1] == 0 {
				e.Content = body[:len(body)-len(b)]
				return e, b[2:], true
			}
			if _, b, ok = readBER(b, depth+1); !ok {
				return Element{}, nil, false
			}
		}
	case n > 0x80:
		size := n & 0x7f
		if size > 4 || len(b) < size {
			return Element{}, nil, false
		}
		n = 0
		for _, c := range b[:size] {
			n = n<<8 | int(c)
		}
		b = b[size:]
	}
	if n < 0 || n > len(b) {
		return Element{}, nil, false
	}
	e.Content = b[:n]
	return e, b[n:], true
}

// Children splits a constructed value's content into its elements.
func Children(content []byte) ([]Element, bool) {
	var kids []Element
	for len(content) > 0 {
		var e Element
		var ok bool
		if e, content, ok = ReadBER(content); !ok {
			return nil, false
		}
		kids = append(kids, e)
	}
	return kids, true
}

// errBER is returned by ToDER for input that is not BER it can read.
var errBER = errors.New("ber: malformed BER")

// ToDER rewrites the first BER value of b in DER's form: every length definite
// and minimal, and every constructed string flattened into a primitive one.
// The order of SET elements is kept, since a signature covers the bytes as
// written. Bytes after the value, such as the zero padding of a PDF Contents
// string, are dropped.
func ToDER(b []byte) ([]byte, error) {
	out, _, err := toDER(b, 0)
	return out, err
}

// stringTags are the universal string types BER may split into pieces. A
// BIT STRING may be split too, but each piece carries its own unused-bits
// octet, so it is left constructed, and encoding/asn1 rejects it.
var stringTags = map[byte]bool{
	0x04: true, 0x0c: true, 0x12: true, 0x13: true, 0x14: true, 0x16: true, 0x17: true,
	0x18: true, 0x1a: true, 0x1e: true,
}

func toDER(b []byte, depth int) (out, rest []byte, err error) {
	e, rest, ok := readBER(b, depth)
	if !ok {
		return nil, nil, errBER
	}
	if e.Tag&0x20 == 0 {
		return encode(e.Tag, e.Content), rest, nil
	}
	if prim := e.Tag &^ 0x20; e.Tag&0xc0 == 0 && stringTags[prim] {
		// A constructed string: the content is its pieces, each of which
		// may itself be constructed.
		flat, err := flatten(e.Content, prim, depth+1)
		if err != nil {
			return nil, nil, err
		}
		return encode(prim, flat), rest, nil
	}
	var body []byte
	for c := e.Content; len(c) > 0; {
		var kid []byte
		if kid, c, err = toDER(c, depth+1); err != nil {
			return nil, nil, err
		}
		body = append(body, kid...)
	}
	return encode(e.Tag, body), rest, nil
}

// flatten joins the pieces of a constructed string of type prim.
func flatten(content []byte, prim byte, depth int) ([]byte, error) {
	var out []byte
	for len(content) > 0 {
		e, rest, ok := readBER(content, depth)
		if !ok || e.Tag&^0x20 != prim {
			return nil, errBER
		}
		if e.Tag&0x20 == 0 {
			out = append(out, e.Content...)
		} else {
			inner, err := flatten(e.Content, prim, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, inner...)
		}
		content = rest
	}
	return out, nil
}

// encode returns the DER encoding of a value with tag and content.
func encode(tag byte, content []byte) []byte {
	n := len(content)
	out := make([]byte, 0, n+6)
	out = append(out, tag)
	switch {
	case n < 0x80:
		out = append(out, byte(n))
	default:
		var l []byte
		for v := n; v > 0; v >>= 8 {
			l = append([]byte{byte(v)}, l...)
		}
		out = append(out, 0x80|byte(len(l)))
		out = append(out, l...)
	}
	return append(out, content...)
}
