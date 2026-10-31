package fontedit

import (
	"fmt"
	"strings"
)

// This file builds synthetic Type 1 font programs: a clear-text header and an
// eexec-encrypted Private dict holding Subrs and CharStrings, whose entries
// are themselves charstring-encrypted.

// type1Encrypt is the inverse of decryptType1 for one cipher: it prepends
// lead filler bytes (the random seed or lenIV padding) and encrypts.
func type1Encrypt(plain []byte, key uint16, lead int) []byte {
	R := key
	in := append(make([]byte, lead), plain...)
	out := make([]byte, len(in))
	for i, p := range in {
		c := p ^ byte(R>>8)
		R = (uint16(c)+R)*52845 + 22719
		out[i] = c
	}
	return out
}

// type1Entry is one Subrs or CharStrings entry: its name (or subr number) and
// its plaintext charstring.
type type1Entry struct {
	name string
	cs   []byte
}

// type1Spec describes a synthetic Type 1 program.
type type1Spec struct {
	header    string // clear text before "eexec"; defaults to a standard header
	lenIV     int    // the /lenIV declared, and the padding charstrings carry
	noLenIV   bool   // declare none: the padding is then the default 4
	rd        string // the RD operator; defaults to "RD"
	subrs     []type1Entry
	glyphs    []type1Entry
	noChars   bool   // omit /CharStrings
	privExtra string // raw text spliced into the Private dict
}

const type1DefaultHeader = "%!PS-AdobeFont-1.0: Test\n/FontMatrix [0.001 0 0 0.001 0 0] readonly def\n/Encoding StandardEncoding def\ncurrentfile eexec\n"

// build returns the whole program.
func (s type1Spec) build() []byte {
	header := s.header
	if header == "" {
		header = type1DefaultHeader
	}
	rd := s.rd
	if rd == "" {
		rd = "RD"
	}
	lead := s.lenIV
	if s.noLenIV {
		lead = 4
	}
	var priv strings.Builder
	priv.WriteString("dup /Private 8 dict dup begin\n")
	if !s.noLenIV {
		fmt.Fprintf(&priv, "/lenIV %d def\n", s.lenIV)
	}
	priv.WriteString(s.privExtra)
	if len(s.subrs) > 0 {
		fmt.Fprintf(&priv, "/Subrs %d array\n", len(s.subrs))
		for _, e := range s.subrs {
			enc := type1Encrypt(e.cs, 4330, lead)
			fmt.Fprintf(&priv, "dup %s %d %s ", e.name, len(enc), rd)
			priv.Write(enc)
			priv.WriteString(" NP\n")
		}
	}
	if !s.noChars {
		fmt.Fprintf(&priv, "2 index /CharStrings %d dict dup begin\n", len(s.glyphs))
		for _, e := range s.glyphs {
			enc := type1Encrypt(e.cs, 4330, lead)
			fmt.Fprintf(&priv, "/%s %d %s ", e.name, len(enc), rd)
			priv.Write(enc)
			priv.WriteString(" ND\n")
		}
		priv.WriteString("end\n")
	}
	return append([]byte(header), type1Encrypt([]byte(priv.String()), 55665, 4)...)
}

// newType1 returns a spec with the usual lenIV of 4.
func newType1() type1Spec { return type1Spec{lenIV: 4} }

// hsbwCS is a charstring "0 wx hsbw endchar" for a width in 108..1131.
func hsbwCS(wx int) []byte {
	v := wx - 108
	return []byte{139, byte(247 + v/256), byte(v % 256), 13, 14}
}
