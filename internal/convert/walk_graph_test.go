package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestWalkGraph verifies that each dict is visited once even through a cycle,
// that a false visit prunes the subtree, and that an edit's replacement is
// written back and walked.
func TestWalkGraph(t *testing.T) {
	leaf := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Leaf": pdf.PDFBoolean(true)})}
	pruned := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Stop": pdf.PDFBoolean(true), "Under": leaf})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"P": pruned,
		"N": pdf.PDFInteger(1),
		"A": pdf.PDFArray{pdf.PDFInteger(2)},
	})}
	root.Entries.Set("Self", root) // cycle

	var visits int
	walkGraph(root, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		visits++
		if d.Entries.Get("Leaf") != nil {
			t.Error("walk descended below a dict whose visit returned false")
		}
		return d.Entries.Get("Stop") == nil
	}, func(v pdf.PDFValue) (pdf.PDFValue, bool) {
		if n, ok := v.(pdf.PDFInteger); ok {
			return n * 10, true
		}
		return nil, false
	})
	if visits != 2 {
		t.Errorf("visited %d dicts, want 2 (root once despite the cycle, and P)", visits)
	}
	if got := root.Entries.Get("N"); got != pdf.PDFInteger(10) {
		t.Errorf("dict slot = %v, want the edit's replacement 10", got)
	}
	if got := root.Entries.Get("A").(pdf.PDFArray)[0]; got != pdf.PDFInteger(20) {
		t.Errorf("array slot = %v, want the edit's replacement 20", got)
	}
}

// TestWalkGraphDepthCap pins that nesting past maxGraphWalkDepth is cut off
// rather than recursed into.
func TestWalkGraphDepthCap(t *testing.T) {
	var v pdf.PDFValue = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Bottom": pdf.PDFBoolean(true)})}
	for range maxGraphWalkDepth + 10 {
		v = pdf.PDFArray{v}
	}
	walkGraph(v, map[uintptr]bool{}, func(pdf.PDFDict) bool {
		t.Fatal("reached a dict nested beyond the depth cap")
		return true
	}, nil)
}

// TestWalkStreamDictsSharedStream verifies that a stream fixed through one
// slot reads as fixed through every slot holding the same object, those
// already passed and those still ahead, and that fix does not run again on a
// slot whose stream it has already changed.
func TestWalkStreamDictsSharedStream(t *testing.T) {
	s := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "X"}}), []byte("old"))
	arr := pdf.PDFArray{s, s, s}
	var calls int
	walkStreamDicts(arr, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		calls++
		if calls != 2 { // fire on the middle slot, so one slot lies behind it
			return false
		}
		d.SetStream([]byte("new"))
		return true
	})
	for i, v := range arr {
		if got := string(v.(pdf.PDFDict).Stream()); got != "new" {
			t.Errorf("slot %d holds %q, want the fixed bytes", i, got)
		}
	}
	if calls != 2 {
		t.Errorf("fix ran %d times, want 2 (not again on a stream already fixed)", calls)
	}
}

// withRef marks d as the indirect object ref and returns it, so a literal can
// be built in one expression.
func withRef(d pdf.PDFDict, ref pdf.PDFRef) pdf.PDFDict {
	d.SetRef(ref)
	return d
}
