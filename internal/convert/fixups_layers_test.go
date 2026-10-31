package convert

import (
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
	"github.com/voidrab/gopdfrab/internal/raster"
)

// hiddenLayerDoc builds a one-page document with two optional content
// groups, "Hidden" turned off by the default configuration and "Shown" left
// on. The hidden group governs a filled square in a marked-content sequence,
// a form XObject through its own /OC entry and a Square annotation; the shown
// group governs a second filled square.
func hiddenLayerDoc() []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /OCProperties << /OCGs [5 0 R 6 0 R] /D << /OFF [5 0 R] >> >> >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200]"+
		" /Resources << /Properties << /H 5 0 R /S 6 0 R >> /XObject << /Fm 7 0 R >> >>"+
		" /Contents 4 0 R /Annots [8 0 R] >>")
	b.StreamObj(4, "<<", []byte("/OC /H BDC 0 g 20 20 60 60 re f EMC\n"+
		"/OC /S BDC 0 g 120 120 60 60 re f EMC\n"+
		"/Fm Do\n"))
	b.Obj(5, "<< /Type /OCG /Name (Hidden) >>")
	b.Obj(6, "<< /Type /OCG /Name (Shown) >>")
	b.StreamObj(7, "<< /Type /XObject /Subtype /Form /BBox [0 0 200 200] /OC 5 0 R", []byte("0 g 20 120 60 60 re f\n"))
	b.Obj(8, "<< /Type /Annot /Subtype /Square /Rect [120 20 180 80] /F 4 /OC 5 0 R >>")
	return b.FinishClassic("<< /Size 9 /Root 1 0 R >>")
}

// TestConvertRemovesHiddenLayers checks that deleting /OCProperties does not
// reveal the layers the document turned off: their content is removed and
// reported, the visible layer is kept, and the output looks like the input.
func TestConvertRemovesHiddenLayers(t *testing.T) {
	cr, err := ConvertBytes(hiddenLayerDoc(), pdf.PDFA1B, Options{CheckFidelity: true})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid {
		t.Fatalf("output not conformant: %v", cr.Result.Issues)
	}

	var got []string
	for _, e := range cr.LostObjects {
		got = append(got, strings.Join(e.Messages(), "; "))
	}
	want := []string{
		`content of hidden optional content group "Hidden" removed`,
		"Square annotation in hidden optional content removed",
	}
	if !slices.Equal(got, want) {
		t.Errorf("LostObjects = %q, want %q", got, want)
	}
	if len(cr.Fidelity) != 1 || cr.Fidelity[0].Similarity < 0.999 || cr.Fidelity[0].Covered > 0 {
		t.Errorf("Fidelity = %+v, want the output to look like the input", cr.Fidelity)
	}

	out := openReader(t, mustOutput(t, cr))
	trailer, err := resolveTrailer(out)
	if err != nil {
		t.Fatalf("resolveTrailer: %v", err)
	}
	p := orderedPages(trailer)[0]
	img, _, err := raster.RenderPage(p.dict, p.resources, p.mediaBox, 72, nil)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	for _, tc := range []struct {
		name string
		x, y int // device pixels, origin top left
		ink  bool
	}{
		{"hidden square", 50, 150, false},
		{"hidden form", 50, 50, false},
		{"shown square", 150, 50, true},
	} {
		if inked := img.RGBAAt(tc.x, tc.y).R < 128; inked != tc.ink {
			t.Errorf("%s: inked = %v, want %v", tc.name, inked, tc.ink)
		}
	}
}

// TestOptionalContentFixerKeepsVisibleLayers checks that a document whose
// layers are all on loses nothing but /OCProperties.
func TestOptionalContentFixerKeepsVisibleLayers(t *testing.T) {
	data := strings.Replace(string(hiddenLayerDoc()), "/D << /OFF [5 0 R] >>", "/D << /ON [5 0 R] >>", 1)
	cr, err := ConvertBytes([]byte(data), pdf.PDFA1B, Options{CheckFidelity: true})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if len(cr.LostObjects) != 0 {
		t.Errorf("LostObjects = %v, want none", cr.LostObjects)
	}
}

// TestOptionalContentFixerReportsEachHiddenGroup checks the record for each
// kind of hidden dictionary, reported once however often its content was
// removed, and that an XObject in a visible layer is kept.
func TestOptionalContentFixerReportsEachHiddenGroup(t *testing.T) {
	off := specDict("Type", specName("OCG"))
	on := specDict("Type", specName("OCG"), "Name", pdf.PDFString{Value: "On"})
	md := specDict("Type", specName("OCMD"), "OCGs", off)
	shown := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": specName("Form"), "BBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(1)},
		"OC": on,
	}), []byte("0 0 1 1 re f"))
	contents := pdf.NewStreamDict(pdf.NewDict(), []byte(
		"/OC /G BDC 0 0 1 1 re f EMC /OC /M BDC 0 0 1 1 re f EMC /OC /G BDC 0 0 1 1 re f EMC /Fm Do"))
	page := specDict("Type", specName("Page"), "Contents", contents, "Resources", specDict(
		"Properties", specDict("G", off, "M", md),
		"XObject", specDict("Fm", shown)))
	root := specDict("Type", specName("Catalog"),
		"Pages", specDict("Type", specName("Pages"), "Kids", pdf.PDFArray{page}, "Count", pdf.PDFInteger(1)),
		"OCProperties", specDict("D", specDict("OFF", pdf.PDFArray{off})))
	trailer := specDict("Root", root)

	var lost []pdf.PDFError
	changed, err := optionalContentFixer{lost: &lost}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v", changed, err)
	}
	var got []string
	for _, e := range lost {
		got = append(got, strings.Join(e.Messages(), "; "))
	}
	want := []string{
		"content of hidden optional content group removed",
		"content of hidden optional content membership removed",
	}
	if !slices.Equal(got, want) {
		t.Errorf("lost = %q, want %q", got, want)
	}
	data, err := pdf.DecodeStream(page.Entries.Get("Contents").(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if s := string(data); strings.Count(s, " f") != 0 || !strings.Contains(s, "/Fm Do") {
		t.Errorf("rewritten content = %q, want every hidden fill gone and the visible form kept", s)
	}
}
