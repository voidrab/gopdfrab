package convert

import (
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
	"github.com/voidrab/gopdfrab/internal/raster"
	"github.com/voidrab/gopdfrab/internal/verify"
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
	// An annotation's Type entry is optional.
	b.Obj(8, "<< /Subtype /Square /Rect [120 20 180 80] /F 4 /OC 5 0 R >>")
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

// ocFixture returns a trailer whose OCProperties breaks every 6.9 rule: D
// has no Name and an AS entry, Configs repeats D itself and repeats a name,
// a group has a name object for its Name, and an Order array, which two
// configurations share, misses a group.
func ocFixture() (trailer, props pdf.PDFDict) {
	a := kv("Type", name("OCG"), "Name", pdf.PDFString{Value: "A"})
	b := kv("Type", name("OCG"), "Name", name("B"))
	c := kv("Type", name("OCG"))
	shared := pdf.PDFArray{a}
	d := kv("AS", pdf.PDFArray{}, "Order", shared)
	x1 := kv("Name", pdf.PDFString{Value: "X"}, "Order", shared)
	x2 := kv("Name", pdf.PDFString{Value: "X"}, "AS", pdf.PDFArray{})
	props = kv("OCGs", pdf.PDFArray{a, b, c}, "D", d, "Configs", pdf.PDFArray{d, x1, pdf.PDFInteger(1), x2, x1})
	return trailerWith("OCProperties", props), props
}

// TestOCConfigFixerRepairsEveryRule verifies that after a fix with every 6.9
// check reported, the configurations break none of the rules.
func TestOCConfigFixerRepairsEveryRule(t *testing.T) {
	trailer, props := ocFixture()
	s := pdf.Checks.Structure
	changed, err := ocConfigFixer{}.Fix(&trailer, issuesOf(s.OCConfigMissingName, s.OCConfigDuplicateName,
		s.OCConfigOrderIncomplete, s.OCConfigAutoState))
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	configs := verify.OCConfigs(props)
	if len(configs) != 3 {
		t.Fatalf("%d configurations, want D and the two distinct Configs entries", len(configs))
	}
	var names []string
	for i, cfg := range configs {
		name, ok := verify.OCConfigName(cfg)
		if !ok {
			t.Errorf("configuration %d has no name", i)
		}
		names = append(names, name)
		if _, ok := cfg.Entries.Lookup("AS"); ok {
			t.Errorf("configuration %d kept AS", i)
		}
		if missing := verify.OCGroupsMissingFromOrder(cfg, verify.OCGroups(props)); len(missing) != 0 {
			t.Errorf("configuration %d Order misses %d groups", i, len(missing))
		}
	}
	if want := []string{"Default", "X", "Configuration"}; !slices.Equal(names, want) {
		t.Errorf("names = %q, want %q", names, want)
	}
	if slices.Contains(verify.OCConfigDuplicates(configs), true) {
		t.Error("a name is still repeated")
	}
	var groupNames []string
	for _, g := range verify.OCGroups(props) {
		groupNames = append(groupNames, verify.OCGroupName(g))
	}
	if want := []string{"A", "B", "Layer"}; !slices.Equal(groupNames, want) {
		t.Errorf("group names = %q, want %q", groupNames, want)
	}
	if order := configs[0].Entries.Get("Order").(pdf.PDFArray); len(order) != 3 {
		t.Errorf("D Order has %d entries, want 3", len(order))
	}
}

// TestOCConfigFixerNoOp verifies that the fixer changes nothing without an
// OCProperties dictionary, or when the reported check finds nothing to fix.
func TestOCConfigFixerNoOp(t *testing.T) {
	s := pdf.Checks.Structure
	all := issuesOf(s.OCConfigMissingName, s.OCConfigDuplicateName, s.OCConfigOrderIncomplete, s.OCConfigAutoState)
	conforming := kv("OCGs", pdf.PDFArray{}, "D", kv("Name", pdf.PDFString{Value: "D"}),
		"Configs", pdf.PDFArray{kv("Name", pdf.PDFString{Value: "E"})})
	for _, trailer := range []pdf.PDFDict{
		kv(),
		trailerWith("Other", pdf.PDFInteger(1)),
		trailerWith("OCProperties", pdf.PDFInteger(1)),
		trailerWith("OCProperties", conforming),
		trailerWith("OCProperties", kv("Configs", pdf.PDFInteger(1))),
	} {
		if changed, err := (ocConfigFixer{}).Fix(&trailer, all); changed || err != nil {
			t.Errorf("Fix(%v) = %v, %v, want no change", trailer, changed, err)
		}
	}
}

func TestUniqueName(t *testing.T) {
	taken := map[string]bool{"Layer": true, "Layer 2": true}
	if got := uniqueName(taken, "Layer"); got != "Layer 3" || !taken["Layer 3"] {
		t.Errorf("uniqueName = %q, taken %v", got, taken)
	}
	if got := uniqueName(taken, "Other"); got != "Other" {
		t.Errorf("uniqueName = %q, want Other", got)
	}
}
