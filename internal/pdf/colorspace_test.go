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
