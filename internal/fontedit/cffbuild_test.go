package fontedit

import (
	"encoding/binary"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

// This file builds synthetic CFF programs for the width, charset and Top DICT
// tests. Every offset operand is a fixed-width 5-byte integer so the size of
// each block is known before the offsets are, which lets assemble lay the
// program out in a single forward pass after measuring.

// cffInt32 encodes v as a CFF DICT 5-byte integer operand.
func cffInt32(v int) []byte {
	b := []byte{29, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(v))
	return b
}

// cffSmall encodes a small integer (-107..107) as a one-byte DICT operand.
func cffSmall(v int) byte { return byte(v + 139) }

// cffRealOperand encodes a decimal string such as "0.001" or "-1E-3" as a
// nibble-encoded DICT real.
func cffRealOperand(s string) []byte {
	var nibs []byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			nibs = append(nibs, c-'0')
		case c == '.':
			nibs = append(nibs, 0xA)
		case c == '-':
			nibs = append(nibs, 0xE)
		case c == 'E' && i+1 < len(s) && s[i+1] == '-':
			nibs = append(nibs, 0xC)
			i++
		case c == 'E':
			nibs = append(nibs, 0xB)
		}
	}
	nibs = append(nibs, 0xF)
	if len(nibs)%2 == 1 {
		nibs = append(nibs, 0xF)
	}
	out := []byte{30}
	for i := 0; i < len(nibs); i += 2 {
		out = append(out, nibs[i]<<4|nibs[i+1])
	}
	return out
}

// cffIndex encodes entries as a CFF INDEX (offset size 1, or 2 when needed).
func cffIndex(entries [][]byte) []byte {
	if len(entries) == 0 {
		return []byte{0, 0}
	}
	total := 1
	for _, e := range entries {
		total += len(e)
	}
	osz := 1
	if total > 255 {
		osz = 2
	}
	out := []byte{byte(len(entries) >> 8), byte(len(entries)), byte(osz)}
	put := func(v int) {
		if osz == 1 {
			out = append(out, byte(v))
		} else {
			out = append(out, byte(v>>8), byte(v))
		}
	}
	off := 1
	put(off)
	for _, e := range entries {
		off += len(e)
		put(off)
	}
	for _, e := range entries {
		out = append(out, e...)
	}
	return out
}

// cffFD describes one Font DICT of a CID-keyed spec.
type cffFD struct {
	private   []byte   // Private DICT body, without the Subrs operator
	lsubrs    [][]byte // local Subrs, placed right after the Private DICT
	matrix    bool     // the Font DICT declares its own FontMatrix
	noPrivate bool     // omit the Private operator
	garbage   bool     // append an operand cffDictNumbers cannot decode
	extra     []byte   // further entries, after Private
}

// cffSpec describes a synthetic CFF program. The zero value is a name-keyed
// font with no glyphs.
type cffSpec struct {
	cid           bool
	customStrings []string
	sids          []int // name-keyed charset, one SID per glyph from gid 1
	cids          []int // CID-keyed charset, one CID per glyph from gid 1
	charstrings   [][]byte
	matrix        []string // Top DICT FontMatrix elements
	private       []byte
	lsubrs        [][]byte
	noPrivate     bool
	cidPrivate    bool // a CID-keyed Top DICT with a Private operator all the same
	gsubrs        [][]byte
	fds           []cffFD
	fdSelect      []byte // raw FDSelect table; nil omits the operator
	charsetRaw    []byte // replaces the generated charset table
	noCharset     bool
	encoding      []byte // custom Encoding table, placed after the charset
	csRaw         []byte // replaces the generated CharStrings INDEX
	noCS          bool
	noFDArray     bool
	fdArrayRaw    []byte // replaces the generated FDArray INDEX
}

func (s cffSpec) privateBlock() (dict, subrs []byte) {
	dict = append([]byte{}, s.private...)
	if len(s.lsubrs) > 0 {
		dict = append(dict, cffInt32(len(dict)+6)...)
		dict = append(dict, 19)
		subrs = cffIndex(s.lsubrs)
	}
	return dict, subrs
}

// assemble lays the program out and returns its bytes.
func (s cffSpec) assemble() []byte {
	build := func(off map[string]int) []byte {
		var top []byte
		if s.cid {
			top = append(top, cffSmall(0), cffSmall(0), cffSmall(0), 12, 30)
		}
		for _, m := range s.matrix {
			top = append(top, cffRealOperand(m)...)
		}
		if len(s.matrix) > 0 {
			top = append(top, 12, 7)
		}
		if !s.noCharset {
			top = append(top, cffInt32(off["charset"])...)
			top = append(top, 15)
		}
		if s.encoding != nil {
			top = append(top, cffInt32(off["encoding"])...)
			top = append(top, 16)
		}
		if !s.noCS {
			top = append(top, cffInt32(off["cs"])...)
			top = append(top, 17)
		}
		if (!s.cid || s.cidPrivate) && !s.noPrivate {
			pd, _ := s.privateBlock()
			top = append(top, cffInt32(len(pd))...)
			top = append(top, cffInt32(off["priv"])...)
			top = append(top, 18)
		}
		if s.cid && !s.noFDArray {
			top = append(top, cffInt32(off["fdarray"])...)
			top = append(top, 12, 36)
		}
		if s.cid && s.fdSelect != nil {
			top = append(top, cffInt32(off["fdselect"])...)
			top = append(top, 12, 37)
		}
		var strs [][]byte
		for _, c := range s.customStrings {
			strs = append(strs, []byte(c))
		}
		charset := s.charsetRaw
		if charset == nil {
			charset = []byte{0}
			ids := s.sids
			if s.cid {
				ids = s.cids
			}
			for _, id := range ids {
				charset = append(charset, byte(id>>8), byte(id))
			}
		}
		cs := s.csRaw
		if cs == nil {
			cs = cffIndex(s.charstrings)
		}
		var out []byte
		out = append(out, 1, 0, 4, 1)
		out = append(out, cffIndex([][]byte{[]byte("Font")})...)
		out = append(out, cffIndex([][]byte{top})...)
		out = append(out, cffIndex(strs)...)
		out = append(out, cffIndex(s.gsubrs)...)
		set := func(name string) { off[name] = len(out) }
		set("charset")
		out = append(out, charset...)
		set("encoding")
		out = append(out, s.encoding...)
		set("cs")
		out = append(out, cs...)
		if !s.cid {
			set("priv")
			pd, sub := s.privateBlock()
			out = append(out, pd...)
			out = append(out, sub...)
			return out
		}
		// CID: FDArray, FDSelect, then each Font DICT's Private block.
		var fdDicts [][]byte
		var privs []byte
		// First measure the FDArray with zero offsets to learn its length.
		mk := func(base int) ([][]byte, []byte) {
			var dicts [][]byte
			var blob []byte
			for _, fd := range s.fds {
				ps := cffSpec{private: fd.private, lsubrs: fd.lsubrs}
				pd, sub := ps.privateBlock()
				var d []byte
				if !fd.noPrivate {
					d = append(d, cffInt32(len(pd))...)
					d = append(d, cffInt32(base+len(blob))...)
					d = append(d, 18)
				}
				d = append(d, fd.extra...)
				if fd.matrix {
					d = append(d, cffRealOperand("0.001")...)
					d = append(d, cffRealOperand("0")...)
					d = append(d, 12, 7)
				}
				if fd.garbage {
					d = append(d, 247) // two-byte operand with no second byte
				}
				dicts = append(dicts, d)
				blob = append(blob, pd...)
				blob = append(blob, sub...)
			}
			return dicts, blob
		}
		set("fdarray")
		fdDicts, _ = mk(0)
		fdArr := s.fdArrayRaw
		if fdArr == nil {
			fdArr = cffIndex(fdDicts)
		}
		off["fdselect"] = len(out) + len(fdArr)
		base := off["fdselect"] + len(s.fdSelect)
		fdDicts, privs = mk(base)
		if s.fdArrayRaw == nil {
			fdArr = cffIndex(fdDicts)
		}
		out = append(out, fdArr...)
		out = append(out, s.fdSelect...)
		out = append(out, privs...)
		return out
	}
	off := map[string]int{}
	build(off)
	return build(off)
}

// stdSID looks up a standard string's SID, for readable specs.
func stdSID(name string) int {
	for i, s := range fontprog.CffStandardStrings {
		if s == name {
			return i
		}
	}
	panic("not a standard CFF string: " + name)
}

// widthPrivate declares defaultWidthX 500 and nominalWidthX 600.
var widthPrivate = []byte{248, 136, 20, 248, 236, 21}
