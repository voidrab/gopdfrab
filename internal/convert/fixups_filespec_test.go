package convert

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestFileSpecFixer(t *testing.T) {
	fs := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"EF":            pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
		"EmbeddedFiles": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
		"F":             pdf.PDFString{Value: "x"},
		"FFilter":       pdf.PDFName{Value: "Fl"},
		"FDecodeParms":  pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	}), []byte("data"))
	trailer := trailerWith("FS", fs)
	var lost []pdf.PDFError
	changed, err := fileSpecFixer{lost: &lost}.Fix(&trailer, everyCheck(fileSpecFixer{}))
	if err != nil || !changed {
		t.Fatalf("fileSpecFixer.Fix = %v, %v", changed, err)
	}
	for _, k := range []string{"EF", "EmbeddedFiles", "F", "FFilter", "FDecodeParms"} {
		if _, ok := fs.Entries.Lookup(k); ok {
			t.Errorf("filespec key %q not removed", k)
		}
	}
	if len(lost) != 1 || lost[0].Check() != pdf.Checks.Structure.EmbeddedFileSpec {
		t.Errorf("lost = %v, want one EmbeddedFileSpec record for the /EF removed", lost)
	}
}

// TestFileSpecFixerReportsEachEmbeddedFile verifies that each embedded file
// the fixer removes is reported once, by name and object, whether it is
// reached through the name tree (/EmbeddedFiles) or through an annotation's
// file specification. A file reached through both is reported only once.
func TestFileSpecFixerReportsEachEmbeddedFile(t *testing.T) {
	ef := func() pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"F": pdf.NewStreamDict(pdf.NewDict(), []byte("payload")),
		})}
	}
	shared := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Filespec"},
		"F":    pdf.PDFString{Value: "a.txt"},
		"UF":   pdf.PDFHexString{Value: hex.EncodeToString(pdf.EncodePDFTextString("ä.txt"))},
		"EF":   ef(),
	})}
	shared.SetRef(pdf.PDFRef{ObjNum: 7})
	unnamed := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"EF": ef()})}
	tree := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Kids": pdf.PDFArray{pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Names": pdf.PDFArray{pdf.PDFString{Value: "a"}, shared, pdf.PDFString{Value: "b"}, unnamed},
		})}},
	})}
	names := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"EmbeddedFiles": tree})}
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"}, "Subtype": pdf.PDFName{Value: "FileAttachment"}, "FS": shared,
	})}
	trailer := trailerWith("Names", names)
	trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Set("Annot0", annot)

	var lost []pdf.PDFError
	changed, err := fileSpecFixer{lost: &lost}.Fix(&trailer, everyCheck(fileSpecFixer{}))
	if err != nil || !changed {
		t.Fatalf("fileSpecFixer.Fix = %v, %v", changed, err)
	}
	if _, ok := names.Entries.Lookup("EmbeddedFiles"); ok {
		t.Error("/EmbeddedFiles not removed")
	}
	var got []string
	for _, e := range lost {
		ref, _ := e.ObjectRef()
		got = append(got, fmt.Sprintf("%d %s", ref.ObjNum, strings.Join(e.Messages(), "; ")))
	}
	sort.Strings(got)
	want := []string{"0 embedded file removed", `7 embedded file "ä.txt" removed`}
	if !slices.Equal(got, want) {
		t.Errorf("lost = %q, want %q", got, want)
	}
}

// compliant2B is a file of the veraPDF corpus that complies with PDF/A-2b.
const compliant2B = "../../tests/veraPDF/PDF_A-2b/6.1 File structure/6.1.13 Implementation limits/veraPDF test suite 6-1-13-t09-pass-b.pdf"

func TestEmbeddedFileFixerDropsNonPDFAFiles(t *testing.T) {
	ok2B, err := os.ReadFile(compliant2B)
	if err != nil {
		t.Fatal(err)
	}
	text := pdf.NewStreamDict(nil, []byte("text"))
	bad := specDict("Type", specName("Filespec"), "F", pdf.PDFString{Value: "a.txt"}, "UF", pdf.PDFString{Value: "a.txt"},
		"EF", specDict("F", text, "UF", text))
	shared := specDict("F", pdf.PDFString{Value: "b.txt"}, "UF", pdf.PDFString{Value: "b.txt"}, "EF", specDict("F", text))
	good := specDict("Type", specName("Filespec"), "F", pdf.PDFString{Value: "c.pdf"}, "UF", pdf.PDFString{Value: "c.pdf"},
		"EF", specDict("F", pdf.NewStreamDict(nil, ok2B)))
	trailer := trailerWith("Names", specDict("EmbeddedFiles", specDict("Names", pdf.PDFArray{
		pdf.PDFString{Value: "a"}, bad, pdf.PDFString{Value: "b"}, shared, pdf.PDFString{Value: "c"}, good,
	})))
	root := trailer.Entries.Get("Root").(pdf.PDFDict)
	onlyBad := specDict("AF", pdf.PDFArray{bad})
	mixed := specDict("AF", pdf.PDFArray{bad, good, pdf.PDFInteger(1)})
	root.Entries.Set("Pages", specDict("Kids", pdf.PDFArray{onlyBad, mixed}))

	var lost []pdf.PDFError
	changed, err := embeddedFileFixer{lost: &lost}.Fix(&trailer, issuesOf(pdf.Checks.Structure.EmbeddedFileNotPDFA))
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want changed", changed, err)
	}
	for _, fs := range []pdf.PDFDict{bad, shared} {
		if _, ok := fs.Entries.Lookup("EF"); ok {
			t.Errorf("EF of %v kept", embeddedFileName(fs))
		}
	}
	if _, ok := good.Entries.Lookup("EF"); !ok {
		t.Error("EF of the compliant file removed")
	}
	if len(lost) != 2 || !strings.Contains(lost[0].Error(), `"a.txt" removed: it is not a readable PDF file`) {
		t.Errorf("lost = %v, want a.txt and b.txt", lost)
	}
	if _, ok := onlyBad.Entries.Lookup("AF"); ok {
		t.Error("AF entry left with no associated file")
	}
	if af := mixed.Entries.Get("AF").(pdf.PDFArray); len(af) != 2 || af[0].(pdf.PDFDict).Entries != good.Entries {
		t.Errorf("AF = %v, want the compliant file and the integer", af)
	}
}

func TestEmbeddedFileFixerFillsFileNames(t *testing.T) {
	ef := func() pdf.PDFDict { return specDict("F", pdf.NewStreamDict(nil, []byte("x"))) }
	onlyF := specDict("Type", specName("Filespec"), "F", pdf.PDFString{Value: "f.txt"}, "EF", ef())
	onlyUF := specDict("UF", pdf.PDFHexString{Value: "FEFF00E4"}, "EF", ef())
	neither := specDict("EF", ef())
	untyped := specDict("Type", specName("Filespec"), "EF", ef())
	noEF := specDict("Type", specName("Filespec"))
	trailer := trailerWith("Names", specDict("EmbeddedFiles", specDict("Names", pdf.PDFArray{
		pdf.PDFString{Value: "o"}, onlyUF, pdf.PDFHexString{Value: "FEFF00F6"}, neither,
	})))
	root := trailer.Entries.Get("Root").(pdf.PDFDict)
	root.Entries.Set("X", pdf.PDFArray{onlyF, untyped, noEF})

	changed, err := embeddedFileFixer{}.Fix(&trailer, issuesOf(pdf.Checks.Structure.EmbeddedFileMissingNames))
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want changed", changed, err)
	}
	for _, tc := range []struct {
		name  string
		fs    pdf.PDFDict
		f, uf string
	}{
		{"only F", onlyF, "f.txt", "f.txt"},
		{"only UF", onlyUF, "_", "ä"},
		{"neither, in the tree", neither, "_", "ö"},
		{"neither, outside the tree", untyped, "attachment", "attachment"},
	} {
		f, _ := pdf.StringBytes(tc.fs.Entries.Get("F"))
		if string(f) != tc.f || pdf.DecodeInfoTextString(tc.fs.Entries.Get("UF")) != tc.uf {
			t.Errorf("%s: F = %q, UF = %q; want %q, %q", tc.name, f, pdf.DecodeInfoTextString(tc.fs.Entries.Get("UF")), tc.f, tc.uf)
		}
	}
	if _, ok := noEF.Entries.Lookup("F"); ok {
		t.Error("file specification without EF given names")
	}
	if changed, _ := (embeddedFileFixer{}).Fix(&trailer, issuesOf(pdf.Checks.Structure.EmbeddedFileMissingNames)); changed {
		t.Error("second Fix changed a repaired document")
	}
}

func TestEmbeddedFileFixerNoRoot(t *testing.T) {
	trailer := pdf.NewPDFDict()
	if changed, err := (embeddedFileFixer{}).Fix(&trailer, everyCheck(embeddedFileFixer{})); changed || err != nil {
		t.Errorf("Fix = %v, %v; want unchanged", changed, err)
	}
}

func TestPresentationFixer(t *testing.T) {
	page := specDict("Type", specName("Page"), "PresSteps", specDict())
	trailer := trailerWith("Requirements", pdf.PDFArray{})
	root := trailer.Entries.Get("Root").(pdf.PDFDict)
	names := specDict("AlternatePresentations", specDict())
	root.Entries.Set("Names", names)
	root.Entries.Set("Pages", specDict("Type", specName("Pages"), "Kids", pdf.PDFArray{page}))
	changed, err := presentationFixer{}.Fix(&trailer, everyCheck(presentationFixer{}))
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want changed", changed, err)
	}
	for _, tc := range []struct {
		d   pdf.PDFDict
		key string
	}{{root, "Requirements"}, {names, "AlternatePresentations"}, {page, "PresSteps"}} {
		if _, ok := tc.d.Entries.Lookup(tc.key); ok {
			t.Errorf("%s kept", tc.key)
		}
	}
	if changed, _ := (presentationFixer{}).Fix(&trailer, everyCheck(presentationFixer{})); changed {
		t.Error("second Fix changed a repaired document")
	}
	empty := pdf.NewPDFDict()
	if changed, _ := (presentationFixer{}).Fix(&empty, everyCheck(presentationFixer{})); changed {
		t.Error("Fix changed a trailer without a catalog")
	}
}

// TestEmbeddedFileFixerKeepsUndecidedFiles verifies that an embedded file
// that could not be judged, here because the run was cancelled, is kept.
func TestEmbeddedFileFixerKeepsUndecidedFiles(t *testing.T) {
	ok2B, err := os.ReadFile(compliant2B)
	if err != nil {
		t.Fatal(err)
	}
	fs := specDict("Type", specName("Filespec"), "F", pdf.PDFString{Value: "a"}, "UF", pdf.PDFString{Value: "a"},
		"EF", specDict("F", pdf.NewStreamDict(nil, ok2B)))
	trailer := trailerWith("X", fs)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	var lost []pdf.PDFError
	changed, err := embeddedFileFixer{ctx: cancelled, lost: &lost}.Fix(&trailer, issuesOf(pdf.Checks.Structure.EmbeddedFileNotPDFA))
	if err != nil || changed || len(lost) != 0 || fs.Entries.Get("EF") == nil {
		t.Errorf("Fix = %v, %v, lost %v; want the file kept", changed, err, lost)
	}
}
