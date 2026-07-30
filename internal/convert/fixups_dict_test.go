package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestViewerPrefFixerRemovesPost14Keys confirms that viewerPrefFixer deletes
// PrintScaling and other post-1.4 ViewerPreferences entries.
func TestViewerPrefFixerRemovesPost14Keys(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries.Set("PrintScaling", pdf.PDFName{Value: "None"})
	vp.Entries.Set("DisplayDocTitle", pdf.PDFBoolean(true)) // 1.4 key, must stay

	root := pdf.NewPDFDict()
	root.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	root.Entries.Set("ViewerPreferences", vp)

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)

	fixer := viewerPrefFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if vp.Entries.Get("PrintScaling") != nil {
		t.Errorf("/PrintScaling still present after viewerPrefFixer")
	}
	if vp.Entries.Get("DisplayDocTitle") == nil {
		t.Errorf("/DisplayDocTitle was removed but should be kept")
	}
}

// TestViewerPrefFixerNoOp confirms viewerPrefFixer reports no change when no
// post-1.4 keys are present.
func TestViewerPrefFixerNoOp(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries.Set("DisplayDocTitle", pdf.PDFBoolean(true))

	root := pdf.NewPDFDict()
	root.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	root.Entries.Set("ViewerPreferences", vp)

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)

	changed, err := viewerPrefFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false for clean ViewerPreferences")
	}
}

// TestActionFixerRemovesJavaScriptNameTree confirms that actionFixer deletes
// the /JavaScript key from the catalog's /Names dict (the name-tree entry),
// in addition to clearing individual JS action dicts.
func TestActionFixerRemovesJavaScriptNameTree(t *testing.T) {
	// Minimal JS action dict (the leaf).
	action := pdf.NewPDFDict()
	action.Entries.Set("S", pdf.PDFName{Value: "JavaScript"})
	action.Entries.Set("JS", pdf.PDFString{Value: "app.alert('hi')"})

	// Name tree dict containing the action.
	nameTree := pdf.NewPDFDict()
	nameTree.Entries.Set("Names", pdf.PDFArray{pdf.PDFString{Value: "open"}, action})

	// Catalog /Names dict with /JavaScript key.
	names := pdf.NewPDFDict()
	names.Entries.Set("JavaScript", nameTree)

	root := pdf.NewPDFDict()
	root.Entries.Set("Names", names)

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)

	fixer := actionFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	// /Names/JavaScript must be gone.
	namesAfter := root.Entries.Get("Names").(pdf.PDFDict)
	if namesAfter.Entries.Get("JavaScript") != nil {
		t.Errorf("/Names/JavaScript still present after actionFixer")
	}

	// The leaf action dict must also be cleared.
	if action.Entries.Get("S") != nil || action.Entries.Get("JS") != nil {
		t.Errorf("leaf action dict not cleared: %v", action.Entries)
	}
}

func TestExtGStateFixer(t *testing.T) {
	gs := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "ExtGState"},
		"TR":    pdf.PDFName{Value: "Identity"},
		"TR2":   pdf.PDFName{Value: "Foo"},
		"RI":    pdf.PDFName{Value: "BadIntent"},
		"SMask": pdf.PDFName{Value: "Foo"},
		"BM":    pdf.PDFName{Value: "Weird"},
		"CA":    pdf.PDFReal(0.5),
		"ca":    pdf.PDFReal(0.5),
	})}
	trailer := trailerWith("GS", gs)
	changed, err := extGStateFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("extGStateFixer.Fix = %v, %v; want changed", changed, err)
	}
	if _, ok := gs.Entries.Lookup("TR"); ok {
		t.Error("TR not removed")
	}
	if gs.Entries.Get("TR2") != (pdf.PDFName{Value: "Default"}) {
		t.Error("TR2 not normalized to Default")
	}
	if _, ok := gs.Entries.Lookup("RI"); ok {
		t.Error("invalid RI not removed")
	}
	if gs.Entries.Get("SMask") != (pdf.PDFName{Value: "None"}) {
		t.Error("SMask not normalized to None")
	}
	if gs.Entries.Get("BM") != (pdf.PDFName{Value: "Normal"}) {
		t.Error("BM not normalized to Normal")
	}
	if gs.Entries.Get("CA") != pdf.PDFReal(1.0) || gs.Entries.Get("ca") != pdf.PDFReal(1.0) {
		t.Error("alpha not normalized to 1.0")
	}
}

func TestAnnotationFlagsFixer(t *testing.T) {
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"},
		"F":    pdf.PDFInteger(2), // Hidden set, Print not set
		"CA":   pdf.PDFReal(0.5),
	})}
	trailer := trailerWith("Annot0", annot)
	changed, err := annotationFlagsFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("annotationFlagsFixer.Fix = %v, %v", changed, err)
	}
	if annot.Entries.Get("CA") != pdf.PDFReal(1.0) {
		t.Error("annotation CA not normalized")
	}
	f := int(annot.Entries.Get("F").(pdf.PDFInteger))
	if f&verify.AnnotFlagPrint == 0 {
		t.Error("Print flag not set")
	}
	if f&verify.AnnotFlagHidden != 0 {
		t.Error("Hidden flag not cleared")
	}
}

func TestFormFixer(t *testing.T) {
	widget := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"}, "Subtype": pdf.PDFName{Value: "Widget"},
		"FT": pdf.PDFName{Value: "Tx"},
		"A":  pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
		"AA": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	})}
	acroForm := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"NeedAppearances": pdf.PDFBoolean(true),
		"XFA":             pdf.PDFArray{},
		"Fields":          pdf.PDFArray{widget},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Catalog"}, "AcroForm": acroForm,
		})},
	})}
	changed, err := formFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("formFixer.Fix = %v, %v", changed, err)
	}
	if acroForm.Entries.Get("NeedAppearances") != pdf.PDFBoolean(false) {
		t.Error("NeedAppearances not cleared")
	}
	if _, ok := acroForm.Entries.Lookup("XFA"); ok {
		t.Error("XFA not removed")
	}
	if _, ok := widget.Entries.Lookup("A"); ok {
		t.Error("widget /A action not removed")
	}
	if _, ok := widget.Entries.Lookup("AA"); ok {
		t.Error("widget /AA additional actions not removed")
	}
}

func TestPostScriptXObjectFixer(t *testing.T) {
	xobj := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype":  pdf.PDFName{Value: "Form"},
		"PS":       pdf.PDFInteger(1),
		"Subtype2": pdf.PDFName{Value: "PS"},
	})}
	trailer := trailerWith("XObj", xobj)
	changed, err := postScriptXObjectFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("postScriptXObjectFixer.Fix = %v, %v", changed, err)
	}
	if _, ok := xobj.Entries.Lookup("PS"); ok {
		t.Error("PS not removed")
	}
	if _, ok := xobj.Entries.Lookup("Subtype2"); ok {
		t.Error("Subtype2 PS not removed")
	}
}

// TestPostScriptXObjectFixerNeutersPSSubtype: a Subtype /PS XObject becomes an
// empty Form XObject (viewers never render PS passthrough, so nothing visual
// is lost) with the stream filter metadata cleared.
func TestPostScriptXObjectFixerNeutersPSSubtype(t *testing.T) {
	xobj := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "XObject"},
		"Subtype": pdf.PDFName{Value: "PS"},
		"Filter":  pdf.PDFName{Value: "FlateDecode"},
		"Level1":  pdf.PDFRef{ObjNum: 9},
	}), HasStream: true, RawStream: []byte("compressed ps")}
	trailer := trailerWith("XObj", xobj)
	changed, err := postScriptXObjectFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("postScriptXObjectFixer.Fix = %v, %v", changed, err)
	}
	if xobj.Entries.Get("Subtype") != (pdf.PDFName{Value: "Form"}) {
		t.Errorf("Subtype = %v, want Form", xobj.Entries.Get("Subtype"))
	}
	if _, ok := xobj.Entries.Get("BBox").(pdf.PDFArray); !ok {
		t.Error("BBox not synthesized")
	}
	for _, k := range []string{"Filter", "DecodeParms", "DP", "Level1"} {
		if _, ok := xobj.Entries.Lookup(k); ok {
			t.Errorf("%s not removed", k)
		}
	}
}

func TestOptionalContentFixer(t *testing.T) {
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":         pdf.PDFName{Value: "Catalog"},
		"OCProperties": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}
	changed, err := optionalContentFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("optionalContentFixer.Fix = %v, %v", changed, err)
	}
	if _, ok := root.Entries.Lookup("OCProperties"); ok {
		t.Error("OCProperties not removed")
	}
}

// TestOptionalContentFixerNoOp covers the two no-op short-circuits: no Root,
// and a Root with no /OCProperties to remove.
func TestOptionalContentFixerNoOp(t *testing.T) {
	noRoot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	if changed, err := (optionalContentFixer{}).Fix(&noRoot, nil); err != nil || changed {
		t.Errorf("Fix(no Root) = %v, %v, want (false, nil)", changed, err)
	}

	noOCProps := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Catalog"}})},
	})}
	if changed, err := (optionalContentFixer{}).Fix(&noOCProps, nil); err != nil || changed {
		t.Errorf("Fix(no OCProperties) = %v, %v, want (false, nil)", changed, err)
	}
}

// TestViewerPrefFixerNoOpMissingEntries covers the no-Root and
// no-ViewerPreferences short-circuits (distinct from TestViewerPrefFixerNoOp,
// which covers ViewerPreferences present but clean).
func TestViewerPrefFixerNoOpMissingEntries(t *testing.T) {
	noRoot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	if changed, err := (viewerPrefFixer{}).Fix(&noRoot, nil); err != nil || changed {
		t.Errorf("Fix(no Root) = %v, %v, want (false, nil)", changed, err)
	}

	noVP := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Catalog"}})},
	})}
	if changed, err := (viewerPrefFixer{}).Fix(&noVP, nil); err != nil || changed {
		t.Errorf("Fix(no ViewerPreferences) = %v, %v, want (false, nil)", changed, err)
	}
}

// TestImageSampleDepthFixerRewritesSixteenBit covers 6.2.4: PDF/A-1 allows 1,
// 2, 4 or 8 bits per component, and real documents carry 16-bit images. The
// samples come back at 8 bits rather than the page being rasterized around
// them; an image mask, whose depth is a different rule's business, is left.
func TestImageSampleDepthFixerRewritesSixteenBit(t *testing.T) {
	// A 2x1 16-bit grayscale image: two big-endian samples per pixel.
	img := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype":          pdf.PDFName{Value: "Image"},
			"Width":            pdf.PDFInteger(2),
			"Height":           pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(16),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
		}),
		HasStream: true,
		RawStream: []byte{0x00, 0x00, 0xff, 0xff},
	}
	mask := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype":          pdf.PDFName{Value: "Image"},
			"Width":            pdf.PDFInteger(2),
			"Height":           pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(16),
			"ImageMask":        pdf.PDFBoolean(true),
		}),
		HasStream: true,
		RawStream: []byte{0x00, 0x00, 0xff, 0xff},
	}
	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Im0", img)
	xobjects.Entries.Set("Im1", mask)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("XObject", xobjects)

	changed, err := (imageSampleDepthFixer{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("Fix reported no change for a 16-bit image")
	}
	got, _ := xobjects.Entries.Get("Im0").(pdf.PDFDict)
	if bpc, _ := got.Entries.Get("BitsPerComponent").(pdf.PDFInteger); bpc != 8 {
		t.Errorf("BitsPerComponent = %v, want 8", got.Entries.Get("BitsPerComponent"))
	}
	if _, err := DecodeImageRGBA(got, pdf.PDFDict{}); err != nil {
		t.Errorf("rewritten image does not decode: %v", err)
	}
	gotMask, _ := xobjects.Entries.Get("Im1").(pdf.PDFDict)
	if bpc, _ := gotMask.Entries.Get("BitsPerComponent").(pdf.PDFInteger); bpc != 16 {
		t.Errorf("image mask BitsPerComponent = %v, want it left to 6.2.4-6", bpc)
	}
	if changed, _ := (imageSampleDepthFixer{}).Fix(&trailer, nil); changed {
		t.Error("second pass reported a change, want idempotent")
	}
}
