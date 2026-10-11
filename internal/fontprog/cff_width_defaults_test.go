package fontprog

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"
)

// fixed16 encodes v as a Type 2 16.16 fixed operand.
func fixed16(v float64) []byte {
	b := []byte{255, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(int32(math.Round(v*65536))))
	return b
}

// fractionalDefaultsSpec is a name-keyed font whose Private DICT gives both
// width defaults as fractional reals, with a glyph for every way a charstring
// can carry (or not carry) its width.
func fractionalDefaultsSpec() cffSpec {
	private := append(cffRealOperand("500.5"), 20)
	private = append(private, cffRealOperand("634.78")...)
	private = append(private, 21)
	return cffSpec{
		sids: []int{stdSID("a"), stdSID("b"), stdSID("c"), stdSID("d")},
		charstrings: [][]byte{
			{14},                               // .notdef: the default width
			{cffSmall(100), 14},                // a: whole width operand
			append(fixed16(-79.25), 14),        // b: 16.16 width operand
			{247, 0, cffSmall(-107), 10},       // c: width, then a local subr that ends the glyph
			{cffSmall(0), cffSmall(0), 21, 14}, // d: rmoveto, no width
		},
		lsubrs:  [][]byte{{14}},
		private: private,
	}
}

// truncatingWidths reads every glyph's width the way a reader that truncates
// fractional width defaults does.
func truncatingWidths(cff []byte) []float64 {
	td, _ := ParseCFFTopDict(cff)
	d, n, lsubrs, _ := cffPrivateInfo(cff, td.PrivateOffset, td.PrivateSize)
	cs, _ := ParseCFFIndex(cff, td.CSOffset)
	out := make([]float64, len(cs))
	for i, c := range cs {
		out[i] = cffCharstringWidth(c, cffGlobalSubrs(cff), lsubrs, math.Trunc(d), math.Trunc(n))
	}
	return out
}

// TestNormalizeCFFWidthDefaults: the rewritten program has whole width
// defaults, every glyph keeps its width, and a reader that truncates the
// defaults now reads the same widths as one that does not. The old
// CharStrings and Private DICT were the program's tail, so they are dropped.
func TestNormalizeCFFWidthDefaults(t *testing.T) {
	src := fractionalDefaultsSpec().assemble()
	if got := truncatingWidths(src); got[2] == CFFAdvanceWidths(src)["b"] {
		t.Fatal("setup: a truncating reader already agrees; the fixture proves nothing")
	}

	out, changed := NormalizeCFFWidthDefaults(src)
	if !changed {
		t.Fatal("NormalizeCFFWidthDefaults reported no change")
	}
	td, _ := ParseCFFTopDict(out)
	d, n, _, _ := cffPrivateInfo(out, td.PrivateOffset, td.PrivateSize)
	if d != 500 && d != 501 || n != 635 {
		t.Errorf("defaults = (%v, %v), want whole numbers (501, 635)", d, n)
	}
	want := CFFAdvanceWidths(src)
	got := CFFAdvanceWidths(out)
	for name, w := range want {
		if math.Abs(got[name]-w) > 1e-3 {
			t.Errorf("width of %s = %v, want %v", name, got[name], w)
		}
	}
	names := CFFGlyphNames(out)
	for i, w := range truncatingWidths(out) {
		// CFFAdvanceWidths leaves out .notdef.
		if wantW, ok := want[names[i]]; ok && math.Abs(w-wantW) > 1e-3 {
			t.Errorf("a truncating reader reads %s as %v, want %v", names[i], w, wantW)
		}
	}
	if len(out) > len(src)+64 {
		t.Errorf("output is %d bytes for a %d-byte input; the old tail was kept", len(out), len(src))
	}

	if again, changed := NormalizeCFFWidthDefaults(out); changed || !bytes.Equal(again, out) {
		t.Error("a second pass changed the program")
	}
}

// TestNormalizeCFFWidthDefaultsKeepsWhatFollows: a charset placed after the
// CharStrings is still referenced, so the program is kept whole and the
// charset's offset moves with everything behind the Top DICT. The font also
// carries DICT entries the rewrite has no business with, which come through
// as they were, and a whole defaultWidthX that glyphs without a width keep.
func TestNormalizeCFFWidthDefaultsKeepsWhatFollows(t *testing.T) {
	spec := fractionalDefaultsSpec()
	spec.matrix = []string{"0.001", "0", "0", "0.001", "0", "0"}
	spec.private = append([]byte{cffSmall(50), 10}, append(append([]byte{}, cffInt32(500)...), 20)...)
	spec.private = append(spec.private, cffRealOperand("634.78")...)
	spec.private = append(spec.private, 21)
	src := relocateCharset(t, spec.assemble())
	out, changed := NormalizeCFFWidthDefaults(src)
	if !changed {
		t.Fatal("NormalizeCFFWidthDefaults reported no change")
	}
	if len(out) < len(src) {
		t.Errorf("output is %d bytes for a %d-byte input; part of the program was dropped", len(out), len(src))
	}
	if a, b := CFFGlyphNames(src), CFFGlyphNames(out); len(a) == 0 || !equalStrings(a, b) {
		t.Errorf("glyph names = %v, want %v", b, a)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// relocateCharset moves a built program's charset to its end and points the
// Top DICT at the copy, the builder having written that offset as a 5-byte
// integer.
func relocateCharset(t *testing.T, cff []byte) []byte {
	t.Helper()
	td, _ := ParseCFFTopDict(cff)
	_, nameEnd := ParseCFFIndex(cff, int(cff[2]))
	tops, _ := ParseCFFIndex(cff, nameEnd)
	at := bytes.Index(cff, tops[0])
	for _, e := range cffDictEntries(tops[0]) {
		if e.op == 15 {
			at += bytes.Index(tops[0], e.raw)
		}
	}
	n := len(CFFGlyphNames(cff))
	charset := cff[td.CharsetOffset : td.CharsetOffset+1+2*(n-1)]
	out := append(append([]byte{}, cff...), charset...)
	binary.BigEndian.PutUint32(out[at+1:], uint32(len(cff)))
	return out
}

// fractionalCIDSpec is a CID-keyed font with two Font DICTs. FDSelect puts
// .notdef and CIDs 1-3 in the first, whose Private DICT gives both width
// defaults as fractional reals and has local Subrs, and CIDs 4-5 in the
// second, whose defaults are whole already.
func fractionalCIDSpec() cffSpec {
	private := append(cffRealOperand("500.5"), 20)
	private = append(private, cffRealOperand("634.78")...)
	private = append(private, 21)
	return cffSpec{
		cid:  true,
		cids: []int{1, 2, 3, 4, 5},
		charstrings: [][]byte{
			{14},                         // .notdef: the default width
			{cffSmall(100), 14},          // CID 1: whole width operand
			append(fixed16(-79.25), 14),  // CID 2: 16.16 width operand
			{247, 0, cffSmall(-107), 10}, // CID 3: width, then a local subr that ends the glyph
			{cffSmall(20), 14},           // CID 4: second Font DICT, width operand
			{14},                         // CID 5: second Font DICT, default width
		},
		fds:      []cffFD{{private: private, lsubrs: [][]byte{{14}}}, {private: widthPrivate}},
		fdSelect: []byte{0, 0, 0, 0, 0, 1, 1},
	}
}

// truncatingCIDWidths reads every CID's width the way a reader that
// truncates fractional width defaults does.
func truncatingCIDWidths(cff []byte) map[int]float64 {
	td, _ := ParseCFFTopDict(cff)
	cs, _ := ParseCFFIndex(cff, td.CSOffset)
	fds, _ := ParseCFFIndex(cff, td.FDArrayOffset)
	fdIndex := parseCFFFDSelect(cff, td.FDSelect, len(cs))
	if fdIndex == nil {
		fdIndex = make([]int, len(cs))
	}
	cids := ParseCFFCharsetCIDs(cff, td.CharsetOffset, len(cs))
	out := map[int]float64{}
	for gid, c := range cs {
		off, size, _ := fontDictPrivate(fds[fdIndex[gid]])
		d, n, lsubrs, _ := cffPrivateInfo(cff, off, size)
		out[cids[gid]] = cffCharstringWidth(c, cffGlobalSubrs(cff), lsubrs, math.Trunc(d), math.Trunc(n))
	}
	return out
}

// checkCIDNormalized checks out against src: every Private DICT has whole
// width defaults, every CID keeps its width, and a truncating reader reads
// the same widths. It returns out's Private DICT width defaults, per Font
// DICT.
func checkCIDNormalized(t *testing.T, src, out []byte) [][2]float64 {
	t.Helper()
	want := CFFCIDAdvanceWidths(src)
	got := CFFCIDAdvanceWidths(out)
	if len(want) == 0 || len(got) != len(want) {
		t.Fatalf("widths = %v, want %v", got, want)
	}
	truncated := truncatingCIDWidths(out)
	for cid, w := range want {
		if math.Abs(got[cid]-w) > 1e-3 {
			t.Errorf("width of CID %d = %v, want %v", cid, got[cid], w)
		}
		if math.Abs(truncated[cid]-w) > 1e-3 {
			t.Errorf("a truncating reader reads CID %d as %v, want %v", cid, truncated[cid], w)
		}
	}
	td, _ := ParseCFFTopDict(out)
	fds, _ := ParseCFFIndex(out, td.FDArrayOffset)
	var defaults [][2]float64
	for _, fd := range fds {
		off, size, _ := fontDictPrivate(fd)
		d, n, _, _ := cffPrivateInfo(out, off, size)
		defaults = append(defaults, [2]float64{d, n})
	}
	return defaults
}

// TestNormalizeCFFWidthDefaultsCIDKeyed: each Font DICT's Private DICT gets
// whole width defaults and each glyph is re-encoded against its own Font
// DICT's. Glyphs of a Font DICT whose defaults were whole already are left
// byte for byte. FDSelect lies past the CharStrings, so the program is kept
// whole and FDSelect moves with it.
func TestNormalizeCFFWidthDefaultsCIDKeyed(t *testing.T) {
	src := fractionalCIDSpec().assemble()
	if truncatingCIDWidths(src)[2] == CFFCIDAdvanceWidths(src)[2] {
		t.Fatal("setup: a truncating reader already agrees; the fixture proves nothing")
	}

	out, changed := NormalizeCFFWidthDefaults(src)
	if !changed {
		t.Fatal("NormalizeCFFWidthDefaults reported no change")
	}
	defaults := checkCIDNormalized(t, src, out)
	if want := [][2]float64{{501, 635}, {500, 600}}; !reflect.DeepEqual(defaults, want) {
		t.Errorf("Font DICT width defaults = %v, want %v", defaults, want)
	}

	td, _ := ParseCFFTopDict(out)
	before, _ := ParseCFFIndex(src, mustTopDict(t, src).CSOffset)
	after, _ := ParseCFFIndex(out, td.CSOffset)
	for gid := 4; gid < len(before); gid++ {
		if !bytes.Equal(before[gid], after[gid]) {
			t.Errorf("glyph %d of the whole Font DICT was rewritten: % x, was % x", gid, after[gid], before[gid])
		}
	}
	if len(out) < len(src) {
		t.Errorf("output is %d bytes for a %d-byte input; part of the program was dropped", len(out), len(src))
	}
	if again, changed := NormalizeCFFWidthDefaults(out); changed || !bytes.Equal(again, out) {
		t.Error("a second pass changed the program")
	}
}

// TestNormalizeCFFWidthDefaultsKeepsEntries: a custom Encoding before the
// CharStrings moves with the Top DICT, and a Font DICT's entries other than
// Private come through as they were.
func TestNormalizeCFFWidthDefaultsKeepsEntries(t *testing.T) {
	encoding := []byte{0, 2, 'a', 'b'} // format 0: codes for gids 1 and 2
	spec := fractionalDefaultsSpec()
	spec.encoding = encoding
	src := spec.assemble()
	out, changed := NormalizeCFFWidthDefaults(src)
	if !changed {
		t.Fatal("name-keyed: NormalizeCFFWidthDefaults reported no change")
	}
	_, nameEnd := ParseCFFIndex(out, int(out[2]))
	tops, _ := ParseCFFIndex(out, nameEnd)
	found := false
	for _, e := range cffDictEntries(tops[0]) {
		if e.op == 16 {
			found = true
			if at := int(e.operands[0]); at+len(encoding) > len(out) || !bytes.Equal(out[at:at+len(encoding)], encoding) {
				t.Errorf("Encoding offset %d does not point at the Encoding", at)
			}
		}
	}
	if !found {
		t.Error("the Encoding entry was dropped")
	}

	fontName := []byte{cffSmall(0), 12, 38}
	cid := fractionalCIDSpec()
	cid.fds[0].extra = fontName
	src = cid.assemble()
	if out, changed = NormalizeCFFWidthDefaults(src); !changed {
		t.Fatal("CID-keyed: NormalizeCFFWidthDefaults reported no change")
	}
	checkCIDNormalized(t, src, out)
	fds, _ := ParseCFFIndex(out, mustTopDict(t, out).FDArrayOffset)
	if !bytes.HasSuffix(fds[0], fontName) {
		t.Errorf("Font DICT = % x, want it to keep its FontName entry", fds[0])
	}
}

// TestNormalizeCFFWidthDefaultsCIDDropsTail: with one Font DICT and no
// FDSelect, nothing the rewrite keeps lies past the old CharStrings, so the
// old CharStrings, FDArray and Private DICT are dropped.
func TestNormalizeCFFWidthDefaultsCIDDropsTail(t *testing.T) {
	spec := fractionalCIDSpec()
	spec.cids, spec.charstrings = spec.cids[:3], spec.charstrings[:4]
	spec.fds, spec.fdSelect = spec.fds[:1], nil
	src := spec.assemble()

	out, changed := NormalizeCFFWidthDefaults(src)
	if !changed {
		t.Fatal("NormalizeCFFWidthDefaults reported no change")
	}
	defaults := checkCIDNormalized(t, src, out)
	if want := [][2]float64{{501, 635}}; !reflect.DeepEqual(defaults, want) {
		t.Errorf("Font DICT width defaults = %v, want %v", defaults, want)
	}
	if len(out) > len(src)+64 {
		t.Errorf("output is %d bytes for a %d-byte input; the old tail was kept", len(out), len(src))
	}
}

func mustTopDict(t *testing.T, cff []byte) CFFTopDict {
	t.Helper()
	td, ok := ParseCFFTopDict(cff)
	if !ok {
		t.Fatal("Top DICT does not parse")
	}
	return td
}

// TestNormalizeCFFWidthDefaultsDeclines covers every program left as it is:
// nothing to do, nothing it can parse, and widths it cannot move with
// certainty.
func TestNormalizeCFFWidthDefaultsDeclines(t *testing.T) {
	whole := fractionalDefaultsSpec()
	whole.private = append([]byte{cffSmall(50), 20}, cffSmall(60), 21)

	cidWhole := fractionalCIDSpec()
	cidWhole.fds[0].private = widthPrivate

	cidMatrix := fractionalCIDSpec()
	cidMatrix.fds[0].matrix = true

	cidNoFDSelect := fractionalCIDSpec()
	cidNoFDSelect.fdSelect = nil

	cidMissingFD := fractionalCIDSpec()
	cidMissingFD.fdSelect = []byte{0, 0, 0, 0, 0, 2, 2}

	cidNoPrivate := fractionalCIDSpec()
	cidNoPrivate.fds[1].noPrivate = true

	cidGarbage := fractionalCIDSpec()
	cidGarbage.fds[1].garbage = true

	cidNoFDArray := fractionalCIDSpec()
	cidNoFDArray.noFDArray = true

	cidEmptyFDArray := fractionalCIDSpec()
	cidEmptyFDArray.fdArrayRaw = []byte{0, 0}

	cidTopPrivate := fractionalCIDSpec()
	cidTopPrivate.cidPrivate = true

	cidSubrWidth := fractionalCIDSpec()
	cidSubrWidth.charstrings[4] = []byte{cffSmall(-107), 10} // FD 1 has no Subrs

	noCS := fractionalDefaultsSpec()
	noCS.noCS = true

	badPrivate := fractionalDefaultsSpec().assemble()
	td, _ := ParseCFFTopDict(badPrivate)
	badPrivate = badPrivate[:td.PrivateOffset+2]

	empty := fractionalDefaultsSpec()
	empty.charstrings = nil

	unparsed := fractionalDefaultsSpec()
	unparsed.charstrings = [][]byte{{12, 9, 14}} // abs before any width

	fromSubr := fractionalDefaultsSpec()
	fromSubr.charstrings = [][]byte{{cffSmall(-107), 10}}
	fromSubr.lsubrs = [][]byte{{cffSmall(40), 14}} // the width lives in the subr

	badSubrs := fractionalDefaultsSpec()
	badSubrs.lsubrs, badSubrs.charstrings = nil, badSubrs.charstrings[:3]
	badSubrs.sids = badSubrs.sids[:2]
	badSubrs.private = append(append([]byte{}, badSubrs.private...), append(cffInt32(9999), 19)...)

	// A width the rewritten operand cannot hold: -32768 less the half unit
	// rounding nominalWidthX up to 1 moves it by. The self-check catches it.
	overflow := fractionalDefaultsSpec()
	overflow.private = append(cffRealOperand("0.5"), 21)
	overflow.sids, overflow.charstrings = overflow.sids[:1], [][]byte{{14}, {28, 0x80, 0x00, 14}}

	for name, cff := range map[string][]byte{
		"width out of range":          overflow.assemble(),
		"whole defaults":              whole.assemble(),
		"CID whole defaults":          cidWhole.assemble(),
		"Font DICT FontMatrix":        cidMatrix.assemble(),
		"two Font DICTs, no FDSelect": cidNoFDSelect.assemble(),
		"FDSelect names no Font DICT": cidMissingFD.assemble(),
		"Font DICT without Private":   cidNoPrivate.assemble(),
		"Font DICT does not parse":    cidGarbage.assemble(),
		"no FDArray":                  cidNoFDArray.assemble(),
		"empty FDArray":               cidEmptyFDArray.assemble(),
		"CID Top DICT with Private":   cidTopPrivate.assemble(),
		"CID width unreadable":        cidSubrWidth.assemble(),
		"unparseable":                 []byte("nope"),
		"no CharStrings":              noCS.assemble(),
		"Private out of range":        badPrivate,
		"no glyphs":                   empty.assemble(),
		"width unreadable":            unparsed.assemble(),
		"width in a subr":             fromSubr.assemble(),
		"Subrs out of range":          badSubrs.assemble(),
		"two Top DICTs":               twoTopDicts(t, fractionalDefaultsSpec().assemble()),
	} {
		if out, changed := NormalizeCFFWidthDefaults(cff); changed || !bytes.Equal(out, cff) {
			t.Errorf("%s: program was rewritten", name)
		}
	}
}

// twoTopDicts gives a built program a second, empty Top DICT, shortening the
// font's name by the byte the extra INDEX offset costs so that every offset
// stays where it was.
func twoTopDicts(t *testing.T, cff []byte) []byte {
	t.Helper()
	hdr := int(cff[2])
	names, nameEnd := ParseCFFIndex(cff, hdr)
	tops, topEnd := ParseCFFIndex(cff, nameEnd)
	out := append([]byte{}, cff[:hdr]...)
	out = append(out, encodeCFFIndex([][]byte{names[0][:len(names[0])-1]})...)
	out = append(out, encodeCFFIndex([][]byte{tops[0], {}})...)
	out = append(out, cff[topEnd:]...)
	if len(out) != len(cff) {
		t.Fatalf("rebuilt program is %d bytes, want %d", len(out), len(cff))
	}
	if _, ok := ParseCFFTopDict(out); !ok {
		t.Fatal("rebuilt program does not parse")
	}
	return out
}

// TestSameCFFWidths covers the self-check every rewrite must pass.
func TestSameCFFWidths(t *testing.T) {
	src := fractionalDefaultsSpec().assemble()
	out, _ := NormalizeCFFWidthDefaults(src)
	if !sameCFFWidths(src, out) {
		t.Fatal("a correct rewrite failed the check")
	}

	other := fractionalDefaultsSpec()
	other.charstrings[1] = []byte{cffSmall(90), 14}
	otherOut, _ := NormalizeCFFWidthDefaults(other.assemble())

	fewer := fractionalDefaultsSpec()
	fewer.sids, fewer.charstrings = fewer.sids[:1], fewer.charstrings[:2]
	fewerOut, _ := NormalizeCFFWidthDefaults(fewer.assemble())

	renamed := fractionalDefaultsSpec()
	renamed.sids[0] = stdSID("e")
	renamedOut, _ := NormalizeCFFWidthDefaults(renamed.assemble())

	cidSrc := fractionalCIDSpec().assemble()
	cidOut, _ := NormalizeCFFWidthDefaults(cidSrc)
	if !sameCFFWidths(cidSrc, cidOut) {
		t.Fatal("a correct CID-keyed rewrite failed the check")
	}

	cidMatrix := fractionalCIDSpec()
	cidMatrix.fds[0].matrix = true

	cidNoFDArray := fractionalCIDSpec()
	cidNoFDArray.noFDArray = true

	cidOther := fractionalCIDSpec()
	cidOther.charstrings[1] = []byte{cffSmall(90), 14}
	cidOtherOut, _ := NormalizeCFFWidthDefaults(cidOther.assemble())

	for name, tc := range map[string][2][]byte{
		"CID: a Font DICT with a FontMatrix": {cidSrc, cidMatrix.assemble()},
		"CID: defaults still fractional":     {cidSrc, cidSrc},
		"CID: no FDArray":                    {cidSrc, cidNoFDArray.assemble()},
		"CID: a width changed":               {cidSrc, cidOtherOut},
		"output does not parse":              {src, []byte("nope")},
		"defaults still fractional":          {src, src},
		"different glyph count":              {src, fewerOut},
		"a glyph renamed":                    {src, renamedOut},
		"a width changed":                    {src, otherOut},
		"input has no widths":                {[]byte("nope"), out},
	} {
		if sameCFFWidths(tc[0], tc[1]) {
			t.Errorf("%s: check passed", name)
		}
	}
}

// TestType2NumberRoundTrip: every operand form encodes to the shortest
// encoding and decodes back to its value.
func TestType2NumberRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		size int
	}{
		{0, 1}, {107, 1}, {-107, 1}, {108, 2}, {1131, 2}, {-108, 2}, {-1131, 2},
		{1132, 3}, {-1132, 3}, {32767, 3}, {-32768, 3}, {0.5, 5}, {-79.25, 5},
	} {
		enc := encodeType2Number(tc.v)
		v, n, ok := type2Number(enc)
		if len(enc) != tc.size || !ok || n != tc.size || v != tc.v {
			t.Errorf("%v: encoded %v (%d bytes), decoded (%v, %d, %v)", tc.v, enc, len(enc), v, n, ok)
		}
	}
	if enc := encodeType2Number(40000); len(enc) != 5 || enc[0] != 255 {
		t.Errorf("40000 = %v, want a 16.16 operand", enc)
	}
	for _, cs := range [][]byte{nil, {247}, {251}, {28, 0}, {255, 0, 0}, {14}} {
		if _, _, ok := type2Number(cs); ok {
			t.Errorf("%v decoded as a number", cs)
		}
	}
}

// TestEncodeCFFIndexOffsetSize: the offset size grows with the data.
func TestEncodeCFFIndexOffsetSize(t *testing.T) {
	for _, tc := range []struct{ size, offSize int }{{10, 1}, {300, 2}, {70000, 3}} {
		idx := encodeCFFIndex([][]byte{make([]byte, tc.size)})
		if int(idx[2]) != tc.offSize {
			t.Errorf("%d bytes: offset size %d, want %d", tc.size, idx[2], tc.offSize)
		}
		entries, _ := ParseCFFIndex(idx, 0)
		if len(entries) != 1 || len(entries[0]) != tc.size {
			t.Errorf("%d bytes: decoded %d entries", tc.size, len(entries))
		}
	}
}
