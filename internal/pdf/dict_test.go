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

// TestDictRef verifies that a dictionary's reference lives in its shared body:
// every alias sees it, it is not an entry, Clone and Assign carry it, and
// String still prints it.
func TestDictRef(t *testing.T) {
	if _, ok := (PDFDict{}).Ref(); ok {
		t.Error("the zero PDFDict reports a reference")
	}
	d := PDFDict{Entries: DictOf(map[string]PDFValue{"B": PDFInteger(2)})}
	alias := d
	if _, ok := d.Ref(); ok {
		t.Error("a direct dict reports a reference")
	}
	d.SetRef(PDFRef{ObjNum: 7})
	if got, ok := alias.Ref(); !ok || got != (PDFRef{ObjNum: 7}) {
		t.Errorf("alias.Ref() = %v, %v; want 7 0 R, true", got, ok)
	}
	if d.Entries.Len() != 1 {
		t.Errorf("Len() = %d, want 1: the reference is not an entry", d.Entries.Len())
	}
	if got, want := d.Entries.String(), "map[ ref:{7 0} B:2]"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	c := PDFDict{Entries: d.Entries.Clone()}
	if got, _ := c.Ref(); got != (PDFRef{ObjNum: 7}) {
		t.Errorf("clone's ref = %v, want 7 0 R", got)
	}
	direct := PDFDict{Entries: NewDict()}
	direct.Entries.Assign(d.Entries)
	if got, _ := direct.Ref(); got != (PDFRef{ObjNum: 7}) {
		t.Errorf("assigned ref = %v, want 7 0 R", got)
	}
	d.Entries.Assign(NewDict())
	if _, ok := alias.Ref(); ok {
		t.Error("assigning a direct body left the reference behind")
	}

	defer func() {
		if recover() == nil {
			t.Error("SetRef on a nil Dict did not panic")
		}
	}()
	PDFDict{}.SetRef(PDFRef{ObjNum: 1})
}
