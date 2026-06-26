package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestViewerPrefFixerRemovesPost14Keys confirms that viewerPrefFixer deletes
// PrintScaling and other post-1.4 ViewerPreferences entries.
func TestViewerPrefFixerRemovesPost14Keys(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries["PrintScaling"] = pdf.PDFName{Value: "None"}
	vp.Entries["DisplayDocTitle"] = pdf.PDFBoolean(true) // 1.4 key, must stay

	root := pdf.NewPDFDict()
	root.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	root.Entries["ViewerPreferences"] = vp

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	fixer := viewerPrefFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if vp.Entries["PrintScaling"] != nil {
		t.Errorf("/PrintScaling still present after viewerPrefFixer")
	}
	if vp.Entries["DisplayDocTitle"] == nil {
		t.Errorf("/DisplayDocTitle was removed but should be kept")
	}
}

// TestViewerPrefFixerNoOp confirms viewerPrefFixer reports no change when no
// post-1.4 keys are present.
func TestViewerPrefFixerNoOp(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries["DisplayDocTitle"] = pdf.PDFBoolean(true)

	root := pdf.NewPDFDict()
	root.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	root.Entries["ViewerPreferences"] = vp

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

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
	action.Entries["S"] = pdf.PDFName{Value: "JavaScript"}
	action.Entries["JS"] = pdf.PDFString{Value: "app.alert('hi')"}

	// Name tree dict containing the action.
	nameTree := pdf.NewPDFDict()
	nameTree.Entries["Names"] = pdf.PDFArray{pdf.PDFString{Value: "open"}, action}

	// Catalog /Names dict with /JavaScript key.
	names := pdf.NewPDFDict()
	names.Entries["JavaScript"] = nameTree

	root := pdf.NewPDFDict()
	root.Entries["Names"] = names

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	fixer := actionFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	// /Names/JavaScript must be gone.
	namesAfter := root.Entries["Names"].(pdf.PDFDict)
	if namesAfter.Entries["JavaScript"] != nil {
		t.Errorf("/Names/JavaScript still present after actionFixer")
	}

	// The leaf action dict must also be cleared.
	if action.Entries["S"] != nil || action.Entries["JS"] != nil {
		t.Errorf("leaf action dict not cleared: %v", action.Entries)
	}
}
