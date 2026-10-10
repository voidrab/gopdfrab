package pdf

import (
	"fmt"
	"testing"
)

// TestDelKeepsIndexAndReleasesTail pins Del on both representations: every
// remaining key still resolves (the incremental index renumbering agrees with
// a full rebuild), order is kept, and the vacated slot no longer references
// the removed value.
func TestDelKeepsIndexAndReleasesTail(t *testing.T) {
	for _, n := range []int{5, promoteThreshold + 8} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			d := NewDict()
			for i := range n {
				d.Set(fmt.Sprint("K", i), PDFInteger(i))
			}
			d.Del("K0")
			d.Del(fmt.Sprint("K", n/2))
			d.Del(fmt.Sprint("K", n-1))
			d.Del("absent")

			var want []string
			for i := 1; i < n-1; i++ {
				if i != n/2 {
					want = append(want, fmt.Sprint("K", i))
				}
			}
			if got := d.Keys(); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("keys = %v, want %v", got, want)
			}
			for _, k := range want {
				var i int
				fmt.Sscanf(k, "K%d", &i)
				if d.Get(k) != PDFInteger(i) {
					t.Errorf("Get(%s) = %v, want %d", k, d.Get(k), i)
				}
			}
			if d.idx != nil {
				for k, i := range d.idx {
					if d.ent[i].Key != k {
						t.Errorf("index maps %s to slot %d holding %s", k, i, d.ent[i].Key)
					}
				}
				if len(d.idx) != len(d.ent) {
					t.Errorf("index has %d keys for %d entries", len(d.idx), len(d.ent))
				}
			}
			for _, e := range d.ent[len(d.ent):cap(d.ent)] {
				if e.Val != nil {
					t.Errorf("vacated slot still holds %v", e.Val)
				}
			}
		})
	}
}
