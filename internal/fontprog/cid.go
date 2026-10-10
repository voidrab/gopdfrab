package fontprog

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// ParseCIDWidths decodes a CID font W array into (CID, pdfWidth-in-1/1000-em) pairs.
// W format: [c1 [w1 w2 ...] c2 c3 w ...]
func ParseCIDWidths(w pdf.PDFArray) [][2]int {
	var pairs [][2]int
	i := 0
	intV := func(v pdf.PDFValue) (int, bool) {
		switch x := v.(type) {
		case pdf.PDFInteger:
			return int(x), true
		case pdf.PDFReal:
			return int(x), true
		}
		return 0, false
	}
	for i < len(w) {
		c1, ok := intV(w[i])
		if !ok {
			i++
			continue
		}
		i++
		if i >= len(w) {
			break
		}
		if arr, ok2 := w[i].(pdf.PDFArray); ok2 {
			for j, wv := range arr {
				if width, ok3 := intV(wv); ok3 {
					pairs = append(pairs, [2]int{c1 + j, width})
				}
			}
			i++
		} else {
			c2, ok2 := intV(w[i])
			if !ok2 {
				i++
				continue
			}
			i++
			if i >= len(w) {
				break
			}
			width, ok3 := intV(w[i])
			if !ok3 {
				i++
				continue
			}
			i++
			for c := c1; c <= c2; c++ {
				pairs = append(pairs, [2]int{c, width})
			}
		}
	}
	return pairs
}
