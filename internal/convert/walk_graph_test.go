package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestWalkGraph pins the shared traversal's contract: each dict is visited
// once even through a cycle, a false visit prunes the subtree, synthetic keys
// are never descended, and an edit's replacement is written back and walked.
func TestWalkGraph(t *testing.T) {
	leaf := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Leaf": pdf.PDFBoolean(true)})}
	pruned := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Stop": pdf.PDFBoolean(true), "Under": leaf})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"_ref": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Hidden": pdf.PDFBoolean(true)})},
		"P":    pruned,
		"N":    pdf.PDFInteger(1),
		"A":    pdf.PDFArray{pdf.PDFInteger(2)},
	})}
	root.Entries.Set("Self", root) // cycle

	var visits int
	walkGraph(root, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		visits++
		if d.Entries.Get("Hidden") != nil {
			t.Error("walk descended into a synthetic key")
		}
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
