package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestFileSpecFixer(t *testing.T) {
	fs := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"EF":            pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
			"EmbeddedFiles": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
			"F":             pdf.PDFString{Value: "x"},
			"FFilter":       pdf.PDFName{Value: "Fl"},
			"FDecodeParms":  pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
		},
		HasStream: true, RawStream: []byte("data"),
	}
	trailer := trailerWith("FS", fs)
	changed, err := fileSpecFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("fileSpecFixer.Fix = %v, %v", changed, err)
	}
	for _, k := range []string{"EF", "EmbeddedFiles", "F", "FFilter", "FDecodeParms"} {
		if _, ok := fs.Entries[k]; ok {
			t.Errorf("filespec key %q not removed", k)
		}
	}
}
