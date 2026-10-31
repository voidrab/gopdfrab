package verify

import (
	"bytes"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

func a2b() pdfa.Spec {
	s, _ := pdfa.For(pdf.A2B)
	return s
}

// memFile is an in-memory file for pdf.NewRawReader.
type memFile struct{ *bytes.Reader }

func (memFile) Close() error { return nil }

// rawReader returns a reader over data with the given trailer and no parsed
// cross-reference table.
func rawReader(data string, trailer pdf.PDFDict) *pdf.Reader {
	return pdf.NewRawReader(memFile{bytes.NewReader([]byte(data))}, trailer, int64(len(data)), 0)
}

func TestHeaderVersionAllowed(t *testing.T) {
	for _, tc := range []struct {
		header   string
		a1b, a2b bool
	}{
		{"%PDF-1.4", true, true},
		{"%PDF-1.0", true, true},
		{"%PDF-1.7", true, true},
		{"%PDF-1.8", true, false},
		{"%PDF-2.0", true, false},
		{"%PDF-0.7", true, false},
		{"%PDF-1.7 ", false, false},
		{"%PDF-17", false, false},
	} {
		if got := headerVersionAllowed(tc.header, a1b()); got != tc.a1b {
			t.Errorf("A1B: headerVersionAllowed(%q) = %v", tc.header, got)
		}
		if got := headerVersionAllowed(tc.header, a2b()); got != tc.a2b {
			t.Errorf("A2B: headerVersionAllowed(%q) = %v", tc.header, got)
		}
	}
}

// TestVerifyFileHeaderNamesNewestVersion verifies that a header newer than the
// level allows is reported with the newest version the level accepts.
func TestVerifyFileHeaderNamesNewestVersion(t *testing.T) {
	errs := verifyFileHeader(rawReader("%PDF-1.8\n%\xA0\xA1\xA2\xA3\n", pdf.NewPDFDict()), a2b())
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Structure.FileHeaderSignature {
		t.Fatalf("verifyFileHeader = %v, want one FileHeaderSignature", errs)
	}
	if msg := errs[0].Messages()[0]; !strings.Contains(msg, "at most %PDF-1.7") {
		t.Errorf("message %q does not name the newest allowed version", msg)
	}
}

// TestTrailerIDBytes verifies that PDF/A-2b requires the trailer ID to hold
// identifier bytes, while PDF/A-1b requires only its presence.
func TestTrailerIDBytes(t *testing.T) {
	hex := func(s string) pdf.PDFHexString { return pdf.PDFHexString{Value: s} }
	for _, tc := range []struct {
		name     string
		id       pdf.PDFValue
		a1b, a2b bool // whether the level reports TrailerID
	}{
		{"missing", nil, true, true},
		{"empty strings", pdf.PDFArray{hex(""), hex("")}, false, true},
		{"empty array", pdf.PDFArray{}, false, true},
		{"not an array", pdf.PDFString{Value: "id"}, false, true},
		{"no strings", pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2)}, false, true},
		{"hex identifiers", pdf.PDFArray{hex("AB"), hex("AB")}, false, false},
		{"one literal identifier", pdf.PDFArray{pdf.PDFString{Value: ""}, pdf.PDFString{Value: "x"}}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trailer := pdf.NewPDFDict()
			if tc.id != nil {
				trailer.Entries.Set("ID", tc.id)
			}
			d := rawReader("trailer\nstartxref\n0\n%%EOF", trailer)
			for _, lv := range []struct {
				spec pdfa.Spec
				want bool
			}{{a1b(), tc.a1b}, {a2b(), tc.a2b}} {
				errs := verifyFileTrailer(d, lv.spec)
				got := len(errs) == 1 && errs[0].Check() == pdf.Checks.Structure.TrailerID
				if got != lv.want || len(errs) > 1 {
					t.Errorf("%s: verifyFileTrailer = %v, want TrailerID %v", lv.spec.Level, errs, lv.want)
				}
			}
		})
	}
}

// TestLinearizedFileIDFollowsTheLevel verifies that only PDF/A-1b compares the
// first ID entries of a linearized file's trailers.
func TestLinearizedFileIDFollowsTheLevel(t *testing.T) {
	data := "%PDF-1.4\n%\xA0\xA1\xA2\xA3\n/ID [<AA>]\n/ID [<BB>]\n%%EOF"
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("ID", pdf.PDFArray{pdf.PDFHexString{Value: "AA"}})
	linearizedID := func(spec pdfa.Spec) bool {
		for _, e := range structuralPreIssues(rawReader(data, trailer), spec) {
			if e.Check() == pdf.Checks.Structure.TrailerID && strings.Contains(e.Messages()[0], "linearized") {
				return true
			}
		}
		return false
	}
	if !linearizedID(a1b()) {
		t.Error("A1B does not report the mismatched linearized IDs")
	}
	if linearizedID(a2b()) {
		t.Error("A2B reports the mismatched linearized IDs")
	}
}

// TestXRefSectionFormatSkipsStreams verifies that a level permitting
// cross-reference streams checks the format of classic tables only.
func TestXRefSectionFormatSkipsStreams(t *testing.T) {
	d := rawReader("1 0 obj\n<</Type/XRef>>\nstream\nendstream\nendobj\n", pdf.NewPDFDict())
	if errs := checkXRefSectionFormat(d, 0, a2b()); errs != nil {
		t.Errorf("A2B: checkXRefSectionFormat on an xref stream = %v, want none", errs)
	}
	if errs := checkXRefSectionFormat(d, 0, a1b()); len(errs) != 1 || errs[0].Check() != pdf.Checks.Structure.XRefKeyword {
		t.Errorf("A1B: checkXRefSectionFormat on an xref stream = %v, want XRefKeyword", errs)
	}
	classic := rawReader("xref\n\n0 1\n0000000000 65535 f \ntrailer\n", pdf.NewPDFDict())
	if errs := checkXRefSectionFormat(classic, 0, a2b()); len(errs) != 1 || errs[0].Check() != pdf.Checks.Structure.XRefSubsectionHeader {
		t.Errorf("A2B: checkXRefSectionFormat on a classic table = %v, want XRefSubsectionHeader", errs)
	}
}

// TestTrailerIDIndirect verifies that an ID given by indirect reference is
// judged by the array it refers to.
func TestTrailerIDIndirect(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool // whether TrailerID is reported
	}{
		{"[<AB> <AB>]", false},
		{"[<> <>]", true},
	} {
		b := pdfgen.NewBuilder("%PDF-1.7\n%\xA0\xA1\xA2\xA3\n")
		b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
		b.Obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
		b.Obj(3, tc.id)
		d, err := pdf.OpenBytes(b.FinishClassic("<< /Size 4 /Root 1 0 R /ID 3 0 R >>"))
		if err != nil {
			t.Fatal(err)
		}
		errs := verifyFileTrailer(d, a2b())
		d.Close()
		if got := len(errs) == 1 && errs[0].Check() == pdf.Checks.Structure.TrailerID; got != tc.want || len(errs) > 1 {
			t.Errorf("ID 3 0 R = %s: verifyFileTrailer = %v, want TrailerID %v", tc.id, errs, tc.want)
		}
	}
}
