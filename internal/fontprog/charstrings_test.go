package fontprog

import (
	"bytes"
	"testing"
)

func TestParseType1Charstrings(t *testing.T) {
	s := newType1()
	s.subrs = []type1Entry{{"0", []byte{11}}}
	s.glyphs = []type1Entry{{"a", hsbwCS(500)}, {"b", []byte{14}}}
	p, ok := ParseType1Charstrings(s.build())
	if !ok {
		t.Fatal("a well-formed program was rejected")
	}
	glyphs, subrs := p.glyphs, p.subrs
	if !bytes.Equal(glyphs["a"], hsbwCS(500)) || !bytes.Equal(glyphs["b"], []byte{14}) {
		t.Errorf("glyphs = %v, want the decrypted charstrings by name", glyphs)
	}
	if len(subrs) != 1 || !bytes.Equal(subrs[0], []byte{11}) {
		t.Errorf("subrs = %v, want the one decrypted subr", subrs)
	}

	// An entry whose declared length runs past the section is left out.
	trunc := newType1()
	trunc.glyphs = []type1Entry{{"a", hsbwCS(500)}}
	trunc.privExtra = "/CharStrings 1 dict dup begin\n/huge 99999 RD x ND\n"
	if p, ok := ParseType1Charstrings(trunc.build()); !ok || p.glyphs["huge"] != nil {
		t.Errorf("ParseType1Charstrings = %v, %v, want the overlong entry skipped", p, ok)
	}

	noChars := newType1()
	noChars.noChars = true
	if _, ok := ParseType1Charstrings(noChars.build()); ok {
		t.Error("a program without CharStrings was accepted")
	}
}

func TestParseCFFSubrsNameKeyed(t *testing.T) {
	s := cffSpec{
		charstrings: [][]byte{{14}},
		private:     []byte{cffSmall(0), 20},
		lsubrs:      [][]byte{{11}, {cffSmall(1), 11}},
		gsubrs:      [][]byte{{cffSmall(2), 11}},
	}
	cff := s.assemble()
	td, ok := ParseCFFTopDict(cff)
	if !ok {
		t.Fatal("top dict")
	}
	subrs := ParseCFFSubrs(cff, td, 1)
	if len(subrs.Global) != 1 || !bytes.Equal(subrs.Global[0], s.gsubrs[0]) {
		t.Errorf("Global = %v, want the Global Subr INDEX", subrs.Global)
	}
	// A name-keyed program has one local INDEX, shared by every glyph.
	for _, gid := range []int{0, 5} {
		if got := subrs.Local(gid); len(got) != 2 || !bytes.Equal(got[1], s.lsubrs[1]) {
			t.Errorf("Local(%d) = %v, want the Private DICT's Subrs", gid, got)
		}
	}
}

func TestParseCFFSubrsCIDKeyed(t *testing.T) {
	s := cffSpec{
		cid:         true,
		cids:        []int{1, 2},
		charstrings: [][]byte{{14}, {14}, {14}},
		fds: []cffFD{
			{private: []byte{cffSmall(0), 20}, lsubrs: [][]byte{{cffSmall(10), 11}}},
			{private: []byte{cffSmall(0), 20}, lsubrs: [][]byte{{cffSmall(20), 11}}},
			{garbage: true},
		},
		fdSelect: []byte{0, 0, 1, 2}, // format 0: gid0->FD0, gid1->FD1, gid2->FD2
	}
	cff := s.assemble()
	td, ok := ParseCFFTopDict(cff)
	if !ok {
		t.Fatal("top dict")
	}
	subrs := ParseCFFSubrs(cff, td, 3)
	if got := subrs.Local(0); len(got) != 1 || !bytes.Equal(got[0], s.fds[0].lsubrs[0]) {
		t.Errorf("Local(0) = %v, want FD 0's Subrs", got)
	}
	if got := subrs.Local(1); len(got) != 1 || !bytes.Equal(got[0], s.fds[1].lsubrs[0]) {
		t.Errorf("Local(1) = %v, want FD 1's Subrs", got)
	}
	// FD 2 is unreadable, and gids outside FDSelect have no Font DICT.
	for _, gid := range []int{2, -1, 3} {
		if got := subrs.Local(gid); got != nil {
			t.Errorf("Local(%d) = %v, want none", gid, got)
		}
	}

	// FDSelect naming a Font DICT the FDArray lacks.
	s.fdSelect = []byte{0, 0, 7, 0}
	cff = s.assemble()
	td, _ = ParseCFFTopDict(cff)
	if got := ParseCFFSubrs(cff, td, 3).Local(1); got != nil {
		t.Errorf("Local(1) = %v, want none for an FD index past the FDArray", got)
	}
}

func TestCFFSubr(t *testing.T) {
	subrs := make([][]byte, 3)
	subrs[2] = []byte{11}
	// Fewer than 1240 subrs: bias 107.
	if got, ok := CFFSubr(subrs, 2-107); !ok || !bytes.Equal(got, []byte{11}) {
		t.Errorf("CFFSubr(-105) = %v %v, want subr 2", got, ok)
	}
	for _, op := range []int{-108, 3 - 107} {
		if _, ok := CFFSubr(subrs, op); ok {
			t.Errorf("CFFSubr(%d) resolved outside the INDEX", op)
		}
	}
}
