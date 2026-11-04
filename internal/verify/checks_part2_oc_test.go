package verify

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// ocDoc returns a PDF whose catalog has the OCProperties dictionary props,
// with two optional content groups, 10 0 R named "A" and 11 0 R named "B",
// a configuration 12 0 R and an Order array 13 0 R that holds itself.
func ocDoc(props string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /OCProperties "+props+" >>")
	b.Obj(2, "<< /Type /Pages /Count 0 /Kids [] >>")
	b.Obj(10, "<< /Type /OCG /Name (A) >>")
	b.Obj(11, "<< /Type /OCG /Name (B) >>")
	b.Obj(12, "<< /Name (Shared) /Order [10 0 R 11 0 R] >>")
	b.Obj(13, "[11 0 R 13 0 R]")
	return b.FinishClassic("<< /Size 14 /Root 1 0 R >>")
}

func TestVerifyOCConfigs(t *testing.T) {
	s := pdf.Checks.Structure
	for _, tc := range []struct {
		name  string
		props string
		want  []pdf.Check
	}{
		{"conforming", "<< /OCGs [10 0 R 11 0 R] /D << /Name (Default) /Order [10 0 R [11 0 R]] >> >>", nil},
		{"no Order", "<< /OCGs [10 0 R 11 0 R] /D << /Name (Default) >> >>", nil},
		{"Order not an array", "<< /OCGs [10 0 R 11 0 R] /D << /Name (Default) /Order 1 >> >>", nil},
		{"no D", "<< /OCGs [10 0 R] /Configs [<< /Name (X) >>] >>", nil},
		{"D missing Name", "<< /OCGs [10 0 R] /D << >> >>", []pdf.Check{s.OCConfigMissingName}},
		{"empty Name", "<< /OCGs [10 0 R] /D << /Name () >> >>", []pdf.Check{s.OCConfigMissingName}},
		{"UTF-16 empty Name", "<< /OCGs [10 0 R] /D << /Name <FEFF> >> >>", []pdf.Check{s.OCConfigMissingName}},
		{"hex-string Names", "<< /OCGs [<< /Type /OCG /Name <FEFF0041> >>] /D << /Name <FEFF0044> /Order [<< /Name (A) >>] >> >>", nil},
		{"Name a name object", "<< /OCGs [10 0 R] /D << /Name /Default >> >>", []pdf.Check{s.OCConfigMissingName}},
		{"Configs entry missing Name", "<< /OCGs [10 0 R] /D << /Name (D) >> /Configs [1 << >>] >>", []pdf.Check{s.OCConfigMissingName}},
		{
			"Configs repeat the default Name",
			"<< /OCGs [10 0 R] /D << /Name (X) >> /Configs [<< /Name (X) >> << /Name (Y) >> << /Name (Y) >>] >>",
			[]pdf.Check{s.OCConfigDuplicateName, s.OCConfigDuplicateName},
		},
		{"empty Names repeat", "<< /D << /Name () >> /Configs [<< /Name () >>] >>", []pdf.Check{s.OCConfigMissingName, s.OCConfigMissingName, s.OCConfigDuplicateName}},
		{"Order misses a group", "<< /OCGs [10 0 R 11 0 R] /D << /Name (D) /Order [10 0 R] >> >>", []pdf.Check{s.OCConfigOrderIncomplete}},
		{"Order lists a group by name only", "<< /OCGs [10 0 R 11 0 R] /D << /Name (D) /Order [10 0 R << /Name (B) >>] >> >>", nil},
		{"Order holds a label and a cycle", "<< /OCGs [10 0 R 11 0 R] /D << /Name (D) /Order [(label) 10 0 R 13 0 R] >> >>", nil},
		{"AS present", "<< /OCGs [10 0 R] /D << /Name (D) /AS [] >> >>", []pdf.Check{s.OCConfigAutoState}},
		{"shared config", "<< /OCGs [10 0 R 11 0 R] /D 12 0 R /Configs [12 0 R] >>", []pdf.Check{s.OCConfigDuplicateName}},
		{"OCProperties not a dictionary", "1", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := pdf.OpenBytes(ocDoc(tc.props))
			if err != nil {
				t.Fatalf("OpenBytes: %v", err)
			}
			defer d.Close()
			var got []pdf.Check
			for _, e := range ocConfigRule(d, nil, nil) {
				got = append(got, e.Check())
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestVerifyOCConfigsGroupWithoutName verifies that a group without a Name
// string is never listed in Order, even by reference, as in veraPDF.
func TestVerifyOCConfigsGroupWithoutName(t *testing.T) {
	doc := bytes.Replace(ocDoc("<< /OCGs [10 0 R] /D 12 0 R >>"), []byte("/Name (A)"), []byte("/Name /A "), 1)
	d, err := pdf.OpenBytes(doc)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()
	errs := ocConfigRule(d, nil, nil)
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Structure.OCConfigOrderIncomplete {
		t.Fatalf("errs = %v, want OCConfigOrderIncomplete", errs)
	}
	if msg := errs[0].Error(); !strings.Contains(msg, `group(s) ""`) || !strings.Contains(msg, "12 0 R") {
		t.Errorf("message %q does not name the unnamed group and the configuration", msg)
	}
}

func TestVerifyOCConfigsCatalogNotADict(t *testing.T) {
	doc := bytes.Replace(catalogDoc(""), []byte("/Root 1 0 R"), []byte("/Root 1    "), 1)
	d, err := pdf.OpenBytes(doc)
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()
	if errs := ocConfigRule(d, nil, nil); errs != nil {
		t.Errorf("verifyOptionalContent = %v, want nil", errs)
	}
}

func TestCollectOrderNamesDepth(t *testing.T) {
	order := pdf.PDFArray{kv("Name", pdf.PDFString{Value: "deep"})}
	for range maxOrderDepth + 1 {
		order = pdf.PDFArray{order}
	}
	listed := map[string]bool{}
	collectOrderNames(order, listed, map[uintptr]bool{}, 0)
	if listed["deep"] {
		t.Error("a name nested beyond maxOrderDepth was collected")
	}
}
