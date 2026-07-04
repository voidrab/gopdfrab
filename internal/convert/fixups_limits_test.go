package convert

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestResourceOperatorTarget(t *testing.T) {
	cases := map[string]string{
		"Do": "XObject", "Tf": "Font", "gs": "ExtGState", "cs": "ColorSpace",
		"CS": "ColorSpace", "scn": "Pattern", "SCN": "Pattern", "sh": "Shading",
		"BDC": "Properties", "DP": "Properties", "zz": "",
	}
	for op, want := range cases {
		if got, _ := resourceOperatorTarget(op); got != want {
			t.Errorf("resourceOperatorTarget(%q) = %q, want %q", op, got, want)
		}
	}
}

func TestTruncateOverlongName(t *testing.T) {
	if _, ok := truncateOverlongName(pdf.PDFName{Value: "short"}); ok {
		t.Error("short name should not be truncated")
	}
	if _, ok := truncateOverlongName(pdf.PDFInteger(1)); ok {
		t.Error("non-name should not be truncated")
	}
	long := strings.Repeat("a", 200)
	v, ok := truncateOverlongName(pdf.PDFName{Value: long})
	if !ok || len(v.(pdf.PDFName).Value) != maxNameLength {
		t.Errorf("overlong name truncated to %d, want %d", len(v.(pdf.PDFName).Value), maxNameLength)
	}
}

func TestShortenDictKey(t *testing.T) {
	long := strings.Repeat("k", 200)
	d := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	first := shortenDictKey(d, long)
	if len(first) > maxNameLength {
		t.Errorf("shortened key len %d exceeds max", len(first))
	}
	// Force a collision so the numeric-suffix path runs.
	d.Entries[first] = pdf.PDFInteger(1)
	second := shortenDictKey(d, long)
	if second == first {
		t.Error("colliding key should get a unique suffix")
	}
}

func TestHasOversizedArray(t *testing.T) {
	big := make(pdf.PDFArray, maxPDFArrayElements+1)
	if !hasOversizedArray(big, map[uintptr]bool{}) {
		t.Error("oversized array not detected")
	}
	nested := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"K": big}}
	if !hasOversizedArray(nested, map[uintptr]bool{}) {
		t.Error("oversized array nested in a dict not detected")
	}
	small := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2)}
	if hasOversizedArray(small, map[uintptr]bool{}) {
		t.Error("small array wrongly flagged")
	}
}

func TestCountPageLeaves(t *testing.T) {
	items := pdf.PDFArray{
		pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Page"}}},
		pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Pages"}, "Count": pdf.PDFInteger(4),
		}},
		pdf.PDFInteger(9), // non-dict, ignored
	}
	if got := countPageLeaves(items); got != 5 {
		t.Errorf("countPageLeaves = %d, want 5 (1 leaf + 4 nested)", got)
	}
}

func TestClampCMapCIDs(t *testing.T) {
	over := []byte("1 begincidchar\n<0041> 70000\nendcidchar\n")
	out, ok := clampCMapCIDs(over)
	if !ok || strings.Contains(string(out), "70000") || !strings.Contains(string(out), "65535") {
		t.Errorf("clampCMapCIDs did not clamp: %q, ok=%v", out, ok)
	}
	if _, ok := clampCMapCIDs([]byte("1 begincidchar\n<0041> 100\nendcidchar\n")); ok {
		t.Error("in-range CID should not be clamped")
	}
}

// TestComputeResourceUsage drives computeResourceUsage, collectResourceUsage*,
// and markResourceUsed over a page whose content references every resource
// category plus a nested Form XObject with its own resources.
func TestComputeResourceUsage(t *testing.T) {
	form := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Subtype":   pdf.PDFName{Value: "Form"},
			"Resources": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Font": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F2": pdf.PDFInteger(1)}}}},
		},
		HasStream: true, RawStream: []byte("/F2 12 Tf"),
	}
	fontSub := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F1": pdf.PDFInteger(1)}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"XObject":    pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Im1": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Image"}}}, "Fm1": form}},
		"Font":       fontSub,
		"ExtGState":  pdf.PDFDict{Entries: map[string]pdf.PDFValue{"GS1": pdf.PDFInteger(1)}},
		"ColorSpace": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"CS1": pdf.PDFInteger(1)}},
		"Pattern":    pdf.PDFDict{Entries: map[string]pdf.PDFValue{"P1": pdf.PDFInteger(1)}},
		"Shading":    pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Sh1": pdf.PDFInteger(1)}},
		"Properties": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"MC1": pdf.PDFInteger(1)}},
	}}
	content := "/Im1 Do /F1 12 Tf /GS1 gs /CS1 cs /P1 scn /Sh1 sh /Tag /MC1 BDC /Fm1 Do"
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents":  pdf.PDFArray{pdf.PDFDict{HasStream: true, RawStream: []byte(content)}},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}}},
		}},
	}}

	used := computeResourceUsage(trailer)
	if set := used[pdf.ValuePointer(fontSub.Entries)]; set == nil || !set["F1"] {
		t.Errorf("Font F1 not marked used: %v", used[pdf.ValuePointer(fontSub.Entries)])
	}
	formFont := form.Entries["Resources"].(pdf.PDFDict).Entries["Font"].(pdf.PDFDict)
	if set := used[pdf.ValuePointer(formFont.Entries)]; set == nil || !set["F2"] {
		t.Error("nested Form Font F2 not marked used")
	}
}

// TestCMapCIDClampFixer drives the whole-graph CMap CID clamp fixer over a
// CMap stream containing an out-of-range CID.
func TestCMapCIDClampFixer(t *testing.T) {
	cmap := pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "CMap"}},
		HasStream: true,
		RawStream: []byte("1 begincidchar\n<0041> 70000\nendcidchar\n"),
	}
	trailer := trailerWith("Enc", cmap)
	changed, err := cmapCIDClampFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("cmapCIDClampFixer.Fix = %v, %v; want changed", changed, err)
	}
	// The clamped stream is re-flated; decode it back and confirm the CID was capped.
	fixed := trailer.Entries["Root"].(pdf.PDFDict).Entries["Enc"].(pdf.PDFDict)
	data, derr := pdf.DecodeStream(fixed)
	if derr != nil {
		t.Fatalf("DecodeStream: %v", derr)
	}
	if strings.Contains(string(data), "70000") || !strings.Contains(string(data), "65535") {
		t.Errorf("CID not clamped in output: %q", data)
	}
}
