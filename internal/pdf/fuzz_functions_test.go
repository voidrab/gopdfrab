package pdf_test

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// FuzzParseFunction builds PDF function dictionaries (Types 0/2/3/4) out of the
// fuzz bytes and evaluates them. It targets the Type-4 PostScript calculator
// (tokenize/parse/exec) and Type-0 sampled interpolation, both rich in indexing
// and recursion. Invariant: neither ParseFunction nor Eval may panic.
func FuzzParseFunction(f *testing.F) {
	f.Add([]byte("{ 2 copy add }"))
	f.Add([]byte("{ { } { } ifelse }"))
	f.Add([]byte{0x00, 0x40, 0x80, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		for _, fn := range candidateFunctions(data) {
			parsed, err := pdf.ParseFunction(fn)
			if err != nil {
				continue
			}
			parsed.Eval([]float64{0})
			parsed.Eval([]float64{0.5, 0.25})
		}
	})
}

// candidateFunctions returns several function dicts seeded from data: a Type-4
// PostScript program (data as the stream), and a Type-0 sampled function (data
// as sample bytes).
func candidateFunctions(data []byte) []pdf.PDFValue {
	unit := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}

	ps := pdf.NewPDFDict()
	ps.HasStream = true
	ps.RawStream = data
	ps.Entries["FunctionType"] = pdf.PDFInteger(4)
	ps.Entries["Domain"] = unit
	ps.Entries["Range"] = unit
	ps.Entries["Length"] = pdf.PDFInteger(len(data))

	sampled := pdf.NewPDFDict()
	sampled.HasStream = true
	sampled.RawStream = data
	sampled.Entries["FunctionType"] = pdf.PDFInteger(0)
	sampled.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFInteger(1)}
	sampled.Entries["Range"] = unit
	sampled.Entries["Size"] = pdf.PDFArray{pdf.PDFInteger(2), pdf.PDFInteger(2)}
	sampled.Entries["BitsPerSample"] = pdf.PDFInteger(8)
	sampled.Entries["Length"] = pdf.PDFInteger(len(data))

	exp := pdf.NewPDFDict()
	exp.Entries["FunctionType"] = pdf.PDFInteger(2)
	exp.Entries["Domain"] = unit
	exp.Entries["N"] = pdf.PDFInteger(1)

	return []pdf.PDFValue{ps, sampled, exp}
}

// FuzzResolveColor drives the colour-space resolver with fuzz-built colour
// spaces and a resources dictionary that may contain a self-referential named
// colour space -- the shape that previously recursed forever. Invariant: no
// panic and no infinite recursion.
func FuzzResolveColor(f *testing.F) {
	f.Add([]byte("Indexed"), byte(0))
	f.Add([]byte("CS0"), byte(1))
	f.Fuzz(func(t *testing.T, name []byte, shape byte) {
		if len(name) > 256 {
			return
		}
		csName := string(name)

		// Build a resources dict whose /ColorSpace maps csName to a value that,
		// depending on shape, forms a cycle or nests another colour space.
		inner := pdf.NewPDFDict()
		switch shape % 4 {
		case 0:
			inner.Entries[csName] = pdf.PDFName{Value: csName} // direct self-cycle
		case 1:
			inner.Entries[csName] = pdf.PDFArray{pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: csName}, pdf.PDFInteger(1), pdf.PDFString{Value: "\x00\x01"}}
		case 2:
			inner.Entries[csName] = pdf.PDFArray{pdf.PDFName{Value: "Separation"}, pdf.PDFName{Value: "All"}, pdf.PDFName{Value: csName}, pdf.PDFInteger(0)}
		default:
			inner.Entries[csName] = pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, pdf.NewPDFDict()}
		}
		resources := pdf.NewPDFDict()
		resources.Entries["ColorSpace"] = inner

		pdf.ResolveColor(pdf.PDFName{Value: csName}, []float64{0, 0.5, 1}, resources)
	})
}
