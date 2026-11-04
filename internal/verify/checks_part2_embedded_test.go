package verify

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// Compliant files of the veraPDF corpora, which claim PDF/A-2b and PDF/A-1b.
const (
	compliant2B = "../../tests/veraPDF/PDF_A-2b/6.1 File structure/6.1.13 Implementation limits/veraPDF test suite 6-1-13-t09-pass-b.pdf"
	compliant1B = "../../tests/veraPDF/PDF_A-1b/6.2 Graphics/6.2.3.3 Uncalibrated color space/veraPDF test suite 6-2-3-3-t03-pass-d.pdf"
	// failing2B claims PDF/A-2b and fails its 6.1.13 checks.
	failing2B = "../../tests/veraPDF/PDF_A-2b/6.1 File structure/6.1.13 Implementation limits/veraPDF test suite 6-1-13-t09-fail-a.pdf"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// embedDoc returns a PDF whose catalog has the extra entries catalog and
// whose page 3 0 R has the extra entries page. Object 4 is the stream holding
// file, and objs maps the object numbers from 5 onward to the objects that
// follow it.
func embedDoc(catalog, page string, file []byte, objs map[int]string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R "+catalog+" >>")
	b.Obj(2, "<< /Type /Pages /Count 1 /Kids [3 0 R] >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "+page+" >>")
	b.StreamObj(4, "<< /Type /EmbeddedFile", file)
	for n := 5; n < 5+len(objs); n++ {
		b.Obj(n, objs[n])
	}
	return b.FinishClassic(fmt.Sprintf("<< /Size %d /Root 1 0 R >>", 5+len(objs)))
}

// part2Checks returns the checks of ISO 19005-2 6.8, 6.10 and 6.11 that
// verifying doc under PDF/A-2b reports, in order.
func part2Checks(t *testing.T, doc []byte) []pdf.Check {
	t.Helper()
	res, err := VerifyBytes(doc, pdf.PDFA2B, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	s := pdf.Checks.Structure
	var got []pdf.Check
	for _, is := range res.Issues {
		switch is.Check() {
		case s.EmbeddedFileMissingNames, s.EmbeddedFileNotPDFA, s.AlternatePresentations,
			s.PresentationSteps, s.CatalogRequirements:
			got = append(got, is.Check())
		}
	}
	return got
}

func TestVerifyEmbeddedFiles(t *testing.T) {
	s := pdf.Checks.Structure
	ok2B, ok1B := readFile(t, compliant2B), readFile(t, compliant1B)
	tree := "/Names << /EmbeddedFiles << /Names [(a.pdf) 5 0 R] >> >>"
	spec := "<< /Type /Filespec /F (a.pdf) /UF (a.pdf) /EF << /F 4 0 R /UF 4 0 R >> >>"
	for _, tc := range []struct {
		name    string
		catalog string
		file    []byte
		objs    map[int]string
		want    []pdf.Check
	}{
		{"PDF/A-2b file", tree, ok2B, map[int]string{5: spec}, nil},
		{"PDF/A-1b file", tree, ok1B, map[int]string{5: spec}, nil},
		{"not a PDF", tree, []byte("text"), map[int]string{5: spec}, []pdf.Check{s.EmbeddedFileNotPDFA}},
		{"failing PDF/A-2b file", tree, readFile(t, failing2B), map[int]string{5: spec}, []pdf.Check{s.EmbeddedFileNotPDFA}},
		{"no UF", tree, ok2B, map[int]string{5: "<< /Type /Filespec /F (a.pdf) /EF << /F 4 0 R >> >>"}, []pdf.Check{s.EmbeddedFileMissingNames}},
		{"UF a name", tree, ok2B, map[int]string{5: "<< /Type /Filespec /F (a.pdf) /UF /a /EF << /F 4 0 R >> >>"}, []pdf.Check{s.EmbeddedFileMissingNames}},
		{"hexadecimal names", tree, ok2B, map[int]string{5: "<< /Type /Filespec /F <61> /UF <FEFF0061> /EF << /F 4 0 R >> >>"}, nil},
		{"empty EF", tree, ok2B, map[int]string{5: "<< /Type /Filespec /F (a.pdf) /EF << >> >>"}, []pdf.Check{s.EmbeddedFileMissingNames}},
		{"no EF", tree, ok2B, map[int]string{5: "<< /Type /Filespec /F (a.pdf) >>"}, nil},
		{"untyped tree value", tree, []byte("text"), map[int]string{5: "<< /UF (a.pdf) /EF << /F 4 0 R >> >>"}, []pdf.Check{s.EmbeddedFileMissingNames, s.EmbeddedFileNotPDFA}},
		{"untyped value outside the tree", "/X 5 0 R", []byte("text"), map[int]string{5: "<< /EF << /F 4 0 R >> >>"}, nil},
		{"typed value outside the tree", "/X 5 0 R", ok2B, map[int]string{5: "<< /Type /Filespec /F (a.pdf) /EF << /F 4 0 R >> >>"}, []pdf.Check{s.EmbeddedFileMissingNames}},
		{
			"two specifications share a file", "/Names << /EmbeddedFiles << /Kids [<< /Names [(a) 5 0 R (b) 6 0 R 1] >> 2] >> >>",
			[]byte("text"), map[int]string{5: spec, 6: spec},
			[]pdf.Check{s.EmbeddedFileNotPDFA, s.EmbeddedFileNotPDFA},
		},
		{"tree not a dictionary", "/Names << /EmbeddedFiles 1 >>", ok2B, nil, nil},
		{"tree that holds itself", "/Names << /EmbeddedFiles 5 0 R >>", ok2B, map[int]string{5: "<< /Kids [5 0 R] >>"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := part2Checks(t, embedDoc(tc.catalog, "", tc.file, tc.objs))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVerifyPresentations(t *testing.T) {
	s := pdf.Checks.Structure
	for _, tc := range []struct {
		name, catalog, page string
		want                []pdf.Check
	}{
		{"none", "", "", nil},
		{"AlternatePresentations", "/Names << /AlternatePresentations << /Names [] >> >>", "", []pdf.Check{s.AlternatePresentations}},
		{"PresSteps", "", "/PresSteps << /Type /NavNode >>", []pdf.Check{s.PresentationSteps}},
		{"Requirements", "/Requirements []", "", []pdf.Check{s.CatalogRequirements}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := part2Checks(t, embedDoc(tc.catalog, tc.page, nil, nil))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPresentationRuleCatalogNotADict(t *testing.T) {
	d, err := pdf.OpenBytes([]byte(strings.Replace(string(catalogDoc("")), "/Root 1 0 R", "/Root 1    ", 1)))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer d.Close()
	if errs := presentationRule(d, nil, nil); errs != nil {
		t.Errorf("presentationRule = %v, want nil", errs)
	}
	if errs := embeddedFilesRule(d, nil, nil); errs != nil {
		t.Errorf("embeddedFilesRule = %v, want nil", errs)
	}
}

func TestEmbeddedFileProblem(t *testing.T) {
	stream := func(data []byte) pdf.PDFDict {
		return pdf.NewStreamDict(nil, data)
	}
	ok2B := readFile(t, compliant2B)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  context.Context
		ef   pdf.PDFDict
		want string
	}{
		{"compliant", nil, stream(ok2B), ""},
		{"undecodable", nil, brokenStreamDict(), "cannot be decoded"},
		{"no claim", nil, stream(catalogDoc("")), "does not claim PDF/A-1b or PDF/A-2b"},
		{"claims PDF/A-2u", nil, stream([]byte(strings.Replace(string(ok2B), `pdfaid:conformance="B"`, `pdfaid:conformance="U"`, 1))), "does not claim"},
		{"too deep", context.WithValue(context.Background(), embedDepthKey{}, maxEmbedDepth), stream(ok2B), "too deep to verify"},
		{"cancelled", cancelled, stream(ok2B), "cannot be verified"},
		{"fails its checks", nil, stream(readFile(t, failing2B)), "claims PDF/A-2b but fails"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := EmbeddedFileProblem(tc.run, tc.ef)
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Errorf("EmbeddedFileProblem = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

// TestEmbeddedFileStreams verifies that each stream of an EF dictionary is
// returned once, in key order, and that values other than streams are not.
func TestEmbeddedFileStreams(t *testing.T) {
	a, b := pdf.NewStreamDict(nil, []byte("a")), pdf.NewStreamDict(nil, []byte("b"))
	ef := pdf.NewPDFDict()
	ef.Entries.Set("Unix", b)
	ef.Entries.Set("UF", a)
	ef.Entries.Set("F", a)
	ef.Entries.Set("DOS", pdf.NewPDFDict())
	fs := pdf.NewPDFDict()
	fs.Entries.Set("EF", ef)
	got := EmbeddedFileStreams(fs)
	if len(got) != 2 || got[0].Entries != a.Entries || got[1].Entries != b.Entries {
		t.Errorf("EmbeddedFileStreams = %v, want [a b]", got)
	}
}
