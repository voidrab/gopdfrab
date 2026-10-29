package fontprog

import (
	"encoding/binary"
	"testing"
)

func TestParseCFFFDSelectFormats(t *testing.T) {
	// Format 0: one byte per glyph.
	f0 := []byte{0x00, 2, 0, 1}
	if got := ParseCFFFDSelect(f0, 0, 3); got == nil || got[0] != 2 || got[1] != 0 || got[2] != 1 {
		t.Errorf("parseCFFFDSelect(format0) = %v", got)
	}

	// Format 3: nRanges ranges of (first, fd), then a sentinel = numGlyphs.
	var f3 []byte
	f3 = append(f3, 0x03, 0x00, 0x02) // format 3, 2 ranges
	f3 = append(f3, 0x00, 0x00, 0x00) // range: first=0, fd=0
	f3 = append(f3, 0x00, 0x02, 0x01) // range: first=2, fd=1
	f3 = append(f3, 0x00, 0x04)       // sentinel = 4
	if got := ParseCFFFDSelect(f3, 0, 4); got == nil || got[0] != 0 || got[1] != 0 || got[2] != 1 || got[3] != 1 {
		t.Errorf("parseCFFFDSelect(format3) = %v", got)
	}

	if ParseCFFFDSelect(nil, -1, 3) != nil {
		t.Error("parseCFFFDSelect should be nil for a negative offset")
	}
	if ParseCFFFDSelect([]byte{0x09}, 0, 3) != nil {
		t.Error("parseCFFFDSelect should be nil for an unknown format")
	}
}

func TestCffDictNumbersOperandEncodings(t *testing.T) {
	type call struct {
		op  int
		ops []float64
	}
	run := func(dict []byte) ([]call, bool) {
		var calls []call
		ok := CFFDictNumbers(dict, func(operator int, operands []float64) {
			calls = append(calls, call{operator, append([]float64{}, operands...)})
		})
		return calls, ok
	}

	// 28: 2-byte int; 29: 4-byte int; 12,x: escape operator (1200+x).
	dict := []byte{28, 0x01, 0x2C, 20, 29, 0x00, 0x00, 0x01, 0x2C, 21, 12, 7}
	calls, ok := run(dict)
	if !ok || len(calls) != 3 {
		t.Fatalf("cffDictNumbers = %v, %v, want 3 calls ok=true", calls, ok)
	}
	if calls[0].op != 20 || calls[0].ops[0] != 300 {
		t.Errorf("call[0] = %+v, want op20 operand 300", calls[0])
	}
	if calls[1].op != 21 || calls[1].ops[0] != 300 {
		t.Errorf("call[1] = %+v, want op21 operand 300", calls[1])
	}
	if calls[2].op != 1207 {
		t.Errorf("call[2] = %+v, want escape operator 1207", calls[2])
	}

	// Real-number operand (op 30) feeding into an operator.
	nibbles := []byte{0x2, 0xA, 0x5, 0xF} // "2.5"
	var real []byte
	for i := 0; i < len(nibbles); i += 2 {
		real = append(real, nibbles[i]<<4|nibbles[i+1])
	}
	dictReal := append([]byte{30}, real...)
	dictReal = append(dictReal, 20)
	calls2, ok2 := run(dictReal)
	if !ok2 || len(calls2) != 1 || calls2[0].ops[0] != 2.5 {
		t.Errorf("cffDictNumbers(real number) = %v, %v, want [{20 [2.5]}] true", calls2, ok2)
	}

	// Truncated operand encodings should fail cleanly.
	for _, bad := range [][]byte{{247}, {251}, {28, 0}, {29, 0, 0}, {30}, {12}} {
		if _, ok := run(bad); ok {
			t.Errorf("cffDictNumbers(%v) should fail on truncated input", bad)
		}
	}
}

func TestCffSubrBias(t *testing.T) {
	cases := []struct {
		count int
		want  int
	}{
		{0, 107}, {1239, 107}, {1240, 1131}, {33899, 1131}, {33900, 32768},
	}
	for _, c := range cases {
		if got := cffSubrBias(c.count); got != c.want {
			t.Errorf("cffSubrBias(%d) = %d, want %d", c.count, got, c.want)
		}
	}
}

func TestCffPrivateInfoMalformed(t *testing.T) {
	if _, _, _, ok := CFFPrivateInfo(nil, -1, 0); ok {
		t.Error("cffPrivateInfo should fail for a negative offset")
	}
	if _, _, _, ok := CFFPrivateInfo([]byte{1, 2, 3}, 0, 0); ok {
		t.Error("cffPrivateInfo should fail for a zero size")
	}
	if _, _, _, ok := CFFPrivateInfo([]byte{1, 2, 3}, 0, 100); ok {
		t.Error("cffPrivateInfo should fail when size exceeds the buffer")
	}
}

func TestCffGlobalSubrsShortInput(t *testing.T) {
	if CFFGlobalSubrs([]byte{1, 2, 3}) != nil {
		t.Error("cffGlobalSubrs should be nil for too-short input")
	}
}

func TestType2CharstringWidthTerminators(t *testing.T) {
	// endchar with the deprecated 4-argument seac form: no width (even count).
	if _, hasWidth, ok := Type2CharstringWidth([]byte{139, 139, 139, 139, 14}, nil, nil); !ok || hasWidth {
		t.Error("endchar with 4 args (seac form) should report hasWidth=false")
	}
	// endchar with a leading width plus the seac form (5 args).
	if w, hasWidth, ok := Type2CharstringWidth([]byte{byte(139 + 5), 139, 139, 139, 139, 14}, nil, nil); !ok || !hasWidth || w != 5 {
		t.Errorf("endchar(5 args) = %g, %v, %v, want 5 true true", w, hasWidth, ok)
	}
	// rmoveto (op 21) with 3 args (width + dx + dy): width present.
	if w, hasWidth, ok := Type2CharstringWidth([]byte{byte(139 + 7), 139, 139, 21}, nil, nil); !ok || !hasWidth || w != 7 {
		t.Errorf("rmoveto(3 args) = %g, %v, %v, want 7 true true", w, hasWidth, ok)
	}
	// vmoveto (op 4) with 2 args: width present.
	if w, hasWidth, ok := Type2CharstringWidth([]byte{byte(139 + 3), 139, 4}, nil, nil); !ok || !hasWidth || w != 3 {
		t.Errorf("vmoveto(2 args) = %g, %v, %v, want 3 true true", w, hasWidth, ok)
	}
	// hstem (op 1) with an odd arg count: width present.
	if w, hasWidth, ok := Type2CharstringWidth([]byte{byte(139 + 9), 139, 139, 1}, nil, nil); !ok || !hasWidth || w != 9 {
		t.Errorf("hstem(odd args) = %g, %v, %v, want 9 true true", w, hasWidth, ok)
	}
	// 16.16 fixed-point operand (op 255) followed by endchar: the single
	// pushed value is itself the width (endchar's 1-arg form).
	fixedOp := []byte{255, 0x00, 0x02, 0x00, 0x00, 14} // 2.0 fixed, then endchar
	if w, hasWidth, ok := Type2CharstringWidth(fixedOp, nil, nil); !ok || !hasWidth || w != 2 {
		t.Errorf("fixed-point operand + endchar(1 arg) = %g, %v, %v, want 2 true true", w, hasWidth, ok)
	}
	// callgsubr (op 29) recursing into a global subr that returns (op 11).
	// Bias for a 1-entry index is 107, so pushing -107 selects subr index 0.
	gsubrs := [][]byte{{11}} // just "return"
	if _, _, ok := Type2CharstringWidth([]byte{32, 29, 14}, gsubrs, nil); !ok {
		t.Error("callgsubr into a returning subr should still succeed")
	}
	// Depth limit: a subr that calls itself (via a valid bias-adjusted index)
	// should fail via the recursion-depth guard rather than looping forever.
	selfCall := [][]byte{{32, 10}} // push -107 (-> index 0 after +107 bias), callsubr
	if _, _, ok := Type2CharstringWidth([]byte{32, 10}, nil, selfCall); ok {
		t.Error("infinite subr recursion should fail via the depth limit")
	}
	// callsubr with no operand on the stack.
	if _, _, ok := Type2CharstringWidth([]byte{10}, nil, nil); ok {
		t.Error("callsubr with an empty stack should fail")
	}
	// An operator outside the width-prefix grammar aborts the walk.
	if _, _, ok := Type2CharstringWidth([]byte{139, 9}, nil, nil); ok {
		t.Error("an unrecognized operator should fail the width walk")
	}
}

func TestType2CharstringWidthViaLocalSubr(t *testing.T) {
	// Local subr 0 (bias 107, so call index -107): pushes width 600 then endchar.
	lsubrs := [][]byte{{248, 236, 0x0e}} // width 600, endchar
	// Glyph charstring: callsubr(-107 + bias 107 = 0), no other operands.
	// Encode -107 via the single-byte range (b-139, b=32 -> -107).
	cs := []byte{32, 10} // push -107, callsubr
	w, hasWidth, ok := Type2CharstringWidth(cs, nil, lsubrs)
	if !ok || !hasWidth || w != 600 {
		t.Errorf("type2CharstringWidth via local subr = %g, hasWidth=%v, ok=%v, want 600 true true", w, hasWidth, ok)
	}

	// Depth-limit / malformed guard: an out-of-range subr index fails cleanly.
	badCS := []byte{139, 10} // push 0, callsubr(0+107=107, out of range for 1 subr)
	if _, _, ok := Type2CharstringWidth(badCS, nil, lsubrs); ok {
		t.Error("type2CharstringWidth should fail for an out-of-range subr index")
	}
}

func TestCffPrivateInfoWithSubrs(t *testing.T) {
	// Private DICT: defaultWidthX=0 (op20), nominalWidthX=0 (op21), Subrs
	// offset=9 relative to the Private DICT's own start (op19).
	priv := []byte{0x8b, 20, 0x8b, 21, 0x8b + 9, 19} // Subrs operand = 9
	// Local Subrs INDEX at offset len(priv)=6 relative... but cffPrivateInfo
	// resolves Subrs relative to the Private DICT's offset within cff.
	var cff []byte
	cff = append(cff, make([]byte, 4)...) // padding so offset arithmetic is simple
	privOff := len(cff)
	cff = append(cff, priv...)
	subrsOff := len(cff)
	cff = append(cff, 0x00, 0x01, 0x01, 1, 2, 0x0e) // INDEX: 1 entry, 1 byte "endchar"
	relSubrs := subrsOff - privOff
	priv[4] = byte(relSubrs + 139) // patch the Subrs operand to the real relative offset
	copy(cff[privOff:], priv)

	dW, nW, subrs, ok := CFFPrivateInfo(cff, privOff, len(priv))
	if !ok || dW != 0 || nW != 0 {
		t.Fatalf("cffPrivateInfo = %v %v %v %v", dW, nW, subrs, ok)
	}
	if len(subrs) != 1 {
		t.Errorf("cffPrivateInfo local Subrs = %v, want 1 entry", subrs)
	}
}

func TestCffRealNumberAndParseSimpleFloat(t *testing.T) {
	// Build "-2.5E-1" via nibbles: '-'=0xE '2'=2 '.'=0xA '5'=5 'E'=0xB '-'=0xE '1'=1, terminator 0xF
	nibbles := []byte{0xE, 0x2, 0xA, 0x5, 0xB, 0xE, 0x1, 0xF}
	var packed []byte
	for i := 0; i < len(nibbles); i += 2 {
		packed = append(packed, nibbles[i]<<4|nibbles[i+1])
	}
	v, n, ok := cffRealNumber(packed)
	if !ok || n != len(packed) {
		t.Fatalf("cffRealNumber = %v, %d, %v", v, n, ok)
	}
	if v != -0.25 {
		t.Errorf("cffRealNumber(-2.5E-1) = %g, want -0.25", v)
	}

	if _, _, ok := cffRealNumber(nil); ok {
		t.Error("cffRealNumber should fail on empty input (no terminator)")
	}

	if got := parseSimpleFloat("bad"); got != 0 {
		t.Errorf("parseSimpleFloat(garbage) = %g, want 0", got)
	}
}

func TestCFFSIDName(t *testing.T) {
	if got := cffSIDName(0, nil); got != ".notdef" {
		t.Errorf("SID 0 = %q, want .notdef", got)
	}
	custom := [][]byte{[]byte("MyGlyph")}
	if got := cffSIDName(len(CffStandardStrings), custom); got != "MyGlyph" {
		t.Errorf("first custom SID = %q, want MyGlyph", got)
	}
	if got := cffSIDName(999999, nil); got != "" {
		t.Errorf("out-of-range SID = %q, want empty", got)
	}
}

func TestParseCFFCharsetCIDsFormats(t *testing.T) {
	// CharsetOffset must be > 2 (offsets 0-2 mean a predefined charset), so
	// pad with 3 leading bytes and place the table at offset 3.
	pad := []byte{0, 0, 0}

	// Format 1: (first, nLeft) ranges, nLeft is a 1-byte count.
	f1 := append(append([]byte{}, pad...), 0x01, 0x00, 0x05, 0x02) // first CID 5, nLeft 2 -> gids 1,2,3 get CIDs 5,6,7
	if got := ParseCFFCharsetCIDs(f1, 3, 4); got == nil || got[1] != 5 || got[2] != 6 || got[3] != 7 {
		t.Errorf("ParseCFFCharsetCIDs(format1) = %v", got)
	}

	// Format 2: like format 1 but nLeft is a 2-byte count.
	f2 := append(append([]byte{}, pad...), 0x02, 0x00, 0x0A, 0x00, 0x01) // first CID 10, nLeft 1 -> gids 1,2 get CIDs 10,11
	if got := ParseCFFCharsetCIDs(f2, 3, 3); got == nil || got[1] != 10 || got[2] != 11 {
		t.Errorf("ParseCFFCharsetCIDs(format2) = %v", got)
	}

	if ParseCFFCharsetCIDs(f1, 3, 0) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil for numGlyphs <= 0")
	}
	if ParseCFFCharsetCIDs(f1, 2, 4) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil for a predefined-charset offset (<=2)")
	}
	if ParseCFFCharsetCIDs(append(append([]byte{}, pad...), 0x09), 3, 4) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil for an unknown format")
	}
	if ParseCFFCharsetCIDs(f1, 3, 100) != nil {
		t.Error("ParseCFFCharsetCIDs should be nil when the table runs past the data")
	}
}

func TestParseCFFTopDictMalformed(t *testing.T) {
	if _, ok := ParseCFFTopDict(nil); ok {
		t.Error("ParseCFFTopDict should fail on empty input")
	}
	if _, ok := ParseCFFTopDict([]byte{1, 0, 4, 1}); ok {
		t.Error("ParseCFFTopDict should fail with no Name INDEX data")
	}
	// A Name INDEX with a truncated Top DICT INDEX count.
	truncated := []byte{1, 0, 4, 1, 0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't', 0x00}
	if _, ok := ParseCFFTopDict(truncated); ok {
		t.Error("ParseCFFTopDict should fail with a truncated Top DICT INDEX")
	}
}

// buildMinimalCIDCFF assembles a tiny CID-keyed CFF font with 3 glyphs
// (.notdef, CID 1 width 600, CID 2 width 700), a format-0 CID charset, a
// single FDArray Font DICT (format-0 FDSelect), and a Private DICT with zero
// width defaults. Ported from verify.buildMinimalCIDCFF (internal/verify's
// test-only helper isn't importable across packages).
func buildMinimalCIDCFF() []byte {
	i32 := func(v int) []byte {
		var b [5]byte
		b[0] = 29
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	}

	header := []byte{0x01, 0x00, 0x04, 0x01}
	nameIndex := []byte{0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't'}

	topDictLen := 6 + 6 + 2 + 7 + 7 // CharStrings, charset, ROS, FDArray, FDSelect
	topDictIndex := []byte{0x00, 0x01, 0x01, byte(1), byte(topDictLen + 1)}

	nameEnd := len(header) + len(nameIndex)
	topDictEnd := nameEnd + len(topDictIndex) + topDictLen
	stringIndex := []byte{0x00, 0x00}
	globalSubrIndex := []byte{0x00, 0x00}
	stringEnd := topDictEnd + len(stringIndex)
	subrEnd := stringEnd + len(globalSubrIndex)

	charsetOff := subrEnd
	charset := []byte{0x00, 0x00, 0x01, 0x00, 0x02} // format 0: gid1->CID1, gid2->CID2
	charsetEnd := charsetOff + len(charset)

	csOff := charsetEnd
	cs0 := []byte{0x0e}           // .notdef: endchar, no width
	cs1 := []byte{248, 236, 0x0e} // CID1: width 600, endchar
	cs2 := []byte{249, 80, 0x0e}  // CID2: width 700, endchar
	csIndex := []byte{0x00, 0x03, 0x01, 1, 2, 5, 8}
	csIndex = append(csIndex, cs0...)
	csIndex = append(csIndex, cs1...)
	csIndex = append(csIndex, cs2...)
	csEnd := csOff + len(csIndex)

	fdArrayOff := csEnd
	const fdDictLen = 11
	fdArrayIndexLen := 2 + 1 + 2 + fdDictLen
	fdArrayEnd := fdArrayOff + fdArrayIndexLen

	fdSelectOff := fdArrayEnd
	fdSelect := []byte{0x00, 0x00, 0x00, 0x00} // format 0, FD 0 for all 3 glyphs
	fdSelectEnd := fdSelectOff + len(fdSelect)

	privOff := fdSelectEnd
	fdDict := append(i32(4), i32(privOff)...)
	fdDict = append(fdDict, 18) // Private: [size offset]
	if len(fdDict) != fdDictLen {
		panic("buildMinimalCIDCFF: fd dict length mismatch")
	}
	fdArrayIndex := []byte{0x00, 0x01, 0x01, 1, byte(fdDictLen + 1)}
	fdArrayIndex = append(fdArrayIndex, fdDict...)

	privateDict := []byte{0x8b, 20, 0x8b, 21} // defaultWidthX=0, nominalWidthX=0

	topDict := append(i32(csOff), 17)
	topDict = append(topDict, i32(charsetOff)...)
	topDict = append(topDict, 15)
	topDict = append(topDict, 12, 30) // ROS
	topDict = append(topDict, i32(fdArrayOff)...)
	topDict = append(topDict, 12, 36)
	topDict = append(topDict, i32(fdSelectOff)...)
	topDict = append(topDict, 12, 37)
	if len(topDict) != topDictLen {
		panic("buildMinimalCIDCFF: top dict length mismatch")
	}

	var cff []byte
	cff = append(cff, header...)
	cff = append(cff, nameIndex...)
	cff = append(cff, topDictIndex...)
	cff = append(cff, topDict...)
	cff = append(cff, stringIndex...)
	cff = append(cff, globalSubrIndex...)
	cff = append(cff, charset...)
	cff = append(cff, csIndex...)
	cff = append(cff, fdArrayIndex...)
	cff = append(cff, fdSelect...)
	cff = append(cff, privateDict...)
	return cff
}

// TestExtractCFFBytes covers all three shapes: a bare CFF program passed
// through as-is, a CFF wrapped in an OpenType ("CFF " table) container, and
// data that is neither.
func TestExtractCFFBytes(t *testing.T) {
	cff := buildMinimalCIDCFF()

	if got := ExtractCFFBytes(cff); string(got) != string(cff) {
		t.Errorf("bare CFF passthrough = %x, want %x", got, cff)
	}

	otf := packSfnt(map[string][]byte{"CFF ": cff})
	if got := ExtractCFFBytes(otf); string(got) != string(cff) {
		t.Errorf("OpenType-wrapped CFF extraction = %x, want %x", got, cff)
	}

	if got := ExtractCFFBytes([]byte("not a font")); got != nil {
		t.Errorf("extractCFFBytes(garbage) = %x, want nil", got)
	}
}
