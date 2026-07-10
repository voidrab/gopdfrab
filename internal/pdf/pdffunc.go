package pdf

import (
	"fmt"
	"math"
)

// Function evaluates a PDF Function (Types 0, 2, 3, 4; ISO 32000-1 §7.10)
// mapping m input values to n output values.
type Function interface {
	Eval(in []float64) []float64
}

// ParseFunction builds a Function from a resolved Function Dictionary or
// Stream (v must already be dereferenced, as everything in this package's
// graph is after ResolveGraph).
func ParseFunction(v PDFValue) (Function, error) {
	return parseFunction(v, 0)
}

// maxFunctionDepth bounds Type-3 (stitching) sub-function nesting so a
// function array that nests stitching functions deeply cannot recurse
// parseFunction into a stack overflow. Real function trees are shallow.
const maxFunctionDepth = 32

func parseFunction(v PDFValue, depth int) (Function, error) {
	if depth > maxFunctionDepth {
		return nil, fmt.Errorf("pdffunc: function nesting too deep")
	}
	d, ok := v.(PDFDict)
	if !ok {
		return nil, fmt.Errorf("pdffunc: function value is not a dictionary")
	}
	ft, _ := PDFNumberToInt(d.Entries["FunctionType"])

	domain, err := FloatArray(d.Entries["Domain"])
	if err != nil {
		return nil, fmt.Errorf("pdffunc: Domain: %w", err)
	}

	switch ft {
	case 0:
		return newSampledFunction(d, domain)
	case 2:
		return newExponentialFunction(d, domain)
	case 3:
		return newStitchingFunction(d, domain, depth)
	case 4:
		return newPostScriptFunction(d, domain)
	default:
		return nil, fmt.Errorf("pdffunc: unsupported FunctionType %d", ft)
	}
}

// floatArray converts a PDFArray of PDFInteger/PDFReal values to []float64.
func FloatArray(v PDFValue) ([]float64, error) {
	arr, ok := v.(PDFArray)
	if !ok {
		return nil, fmt.Errorf("expected an array, got %T", v)
	}
	out := make([]float64, len(arr))
	for i, item := range arr {
		f, ok := PDFNumberToFloat(item)
		if !ok {
			return nil, fmt.Errorf("element %d is not a number: %T", i, item)
		}
		out[i] = f
	}
	return out, nil
}

// pdfNumberToFloat extracts a float64 from a PDFInteger/PDFReal value, the
// float counterpart of pdfNumberToInt.
func PDFNumberToFloat(v PDFValue) (float64, bool) {
	switch x := v.(type) {
	case PDFInteger:
		return float64(x), true
	case PDFReal:
		return float64(x), true
	}
	return 0, false
}

// clampDomain restricts in[i] to domain[2i],domain[2i+1] for each input,
// per the Domain clipping all four function types share.
func clampDomain(in, domain []float64) []float64 {
	out := make([]float64, len(in))
	for i, x := range in {
		// A malformed function may declare fewer Domain pairs than the caller
		// supplies inputs (e.g. a DeviceN tint transform with too short a
		// Domain); pass those extra inputs through unclamped rather than
		// indexing out of range.
		if 2*i+1 < len(domain) {
			lo, hi := domain[2*i], domain[2*i+1]
			out[i] = math.Min(math.Max(x, lo), hi)
		} else {
			out[i] = x
		}
	}
	return out
}

// interpolate maps x from [xmin,xmax] to [ymin,ymax] linearly (the PDF spec's
// "Interpolation" function used throughout Function evaluation).
func interpolate(x, xmin, xmax, ymin, ymax float64) float64 {
	if xmax == xmin {
		return ymin
	}
	return ymin + (x-xmin)*(ymax-ymin)/(xmax-xmin)
}

// exponentialFunction implements FunctionType 2: C0 + x^N * (C1-C0).
type exponentialFunction struct {
	domain []float64
	c0, c1 []float64
	n      float64
}

func newExponentialFunction(d PDFDict, domain []float64) (*exponentialFunction, error) {
	c0 := []float64{0}
	c1 := []float64{1}
	if v, ok := d.Entries["C0"]; ok {
		var err error
		if c0, err = FloatArray(v); err != nil {
			return nil, fmt.Errorf("pdffunc: C0: %w", err)
		}
	}
	if v, ok := d.Entries["C1"]; ok {
		var err error
		if c1, err = FloatArray(v); err != nil {
			return nil, fmt.Errorf("pdffunc: C1: %w", err)
		}
	}
	n, _ := PDFNumberToFloat(d.Entries["N"])
	return &exponentialFunction{domain: domain, c0: c0, c1: c1, n: n}, nil
}

func (f *exponentialFunction) Eval(in []float64) []float64 {
	x := clampDomain(in, f.domain)[0]
	xn := math.Pow(x, f.n)
	out := make([]float64, len(f.c0))
	for i := range out {
		out[i] = f.c0[i] + xn*(f.c1[i]-f.c0[i])
	}
	return out
}

// stitchingFunction implements FunctionType 3: dispatches its single input
// to one of several sub-functions by Bounds, then remaps via Encode.
type stitchingFunction struct {
	domain    []float64
	functions []Function
	bounds    []float64
	encode    []float64
}

func newStitchingFunction(d PDFDict, domain []float64, depth int) (*stitchingFunction, error) {
	fnsArr, ok := d.Entries["Functions"].(PDFArray)
	if !ok {
		return nil, fmt.Errorf("pdffunc: Functions: expected an array")
	}
	if len(fnsArr) == 0 {
		return nil, fmt.Errorf("pdffunc: Functions: empty array")
	}
	fns := make([]Function, len(fnsArr))
	for i, fv := range fnsArr {
		fn, err := parseFunction(fv, depth+1)
		if err != nil {
			return nil, fmt.Errorf("pdffunc: Functions[%d]: %w", i, err)
		}
		fns[i] = fn
	}
	bounds, err := FloatArray(d.Entries["Bounds"])
	if err != nil {
		return nil, fmt.Errorf("pdffunc: Bounds: %w", err)
	}
	encode, err := FloatArray(d.Entries["Encode"])
	if err != nil {
		return nil, fmt.Errorf("pdffunc: Encode: %w", err)
	}
	// Eval indexes bounds[k-1]/bounds[k] and encode[2k]/encode[2k+1] for
	// k in [0, len(fns)-1]; require the arrays the spec mandates so a short
	// Bounds/Encode cannot panic Eval on crafted input.
	if len(domain) < 2 {
		return nil, fmt.Errorf("pdffunc: Domain too short")
	}
	if len(bounds) < len(fns)-1 {
		return nil, fmt.Errorf("pdffunc: Bounds too short")
	}
	if len(encode) < 2*len(fns) {
		return nil, fmt.Errorf("pdffunc: Encode too short")
	}
	return &stitchingFunction{domain: domain, functions: fns, bounds: bounds, encode: encode}, nil
}

func (f *stitchingFunction) Eval(in []float64) []float64 {
	x := clampDomain(in, f.domain)[0]

	k := len(f.functions) - 1
	for i, b := range f.bounds {
		if x < b {
			k = i
			break
		}
	}

	lo := f.domain[0]
	if k > 0 {
		lo = f.bounds[k-1]
	}
	hi := f.domain[1]
	if k < len(f.bounds) {
		hi = f.bounds[k]
	}

	encLo, encHi := f.encode[2*k], f.encode[2*k+1]
	x2 := interpolate(x, lo, hi, encLo, encHi)
	return f.functions[k].Eval([]float64{x2})
}

// sampledFunction implements FunctionType 0: multilinear interpolation over
// a grid of sample points read from the function's stream.
type sampledFunction struct {
	domain        []float64
	rangeArr      []float64
	size          []int
	bitsPerSample int
	encode        []float64
	decode        []float64
	samples       []byte
	numOutputs    int
}

func newSampledFunction(d PDFDict, domain []float64) (*sampledFunction, error) {
	sizeArr, err := FloatArray(d.Entries["Size"])
	if err != nil {
		return nil, fmt.Errorf("pdffunc: Size: %w", err)
	}
	size := make([]int, len(sizeArr))
	for i, s := range sizeArr {
		size[i] = int(s)
	}

	bps, _ := PDFNumberToInt(d.Entries["BitsPerSample"])

	rangeArr, err := FloatArray(d.Entries["Range"])
	if err != nil {
		return nil, fmt.Errorf("pdffunc: Range: %w", err)
	}
	numOutputs := len(rangeArr) / 2

	encode := defaultSampledEncode(size)
	if v, ok := d.Entries["Encode"]; ok {
		if encode, err = FloatArray(v); err != nil {
			return nil, fmt.Errorf("pdffunc: Encode: %w", err)
		}
	}
	decode := rangeArr
	if v, ok := d.Entries["Decode"]; ok {
		if decode, err = FloatArray(v); err != nil {
			return nil, fmt.Errorf("pdffunc: Decode: %w", err)
		}
	}

	samples, err := DecodeStream(d)
	if err != nil {
		return nil, fmt.Errorf("pdffunc: sample data: %w", err)
	}

	// Validate array shapes so Eval/multilinearSample cannot index out of
	// range or explode combinatorially. multilinearSample blends 2^m corners
	// (m = number of inputs), so an unbounded m is an exponential-blowup DoS.
	const maxSampledInputs = 12
	if len(size) == 0 || len(size) > maxSampledInputs {
		return nil, fmt.Errorf("pdffunc: unsupported Size dimensionality %d", len(size))
	}
	for _, s := range size {
		if s < 1 {
			return nil, fmt.Errorf("pdffunc: non-positive Size entry")
		}
	}
	if numOutputs == 0 {
		return nil, fmt.Errorf("pdffunc: empty Range")
	}
	if len(domain) < 2*len(size) || len(encode) < 2*len(size) {
		return nil, fmt.Errorf("pdffunc: Domain/Encode too short for Size")
	}
	if len(decode) < 2*numOutputs {
		return nil, fmt.Errorf("pdffunc: Decode/Range too short")
	}

	return &sampledFunction{
		domain: domain, rangeArr: rangeArr, size: size, bitsPerSample: bps,
		encode: encode, decode: decode, samples: samples, numOutputs: numOutputs,
	}, nil
}

func defaultSampledEncode(size []int) []float64 {
	out := make([]float64, 2*len(size))
	for i, s := range size {
		out[2*i] = 0
		out[2*i+1] = float64(s - 1)
	}
	return out
}

func (f *sampledFunction) Eval(in []float64) []float64 {
	in = clampDomain(in, f.domain)
	m := len(f.size)

	// Map each input to a fractional sample-grid coordinate, then clamp to
	// the grid's valid index range (Encode can map outside it).
	e := make([]float64, m)
	for i, x := range in {
		ei := interpolate(x, f.domain[2*i], f.domain[2*i+1], f.encode[2*i], f.encode[2*i+1])
		e[i] = math.Min(math.Max(ei, 0), float64(f.size[i]-1))
	}

	out := f.multilinearSample(e)
	for i := range out {
		dmax := float64((uint64(1) << f.bitsPerSample) - 1)
		out[i] = interpolate(out[i], 0, dmax, f.decode[2*i], f.decode[2*i+1])
	}
	return out
}

// multilinearSample interpolates the sample grid at fractional coordinate e,
// blending the 2^m corner samples surrounding it.
func (f *sampledFunction) multilinearSample(e []float64) []float64 {
	m := len(e)
	floor := make([]int, m)
	frac := make([]float64, m)
	for i, v := range e {
		floor[i] = int(math.Floor(v))
		frac[i] = v - float64(floor[i])
	}

	out := make([]float64, f.numOutputs)
	corners := 1 << m
	for c := 0; c < corners; c++ {
		weight := 1.0
		idx := make([]int, m)
		for i := 0; i < m; i++ {
			if c&(1<<i) != 0 {
				idx[i] = floor[i] + 1
				if idx[i] > f.size[i]-1 {
					idx[i] = f.size[i] - 1
				}
				weight *= frac[i]
			} else {
				idx[i] = floor[i]
				weight *= 1 - frac[i]
			}
		}
		if weight == 0 {
			continue
		}
		sample := f.sampleAt(idx)
		for i := range out {
			out[i] += weight * sample[i]
		}
	}
	return out
}

// sampleAt reads the numOutputs sample values at grid index idx, unpacking
// bitsPerSample-wide big-endian fields from the stream's sample data.
func (f *sampledFunction) sampleAt(idx []int) []float64 {
	flat := 0
	stride := 1
	for i := 0; i < len(idx); i++ {
		flat += idx[i] * stride
		stride *= f.size[i]
	}

	bitOffset := flat * f.numOutputs * f.bitsPerSample
	out := make([]float64, f.numOutputs)
	for i := 0; i < f.numOutputs; i++ {
		out[i] = float64(ReadBits(f.samples, bitOffset+i*f.bitsPerSample, f.bitsPerSample))
	}
	return out
}

// readBits reads an n-bit (n<=32) big-endian unsigned field starting at the
// given bit offset, the sample-unpacking primitive shared by Type 0
// functions and image sample decoding.
func ReadBits(data []byte, bitOffset, n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		byteIdx := (bitOffset + i) / 8
		bitIdx := 7 - (bitOffset+i)%8
		var bit uint64
		if byteIdx < len(data) {
			bit = uint64(data[byteIdx]>>bitIdx) & 1
		}
		v = v<<1 | bit
	}
	return v
}
