package convert

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// fixPassTrailer builds a trailer with n indirect child dicts under Root and
// numbers the graph, returning the pass and the children keyed by their
// assigned object numbers.
func fixPassTrailer(t *testing.T, n int) (*fixPass, map[int]pdf.PDFDict) {
	t.Helper()
	root := pdf.NewPDFDict()
	root.SetRef(pdf.PDFRef{ObjNum: 1})
	kids := make(pdf.PDFArray, 0, n)
	for i := 0; i < n; i++ {
		child := pdf.NewPDFDict()
		child.SetRef(pdf.PDFRef{ObjNum: 100 + i})
		kids = append(kids, child)
	}
	root.Entries.Set("Kids", kids)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)

	objs := writer.NumberObjects(trailer, 0)
	byNum := map[int]pdf.PDFDict{}
	for num, obj := range objs {
		d, ok := obj.(pdf.PDFDict)
		if !ok {
			t.Fatalf("numbered object %d is %T, want PDFDict", num, obj)
		}
		if pdf.ValuePointer(d.Entries) != pdf.ValuePointer(root.Entries) {
			byNum[num] = d
		}
	}
	if len(byNum) != n {
		t.Fatalf("numbered %d child dicts, want %d", len(byNum), n)
	}
	return &fixPass{trailer: &trailer, objs: objs}, byNum
}

func issueForRef(c pdf.Check, num int) pdf.PDFError {
	ref := pdf.PDFRef{ObjNum: num}
	return pdf.NewError(c, []error{errors.New("test issue")}, 0, &ref)
}

func TestFixPassDictForRef(t *testing.T) {
	pass, byNum := fixPassTrailer(t, 2)

	for num, want := range byNum {
		got, ok := pass.dictForRef(pdf.PDFRef{ObjNum: num})
		if !ok || pdf.ValuePointer(got.Entries) != pdf.ValuePointer(want.Entries) {
			t.Errorf("dictForRef(%d) ok=%v, want the numbered child dict", num, ok)
		}
	}

	if _, ok := pass.dictForRef(pdf.PDFRef{ObjNum: 999}); ok {
		t.Error("dictForRef(999) ok = true, want false for absent object")
	}

	// A dict renumbered after the index was built must be rejected.
	var anyNum int
	for num := range byNum {
		anyNum = num
		break
	}
	byNum[anyNum].SetRef(pdf.PDFRef{ObjNum: 777})
	if _, ok := pass.dictForRef(pdf.PDFRef{ObjNum: anyNum}); ok {
		t.Error("dictForRef ok = true for a dict whose _ref no longer matches")
	}

	var nilPass *fixPass
	if _, ok := nilPass.dictForRef(pdf.PDFRef{ObjNum: 1}); ok {
		t.Error("nil pass dictForRef ok = true, want false")
	}
}

func TestFixPassDictsForIssuesGateAndOrder(t *testing.T) {
	pass, byNum := fixPassTrailer(t, 3)
	check := pdf.Checks.Structure.NameTooLong

	nums := make([]int, 0, len(byNum))
	for num := range byNum {
		nums = append(nums, num)
	}
	// Issues arrive in descending-number order with a duplicate; targets must
	// come back deduped and ascending.
	issues := []pdf.PDFError{}
	max, mid, min := maxMidMin(nums)
	for _, num := range []int{max, mid, min, max} {
		issues = append(issues, issueForRef(check, num))
	}

	targets, ok := pass.dictsForIssues(issues)
	if !ok {
		t.Fatal("dictsForIssues ok = false, want true when every issue has a ref")
	}
	if len(targets) != 3 {
		t.Fatalf("got %d targets, want 3 (deduped)", len(targets))
	}
	for i, num := range []int{min, mid, max} {
		if pdf.ValuePointer(targets[i].Entries) != pdf.ValuePointer(byNum[num].Entries) {
			t.Errorf("targets[%d] is not object %d: order must be ascending by ObjNum", i, num)
		}
	}

	// One ref-less issue anywhere in the batch disables targeting entirely.
	noRef := pdf.NewError(check, []error{errors.New("no ref")}, 0, nil)
	if _, ok := pass.dictsForIssues(append(issues, noRef)); ok {
		t.Error("dictsForIssues ok = true with a ref-less issue, want full-walk fallback")
	}

	if _, ok := pass.dictsForIssues(nil); ok {
		t.Error("dictsForIssues(nil) ok = true, want false")
	}

	if _, ok := pass.dictsForIssues([]pdf.PDFError{issueForRef(check, 999)}); ok {
		t.Error("dictsForIssues ok = true for unresolvable ref, want false")
	}
}

// TestFixPassIsContentStream drives collectContentStreamPtrs' four dispatch
// shapes -- array-form Page /Contents, a tiling Pattern, a Form XObject, and
// a Type3 CharProcs glyph -- plus a plain (non-content) stream that must not
// be flagged.
func TestFixPassIsContentStream(t *testing.T) {
	pageStreamA := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{}), []byte("q Q"))
	pageStreamB := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{}), []byte("q Q"))
	pattern := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"PatternType": pdf.PDFInteger(1)}), []byte("q Q"))
	form := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}}), []byte("q Q"))
	glyph := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{}), []byte("q Q"))
	type3Font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype":   pdf.PDFName{Value: "Type3"},
		"CharProcs": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"g1": glyph})},
	})}
	notContent := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Image"}}), []byte("\x00\x01"))

	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":     pdf.PDFName{Value: "Page"},
		"Contents": pdf.PDFArray{pageStreamA, pageStreamB},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pattern": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"P1": pattern})},
			"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm1": form, "Im1": notContent})},
			"Font":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"T3": type3Font})},
		})},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}})},
		})},
	})}
	pass := &fixPass{trailer: &trailer}

	for _, want := range []struct {
		name string
		d    pdf.PDFDict
	}{
		{"page Contents[0]", pageStreamA}, {"page Contents[1]", pageStreamB},
		{"Pattern", pattern}, {"Form", form}, {"Type3 glyph", glyph},
	} {
		if !pass.isContentStream(want.d) {
			t.Errorf("isContentStream(%s) = false, want true", want.name)
		}
	}
	if pass.isContentStream(notContent) {
		t.Error("isContentStream(Image XObject) = true, want false")
	}
}

func maxMidMin(nums []int) (max, mid, min int) {
	max, mid, min = nums[0], nums[0], nums[0]
	for _, n := range nums {
		if n > max {
			max = n
		}
		if n < min {
			min = n
		}
	}
	for _, n := range nums {
		if n != max && n != min {
			mid = n
		}
	}
	return max, mid, min
}

func TestFixPassDictForIssueFileTrailer(t *testing.T) {
	pass, _ := fixPassTrailer(t, 1)
	detail := pdf.ObjModelDetail{TypeName: "FileTrailer", Key: "Root"}

	iss := pdf.NewError(pdf.Checks.Structure.StreamLZWFilter, nil, 0, nil).WithObjModelDetail(detail)
	got, ok := pass.dictForIssue(iss)
	if !ok || pdf.ValuePointer(got.Entries) != pdf.ValuePointer(pass.trailer.Entries) {
		t.Errorf("trailer issue resolved ok=%v, want the trailer itself", ok)
	}

	other := pdf.NewError(pdf.Checks.Structure.StreamLZWFilter, nil, 0, nil).
		WithObjModelDetail(pdf.ObjModelDetail{TypeName: "Catalog"})
	if _, ok := pass.dictForIssue(other); ok {
		t.Error("a ref-less issue about a non-trailer type resolved to a dict")
	}
}

func TestFixOwnedScalarsNestedAndCyclicArrays(t *testing.T) {
	inner := pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2)}
	cyc := pdf.PDFArray{pdf.PDFInteger(3), nil}
	cyc[1] = cyc
	child := pdf.NewPDFDict()
	child.Entries.Set("Skip", pdf.PDFInteger(9))
	d := pdf.NewPDFDict()
	d.Entries.Set("Nested", pdf.PDFArray{inner, child})
	d.Entries.Set("Cyc", cyc)

	calls := 0
	changed := fixOwnedScalars(d, func(v pdf.PDFValue) (pdf.PDFValue, bool) {
		calls++
		return pdf.PDFInteger(0), true
	})
	if !changed {
		t.Error("changed = false, want true")
	}
	if calls != 3 { // inner's two, cyc's one; the cycle is not re-entered, the child dict is not entered
		t.Errorf("fix called %d times, want 3", calls)
	}
	if inner[0] != pdf.PDFInteger(0) || inner[1] != pdf.PDFInteger(0) || cyc[0] != pdf.PDFInteger(0) {
		t.Errorf("scalars not rewritten: inner=%v cyc[0]=%v", inner, cyc[0])
	}
	if child.Entries.Get("Skip") != pdf.PDFInteger(9) {
		t.Error("a child dict's scalar was rewritten; it belongs to the child")
	}
}
