package gopdfrab

import (
	"context"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// linkDoc builds a two-page document, both 200x100 pt. Page one carries the
// annotations in annots; the catalog gets catalogExtra.
func linkDoc(t *testing.T, annots, catalogExtra string) *Document {
	t.Helper()
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R "+catalogExtra+" >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Annots ["+annots+"] >>")
	b.Obj(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] >>")
	b.Obj(5, "<< /Type /Annot /Subtype /Link /Rect [10 70 60 90] /A << /S /URI /URI (https://example.com/a) >> >>")
	b.Obj(6, "<< /Type /Annot /Subtype /Link /Rect [70 20 120 40] /Dest [4 0 R /XYZ 0 60 0] >>")
	b.Obj(7, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /A << /S /GoTo /D (chapter) >> >>")
	b.Obj(8, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest /intro >>")
	b.Obj(9, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest [4 0 R /Fit] >>")
	b.Obj(10, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest (deep) >>")
	b.Obj(11, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest (nowhere) >>")
	b.Obj(12, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /A << /S /Launch >> >>")
	b.Obj(13, "<< /Type /Annot /Subtype /Text /Rect [0 0 10 10] >>")
	b.Obj(14, "<< /Type /Annot /Subtype /Link /Rect [1 2 3] /Dest [4 0 R /Fit] >>")
	b.Obj(15, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest [] >>")
	b.Obj(16, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest [99 /Fit] >>")
	b.Obj(17, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest [3 0 R /FitH 25] >>")
	b.Obj(18, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /A << /S /URI /URI () >> >>")
	b.Obj(19, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /Dest 7 >>")
	doc, err := OpenBytes(b.FinishClassic("<< /Size 20 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { doc.Close() })
	return doc
}

func TestLinksFromURIsAndDestinations(t *testing.T) {
	doc := linkDoc(t, "5 0 R 6 0 R 9 0 R 17 0 R", "")
	p, _ := doc.Page(0)
	got, err := p.Links(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Link{
		// From the lower left in PDF space to the upper left of the page.
		{Box: Rect{10, 10, 60, 30}, URI: "https://example.com/a", Page: -1},
		{Box: Rect{70, 60, 120, 80}, Page: 1, Y: 40}, // /XYZ 0 60: 40 down from the top
		{Box: Rect{0, 90, 10, 100}, Page: 1},         // /Fit names no position
		{Box: Rect{0, 90, 10, 100}, Page: 0, Y: 75},  // /FitH 25: 75 down from the top
	}
	if len(got) != len(want) {
		t.Fatalf("%d links, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNamedDestinationsInTheCatalogAndTheNameTree(t *testing.T) {
	// "chapter" is in /Dests, "intro" is a dictionary there, "deep" is in a name
	// tree two levels down with limits that rule the other branch out.
	extra := `/Dests << /chapter [4 0 R /XYZ 0 50 0] /intro << /D [4 0 R /Fit] >> >> ` +
		`/Names << /Dests << /Kids [ << /Limits [(a) (c)] /Names [(b) [3 0 R /Fit]] >> ` +
		`<< /Limits [(d) (f)] /Kids [ << /Names [(deep) [4 0 R /FitH 10]] >> ] >> ] >> >>`
	doc := linkDoc(t, "7 0 R 8 0 R 10 0 R 11 0 R", extra)
	p, _ := doc.Page(0)
	got, _ := p.Links(context.Background())
	if len(got) != 3 {
		t.Fatalf("%d links, want 3 (one names nothing): %+v", len(got), got)
	}
	if got[0].Page != 1 || got[0].Y != 50 {
		t.Errorf("(chapter) = %+v", got[0])
	}
	if got[1].Page != 1 || got[1].Y != 0 {
		t.Errorf("/intro = %+v", got[1])
	}
	if got[2].Page != 1 || got[2].Y != 90 {
		t.Errorf("(deep) = %+v, want page 1, 90 down", got[2])
	}
}

func TestLinksThatCannotBeUnderstoodAreLeftOut(t *testing.T) {
	// Another action, a note, a short /Rect, an empty or unresolvable
	// destination, an empty URI, and a number as a destination.
	doc := linkDoc(t, "12 0 R 13 0 R 14 0 R 15 0 R 16 0 R 18 0 R 19 0 R 99 0 R", "")
	p, _ := doc.Page(0)
	if got, err := p.Links(context.Background()); err != nil || len(got) != 0 {
		t.Errorf("links = %+v, err %v", got, err)
	}
	// A page without annotations has none, and a cancelled call fails.
	p2, _ := doc.Page(1)
	if got, _ := p2.Links(context.Background()); len(got) != 0 {
		t.Errorf("links of a page without annotations: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Links(ctx); err == nil {
		t.Error("a cancelled call succeeded")
	}
}

func TestLinkBoxesFollowRotation(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Rotate 90 /Annots [4 0 R] >>")
	b.Obj(4, "<< /Type /Annot /Subtype /Link /Rect [10 70 60 90] /A << /S /URI /URI <68747470733A2F2F782E6F7267> >> >>")
	doc, err := OpenBytes(b.FinishClassic("<< /Size 5 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	p, _ := doc.Page(0)
	got, _ := p.Links(context.Background())
	// Unrotated the box is x 10..60, y 10..30 from the top; a quarter turn
	// clockwise of a page 100 tall maps it to x 100-30..100-10, y 10..60.
	if len(got) != 1 || got[0].Box != (Rect{70, 10, 90, 60}) || got[0].URI != "https://x.org" {
		t.Errorf("links = %+v", got)
	}
}

func TestDestinationEdgeCases(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"MediaBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(200), pdf.PDFInteger(100)}})}
	name := func(s string) pdf.PDFValue { return pdf.PDFName{Value: s} }
	for _, dest := range []pdf.PDFArray{
		{page},                                 // no view at all
		{page, name("XYZ"), pdf.PDFInteger(0)}, // /XYZ cut short
		{page, name("XYZ"), pdf.PDFInteger(0), name("top")}, // a top that is not a number
		{page, name("FitV"), pdf.PDFInteger(5)},             // a view without a top
	} {
		if y := destY(page, dest); y != 0 {
			t.Errorf("destY(%v) = %v, want 0", dest, y)
		}
	}
	if pdfText(pdf.PDFInteger(3)) != "" {
		t.Error("an integer was taken for a string")
	}

	// A name tree is searched no deeper than it can sensibly be.
	leaf := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Names": pdf.PDFArray{pdf.PDFString{Value: "k"}, pdf.PDFInteger(1)}})}
	node := leaf
	for range 40 {
		node = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{node}})}
	}
	if nameTreeLookup(node, "k", 0) != nil {
		t.Error("a name tree 40 levels deep was searched to the bottom")
	}
	odd := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{
		pdf.PDFInteger(1), // a kid that is not a node
		pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Limits": pdf.PDFArray{pdf.PDFString{Value: "a"}}, "Names": leaf.Entries.Get("Names")})}, // malformed limits
	}})}
	if v := nameTreeLookup(odd, "k", 0); v != pdf.PDFInteger(1) {
		t.Errorf("lookup past malformed nodes = %v", v)
	}

	// The catalog may have /Dests without the name, and /Names without /Dests.
	d := &Document{root: pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Dests": pdf.PDFDict{Entries: pdf.DictOf(nil)},
		"Names": pdf.PDFDict{Entries: pdf.DictOf(nil)},
	})}}
	if d.namedDest("x") != nil {
		t.Error("a destination was found in empty tables")
	}
}
