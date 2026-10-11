package pdf

import (
	"fmt"
	"testing"
)

// TestNilDictIsReadable pins that every read accessor tolerates a nil Dict
// (the zero value a failed parse returns) and that Set on it panics.
func TestNilDictIsReadable(t *testing.T) {
	var d Dict
	for range d.All() {
		t.Fatal("All yielded on a nil Dict")
	}
	if k := d.Keys(); k != nil {
		t.Errorf("Keys = %v, want nil", k)
	}
	if d.Len() != 0 || d.Get("A") != nil {
		t.Errorf("Len/Get on nil Dict not zero")
	}
	d.DeleteFunc(func(string, PDFValue) bool { t.Fatal("pred called"); return true })
	if got := d.String(); got != "map[]" {
		t.Errorf("String = %q, want map[]", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("Set on a nil Dict did not panic")
		}
	}()
	d.Set("A", PDFInteger(1))
}

// TestDeleteFuncKeepsOrderAndIndex covers both representations: survivors
// keep order and stay reachable through the index.
func TestDeleteFuncKeepsOrderAndIndex(t *testing.T) {
	for _, n := range []int{5, promoteThreshold + 8} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			d := NewDict()
			for i := range n {
				d.Set(fmt.Sprint("K", i), PDFInteger(i))
			}
			d.DeleteFunc(func(_ string, v PDFValue) bool { return v.(PDFInteger)%2 == 0 })
			var want []string
			for i := 1; i < n; i += 2 {
				want = append(want, fmt.Sprint("K", i))
				if d.Get(want[len(want)-1]) != PDFInteger(i) {
					t.Errorf("K%d lost", i)
				}
			}
			if fmt.Sprint(d.Keys()) != fmt.Sprint(want) {
				t.Errorf("keys = %v, want %v", d.Keys(), want)
			}
			if d.Get("K0") != nil {
				t.Error("K0 still present")
			}
			for _, e := range d.ent[len(d.ent):cap(d.ent)] {
				if e != (DictEntry{}) {
					t.Errorf("tail slot still holds %v", e)
				}
			}
		})
	}
}

func TestDictStringSortsKeys(t *testing.T) {
	d := NewDict()
	d.Set("B", PDFInteger(2))
	d.Set("A", PDFInteger(1))
	if got, want := d.String(), "map[A:1 B:2]"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}
