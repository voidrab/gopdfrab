package convert

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontedit"
	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestFixSimpleTrueTypeWidthsMacCmap verifies that, with no (3,1) cmap, a
// non-symbolic font's code resolves through its glyph name's Mac OS Roman code
// in (1,0) (ISO 32000-1 9.6.6.4), as verify's 6.3.6 check resolves it, so the
// fixer repairs the width verify reports. WinAnsi 0xE9 is eacute, Mac Roman
// 0x8E, GID 1, 600 wide. A lookup of the raw code 0xE9 finds nothing, so the
// mismatch is left in place.
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
	ff.SetStream(fontedit.PackSfnt(map[string][]byte{
		"head": head, "maxp": {0, 0, 0x50, 0, 0, 2}, "hhea": hhea, "hmtx": hmtx, "cmap": cmap,
	}))
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

// symbolOnlyTrueType is a TrueType program whose (1,0) and (3,0) cmaps both
// map only the raw code 0xB0 to GID 1. This is the shape of a symbolic subset
// in which WinAnsi code 176 (degree) has no strict mapping.
func symbolOnlyTrueType() pdf.PDFDict {
	sub := make([]byte, 262)
	binary.BigEndian.PutUint16(sub[2:], 262)
	sub[6+0xB0] = 1
	cmap := []byte{0, 0, 0, 2, 0, 1, 0, 0, 0, 0, 0, 20, 0, 3, 0, 0, 0, 0, 0, 20}
	cmap = append(cmap, sub...)
	ff := pdf.NewPDFDict()
	ff.SetStream(fontedit.PackSfnt(map[string][]byte{"maxp": {0, 0, 0x50, 0, 0, 2}, "cmap": cmap}))
	return ff
}

func simpleTrueTypeFont(enc pdf.PDFValue, ff pdf.PDFDict) pdf.PDFDict {
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile2", ff)
	d := pdf.NewPDFDict()
	d.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	d.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	if enc != nil {
		d.Entries.Set("Encoding", enc)
	}
	d.Entries.Set("FirstChar", pdf.PDFInteger(176))
	d.Entries.Set("LastChar", pdf.PDFInteger(176))
	d.Entries.Set("FontDescriptor", desc)
	return d
}

func windowsCmapOf(t *testing.T, d pdf.PDFDict) map[uint16]uint16 {
	t.Helper()
	ff := d.Entries.Get("FontDescriptor").(pdf.PDFDict).Entries.Get("FontFile2").(pdf.PDFDict)
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatal(err)
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		t.Fatal("repaired program does not parse")
	}
	return fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
}

// TestRepairNonSymbolicTrueTypeCmap verifies that a non-symbolic font's code,
// which ISO 32000-1 9.6.6.4 cannot resolve but the raw-code lookup used by
// lenient viewers can, gets a (3,1) mapping to that same glyph. Strict readers
// (verify, veraPDF) and lenient ones then render the same glyph.
func TestRepairNonSymbolicTrueTypeCmap(t *testing.T) {
	d := simpleTrueTypeFont(pdf.PDFName{Value: "WinAnsiEncoding"}, symbolOnlyTrueType())
	repairNonSymbolicTrueTypeCmap(d)
	if got := windowsCmapOf(t, d); got[0x00B0] != 1 {
		t.Fatalf("(3,1) = %v, want U+00B0 -> 1", got)
	}
	ff := d.Entries.Get("FontDescriptor").(pdf.PDFDict).Entries.Get("FontFile2").(pdf.PDFDict)
	ctx := verify.NewContext(nil)
	verify.ValidateSimpleTrueTypeSubset(d, ff, 176, 176, pdf.PDFArray{pdf.PDFInteger(400)}, ctx)
	if issues := ctx.Issues(); len(issues) != 0 {
		t.Errorf("repaired font still reported: %v", issues)
	}

	// A symbolic (unencoded) font already resolves by raw code: untouched.
	d = simpleTrueTypeFont(nil, symbolOnlyTrueType())
	repairNonSymbolicTrueTypeCmap(d)
	if got := windowsCmapOf(t, d); got != nil {
		t.Errorf("symbolic font gained a (3,1) cmap: %v", got)
	}
}

// TestRepairNonSymbolicTrueTypeCmapSharedProgram verifies that a font program
// two descriptors share is repaired for both: the other descriptor reads the
// repaired bytes, and /Length1 matches them.
func TestRepairNonSymbolicTrueTypeCmapSharedProgram(t *testing.T) {
	shared := symbolOnlyTrueType()
	a := simpleTrueTypeFont(pdf.PDFName{Value: "WinAnsiEncoding"}, shared)
	b := simpleTrueTypeFont(pdf.PDFName{Value: "WinAnsiEncoding"}, shared)
	repairNonSymbolicTrueTypeCmap(a)
	if got := windowsCmapOf(t, b); got[0x00B0] != 1 {
		t.Fatalf("other font's (3,1) = %v, want U+00B0 -> 1", got)
	}
	ff := b.Entries.Get("FontDescriptor").(pdf.PDFDict).Entries.Get("FontFile2").(pdf.PDFDict)
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatal(err)
	}
	if n := ff.Entries.Get("Length1"); n != pdf.PDFInteger(len(data)) {
		t.Errorf("/Length1 = %v, want %d, the repaired program's length", n, len(data))
	}
}

// TestRepairNonSymbolicTrueTypeCmapEdges verifies that without FirstChar and
// LastChar every code is considered, and that a program that will not decode
// is left alone.
func TestRepairNonSymbolicTrueTypeCmapEdges(t *testing.T) {
	d := simpleTrueTypeFont(pdf.PDFName{Value: "WinAnsiEncoding"}, symbolOnlyTrueType())
	d.Entries.Del("FirstChar")
	d.Entries.Del("LastChar")
	repairNonSymbolicTrueTypeCmap(d)
	if got := windowsCmapOf(t, d); got[0x00B0] != 1 {
		t.Errorf("no FirstChar/LastChar: (3,1) = %v, want U+00B0 -> 1", got)
	}

	broken := pdf.NewPDFDict()
	broken.SetStream([]byte("not zlib"))
	broken.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	d = simpleTrueTypeFont(pdf.PDFName{Value: "WinAnsiEncoding"}, broken)
	repairNonSymbolicTrueTypeCmap(d)
	ff := d.Entries.Get("FontDescriptor").(pdf.PDFDict).Entries.Get("FontFile2").(pdf.PDFDict)
	if string(ff.Stream()) != "not zlib" {
		t.Error("undecodable program was rewritten")
	}
}

// type1Encrypt is the Type 1 cipher with lead zero filler bytes prepended.
func type1Encrypt(plain []byte, key uint16, lead int) []byte {
	R := key
	in := append(make([]byte, lead), plain...)
	out := make([]byte, len(in))
	for i, p := range in {
		out[i] = p ^ byte(R>>8)
		R = (uint16(out[i])+R)*52845 + 22719
	}
	return out
}

// TestInlineType1WidthSubrsInFont verifies that a Type 1 glyph whose hsbw sits
// in a subr ("0 280 0 callsubr endchar", subr 0 = "hsbw return") is rewritten
// to carry hsbw itself. Such a glyph is read as width 0 by veraPDF, which does
// not follow callsubr.
func TestInlineType1WidthSubrsInFont(t *testing.T) {
	subr := type1Encrypt([]byte{13, 11}, 4330, 4)
	glyph := type1Encrypt([]byte{139, 247, 172, 139, 10, 14}, 4330, 4)
	private := "dup /Private 8 dict dup begin\n/Subrs 1 array\ndup 0 " + strconv.Itoa(len(subr)) + " RD " + string(subr) +
		" NP\n2 index /CharStrings 1 dict dup begin\n/f " + strconv.Itoa(len(glyph)) + " RD " + string(glyph) + " ND\nend\n"
	header := "%!PS-AdobeFont-1.0: T\n/Encoding StandardEncoding def\ncurrentfile eexec\n"
	eexec := type1Encrypt([]byte(private), 55665, 4)
	program := append([]byte(header), eexec...)

	ff := pdf.NewPDFDict()
	ff.SetStream(program)
	ff.Entries.Set("Length1", pdf.PDFInteger(len(header)))
	ff.Entries.Set("Length2", pdf.PDFInteger(len(eexec)))
	ff.Entries.Set("Length3", pdf.PDFInteger(0))
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile", ff)
	d := pdf.NewPDFDict()
	d.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	d.Entries.Set("FontDescriptor", desc)

	inlineType1WidthSubrsInFont(d)

	ff = desc.Entries.Get("FontFile").(pdf.PDFDict)
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatal(err)
	}
	if l1, l2 := ff.Entries.Get("Length1").(pdf.PDFInteger), ff.Entries.Get("Length2").(pdf.PDFInteger); int(l1+l2) != len(data) {
		t.Errorf("Length1 %d + Length2 %d != program length %d", l1, l2, len(data))
	}
	plain := fontprog.DecryptType1Block(data[fontprog.Type1EexecBinStart(data):], 55665)
	cs := bytes.Index(plain, []byte("/CharStrings"))
	if cs < 0 {
		t.Fatal("no /CharStrings in the eexec section")
	}
	section, lenIV := plain[cs:], fontprog.Type1LenIV(plain[:cs])
	m := fontprog.Type1CharStringRe.FindSubmatchIndex(section)
	n, _ := strconv.Atoi(string(section[m[4]:m[5]]))
	if got := fontprog.DecryptType1CharString(section[m[1]:m[1]+n], lenIV); !bytes.Equal(got, []byte{139, 247, 172, 13, 14}) {
		t.Errorf("/f = %v, want 0 280 hsbw endchar", got)
	}

	// Length1/Length2 missing or wrong: left alone.
	for _, drop := range []string{"Length1", "Length2"} {
		ff := pdf.NewPDFDict()
		ff.SetStream(program)
		ff.Entries.Set("Length1", pdf.PDFInteger(len(header)))
		ff.Entries.Set("Length2", pdf.PDFInteger(len(eexec)))
		ff.Entries.Del(drop)
		desc := pdf.NewPDFDict()
		desc.Entries.Set("FontFile", ff)
		d := pdf.NewPDFDict()
		d.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
		d.Entries.Set("FontDescriptor", desc)
		inlineType1WidthSubrsInFont(d)
		if got := desc.Entries.Get("FontFile").(pdf.PDFDict).Stream(); !bytes.Equal(got, program) {
			t.Errorf("no %s: program rewritten", drop)
		}
	}
}

func TestInlineType1WidthSubrsInFontUndecodable(t *testing.T) {
	ff := pdf.NewPDFDict()
	ff.SetStream([]byte("not zlib"))
	ff.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	ff.Entries.Set("Length1", pdf.PDFInteger(1))
	ff.Entries.Set("Length2", pdf.PDFInteger(4))
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile", ff)
	d := pdf.NewPDFDict()
	d.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	d.Entries.Set("FontDescriptor", desc)
	inlineType1WidthSubrsInFont(d)
	if got := desc.Entries.Get("FontFile").(pdf.PDFDict).Stream(); string(got) != "not zlib" {
		t.Error("undecodable program was rewritten")
	}
}
