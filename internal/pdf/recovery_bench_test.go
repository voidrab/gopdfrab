package pdf

import (
	"fmt"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// buildManyObjectDoc builds a classic PDF with n filler objects plus a minimal
// page tree, for measuring how Open scales with object count.
func buildManyObjectDoc(n int) []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	for i := 4; i < 4+n; i++ {
		b.Obj(i, fmt.Sprintf("<< /Index %d >>", i))
	}
	return b.FinishClassic(fmt.Sprintf("<< /Size %d /Root 1 0 R >>", 4+n))
}

// BenchmarkXRefRecovery compares a clean Open against Open of the same document
// with its startxref destroyed, so the whole cross-reference table is rebuilt
// by a full-file object scan. Running both at growing object counts shows the
// recovery path stays linear (item 24: recovery must not be a DoS vector).
func BenchmarkXRefRecovery(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		intact := buildManyObjectDoc(n)
		broken := pdfgen.BreakStartxref(intact)

		b.Run(fmt.Sprintf("intact/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				d, err := OpenBytes(intact)
				if err != nil {
					b.Fatal(err)
				}
				d.Close()
			}
		})
		b.Run(fmt.Sprintf("recovered/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				d, err := OpenBytes(broken)
				if err != nil {
					b.Fatal(err)
				}
				d.Close()
			}
		})
	}
}
