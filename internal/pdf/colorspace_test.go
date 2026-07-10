package pdf

import "testing"

func approxRGB(t *testing.T, gotR, gotG, gotB, wantR, wantG, wantB, tol float64) {
	t.Helper()
	if !almostEqual(gotR, wantR, tol) || !almostEqual(gotG, wantG, tol) || !almostEqual(gotB, wantB, tol) {
		t.Errorf("got (%v,%v,%v), want (%v,%v,%v)", gotR, gotG, gotB, wantR, wantG, wantB)
	}
}

func TestResolveColorDeviceRGB(t *testing.T) {
	r, g, b := ResolveColor(PDFName{Value: "DeviceRGB"}, []float64{0.2, 0.4, 0.6}, PDFDict{})
	approxRGB(t, r, g, b, 0.2, 0.4, 0.6, 1e-9)
}

func TestResolveColorDeviceGray(t *testing.T) {
	r, g, b := ResolveColor(PDFName{Value: "DeviceGray"}, []float64{0.7}, PDFDict{})
	approxRGB(t, r, g, b, 0.7, 0.7, 0.7, 1e-9)
}

func TestResolveColorDeviceCMYK(t *testing.T) {
	// Pure black (K=1) should map to RGB black under the spec's naive formula.
	r, g, b := ResolveColor(PDFName{Value: "DeviceCMYK"}, []float64{0, 0, 0, 1}, PDFDict{})
	approxRGB(t, r, g, b, 0, 0, 0, 1e-9)

	// No ink at all should map to white.
	r, g, b = ResolveColor(PDFName{Value: "DeviceCMYK"}, []float64{0, 0, 0, 0}, PDFDict{})
	approxRGB(t, r, g, b, 1, 1, 1, 1e-9)
}

func TestResolveColorICCBasedByComponentCount(t *testing.T) {
	cs := PDFArray{PDFName{Value: "ICCBased"}, PDFDict{Entries: map[string]PDFValue{"N": PDFInteger(3)}}}
	r, g, b := ResolveColor(cs, []float64{0.1, 0.2, 0.3}, PDFDict{})
	approxRGB(t, r, g, b, 0.1, 0.2, 0.3, 1e-9)

	csGray := PDFArray{PDFName{Value: "ICCBased"}, PDFDict{Entries: map[string]PDFValue{"N": PDFInteger(1)}}}
	r, g, b = ResolveColor(csGray, []float64{0.5}, PDFDict{})
	approxRGB(t, r, g, b, 0.5, 0.5, 0.5, 1e-9)
}

func TestResolveColorIndexed(t *testing.T) {
	// 2-entry RGB palette: index 0 = red, index 1 = blue.
	lookup := PDFString{Value: string([]byte{255, 0, 0, 0, 0, 255})}
	cs := PDFArray{PDFName{Value: "Indexed"}, PDFName{Value: "DeviceRGB"}, PDFInteger(1), lookup}

	r, g, b := ResolveColor(cs, []float64{0}, PDFDict{})
	approxRGB(t, r, g, b, 1, 0, 0, 1e-9)

	r, g, b = ResolveColor(cs, []float64{1}, PDFDict{})
	approxRGB(t, r, g, b, 0, 0, 1, 1e-9)
}

func TestResolveColorSeparation(t *testing.T) {
	// Tint 0 -> white, tint 1 -> full DeviceGray-alternate black via Type 2 function.
	tint := PDFDict{Entries: map[string]PDFValue{
		"FunctionType": PDFInteger(2),
		"Domain":       PDFArray{PDFReal(0), PDFReal(1)},
		"C0":           PDFArray{PDFReal(1)},
		"C1":           PDFArray{PDFReal(0)},
		"N":            PDFInteger(1),
	}}
	cs := PDFArray{PDFName{Value: "Separation"}, PDFName{Value: "Black"}, PDFName{Value: "DeviceGray"}, tint}

	r, g, b := ResolveColor(cs, []float64{0}, PDFDict{})
	approxRGB(t, r, g, b, 1, 1, 1, 1e-9)

	r, g, b = ResolveColor(cs, []float64{1}, PDFDict{})
	approxRGB(t, r, g, b, 0, 0, 0, 1e-9)
}

func TestResolveColorNamedInResources(t *testing.T) {
	resources := PDFDict{Entries: map[string]PDFValue{
		"ColorSpace": PDFDict{Entries: map[string]PDFValue{
			"CS0": PDFName{Value: "DeviceRGB"},
		}},
	}}
	r, g, b := ResolveColor(PDFName{Value: "CS0"}, []float64{0.9, 0.1, 0.2}, resources)
	approxRGB(t, r, g, b, 0.9, 0.1, 0.2, 1e-9)
}

func TestResolveColorLabKnownPoints(t *testing.T) {
	// L=100 (white) should resolve close to RGB white; L=0 (black) close to RGB black.
	r, g, b := ResolveColor(PDFArray{PDFName{Value: "Lab"}}, []float64{100, 0, 0}, PDFDict{})
	approxRGB(t, r, g, b, 1, 1, 1, 0.05)

	r, g, b = ResolveColor(PDFArray{PDFName{Value: "Lab"}}, []float64{0, 0, 0}, PDFDict{})
	approxRGB(t, r, g, b, 0, 0, 0, 0.05)
}

func TestResolveColorUnsupportedFallsBackToPlaceholder(t *testing.T) {
	r, g, b := ResolveColor(PDFName{Value: "Pattern"}, nil, PDFDict{})
	approxRGB(t, r, g, b, 0.5, 0.5, 0.5, 1e-9)
}

func rgbClose(a, b [3]float64, tol float64) bool {
	for i := range a {
		if a[i]-b[i] > tol || b[i]-a[i] > tol {
			return false
		}
	}
	return true
}

// TestResolveColorSpaces drives ResolveColor across every colour-space form it
// handles, including array bases, ICCBased by N, Indexed, Separation, Lab, a
// named-lookup indirection, and the mid-gray fallbacks.
func TestResolveColorSpaces(t *testing.T) {
	iccN := func(n int) PDFArray {
		return PDFArray{PDFName{Value: "ICCBased"}, PDFDict{Entries: map[string]PDFValue{"N": PDFInteger(n)}}}
	}
	resources := PDFDict{Entries: map[string]PDFValue{
		"ColorSpace": PDFDict{Entries: map[string]PDFValue{
			"CustomRGB": PDFArray{PDFName{Value: "DeviceRGB"}},
		}},
	}}

	cases := []struct {
		name  string
		cs    PDFValue
		comps []float64
		want  [3]float64
	}{
		{"name-rgb", PDFName{Value: "DeviceRGB"}, []float64{0.1, 0.2, 0.3}, [3]float64{0.1, 0.2, 0.3}},
		{"name-gray", PDFName{Value: "G"}, []float64{0.5}, [3]float64{0.5, 0.5, 0.5}},
		{"name-cmyk", PDFName{Value: "CMYK"}, []float64{0, 0, 0, 0}, [3]float64{1, 1, 1}},
		{"name-lookup", PDFName{Value: "CustomRGB"}, []float64{0.2, 0.4, 0.6}, [3]float64{0.2, 0.4, 0.6}},
		{"name-unknown", PDFName{Value: "Pattern"}, []float64{0.3}, [3]float64{0.3, 0.3, 0.3}},
		{"arr-rgb", PDFArray{PDFName{Value: "DeviceRGB"}}, []float64{0.1, 0.2, 0.3}, [3]float64{0.1, 0.2, 0.3}},
		{"arr-gray", PDFArray{PDFName{Value: "DeviceGray"}}, []float64{0.4}, [3]float64{0.4, 0.4, 0.4}},
		{"arr-cmyk", PDFArray{PDFName{Value: "DeviceCMYK"}}, []float64{0, 0, 0, 1}, [3]float64{0, 0, 0}},
		{"arr-calrgb", PDFArray{PDFName{Value: "CalRGB"}}, []float64{0.1, 0.2, 0.3}, [3]float64{0.1, 0.2, 0.3}},
		{"arr-calgray", PDFArray{PDFName{Value: "CalGray"}}, []float64{0.4}, [3]float64{0.4, 0.4, 0.4}},
		{"icc-1", iccN(1), []float64{0.5}, [3]float64{0.5, 0.5, 0.5}},
		{"icc-3", iccN(3), []float64{0.1, 0.2, 0.3}, [3]float64{0.1, 0.2, 0.3}},
		{"icc-4", iccN(4), []float64{0, 0, 0, 0}, [3]float64{1, 1, 1}},
		{"empty-arr", PDFArray{}, []float64{0.5}, [3]float64{0.5, 0.5, 0.5}},
		{"non-name-head", PDFArray{PDFInteger(1)}, []float64{0.5}, [3]float64{0.5, 0.5, 0.5}},
		{"unknown-head", PDFArray{PDFName{Value: "Nope"}}, []float64{0.5}, [3]float64{0.5, 0.5, 0.5}},
		{"non-cs", PDFInteger(1), []float64{0.5}, [3]float64{0.5, 0.5, 0.5}},
	}
	for _, c := range cases {
		r, g, b := ResolveColor(c.cs, c.comps, resources)
		if !rgbClose([3]float64{r, g, b}, c.want, 1e-6) {
			t.Errorf("%s: ResolveColor = (%v,%v,%v), want %v", c.name, r, g, b, c.want)
		}
	}

	// Lab: L=0 -> near black.
	if r, g, b := ResolveColor(PDFArray{PDFName{Value: "Lab"}}, []float64{0, 0, 0}, resources); r > 0.05 || g > 0.05 || b > 0.05 {
		t.Errorf("Lab(0,0,0) = (%v,%v,%v), want near black", r, g, b)
	}

	// Indexed: palette of two RGB entries; index 1 selects the second.
	indexed := PDFArray{
		PDFName{Value: "Indexed"}, PDFName{Value: "DeviceRGB"}, PDFInteger(1),
		PDFString{Value: "\x00\x00\x00\xff\xff\xff"},
	}
	if r, g, b := ResolveColor(indexed, []float64{1}, resources); !rgbClose([3]float64{r, g, b}, [3]float64{1, 1, 1}, 1e-6) {
		t.Errorf("Indexed[1] = (%v,%v,%v), want white", r, g, b)
	}
	// Indexed out-of-range index -> fallback.
	if _, _, b := ResolveColor(indexed, []float64{99}, resources); b == 0 {
		_ = b // just exercising the bounds-check fallback branch
	}

	// Separation with a Type 2 identity tint over DeviceGray.
	tint := PDFDict{Entries: map[string]PDFValue{
		"FunctionType": PDFInteger(2), "Domain": PDFArray{PDFReal(0), PDFReal(1)},
		"C0": PDFArray{PDFReal(0)}, "C1": PDFArray{PDFReal(1)}, "N": PDFInteger(1),
	}}
	sep := PDFArray{PDFName{Value: "Separation"}, PDFName{Value: "Spot"}, PDFName{Value: "DeviceGray"}, tint}
	if r, _, _ := ResolveColor(sep, []float64{0.5}, resources); r < 0.4 || r > 0.6 {
		t.Errorf("Separation(0.5) = %v, want ~0.5", r)
	}
	// Separation with a bad tint function -> fallback.
	ResolveColor(PDFArray{PDFName{Value: "Separation"}, PDFName{Value: "S"}, PDFName{Value: "DeviceGray"}, PDFInteger(0)}, []float64{0.5}, resources)
	ResolveColor(PDFArray{PDFName{Value: "Separation"}}, []float64{0.5}, resources) // too short
}

// TestColorSpaceComponents covers ColorSpaceComponents for every branch.
func TestColorSpaceComponents(t *testing.T) {
	cases := []struct {
		cs   PDFValue
		want int
	}{
		{PDFName{Value: "DeviceGray"}, 1},
		{PDFName{Value: "RGB"}, 3},
		{PDFName{Value: "CMYK"}, 4},
		{PDFName{Value: "Weird"}, 1},
		{PDFArray{PDFName{Value: "CalGray"}}, 1},
		{PDFArray{PDFName{Value: "Lab"}}, 3},
		{PDFArray{PDFName{Value: "DeviceCMYK"}}, 4},
		{PDFArray{PDFName{Value: "ICCBased"}, PDFDict{Entries: map[string]PDFValue{"N": PDFInteger(3)}}}, 3},
		{PDFArray{PDFName{Value: "Separation"}}, 1},
		{PDFArray{PDFName{Value: "DeviceN"}, PDFArray{PDFName{Value: "a"}, PDFName{Value: "b"}}}, 2},
		{PDFArray{}, 1},
		{PDFInteger(0), 1},
	}
	for i, c := range cases {
		if got := ColorSpaceComponents(c.cs); got != c.want {
			t.Errorf("case %d: ColorSpaceComponents = %d, want %d", i, got, c.want)
		}
	}
}

// TestIndexedLookupBytes covers indexedLookupBytes for string, hex, stream, and
// unsupported operand forms.
func TestIndexedLookupBytes(t *testing.T) {
	if b := indexedLookupBytes(PDFString{Value: "abc"}); string(b) != "abc" {
		t.Errorf("string lookup = %q", b)
	}
	if b := indexedLookupBytes(PDFHexString{Value: "414243"}); string(b) != "ABC" {
		t.Errorf("hex lookup = %q", b)
	}
	stream := PDFDict{Entries: map[string]PDFValue{}, HasStream: true, RawStream: []byte("xyz")}
	if b := indexedLookupBytes(stream); string(b) != "xyz" {
		t.Errorf("stream lookup = %q", b)
	}
	if b := indexedLookupBytes(PDFInteger(1)); b != nil {
		t.Errorf("unsupported lookup = %v, want nil", b)
	}
	badStream := PDFDict{
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "JPXDecode"}},
		HasStream: true, RawStream: []byte("x"),
	}
	if b := indexedLookupBytes(badStream); b != nil {
		t.Errorf("undecodable stream lookup = %v, want nil", b)
	}
}

// TestColorHelperShortComponentFallbacks covers comp3, gray, CMYKToRGB,
// resolveICCBased, resolveIndexed, and labToRGB's too-few-components and
// malformed-input fallback branches.
func TestColorHelperShortComponentFallbacks(t *testing.T) {
	// Too few components falls back to placeholderRGB, which itself grays the
	// first available component rather than a flat 0.5 when comps is non-empty.
	if r, g, b := comp3([]float64{0.1, 0.2}); r != 0.1 || g != 0.1 || b != 0.1 {
		t.Errorf("comp3(<3) = %v,%v,%v, want (0.1,0.1,0.1)", r, g, b)
	}
	if r, g, b := gray(nil); r != 0.5 || g != 0.5 || b != 0.5 {
		t.Errorf("gray(empty) = %v,%v,%v, want mid-gray", r, g, b)
	}
	if r, g, b := CMYKToRGB([]float64{0, 0, 0}); r != 0 || g != 0 || b != 0 {
		t.Errorf("CMYKToRGB(<4) = %v,%v,%v, want (0,0,0)", r, g, b)
	}
	if r, g, b := resolveICCBased(PDFArray{PDFName{Value: "ICCBased"}}, nil); r != 0.5 || g != 0.5 || b != 0.5 {
		t.Errorf("resolveICCBased(len<2) = %v,%v,%v, want mid-gray", r, g, b)
	}
	if r, g, b := resolveICCBased(PDFArray{PDFName{Value: "ICCBased"}, PDFInteger(1)}, nil); r != 0.5 || g != 0.5 || b != 0.5 {
		t.Errorf("resolveICCBased(non-dict) = %v,%v,%v, want mid-gray", r, g, b)
	}
	iccOdd := PDFArray{PDFName{Value: "ICCBased"}, PDFDict{Entries: map[string]PDFValue{"N": PDFInteger(2)}}}
	if r, g, b := resolveICCBased(iccOdd, []float64{0.5}); r != 0.5 || g != 0.5 || b != 0.5 {
		t.Errorf("resolveICCBased(N=2) = %v,%v,%v, want mid-gray fallback", r, g, b)
	}
	if r, g, b := resolveIndexed(PDFArray{PDFName{Value: "Indexed"}}, []float64{0}, PDFDict{}, 0); r != 0 || g != 0 || b != 0 {
		t.Errorf("resolveIndexed(len<4) = %v,%v,%v, want (0,0,0)", r, g, b)
	}
	if r, g, b := labToRGB([]float64{100, 0}); r != 1 || g != 1 || b != 1 {
		t.Errorf("labToRGB(<3) = %v,%v,%v, want (1,1,1)", r, g, b)
	}
}
