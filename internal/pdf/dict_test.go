package pdf

import "testing"

// TestDictCloneAndAssign pins the copy-on-write pair: a clone edits apart
// from its source, and Assign publishes the edit to every value sharing the
// source's body without leaving it sharing the clone's.
func TestDictCloneAndAssign(t *testing.T) {
	big := map[string]PDFValue{}
	for i := range promoteThreshold + 2 { // past the threshold, so idx is cloned too
		big[string(rune('a'+i))] = PDFInteger(i)
	}
	orig := DictOf(big)
	alias := PDFDict{Entries: orig} // another reference to the same object

	c := orig.Clone()
	c.Set("z", PDFInteger(99))
	c.Del("a")
	if orig.Get("z") != nil || orig.Get("a") != PDFInteger(0) {
		t.Fatal("editing the clone changed the original")
	}

	orig.Assign(c)
	if alias.Entries.Get("z") != PDFInteger(99) || alias.Entries.Get("a") != nil {
		t.Error("Assign did not reach a value sharing the original's body")
	}
	c.Set("y", PDFInteger(7))
	if orig.Get("y") != nil {
		t.Error("Assign left the original sharing the clone's body")
	}
}
