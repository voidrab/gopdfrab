package convert

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
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

// TestPruneUnusedResourceEntriesIsDeterministic: an oversized resource dict
// normally has more prunable entries than excess to shed, so the choice must
// not come from map order -- two identical documents would otherwise convert to
// different bytes.
func TestPruneUnusedResourceEntriesIsDeterministic(t *testing.T) {
	build := func() pdf.PDFDict {
		d := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
		for i := range maxDictEntries + 10 {
			d.Entries.Set(fmt.Sprintf("C%04d", i), pdf.PDFInteger(i))
		}
		return d
	}
	used := map[string]bool{"C0000": true, "C0001": true}

	var first []string
	for run := range 3 {
		sub := build()
		if !pruneUnusedResourceEntries(sub, used, maxDictEntries) {
			t.Fatal("oversized dict was not pruned")
		}
		if got := dictRealEntryCount(sub); got != maxDictEntries {
			t.Errorf("pruned to %d entries, want %d", got, maxDictEntries)
		}
		for k := range used {
			if _, ok := sub.Entries.Lookup(k); !ok {
				t.Errorf("pruned the used entry %q", k)
			}
		}
		keys := make([]string, 0, sub.Entries.Len())
		for k := range sub.Entries.All() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if run == 0 {
			first = keys
			continue
		}
		if !slices.Equal(keys, first) {
			t.Fatalf("run %d pruned a different set: %v vs %v", run, keys[:5], first[:5])
		}
	}

	// Nothing to shed: a dict at the limit is left alone.
	small := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"A": pdf.PDFInteger(1)})}
	if pruneUnusedResourceEntries(small, nil, maxDictEntries) {
		t.Error("a dict under the limit was pruned")
	}
}

func TestTruncateOverlongName(t *testing.T) {
	if _, ok := truncateOverlongName(pdf.PDFName{Value: "short"}, maxNameLength); ok {
		t.Error("short name should not be truncated")
	}
	if _, ok := truncateOverlongName(pdf.PDFInteger(1), maxNameLength); ok {
		t.Error("non-name should not be truncated")
	}
	long := strings.Repeat("a", 200)
	v, ok := truncateOverlongName(pdf.PDFName{Value: long}, maxNameLength)
	if !ok || len(v.(pdf.PDFName).Value) != maxNameLength {
		t.Errorf("overlong name truncated to %d, want %d", len(v.(pdf.PDFName).Value), maxNameLength)
	}
}

func TestShortenDictKey(t *testing.T) {
	long := strings.Repeat("k", 200)
	d := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	first := shortenDictKey(d, long, maxNameLength)
	if len(first) > maxNameLength {
		t.Errorf("shortened key len %d exceeds max", len(first))
	}
	// Force a collision so the numeric-suffix path runs.
	d.Entries.Set(first, pdf.PDFInteger(1))
	second := shortenDictKey(d, long, maxNameLength)
	if second == first {
		t.Error("colliding key should get a unique suffix")
	}
}

func TestHasOversizedArray(t *testing.T) {
	big := make(pdf.PDFArray, maxPDFArrayElements+1)
	if !hasOversizedArray(big, map[uintptr]bool{}, maxPDFArrayElements) {
		t.Error("oversized array not detected")
	}
	nested := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"K": big})}
	if !hasOversizedArray(nested, map[uintptr]bool{}, maxPDFArrayElements) {
		t.Error("oversized array nested in a dict not detected")
	}
	small := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2)}
	if hasOversizedArray(small, map[uintptr]bool{}, maxPDFArrayElements) {
		t.Error("small array wrongly flagged")
	}
}

// TestDropOversizedStructure covers the drop-when-oversized path (removing
// StructTreeRoot/MarkInfo and every StructParent(s) link) and the two no-op
// short-circuits: no Root, and a structure tree within the array limit.
func TestDropOversizedStructure(t *testing.T) {
	big := make(pdf.PDFArray, maxPDFArrayElements+1)
	st := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"K": big})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"}, "StructParents": pdf.PDFInteger(0),
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"StructTreeRoot": st,
		"MarkInfo":       pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Marked": pdf.PDFBoolean(true)})},
		"Pages":          pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}})},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}

	if !dropOversizedStructure(&trailer, maxPDFArrayElements) {
		t.Fatalf("dropOversizedStructure = false, want true (StructTreeRoot holds an oversized array)")
	}
	gotRoot := trailer.Entries.Get("Root").(pdf.PDFDict)
	if gotRoot.Entries.Get("StructTreeRoot") != nil {
		t.Error("StructTreeRoot not removed")
	}
	if gotRoot.Entries.Get("MarkInfo") != nil {
		t.Error("MarkInfo not removed")
	}
	gotPage := gotRoot.Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
	if gotPage.Entries.Get("StructParents") != nil {
		t.Error("StructParents not removed from page")
	}
}

func TestDropOversizedStructureNoOp(t *testing.T) {
	noRoot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	if dropOversizedStructure(&noRoot, maxPDFArrayElements) {
		t.Error("dropOversizedStructure with no Root = true, want false")
	}

	small := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"StructTreeRoot": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"K": pdf.PDFArray{pdf.PDFInteger(1)}})},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": small})}
	if dropOversizedStructure(&trailer, maxPDFArrayElements) {
		t.Error("dropOversizedStructure with a within-limit structure tree = true, want false")
	}
}

func TestCountPageLeaves(t *testing.T) {
	items := pdf.PDFArray{
		pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Page"}})},
		pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Pages"}, "Count": pdf.PDFInteger(4),
		})},
		pdf.PDFInteger(9), // non-dict, ignored
	}
	if got := countPageLeaves(items); got != 5 {
		t.Errorf("countPageLeaves = %d, want 5 (1 leaf + 4 nested)", got)
	}
}

func TestClampCMapCIDs(t *testing.T) {
	over := []byte("1 begincidchar\n<0041> 70000\nendcidchar\n")
	out, ok := clampCMapCIDs(over, maxCMapCID)
	if !ok || strings.Contains(string(out), "70000") || !strings.Contains(string(out), "65535") {
		t.Errorf("clampCMapCIDs did not clamp: %q, ok=%v", out, ok)
	}
	if _, ok := clampCMapCIDs([]byte("1 begincidchar\n<0041> 100\nendcidchar\n"), maxCMapCID); ok {
		t.Error("in-range CID should not be clamped")
	}
}

// TestComputeResourceUsage drives computeResourceUsage, collectResourceUsage*,
// and markResourceUsed over a page whose content references every resource
// category plus a nested Form XObject with its own resources.
func TestComputeResourceUsage(t *testing.T) {
	form := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype":   pdf.PDFName{Value: "Form"},
			"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F2": pdf.PDFInteger(1)})}})},
		}),
		HasStream: true, RawStream: []byte("/F2 12 Tf"),
	}
	fontSub := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": pdf.PDFInteger(1)})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im1": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Image"}})}, "Fm1": form})},
		"Font":       fontSub,
		"ExtGState":  pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"GS1": pdf.PDFInteger(1)})},
		"ColorSpace": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"CS1": pdf.PDFInteger(1)})},
		"Pattern":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"P1": pdf.PDFInteger(1)})},
		"Shading":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Sh1": pdf.PDFInteger(1)})},
		"Properties": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"MC1": pdf.PDFInteger(1)})},
	})}
	content := "/Im1 Do /F1 12 Tf /GS1 gs /CS1 cs /P1 scn /Sh1 sh /Tag /MC1 BDC /Fm1 Do"
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents":  pdf.PDFArray{pdf.PDFDict{HasStream: true, RawStream: []byte(content)}},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}})},
		})},
	})}

	used := computeResourceUsage(trailer)
	if set := used[pdf.ValuePointer(fontSub.Entries)]; set == nil || !set["F1"] {
		t.Errorf("Font F1 not marked used: %v", used[pdf.ValuePointer(fontSub.Entries)])
	}
	formFont := form.Entries.Get("Resources").(pdf.PDFDict).Entries.Get("Font").(pdf.PDFDict)
	if set := used[pdf.ValuePointer(formFont.Entries)]; set == nil || !set["F2"] {
		t.Error("nested Form Font F2 not marked used")
	}
}

// TestNameTooLongFixerRenamesResourceReferencesAcrossContentShapes drives
// renameResourceReferences (via nameTooLongFixer.Fix), covering
// walkResourceAwareContent/rewritePageContents/rewriteResourceAwareStream's
// three dispatch shapes at once: an array-form Page /Contents and a
// recursed Form XObject sharing the same overlong-keyed /Font resource dict.
func TestNameTooLongFixerRenamesResourceReferencesAcrossContentShapes(t *testing.T) {
	longKey := strings.Repeat("k", 200)
	fontsDict := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		longKey: pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}})},
	})}

	form := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype":   pdf.PDFName{Value: "Form"},
			"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": fontsDict})},
		}),
		HasStream: true, RawStream: []byte("/" + longKey + " 12 Tf"),
	}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Font":    fontsDict,
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm1": form})},
	})}
	streamA := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: []byte("/" + longKey + " 10 Tf")}
	streamB := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: []byte("/Fm1 Do")}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents":  pdf.PDFArray{streamA, streamB},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}})},
		})},
	})}

	changed, err := nameTooLongFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("expected the overlong resource key to trigger a change")
	}

	var newKey string
	for k := range fontsDict.Entries.All() {
		newKey = k
	}
	if newKey == longKey || len(newKey) > maxNameLength {
		t.Fatalf("resource key not shortened: %q", newKey)
	}

	gotPage := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
	contents := gotPage.Entries.Get("Contents").(pdf.PDFArray)

	decodedA, err := pdf.DecodeStream(contents[0].(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream(Contents[0]): %v", err)
	}
	if strings.Contains(string(decodedA), longKey) || !strings.Contains(string(decodedA), newKey) {
		t.Errorf("Contents[0] (array form) = %q, want the Tf operand renamed to %q", decodedA, newKey)
	}

	decodedB, err := pdf.DecodeStream(contents[1].(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream(Contents[1]): %v", err)
	}
	gotForm := gotPage.Entries.Get("Resources").(pdf.PDFDict).Entries.Get("XObject").(pdf.PDFDict).Entries.Get("Fm1").(pdf.PDFDict)
	decodedForm, err := pdf.DecodeStream(gotForm)
	if err != nil {
		t.Fatalf("DecodeStream(Form): %v", err)
	}
	if strings.Contains(string(decodedForm), longKey) || !strings.Contains(string(decodedForm), newKey) {
		t.Errorf("recursed Form stream = %q (from %q), want the Tf operand renamed to %q", decodedForm, decodedB, newKey)
	}
}

// TestClampCMapStreamDictSkipsNonCMap covers the type/stream guard: a dict
// that isn't a stream-bearing /Type /CMap must be left untouched.
func TestClampCMapStreamDictSkipsNonCMap(t *testing.T) {
	notCMap := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}}), HasStream: true, RawStream: []byte("1 begincidchar\n<0041> 70000\nendcidchar\n")}
	if _, ok := clampCMapStreamDict(notCMap, maxCMapCID); ok {
		t.Error("clampCMapStreamDict on a non-CMap dict = true, want false")
	}
	noStream := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "CMap"}})}
	if _, ok := clampCMapStreamDict(noStream, maxCMapCID); ok {
		t.Error("clampCMapStreamDict on a streamless CMap dict = true, want false")
	}
}

// TestCMapCIDClampFixer drives the whole-graph CMap CID clamp fixer over a
// CMap stream containing an out-of-range CID.
func TestCMapCIDClampFixer(t *testing.T) {
	cmap := pdf.PDFDict{
		Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "CMap"}}),
		HasStream: true,
		RawStream: []byte("1 begincidchar\n<0041> 70000\nendcidchar\n"),
	}
	trailer := trailerWith("Enc", cmap)
	changed, err := cmapCIDClampFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("cmapCIDClampFixer.Fix = %v, %v; want changed", changed, err)
	}
	// The clamped stream is re-flated; decode it back and confirm the CID was capped.
	fixed := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Enc").(pdf.PDFDict)
	data, derr := pdf.DecodeStream(fixed)
	if derr != nil {
		t.Fatalf("DecodeStream: %v", derr)
	}
	if strings.Contains(string(data), "70000") || !strings.Contains(string(data), "65535") {
		t.Errorf("CID not clamped in output: %q", data)
	}
}

// TestResourceAwareRewritePreservesUntouchedOperators verifies that
// rewriteResourceAwareStream leaves operators the rename does not touch with
// their own operands, as TestDeviceNRewritePreservesUntouchedOperators does for
// the deviceN rewriter. Both rewriters must copy operands out of the content
// scanner's reused operand stack; the aliasing only shows on a stream with more
// than one operator that has operands.
func TestResourceAwareRewritePreservesUntouchedOperators(t *testing.T) {
	longKey := strings.Repeat("k", 200)
	fontsDict := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		longKey: pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}})},
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": fontsDict})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents": pdf.PDFDict{
			Entries:   pdf.DictOf(map[string]pdf.PDFValue{}),
			HasStream: true,
			RawStream: []byte("1 0 0 RG 5 w BT /" + longKey + " 12 Tf (hi) Tj ET 10 20 m 30 40 l S"),
		},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}})},
		})},
	})}

	changed, err := nameTooLongFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("expected the overlong resource key to trigger a change")
	}

	var newKey string
	for k := range fontsDict.Entries.All() {
		newKey = k
	}
	gotPage := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
	decoded, err := pdf.DecodeStream(gotPage.Entries.Get("Contents").(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	got := string(decoded)

	for _, want := range []string{"1 0 0 RG", "5 w", "(hi) Tj", "10 20 m", "30 40 l", "/" + newKey + " 12 Tf"} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten stream lost or corrupted %q\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, longKey) {
		t.Errorf("overlong key survived\ngot: %s", got)
	}
}

func TestHasOversizedArrayCycleAndElement(t *testing.T) {
	cyc := pdf.PDFArray{nil}
	cyc[0] = cyc
	if hasOversizedArray(cyc, map[uintptr]bool{}, maxPDFArrayElements) {
		t.Error("a small self-containing array was flagged")
	}
	big := make(pdf.PDFArray, maxPDFArrayElements+1)
	if !hasOversizedArray(pdf.PDFArray{pdf.PDFInteger(1), big}, map[uintptr]bool{}, maxPDFArrayElements) {
		t.Error("an oversized array nested in a small array was not found")
	}
}

// contentPage builds a trailer with one page whose single content stream is
// content, using the given resources.
func contentPage(content string, resources pdf.PDFDict) pdf.PDFDict {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents":  pdf.PDFDict{Entries: pdf.DictOf(nil), HasStream: true, RawStream: []byte(content)},
	})}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}})},
		})},
	})}
}

// degenerateFormResources is a Resources dict whose XObject Fm1 is a form with
// no Resources of its own (so it inherits the page's), and a Font category.
func degenerateFormResources() (resources, fonts, form pdf.PDFDict) {
	form = pdf.PDFDict{
		Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}}),
		HasStream: true, RawStream: []byte("/F9 12 Tf"),
	}
	fonts = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F9": pdf.PDFInteger(1)})}
	resources = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm1": form})},
		"Font":    fonts,
	})}
	return resources, fonts, form
}

// degenerateContent mixes malformed and repeated XObject invocations (Do) and
// graphics state selections (gs) around the well-formed form invocation: an
// invocation without an operand, a non-name operand, an unknown XObject, a
// second invocation of the same form, and a graphics state naming a category
// the page lacks.
const degenerateContent = "Do 1 Do /Missing Do /Fm1 Do /Fm1 Do /GS1 gs"

func TestComputeResourceUsageDegenerateOperators(t *testing.T) {
	resources, fonts, _ := degenerateFormResources()
	used := computeResourceUsage(contentPage(degenerateContent, resources))
	if !used[pdf.ValuePointer(fonts.Entries)]["F9"] {
		t.Error("a form without its own Resources must record its usage against the page's")
	}
	xobjects := resources.Entries.Get("XObject").(pdf.PDFDict)
	if got := used[pdf.ValuePointer(xobjects.Entries)]; !got["Fm1"] || !got["Missing"] || len(got) != 2 {
		t.Errorf("XObject usage = %v, want exactly Fm1 and Missing", got)
	}
	if len(used) != 2 {
		t.Errorf("usage recorded for %d categories, want 2 (the absent ExtGState records nothing)", len(used))
	}

	// A page whose resources lack an XObject category: an invocation marks
	// nothing.
	bare := pdf.PDFDict{Entries: pdf.DictOf(nil)}
	if used := computeResourceUsage(contentPage("/Q Do", bare)); len(used) != 0 {
		t.Errorf("usage = %v, want empty", used)
	}
}

func TestWalkResourceAwareContentDegenerateOperators(t *testing.T) {
	resources, fonts, _ := degenerateFormResources()
	trailer := contentPage(degenerateContent, resources)

	var ops []string
	var tfResources pdf.PDFDict
	changed := walkResourceAwareContent(&trailer, func(op string, _ []pdf.PDFValue, res pdf.PDFDict, _ *bool) {
		ops = append(ops, op)
		if op == "Tf" {
			tfResources = res
		}
	})
	if changed {
		t.Error("changed = true although the rewriter changed nothing")
	}
	if want := "Do Do Do Do Tf Do gs"; strings.Join(ops, " ") != want {
		t.Errorf("operators seen = %q, want %q (the form is entered once)", strings.Join(ops, " "), want)
	}
	if pdf.ValuePointer(tfResources.Entries.Get("Font").(pdf.PDFDict).Entries) != pdf.ValuePointer(fonts.Entries) {
		t.Error("the form's operators did not see the page's resources")
	}

	bare := contentPage("/Q Do", pdf.PDFDict{Entries: pdf.DictOf(nil)})
	if walkResourceAwareContent(&bare, func(string, []pdf.PDFValue, pdf.PDFDict, *bool) {}) {
		t.Error("changed = true for a page without XObject resources")
	}
}

func TestRewritePageContentsSkipsUnusableStreams(t *testing.T) {
	good := pdf.PDFDict{Entries: pdf.DictOf(nil), HasStream: true, RawStream: []byte("/F1 12 Tf")}
	corrupt := pdf.PDFDict{
		Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}),
		HasStream: true, RawStream: []byte("not a zlib stream"),
	}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Contents": pdf.PDFArray{
			pdf.PDFInteger(7),                     // not a dict
			pdf.PDFDict{Entries: pdf.DictOf(nil)}, // dict without a stream
			corrupt,
			good,
		},
	})}
	var changed bool
	seen := 0
	rewritePageContents(page, pdf.PDFDict{}, func(op string, operands []pdf.PDFValue, _ pdf.PDFDict, c *bool) {
		seen++
		operands[0] = pdf.PDFName{Value: "F2"}
		*c = true
	}, map[uintptr]bool{}, &changed)

	if !changed || seen != 1 {
		t.Errorf("changed=%v, rewriter calls=%d; want true and exactly 1 (only the good stream is readable)", changed, seen)
	}
	arr := page.Entries.Get("Contents").(pdf.PDFArray)
	data, err := pdf.DecodeStream(arr[3].(pdf.PDFDict))
	if err != nil || !strings.Contains(string(data), "/F2") {
		t.Errorf("good stream = %q, %v; want the rewritten operand", data, err)
	}
	if string(arr[2].(pdf.PDFDict).RawStream) != "not a zlib stream" {
		t.Error("the undecodable stream was modified")
	}
}

func TestRenameResourceReferencesSkipsMissingCategory(t *testing.T) {
	trailer := contentPage("/F1 12 Tf", pdf.PDFDict{Entries: pdf.DictOf(nil)})
	renameResourceReferences(&trailer, map[uintptr]map[string]string{})
	page := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
	if got := string(page.Entries.Get("Contents").(pdf.PDFDict).RawStream); got != "/F1 12 Tf" {
		t.Errorf("content = %q, want it untouched", got)
	}
}

func TestNameTooLongFixerTruncatesNameValues(t *testing.T) {
	long := strings.Repeat("n", maxNameLength+50)
	trailer := trailerWith("Mode", pdf.PDFName{Value: long})
	changed, err := nameTooLongFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want changed", changed, err)
	}
	got := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Mode").(pdf.PDFName).Value
	if got != long[:maxNameLength] {
		t.Errorf("name = %d bytes, want the first %d", len(got), maxNameLength)
	}
}

func TestCMapCIDClampFixTargetedSkipsUnclampable(t *testing.T) {
	mk := func(num int, typ string, raw string, filter bool) pdf.PDFDict {
		d := pdf.PDFDict{
			Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Type": pdf.PDFName{Value: typ}, pdf.RefKey: pdf.PDFRef{ObjNum: num},
			}),
			HasStream: true, RawStream: []byte(raw),
		}
		if filter {
			d.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
		}
		return d
	}
	const inRange = "1 begincidchar\n<0041> 100\nendcidchar\n"
	skipped := []pdf.PDFDict{
		mk(20, "Font", "1 begincidchar\n<0041> 70000\nendcidchar\n", false), // not a CMap
		mk(21, "CMap", "garbage", true),                                     // undecodable
		mk(22, "CMap", inRange, false),                                      // nothing to clamp
	}
	root := pdf.NewPDFDict()
	root.Entries.Set(pdf.RefKey, pdf.PDFRef{ObjNum: 1})
	for i, d := range skipped {
		root.Entries.Set(fmt.Sprintf("K%d", i), d)
	}
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)
	pass := &fixPass{trailer: &trailer, objs: writer.NumberObjects(trailer, 0)}
	// Numbering assigns the real object numbers, so read the refs afterwards.
	var issues []pdf.PDFError
	for _, d := range skipped {
		ref := d.Entries.Get(pdf.RefKey).(pdf.PDFRef)
		issues = append(issues, pdf.NewError(pdf.Checks.Structure.CMapCIDOutOfRange, nil, 0, &ref))
	}

	changed, handled, err := cmapCIDClampFixer{spec: a1bSpec}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Errorf("fixTargeted = changed %v, handled %v, %v; want unchanged but handled", changed, handled, err)
	}

	// An issue with no object ref cannot be targeted: the full walk must run.
	noRef := pdf.NewError(pdf.Checks.Structure.CMapCIDOutOfRange, nil, 0, nil)
	if _, handled, _ := (cmapCIDClampFixer{spec: a1bSpec}).fixTargeted(pass, []pdf.PDFError{noRef}); handled {
		t.Error("an issue without an object ref was reported as handled")
	}
}

func TestClampCMapCIDsClampsEveryOutOfRangeCID(t *testing.T) {
	got, ok := clampCMapCIDs([]byte("2 begincidchar\n<41> 70000\n<42> 80000\nendcidchar\n"), maxCMapCID)
	if !ok {
		t.Fatal("nothing clamped")
	}
	if want := "2 begincidchar\n<41> 65535\n<42> 65535\nendcidchar\n"; string(got) != want {
		t.Errorf("clamped = %q, want %q", got, want)
	}
}
