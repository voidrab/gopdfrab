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
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}
	kids := make(pdf.PDFArray, 0, n)
	for i := 0; i < n; i++ {
		child := pdf.NewPDFDict()
		child.Entries["_ref"] = pdf.PDFRef{ObjNum: 100 + i}
		kids = append(kids, child)
	}
	root.Entries["Kids"] = kids
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
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
	byNum[anyNum].Entries["_ref"] = pdf.PDFRef{ObjNum: 777}
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
