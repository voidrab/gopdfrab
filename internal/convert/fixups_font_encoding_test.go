package convert

import (
	"encoding/binary"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestFixSimpleTrueTypeWidthsMacCmap: with no (3,1) cmap, a non-symbolic
// font's code resolves through its glyph name's Mac OS Roman code in (1,0)
// (ISO 32000-1 9.6.6.4), exactly as verify's 6.3.6 check now resolves it, so
// the fixer repairs the width verify reports. WinAnsi 0xE9 is eacute, Mac
// Roman 0x8E, GID 1, 600 wide; looking up the raw code 0xE9 found nothing
// and left the mismatch in place.
func TestFixSimpleTrueTypeWidthsMacCmap(t *testing.T) {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000) // unitsPerEm
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 2) // numberOfHMetrics
	hmtx := make([]byte, 8)
	binary.BigEndian.PutUint16(hmtx[4:], 600)
	sub := make([]byte, 262)
	binary.BigEndian.PutUint16(sub[2:], 262) // format 0
	sub[6+0x8E] = 1
	cmap := append([]byte{0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 12}, sub...) // one (1,0) record

	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.RawStream = fontprog.PackSfnt(map[string][]byte{
		"head": head, "maxp": {0, 0, 0x50, 0, 0, 2}, "hhea": hhea, "hmtx": hmtx, "cmap": cmap,
	})
	v := pdf.NewPDFDict()
	v.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	v.Entries.Set("FirstChar", pdf.PDFInteger(0xE9))
	v.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(900)})

	if !fixSimpleTrueTypeWidths(v, ff) {
		t.Fatal("fixSimpleTrueTypeWidths reported no change")
	}
	if got := v.Entries.Get("Widths").(pdf.PDFArray)[0]; got != pdf.PDFInteger(600) {
		t.Errorf("Widths[0] = %v, want 600", got)
	}
}
