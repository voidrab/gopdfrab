package convert

import (
	"encoding/hex"
	"fmt"
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
