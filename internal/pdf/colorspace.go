package pdf

import "math"

// ResolveColor converts comps (component values in the colour space cs) to
// an RGB triple in [0,1]. resources supplies the /ColorSpace dictionary used
// to look up named (non-device) colour spaces referenced by name.
//
// This mirrors the same component-count device-model approximation
// deviceColourModel (checks_colour.go) already uses elsewhere in this
// codebase: no ICC profile is actually applied, CalGray/CalRGB are treated
// as their device equivalents, and an unrecognized or unsupported space
// (e.g. Pattern) falls back to mid-gray rather than failing.
func ResolveColor(cs PDFValue, comps []float64, resources PDFDict) (r, g, b float64) {
	return resolveColor(cs, comps, resources, 0)
}

// maxColorSpaceDepth bounds colour-space resolution recursion. A named colour
// space can be looked up through the caller-controlled resources dict, so a
// name that (directly or transitively) resolves back to itself would otherwise
// recurse forever; nested Indexed/Separation bases add further depth. Real
// colour spaces nest only a couple of levels.
const maxColorSpaceDepth = 16

func resolveColor(cs PDFValue, comps []float64, resources PDFDict, depth int) (r, g, b float64) {
	if depth > maxColorSpaceDepth {
		return placeholderRGB(comps)
	}
	switch v := cs.(type) {
	case PDFName:
		switch v.Value {
		case "DeviceRGB", "RGB", "CalRGB":
			return comp3(comps)
		case "DeviceGray", "G", "CalGray":
			return gray(comps)
		case "DeviceCMYK", "CMYK":
			return CMYKToRGB(comps)
		}
		if named, ok := LookupNamedColorSpace(v.Value, resources); ok {
			return resolveColor(named, comps, resources, depth+1)
		}
		return placeholderRGB(comps)

	case PDFArray:
		if len(v) == 0 {
			return placeholderRGB(comps)
		}
		head, ok := v[0].(PDFName)
		if !ok {
			return placeholderRGB(comps)
		}
		switch head.Value {
		case "DeviceRGB":
			return comp3(comps)
		case "DeviceGray":
			return gray(comps)
		case "DeviceCMYK":
			return CMYKToRGB(comps)
		case "CalRGB":
			return comp3(comps)
		case "CalGray":
			return gray(comps)
		case "Lab":
			return labToRGB(comps)
		case "ICCBased":
			return resolveICCBased(v, comps)
		case "Indexed", "I":
			return resolveIndexed(v, comps, resources, depth)
		case "Separation", "DeviceN":
			return resolveSeparation(v, comps, resources, depth)
		}
		return placeholderRGB(comps)
	}
	return placeholderRGB(comps)
}

// placeholderRGB is the mid-gray fallback for colour spaces this resolver
// can't faithfully evaluate (e.g. Pattern).
func placeholderRGB(comps []float64) (r, g, b float64) {
	if len(comps) > 0 {
		return gray(comps[:1])
	}
	return 0.5, 0.5, 0.5
}

func comp3(comps []float64) (r, g, b float64) {
	if len(comps) < 3 {
		return placeholderRGB(comps)
	}
	return clamp01(comps[0]), clamp01(comps[1]), clamp01(comps[2])
}

func gray(comps []float64) (r, g, b float64) {
	if len(comps) < 1 {
		return 0.5, 0.5, 0.5
	}
	v := clamp01(comps[0])
	return v, v, v
}

// cmykToRGB applies the PDF specification's default (non-ICC) CMYK -> RGB
// approximation: R = 1-min(1,C+K), and likewise for G/B.
func CMYKToRGB(comps []float64) (r, g, b float64) {
	if len(comps) < 4 {
		return placeholderRGB(comps)
	}
	c, m, y, k := comps[0], comps[1], comps[2], comps[3]
	return 1 - math.Min(1, c+k), 1 - math.Min(1, m+k), 1 - math.Min(1, y+k)
}

func clamp01(v float64) float64 {
	return math.Min(1, math.Max(0, v))
}

func LookupNamedColorSpace(name string, resources PDFDict) (PDFValue, bool) {
	csDict, ok := resources.Entries["ColorSpace"].(PDFDict)
	if !ok {
		return nil, false
	}
	v, ok := csDict.Entries[name]
	return v, ok
}

func resolveICCBased(arr PDFArray, comps []float64) (r, g, b float64) {
	if len(arr) < 2 {
		return placeholderRGB(comps)
	}
	stream, ok := arr[1].(PDFDict)
	if !ok {
		return placeholderRGB(comps)
	}
	n, _ := PDFNumberToInt(stream.Entries["N"])
	switch n {
	case 1:
		return gray(comps)
	case 3:
		return comp3(comps)
	case 4:
		return CMYKToRGB(comps)
	}
	return placeholderRGB(comps)
}

// colorSpaceComponents returns the number of colour components a (non-
// Indexed) base colour space takes.
func ColorSpaceComponents(cs PDFValue) int {
	switch v := cs.(type) {
	case PDFName:
		switch v.Value {
		case "DeviceGray", "G", "CalGray":
			return 1
		case "DeviceRGB", "RGB", "CalRGB":
			return 3
		case "DeviceCMYK", "CMYK":
			return 4
		}
	case PDFArray:
		if len(v) == 0 {
			return 1
		}
		head, _ := v[0].(PDFName)
		switch head.Value {
		case "DeviceGray", "CalGray":
			return 1
		case "DeviceRGB", "CalRGB", "Lab":
			return 3
		case "DeviceCMYK":
			return 4
		case "ICCBased":
			if len(v) > 1 {
				if stream, ok := v[1].(PDFDict); ok {
					if n, ok := PDFNumberToInt(stream.Entries["N"]); ok {
						return n
					}
				}
			}
		case "Separation":
			return 1
		case "DeviceN":
			if len(v) > 1 {
				if names, ok := v[1].(PDFArray); ok {
					return len(names)
				}
			}
		}
	}
	return 1
}

// resolveIndexed looks up comps[0] (a palette index) in an Indexed colour
// space's lookup table and resolves the resulting base-space components.
func resolveIndexed(arr PDFArray, comps []float64, resources PDFDict, depth int) (r, g, b float64) {
	if len(arr) < 4 || len(comps) < 1 {
		return placeholderRGB(comps)
	}
	base := arr[1]
	lookup := indexedLookupBytes(arr[3])
	n := ColorSpaceComponents(base)
	idx := int(comps[0])
	start := idx * n
	if lookup == nil || start < 0 || start+n > len(lookup) {
		return placeholderRGB(comps)
	}
	baseComps := make([]float64, n)
	for i := 0; i < n; i++ {
		baseComps[i] = float64(lookup[start+i]) / 255
	}
	return resolveColor(base, baseComps, resources, depth+1)
}

func indexedLookupBytes(v PDFValue) []byte {
	switch x := v.(type) {
	case PDFString:
		return []byte(x.Value)
	case PDFHexString:
		return DecodePDFHexStringBytes(x.Value)
	case PDFDict:
		data, err := DecodeStream(x)
		if err != nil {
			return nil
		}
		return data
	}
	return nil
}

// resolveSeparation applies a Separation/DeviceN colour space's tint
// transform (a PDF Function) and resolves the resulting alternate-space
// components.
func resolveSeparation(arr PDFArray, comps []float64, resources PDFDict, depth int) (r, g, b float64) {
	if len(arr) < 4 {
		return placeholderRGB(comps)
	}
	alt := arr[2]
	fn, err := ParseFunction(arr[3])
	if err != nil {
		return placeholderRGB(comps)
	}
	altComps := fn.Eval(comps)
	return resolveColor(alt, altComps, resources, depth+1)
}

// labToRGB converts CIE L*a*b* components to sRGB via XYZ, approximating
// the white point as D50 (the ICC PCS reference white) with no chromatic
// adaptation -- a documented simplification, not a colour-managed transform.
func labToRGB(comps []float64) (r, g, b float64) {
	if len(comps) < 3 {
		return placeholderRGB(comps)
	}
	l, a, bb := comps[0], comps[1], comps[2]

	fy := (l + 16) / 116
	fx := fy + a/500
	fz := fy - bb/200

	finv := func(t float64) float64 {
		const delta = 6.0 / 29.0
		if t > delta {
			return t * t * t
		}
		return 3 * delta * delta * (t - 4.0/29.0)
	}

	const xn, yn, zn = 0.9642, 1.0, 0.8249 // D50 white point
	x := xn * finv(fx)
	y := yn * finv(fy)
	z := zn * finv(fz)

	// XYZ(D50) -> linear sRGB (Bruce Lindbloom's unadapted matrix).
	rl := 3.1338561*x - 1.6168667*y - 0.4906146*z
	gl := -0.9787684*x + 1.9161415*y + 0.0334540*z
	bl := 0.0719453*x - 0.2289914*y + 1.4052427*z

	return gammaEncode(rl), gammaEncode(gl), gammaEncode(bl)
}

func gammaEncode(c float64) float64 {
	c = clamp01(c)
	if c <= 0.0031308 {
		return clamp01(12.92 * c)
	}
	return clamp01(1.055*math.Pow(c, 1/2.4) - 0.055)
}
