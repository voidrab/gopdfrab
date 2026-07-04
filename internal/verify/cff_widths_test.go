package verify

import (
	"testing"
)

func TestCFFAdvanceWidths(t *testing.T) {
	widths := CFFAdvanceWidths(buildMinimalCFF())
	if widths == nil {
		t.Fatal("CFFAdvanceWidths returned nil for a valid name-keyed CFF")
	}
	if _, ok := widths["A"]; !ok {
		t.Errorf("CFFAdvanceWidths missing glyph A: %v", widths)
	}
}

func TestCFFCIDAdvanceWidthsAndFDSelect(t *testing.T) {
	cff := buildMinimalCIDCFF()
	widths := CFFCIDAdvanceWidths(cff)
	if widths[1] != 600 || widths[2] != 700 {
		t.Fatalf("CFFCIDAdvanceWidths = %v, want {1:600, 2:700, ...}", widths)
	}

	// Name-keyed (non-CID) CFF: CFFCIDAdvanceWidths must decline it.
	if CFFCIDAdvanceWidths(buildMinimalCFF()) != nil {
		t.Error("CFFCIDAdvanceWidths should be nil for a name-keyed CFF")
	}
}

func TestParseCFFFDSelectFormats(t *testing.T) {
	// Format 0: one byte per glyph.
	f0 := []byte{0x00, 2, 0, 1}
	if got := parseCFFFDSelect(f0, 0, 3); got == nil || got[0] != 2 || got[1] != 0 || got[2] != 1 {
		t.Errorf("parseCFFFDSelect(format0) = %v", got)
	}

	// Format 3: nRanges ranges of (first, fd), then a sentinel = numGlyphs.
	var f3 []byte
	f3 = append(f3, 0x03, 0x00, 0x02) // format 3, 2 ranges
	f3 = append(f3, 0x00, 0x00, 0x00) // range: first=0, fd=0
	f3 = append(f3, 0x00, 0x02, 0x01) // range: first=2, fd=1
	f3 = append(f3, 0x00, 0x04)       // sentinel = 4
	if got := parseCFFFDSelect(f3, 0, 4); got == nil || got[0] != 0 || got[1] != 0 || got[2] != 1 || got[3] != 1 {
		t.Errorf("parseCFFFDSelect(format3) = %v", got)
	}

	if parseCFFFDSelect(nil, -1, 3) != nil {
		t.Error("parseCFFFDSelect should be nil for a negative offset")
	}
	if parseCFFFDSelect([]byte{0x09}, 0, 3) != nil {
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
		ok := cffDictNumbers(dict, func(operator int, operands []float64) {
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

func TestCFFAdvanceWidthsRejections(t *testing.T) {
	if CFFAdvanceWidths(buildMinimalCIDCFF()) != nil {
		t.Error("CFFAdvanceWidths should be nil for a CID-keyed CFF")
	}
	if CFFAdvanceWidths(nil) != nil {
		t.Error("CFFAdvanceWidths should be nil for unparseable input")
	}
}

func TestCFFCIDAdvanceWidthsSingleFDFallback(t *testing.T) {
	// buildMinimalCIDCFF's FDSelect is format 0 explicit; CFFCIDAdvanceWidths
	// also has a fallback for a font with no FDSelect and exactly one FD --
	// exercise it by pointing FDSelect at an invalid offset (parseCFFFDSelect
	// returns nil) while FDArray still has exactly one entry.
	cff := buildMinimalCIDCFF()
	td, ok := ParseCFFTopDict(cff)
	if !ok {
		t.Fatal("sanity: buildMinimalCIDCFF should parse")
	}
	broken := append([]byte{}, cff...)
	// Corrupt the FDSelect format byte to an unrecognized value.
	broken[td.FDSelect] = 0x09
	widths := CFFCIDAdvanceWidths(broken)
	if widths == nil || widths[1] != 600 || widths[2] != 700 {
		t.Errorf("CFFCIDAdvanceWidths(single-FD fallback) = %v, want {1:600,2:700}", widths)
	}
}

func TestCffPrivateInfoMalformed(t *testing.T) {
	if _, _, _, ok := cffPrivateInfo(nil, -1, 0); ok {
		t.Error("cffPrivateInfo should fail for a negative offset")
	}
	if _, _, _, ok := cffPrivateInfo([]byte{1, 2, 3}, 0, 0); ok {
		t.Error("cffPrivateInfo should fail for a zero size")
	}
	if _, _, _, ok := cffPrivateInfo([]byte{1, 2, 3}, 0, 100); ok {
		t.Error("cffPrivateInfo should fail when size exceeds the buffer")
	}
}

func TestCffGlobalSubrsShortInput(t *testing.T) {
	if cffGlobalSubrs([]byte{1, 2, 3}) != nil {
		t.Error("cffGlobalSubrs should be nil for too-short input")
	}
}

func TestType2CharstringWidthTerminators(t *testing.T) {
	// endchar with the deprecated 4-argument seac form: no width (even count).
	if _, hasWidth, ok := type2CharstringWidth([]byte{139, 139, 139, 139, 14}, nil, nil); !ok || hasWidth {
		t.Error("endchar with 4 args (seac form) should report hasWidth=false")
	}
	// endchar with a leading width plus the seac form (5 args).
	if w, hasWidth, ok := type2CharstringWidth([]byte{byte(139 + 5), 139, 139, 139, 139, 14}, nil, nil); !ok || !hasWidth || w != 5 {
		t.Errorf("endchar(5 args) = %g, %v, %v, want 5 true true", w, hasWidth, ok)
	}
	// rmoveto (op 21) with 3 args (width + dx + dy): width present.
	if w, hasWidth, ok := type2CharstringWidth([]byte{byte(139 + 7), 139, 139, 21}, nil, nil); !ok || !hasWidth || w != 7 {
		t.Errorf("rmoveto(3 args) = %g, %v, %v, want 7 true true", w, hasWidth, ok)
	}
	// vmoveto (op 4) with 2 args: width present.
	if w, hasWidth, ok := type2CharstringWidth([]byte{byte(139 + 3), 139, 4}, nil, nil); !ok || !hasWidth || w != 3 {
		t.Errorf("vmoveto(2 args) = %g, %v, %v, want 3 true true", w, hasWidth, ok)
	}
	// hstem (op 1) with an odd arg count: width present.
	if w, hasWidth, ok := type2CharstringWidth([]byte{byte(139 + 9), 139, 139, 1}, nil, nil); !ok || !hasWidth || w != 9 {
		t.Errorf("hstem(odd args) = %g, %v, %v, want 9 true true", w, hasWidth, ok)
	}
	// 16.16 fixed-point operand (op 255) followed by endchar: the single
	// pushed value is itself the width (endchar's 1-arg form).
	fixedOp := []byte{255, 0x00, 0x02, 0x00, 0x00, 14} // 2.0 fixed, then endchar
	if w, hasWidth, ok := type2CharstringWidth(fixedOp, nil, nil); !ok || !hasWidth || w != 2 {
		t.Errorf("fixed-point operand + endchar(1 arg) = %g, %v, %v, want 2 true true", w, hasWidth, ok)
	}
	// callgsubr (op 29) recursing into a global subr that returns (op 11).
	// Bias for a 1-entry index is 107, so pushing -107 selects subr index 0.
	gsubrs := [][]byte{{11}} // just "return"
	if _, _, ok := type2CharstringWidth([]byte{32, 29, 14}, gsubrs, nil); !ok {
		t.Error("callgsubr into a returning subr should still succeed")
	}
	// Depth limit: a subr that calls itself (via a valid bias-adjusted index)
	// should fail via the recursion-depth guard rather than looping forever.
	selfCall := [][]byte{{32, 10}} // push -107 (-> index 0 after +107 bias), callsubr
	if _, _, ok := type2CharstringWidth([]byte{32, 10}, nil, selfCall); ok {
		t.Error("infinite subr recursion should fail via the depth limit")
	}
	// callsubr with no operand on the stack.
	if _, _, ok := type2CharstringWidth([]byte{10}, nil, nil); ok {
		t.Error("callsubr with an empty stack should fail")
	}
	// An operator outside the width-prefix grammar aborts the walk.
	if _, _, ok := type2CharstringWidth([]byte{139, 9}, nil, nil); ok {
		t.Error("an unrecognized operator should fail the width walk")
	}
}

func TestType2CharstringWidthViaLocalSubr(t *testing.T) {
	// Local subr 0 (bias 107, so call index -107): pushes width 600 then endchar.
	lsubrs := [][]byte{{248, 236, 0x0e}} // width 600, endchar
	// Glyph charstring: callsubr(-107 + bias 107 = 0), no other operands.
	// Encode -107 via the single-byte range (b-139, b=32 -> -107).
	cs := []byte{32, 10} // push -107, callsubr
	w, hasWidth, ok := type2CharstringWidth(cs, nil, lsubrs)
	if !ok || !hasWidth || w != 600 {
		t.Errorf("type2CharstringWidth via local subr = %g, hasWidth=%v, ok=%v, want 600 true true", w, hasWidth, ok)
	}

	// Depth-limit / malformed guard: an out-of-range subr index fails cleanly.
	badCS := []byte{139, 10} // push 0, callsubr(0+107=107, out of range for 1 subr)
	if _, _, ok := type2CharstringWidth(badCS, nil, lsubrs); ok {
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

	dW, nW, subrs, ok := cffPrivateInfo(cff, privOff, len(priv))
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
