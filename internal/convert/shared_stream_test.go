package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestConvertSharedStreamRewrittenEverywhere verifies that a stream reached
// from two slots is rewritten in every slot. A 16-bit image is both page 1's
// /Thumb and an XObject on both pages; imageSampleDepthFixer rewrites it to
// 8 bits. Patching only the slot the walk came through would leave the other
// copies with the new /Filter and /BitsPerComponent over raw 16-bit samples,
// a FlateDecode image that still verifies as valid.
func TestConvertSharedStreamRewrittenEverywhere(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>")
	b.Obj(3, "<< /Type /Page /Thumb 5 0 R /Parent 2 0 R /MediaBox [0 0 20 20] /Resources << /XObject << /Im0 5 0 R >> >> /Contents 6 0 R >>")
	b.Obj(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 20 20] /Resources << /XObject << /Im0 5 0 R >> >> /Contents 6 0 R >>")
	b.StreamObj(5, "<< /Type /XObject /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 16",
		[]byte{0, 0, 0xff, 0xff, 0x80, 0, 0x40, 0})
	b.StreamObj(6, "<<", []byte("q 10 0 0 10 0 0 cm /Im0 Do Q"))
	in := b.FinishClassic("<< /Size 7 /Root 1 0 R /ID [<00112233445566778899aabbccddeeff><00112233445566778899aabbccddeeff>] >>")

	cr, err := ConvertBytes(in, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()
	out, err := cr.Output()
	if err != nil {
		t.Fatal(err)
	}
	r, err := pdf.OpenBytes(out)
	if err != nil {
		t.Fatal(err)
	}
	g, err := r.ResolveGraph()
	if err != nil {
		t.Fatal(err)
	}
	kids := g.(pdf.PDFDict).Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)
	page1 := kids[0].(pdf.PDFDict)
	images := map[string]pdf.PDFDict{
		"page 1 /Thumb": page1.Entries.Get("Thumb").(pdf.PDFDict),
	}
	for i, k := range kids {
		res := k.(pdf.PDFDict).Entries.Get("Resources").(pdf.PDFDict)
		images["page "+string(rune('1'+i))+" /Im0"] = res.Entries.Get("XObject").(pdf.PDFDict).Entries.Get("Im0").(pdf.PDFDict)
	}
	for where, im := range images {
		if bpc := im.Entries.Get("BitsPerComponent"); bpc != pdf.PDFInteger(8) {
			t.Errorf("%s: BitsPerComponent = %v, want 8", where, bpc)
		}
		data, err := pdf.DecodeStream(im)
		if err != nil {
			t.Errorf("%s: output image does not decode: %v", where, err)
			continue
		}
		// The colour fixer may also have moved it to the output intent's space.
		cs, _ := im.Entries.Get("ColorSpace").(pdf.PDFName)
		want := 4 * map[string]int{"DeviceGray": 1, "DeviceRGB": 3, "DeviceCMYK": 4}[cs.Value]
		if len(data) != want {
			t.Errorf("%s: decoded %d bytes, want %d (2x2 at 8 bits in %s)", where, len(data), want, cs.Value)
		}
	}
}
