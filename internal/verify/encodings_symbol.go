package verify

// MacRomanToUnicode maps MacRomanEncoding character codes 0–255 to Unicode,
// per PDF 32000-1 Annex D.2 with AGL codepoints.
var MacRomanToUnicode [256]uint16

func init() {
	for i := 0x20; i <= 0x7E; i++ {
		MacRomanToUnicode[i] = uint16(i)
	}
	high := [128]uint16{
		0x00C4, 0x00C5, 0x00C7, 0x00C9, 0x00D1, 0x00D6, 0x00DC, 0x00E1, // 0x80
		0x00E0, 0x00E2, 0x00E4, 0x00E3, 0x00E5, 0x00E7, 0x00E9, 0x00E8,
		0x00EA, 0x00EB, 0x00ED, 0x00EC, 0x00EE, 0x00EF, 0x00F1, 0x00F3, // 0x90
		0x00F2, 0x00F4, 0x00F6, 0x00F5, 0x00FA, 0x00F9, 0x00FB, 0x00FC,
		0x2020, 0x00B0, 0x00A2, 0x00A3, 0x00A7, 0x2022, 0x00B6, 0x00DF, // 0xA0
		0x00AE, 0x00A9, 0x2122, 0x00B4, 0x00A8, 0x2260, 0x00C6, 0x00D8,
		0x221E, 0x00B1, 0x2264, 0x2265, 0x00A5, 0x00B5, 0x2202, 0x2211, // 0xB0
		0x220F, 0x03C0, 0x222B, 0x00AA, 0x00BA, 0x2126, 0x00E6, 0x00F8,
		0x00BF, 0x00A1, 0x00AC, 0x221A, 0x0192, 0x2248, 0x2206, 0x00AB, // 0xC0
		0x00BB, 0x2026, 0x00A0, 0x00C0, 0x00C3, 0x00D5, 0x0152, 0x0153,
		0x2013, 0x2014, 0x201C, 0x201D, 0x2018, 0x2019, 0x00F7, 0x25CA, // 0xD0
		0x00FF, 0x0178, 0x2044, 0x00A4, 0x2039, 0x203A, 0xFB01, 0xFB02,
		0x2021, 0x00B7, 0x201A, 0x201E, 0x2030, 0x00C2, 0x00CA, 0x00C1, // 0xE0
		0x00CB, 0x00C8, 0x00CD, 0x00CE, 0x00CF, 0x00CC, 0x00D3, 0x00D4,
		0xF8FF, 0x00D2, 0x00DA, 0x00DB, 0x00D9, 0x0131, 0x02C6, 0x02DC, // 0xF0
		0x00AF, 0x02D8, 0x02D9, 0x02DA, 0x00B8, 0x02DD, 0x02DB, 0x02C7,
	}
	for i, u := range high {
		MacRomanToUnicode[0x80+i] = u
	}
}

// StandardToUnicode maps StandardEncoding character codes 0–255 to Unicode,
// derived from the StandardEncoding glyph names.
var StandardToUnicode [256]uint16

func init() {
	for cc, n := range StandardEncoding {
		if n != "" {
			if u, ok := GlyphNameToUnicode(n); ok {
				StandardToUnicode[cc] = u
			}
		}
	}
}

// SymbolToUnicode maps the Symbol font's built-in encoding codes 0–255 to
// Unicode, per PDF 32000-1 Annex D.5 with AGL Symbol-list codepoints.
var SymbolToUnicode [256]uint16

// SymbolGlyphNameUnicode maps Symbol-font glyph names to Unicode (AGL Symbol
// glyph list), for resolving /Differences that name Symbol glyphs.
var SymbolGlyphNameUnicode map[string]uint16

var symbolCodes = []struct {
	code int
	name string
	u    uint16
}{
	{32, "space", 0x0020}, {33, "exclam", 0x0021}, {34, "universal", 0x2200},
	{35, "numbersign", 0x0023}, {36, "existential", 0x2203}, {37, "percent", 0x0025},
	{38, "ampersand", 0x0026}, {39, "suchthat", 0x220B}, {40, "parenleft", 0x0028},
	{41, "parenright", 0x0029}, {42, "asteriskmath", 0x2217}, {43, "plus", 0x002B},
	{44, "comma", 0x002C}, {45, "minus", 0x2212}, {46, "period", 0x002E},
	{47, "slash", 0x002F}, {48, "zero", 0x0030}, {49, "one", 0x0031},
	{50, "two", 0x0032}, {51, "three", 0x0033}, {52, "four", 0x0034},
	{53, "five", 0x0035}, {54, "six", 0x0036}, {55, "seven", 0x0037},
	{56, "eight", 0x0038}, {57, "nine", 0x0039}, {58, "colon", 0x003A},
	{59, "semicolon", 0x003B}, {60, "less", 0x003C}, {61, "equal", 0x003D},
	{62, "greater", 0x003E}, {63, "question", 0x003F}, {64, "congruent", 0x2245},
	{65, "Alpha", 0x0391}, {66, "Beta", 0x0392}, {67, "Chi", 0x03A7},
	{68, "Delta", 0x0394}, {69, "Epsilon", 0x0395}, {70, "Phi", 0x03A6},
	{71, "Gamma", 0x0393}, {72, "Eta", 0x0397}, {73, "Iota", 0x0399},
	{74, "theta1", 0x03D1}, {75, "Kappa", 0x039A}, {76, "Lambda", 0x039B},
	{77, "Mu", 0x039C}, {78, "Nu", 0x039D}, {79, "Omicron", 0x039F},
	{80, "Pi", 0x03A0}, {81, "Theta", 0x0398}, {82, "Rho", 0x03A1},
	{83, "Sigma", 0x03A3}, {84, "Tau", 0x03A4}, {85, "Upsilon", 0x03A5},
	{86, "sigma1", 0x03C2}, {87, "Omega", 0x03A9}, {88, "Xi", 0x039E},
	{89, "Psi", 0x03A8}, {90, "Zeta", 0x0396}, {91, "bracketleft", 0x005B},
	{92, "therefore", 0x2234}, {93, "bracketright", 0x005D}, {94, "perpendicular", 0x22A5},
	{95, "underscore", 0x005F}, {96, "radicalex", 0xF8E5}, {97, "alpha", 0x03B1},
	{98, "beta", 0x03B2}, {99, "chi", 0x03C7}, {100, "delta", 0x03B4},
	{101, "epsilon", 0x03B5}, {102, "phi", 0x03C6}, {103, "gamma", 0x03B3},
	{104, "eta", 0x03B7}, {105, "iota", 0x03B9}, {106, "phi1", 0x03D5},
	{107, "kappa", 0x03BA}, {108, "lambda", 0x03BB}, {109, "mu", 0x03BC},
	{110, "nu", 0x03BD}, {111, "omicron", 0x03BF}, {112, "pi", 0x03C0},
	{113, "theta", 0x03B8}, {114, "rho", 0x03C1}, {115, "sigma", 0x03C3},
	{116, "tau", 0x03C4}, {117, "upsilon", 0x03C5}, {118, "omega1", 0x03D6},
	{119, "omega", 0x03C9}, {120, "xi", 0x03BE}, {121, "psi", 0x03C8},
	{122, "zeta", 0x03B6}, {123, "braceleft", 0x007B}, {124, "bar", 0x007C},
	{125, "braceright", 0x007D}, {126, "similar", 0x223C},
	{160, "Euro", 0x20AC}, {161, "Upsilon1", 0x03D2}, {162, "minute", 0x2032},
	{163, "lessequal", 0x2264}, {164, "fraction", 0x2044}, {165, "infinity", 0x221E},
	{166, "florin", 0x0192}, {167, "club", 0x2663}, {168, "diamond", 0x2666},
	{169, "heart", 0x2665}, {170, "spade", 0x2660}, {171, "arrowboth", 0x2194},
	{172, "arrowleft", 0x2190}, {173, "arrowup", 0x2191}, {174, "arrowright", 0x2192},
	{175, "arrowdown", 0x2193}, {176, "degree", 0x00B0}, {177, "plusminus", 0x00B1},
	{178, "second", 0x2033}, {179, "greaterequal", 0x2265}, {180, "multiply", 0x00D7},
	{181, "proportional", 0x221D}, {182, "partialdiff", 0x2202}, {183, "bullet", 0x2022},
	{184, "divide", 0x00F7}, {185, "notequal", 0x2260}, {186, "equivalence", 0x2261},
	{187, "approxequal", 0x2248}, {188, "ellipsis", 0x2026}, {189, "arrowvertex", 0xF8E6},
	{190, "arrowhorizex", 0xF8E7}, {191, "carriagereturn", 0x21B5}, {192, "aleph", 0x2135},
	{193, "Ifraktur", 0x2111}, {194, "Rfraktur", 0x211C}, {195, "weierstrass", 0x2118},
	{196, "circlemultiply", 0x2297}, {197, "circleplus", 0x2295}, {198, "emptyset", 0x2205},
	{199, "intersection", 0x2229}, {200, "union", 0x222A}, {201, "propersuperset", 0x2283},
	{202, "reflexsuperset", 0x2287}, {203, "notsubset", 0x2284}, {204, "propersubset", 0x2282},
	{205, "reflexsubset", 0x2286}, {206, "element", 0x2208}, {207, "notelement", 0x2209},
	{208, "angle", 0x2220}, {209, "gradient", 0x2207}, {210, "registerserif", 0xF6DA},
	{211, "copyrightserif", 0xF6D9}, {212, "trademarkserif", 0xF6DB}, {213, "product", 0x220F},
	{214, "radical", 0x221A}, {215, "dotmath", 0x22C5}, {216, "logicalnot", 0x00AC},
	{217, "logicaland", 0x2227}, {218, "logicalor", 0x2228}, {219, "arrowdblboth", 0x21D4},
	{220, "arrowdblleft", 0x21D0}, {221, "arrowdblup", 0x21D1}, {222, "arrowdblright", 0x21D2},
	{223, "arrowdbldown", 0x21D3}, {224, "lozenge", 0x25CA}, {225, "angleleft", 0x2329},
	{226, "registersans", 0xF8E8}, {227, "copyrightsans", 0xF8E9}, {228, "trademarksans", 0xF8EA},
	{229, "summation", 0x2211}, {230, "parenlefttp", 0xF8EB}, {231, "parenleftex", 0xF8EC},
	{232, "parenleftbt", 0xF8ED}, {233, "bracketlefttp", 0xF8EE}, {234, "bracketleftex", 0xF8EF},
	{235, "bracketleftbt", 0xF8F0}, {236, "bracelefttp", 0xF8F1}, {237, "braceleftmid", 0xF8F2},
	{238, "braceleftbt", 0xF8F3}, {239, "braceex", 0xF8F4}, {240, "apple", 0xF8FF},
	{241, "angleright", 0x232A}, {242, "integral", 0x222B}, {243, "integraltp", 0x2320},
	{244, "integralex", 0xF8F5}, {245, "integralbt", 0x2321}, {246, "parenrighttp", 0xF8F6},
	{247, "parenrightex", 0xF8F7}, {248, "parenrightbt", 0xF8F8}, {249, "bracketrighttp", 0xF8F9},
	{250, "bracketrightex", 0xF8FA}, {251, "bracketrightbt", 0xF8FB}, {252, "bracerighttp", 0xF8FC},
	{253, "bracerightmid", 0xF8FD}, {254, "bracerightbt", 0xF8FE},
}

func init() {
	SymbolGlyphNameUnicode = make(map[string]uint16, len(symbolCodes))
	for _, e := range symbolCodes {
		SymbolToUnicode[e.code] = e.u
		if _, ok := SymbolGlyphNameUnicode[e.name]; !ok {
			SymbolGlyphNameUnicode[e.name] = e.u
		}
	}
}

// ZapfDingbatsToUnicode maps the ZapfDingbats built-in encoding codes 0–255 to
// Unicode, per PDF 32000-1 Annex D.6 with AGL ZapfDingbats-list codepoints.
var ZapfDingbatsToUnicode [256]uint16

// ZapfDingbatsGlyphNameUnicode maps ITC Zapf Dingbats glyph names (a1…a206) to
// Unicode, for resolving /Differences that name dingbat glyphs.
var ZapfDingbatsGlyphNameUnicode map[string]uint16

func init() {
	// Codes 33–126 follow the Unicode Dingbats block at offset 0x26E0, with a
	// few glyphs Unicode had already encoded elsewhere.
	lowNames := []string{
		"a1", "a2", "a202", "a3", "a4", "a5", "a119", "a118", "a117", "a11",
		"a12", "a13", "a14", "a15", "a16", "a105", "a17", "a18", "a19", "a20",
		"a21", "a22", "a23", "a24", "a25", "a26", "a27", "a28", "a6", "a7",
		"a8", "a9", "a10", "a29", "a30", "a31", "a32", "a33", "a34", "a35",
		"a36", "a37", "a38", "a39", "a40", "a41", "a42", "a43", "a44", "a45",
		"a46", "a47", "a48", "a49", "a50", "a51", "a52", "a53", "a54", "a55",
		"a56", "a57", "a58", "a59", "a60", "a61", "a62", "a63", "a64", "a65",
		"a66", "a67", "a68", "a69", "a70", "a71", "a72", "a73", "a74", "a203",
		"a75", "a204", "a76", "a77", "a78", "a79", "a81", "a82", "a83", "a84",
		"a97", "a98", "a99", "a100",
	}
	lowExceptions := map[int]uint16{
		37: 0x260E, 42: 0x261B, 43: 0x261E, 72: 0x2605, 108: 0x25CF,
		110: 0x25A0, 115: 0x25B2, 116: 0x25BC, 117: 0x25C6, 119: 0x25D7,
	}
	// Codes 128–141 are the parenthesis ornaments U+2768–U+2775 in code order.
	parenNames := []string{
		"a89", "a90", "a93", "a94", "a91", "a92", "a205", "a85",
		"a206", "a86", "a87", "a88", "a95", "a96",
	}
	// Codes 161–254 follow the Dingbats block at offset 0x26C0, except the
	// card suits, circled digits, and plain arrows Unicode already had.
	highNames := []string{
		"a101", "a102", "a103", "a104", "a106", "a107", "a108", "a112",
		"a111", "a110", "a109", "a120", "a121", "a122", "a123", "a124",
		"a125", "a126", "a127", "a128", "a129", "a130", "a131", "a132",
		"a133", "a134", "a135", "a136", "a137", "a138", "a139", "a140",
		"a141", "a142", "a143", "a144", "a145", "a146", "a147", "a148",
		"a149", "a150", "a151", "a152", "a153", "a154", "a155", "a156",
		"a157", "a158", "a159", "a160", "a161", "a163", "a164", "a196",
		"a165", "a192", "a166", "a167", "a168", "a169", "a170", "a171",
		"a172", "a173", "a162", "a174", "a175", "a176", "a177", "a178",
		"a179", "a193", "a180", "a199", "a181", "a200", "a182", "",
		"a201", "a183", "a184", "a197", "a185", "a194", "a198", "a186",
		"a195", "a187", "a188", "a189", "a190", "a191",
	}
	highExceptions := map[int]uint16{
		168: 0x2663, 169: 0x2666, 170: 0x2665, 171: 0x2660,
		213: 0x2192, 214: 0x2194, 215: 0x2195,
	}
	for i := 172; i <= 181; i++ { // circled digits one..ten
		highExceptions[i] = uint16(0x2460 + i - 172)
	}

	ZapfDingbatsGlyphNameUnicode = make(map[string]uint16, 202)
	set := func(code int, name string, u uint16) {
		ZapfDingbatsToUnicode[code] = u
		if name != "" {
			if _, ok := ZapfDingbatsGlyphNameUnicode[name]; !ok {
				ZapfDingbatsGlyphNameUnicode[name] = u
			}
		}
	}
	ZapfDingbatsToUnicode[32] = 0x0020
	for i, name := range lowNames {
		code := 33 + i
		u, ok := lowExceptions[code]
		if !ok {
			u = uint16(0x26E0 + code)
		}
		set(code, name, u)
	}
	for i, name := range parenNames {
		set(128+i, name, uint16(0x2768+i))
	}
	for i, name := range highNames {
		if name == "" {
			continue
		}
		code := 161 + i
		u, ok := highExceptions[code]
		if !ok {
			u = uint16(0x26C0 + code)
		}
		set(code, name, u)
	}
}
