package verify

import (
	"os"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// -- Top-level entry points

func TestVerifyFile(t *testing.T) {
	res, err := VerifyFile(sampleVeraPassFile, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	if !res.Valid {
		t.Errorf("VerifyFile(%s) = invalid, want valid: %v", sampleVeraPassFile, res.Issues)
	}

	if _, err := VerifyFile("/nonexistent/path.pdf", pdf.PDFA_1B); err == nil {
		t.Error("VerifyFile should error for a nonexistent path")
	}
}

func TestVerifyBytes(t *testing.T) {
	data, err := os.ReadFile(sampleVeraPassFile)
	if err != nil {
		t.Skip("corpus not available")
	}
	res, err := VerifyBytes(data, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if !res.Valid {
		t.Errorf("VerifyBytes(pass file) = invalid, want valid: %v", res.Issues)
	}

	if _, err := VerifyBytes([]byte("not a pdf"), pdf.PDFA_1B); err == nil {
		t.Error("VerifyBytes should error for malformed data")
	}
}

func TestVerifyAll(t *testing.T) {
	paths := []string{sampleVeraPassFile, "/nonexistent/path.pdf"}
	results, err := VerifyAll(paths, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("VerifyAll: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("VerifyAll returned %d results, want 2", len(results))
	}
	if results[0].Path != sampleVeraPassFile || results[0].Err != nil || !results[0].Result.Valid {
		t.Errorf("VerifyAll[0] = %+v, want a valid result for the pass file", results[0])
	}
	if results[1].Err == nil {
		t.Error("VerifyAll[1] should carry an error for the nonexistent path")
	}

	if results, err := VerifyAll(nil, pdf.PDFA_1B); err != nil || len(results) != 0 {
		t.Errorf("VerifyAll(nil) = %v, %v, want empty slice, nil error", results, err)
	}
}

// -- PDF/A-1b

// 6.1.2

func TestDocument_VerifyPDFAHeader(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n%\xA0\xA1\xA2\xA3\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)

	defer doc.Close()

	if err := verifyFileHeader(doc); err != nil {
		t.Errorf("Unexpected error while verifying header: %v", err)
	}
}

func TestDocument_VerifyPDFAHeader_InvalidHeader(t *testing.T) {
	filename := "test.pdf"
	content := []byte("1.7\n%\xA0\xA1\xA2\xA3\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)
	defer doc.Close()

	errs := verifyFileHeader(doc)

	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid header, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.2" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAHeader_NoComment(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)
	defer doc.Close()

	errs := verifyFileHeader(doc)

	if len(errs) != 1 {
		t.Errorf("Expected one error for missing comment, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.2" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAHeader_InvalidCommentLength(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n%\xA0\xA1\xA2\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)
	defer doc.Close()

	errs := verifyFileHeader(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid comment length, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.2" || errs[0].Check().Subclause() != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAHeader_InvalidCommentContent(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n%wrong\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)
	defer doc.Close()

	errs := verifyFileHeader(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for non-binary characters in comment, got %v", errs)
	}

	// Only the first four bytes following '%' are required to be binary
	// (bytes beyond that are unconstrained), so 4 errors are expected here.
	if errs[0].Check().Clause() != "6.1.2" || errs[0].Check().Subclause() != 4 || len(errs[0].Messages()) != 4 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.3

func TestDocument_VerifyPDFATrailer_NoId(t *testing.T) {
	filename := "test.pdf"
	content := []byte("trailer\n<</ID a>>\nstartxref\n1111\n%%EOF")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()

	f, _ := os.Open(filename)
	info, _ := f.Stat()
	doc := pdf.NewRawReader(f, trailer, info.Size(), 0)
	defer doc.Close()

	errs := verifyFileTrailer(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for missing ID key, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.3" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFATrailer_Encrypt(t *testing.T) {
	filename := "test.pdf"
	content := []byte("trailer\n<</Encrypt a>>\nstartxref\n1111\n%%EOF")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	trailer.Entries["ID"] = pdf.PDFString{Value: "a"}
	trailer.Entries["Encrypt"] = pdf.PDFString{Value: "a"}

	f, _ := os.Open(filename)
	info, _ := f.Stat()
	doc := pdf.NewRawReader(f, trailer, info.Size(), 0)
	defer doc.Close()

	errs := verifyFileTrailer(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for forbidden Encrypt key, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.3" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFATrailer_InvalidEOF(t *testing.T) {
	filename := "test.pdf"
	content := []byte("trailer\n<</ID a>>\nstartxref\n1111\n%EOF")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	trailer.Entries["ID"] = pdf.PDFString{Value: "a"}

	f, _ := os.Open(filename)
	info, _ := f.Stat()
	doc := pdf.NewRawReader(f, trailer, info.Size(), 0)
	defer doc.Close()

	errs := verifyFileTrailer(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid EOF, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.3" || errs[0].Check().Subclause() != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.4

func TestDocument_VerifyPDFACrossReferenceTable_MissingXref(t *testing.T) {
	filename := "test.pdf"
	content := []byte("\n0 10\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)
	defer doc.Close()

	errs := verifyCrossReferenceTable(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for missing xref keyword, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.4" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFACrossReferenceTable_MissingXrefHeader(t *testing.T) {
	filename := "test.pdf"
	content := []byte("xref")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)
	defer doc.Close()

	errs := verifyCrossReferenceTable(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for missing xref header, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.4" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFACrossReferenceTable_MultipleEOLSeperators(t *testing.T) {
	filename := "test.pdf"
	content := []byte("xref\r\n0 10 10\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, pdf.PDFDict{}, 0, 0)
	defer doc.Close()

	errs := verifyCrossReferenceTable(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid EOL, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.4" || errs[0].Check().Subclause() != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.5

func TestDocument_VerifyPDFADocumentInformationDictionary_InvalidMetadata(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	info := make(pdf.PDFArray, 0)

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyDocumentInformationDictionary(trailer)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid metadata type, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.5" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// Non-standard info dict keys are permitted (veraPDF's 6-1-5-t02-pass-a.pdf
// has a conformant /Description entry), so a custom key alone is not flagged.
func TestDocument_VerifyPDFADocumentInformationDictionary_CustomKeyAllowed(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	info := pdf.NewPDFDict()

	info.Entries["Title"] = pdf.PDFString{Value: "Test"}
	info.Entries["CustomKey"] = pdf.PDFString{Value: "Value"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyDocumentInformationDictionary(trailer)
	if len(errs) != 0 {
		t.Errorf("Expected no errors for a custom info dict key, got %v", errs)
	}
}

func TestDocument_VerifyPDFADocumentInformationDictionary_EmptyValue(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	info := pdf.NewPDFDict()

	info.Entries["Title"] = pdf.PDFString{Value: ""}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyDocumentInformationDictionary(trailer)
	if len(errs) != 1 {
		t.Errorf("Expected one error for empty metadata value, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.5" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// The empty-value check (6.1.5/2) only applies to the standard info dict
// fields (Table 10.2); a custom key's value, empty or not, is unconstrained.
func TestDocument_VerifyPDFADocumentInformationDictionary_CustomKeyEmptyValueAllowed(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	info := pdf.NewPDFDict()

	info.Entries["CustomKey"] = pdf.PDFString{Value: ""}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyDocumentInformationDictionary(trailer)
	if len(errs) != 0 {
		t.Errorf("Expected no errors for an empty-valued custom key, got %v", errs)
	}
}

// 6.1.6

func TestDocument_VerifyPDFADocumentHex_InvalidChar(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()

	info.Entries["Title"] = pdf.PDFHexString{Value: "XXXX"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid hex, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.6" || errs[0].Check().Subclause() != 1 || len(errs[0].Messages()) != 4 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentHex_InvalidLength(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()

	info.Entries["Title"] = pdf.PDFHexString{Value: "AAA"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for odd number of hex chars, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.6" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.7

func TestDocument_VerifyPDFADocumentHex_InvalidKeyF(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()
	info.HasStream = true

	info.Entries["F"] = pdf.PDFHexString{Value: "aaaa"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key F, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.7" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentHex_InvalidKeyFFilter(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()
	info.HasStream = true

	info.Entries["FFilter"] = pdf.PDFHexString{Value: "aaaa"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key FFilter, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.7" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentHex_InvalidKeyFDecodeParms(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()
	info.HasStream = true

	info.Entries["FDecodeParms"] = pdf.PDFHexString{Value: "aaaa"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key FDecodeParms, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.7" || errs[0].Check().Subclause() != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.10

func TestDocument_VerifyPDFAFilter_LZWDecode(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()
	info.HasStream = true

	info.Entries["Filter"] = pdf.PDFName{Value: "LZWDecode"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid Filter LZWDecode, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.10" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.11

func TestDocument_VerifyPDFAEmbeddedFiles_EF(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()

	info.Entries["EF"] = pdf.PDFHexString{Value: "aaaa"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key EF, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.11" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAObjectEmbeddedFiles_EmbeddedFiles(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()

	info.Entries["EmbeddedFiles"] = pdf.PDFHexString{Value: "aaaa"}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key EF, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.11" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.12

func TestDocument_VerifyPDFAArchitecturalLimits_MaxNameSize(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()

	info.Entries["TooLarge"] = pdf.PDFName{Value: strings.Repeat("a", 128)}

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid name length, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.12" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAArchitecturalLimits_MaxIntSize(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	minimalConformantRoot(trailer)
	info := pdf.NewPDFDict()

	info.Entries["TooLarge"] = pdf.PDFInteger(2_147_483_648)
	info.Entries["TooSmall"] = pdf.PDFInteger(-2_147_483_649)

	trailer.Entries["Info"] = info

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.BuildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 2 {
		t.Errorf("Expected two errors for invalid integer value sizes, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.12" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}

	if errs[1].Check().Clause() != "6.1.12" || errs[1].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[1])
	}
}

// 6.1.13

func TestDocument_VerifyPDFAOptionalContent_OCProperties(t *testing.T) {
	filename := "test.pdf"
	if err := createValidPDF(filename); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(filename)

	doc, err := pdf.Open(filename)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer doc.Close()

	errs := verifyOptionalContent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid OCProperties, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.13" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.2.2

func TestDocument_VerifyPDFAOutputIntent(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.NewPDFDict()
	destOutputProfile := pdf.NewPDFDict()
	destOutputProfile.Entries["N"] = pdf.PDFInteger(3)

	outputIntent.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	outputIntent.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: "Test"}
	// "Test" does not name a standard production condition, so a
	// DestOutputProfile must be present (6.2.2/7).
	outputIntent.Entries["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 0 {
		t.Errorf("Unexpected error: %v", errs)
	}
}

func TestDocument_VerifyPDFAOutputIntent_InvalidOutputIntents(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.NewPDFDict()
	outputIntent := pdf.NewPDFDict()

	outputIntents.Entries["OutputIntents"] = outputIntent

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid OutputIntents type, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_InvalidOutputIntent(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.PDFArray{}

	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid OutputIntent type, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_InvalidSType(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.NewPDFDict()
	outputIntent.Entries["S"] = pdf.PDFInteger(1)

	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid S type, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongS(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.NewPDFDict()
	destOutputProfile := pdf.NewPDFDict()
	destOutputProfile.Entries["N"] = pdf.PDFInteger(3)

	outputIntent.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent.Entries["S"] = pdf.PDFName{Value: "Wrong"}
	outputIntent.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: "Test"}
	outputIntent.Entries["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong S, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 4 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongOutputConditionIdentifier(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.NewPDFDict()

	outputIntent.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	outputIntent.Entries["OutputConditionIdentifier"] = nil
	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for nil OutputConditionIentifier, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 5 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_DifferingDestOutputProfiles(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent1 := pdf.NewPDFDict()
	destOutputProfile1 := pdf.NewPDFDict()

	destOutputProfile1.Entries["N"] = pdf.PDFInteger(1)

	outputIntent1.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent1.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	outputIntent1.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: "Test"}
	outputIntent1.Entries["DestOutputProfile"] = destOutputProfile1

	outputIntent2 := pdf.NewPDFDict()
	destOutputProfile2 := pdf.NewPDFDict()

	destOutputProfile2.Entries["N"] = pdf.PDFInteger(2)

	outputIntent2.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent2.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	outputIntent2.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: "Test"}
	outputIntent2.Entries["DestOutputProfile"] = destOutputProfile2

	outputIntents = append(outputIntents, outputIntent1, outputIntent2)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for differing DestOutputProfiles, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 6 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_DestOutputProfileWrongFormat(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.NewPDFDict()
	destOutputProfile := pdf.PDFInteger(1)

	outputIntent.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	outputIntent.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: "Test"}
	outputIntent.Entries["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong DestOutputProfile format, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 8 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongNType(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.NewPDFDict()
	destOutputProfile := pdf.NewPDFDict()

	destOutputProfile.Entries["N"] = pdf.PDFString{Value: "3"}

	outputIntent.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	outputIntent.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: "Test"}
	outputIntent.Entries["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong N, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 9 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongN(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := pdf.NewPDFDict()
	outputIntents := pdf.PDFArray{}
	outputIntent := pdf.NewPDFDict()
	destOutputProfile := pdf.NewPDFDict()

	destOutputProfile.Entries["N"] = pdf.PDFInteger(5)

	outputIntent.Entries["Type"] = pdf.PDFString{Value: "OutputIntent"}
	outputIntent.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	outputIntent.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: "Test"}
	outputIntent.Entries["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer.Entries["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := pdf.NewRawReader(f, trailer, 0, 0)
	defer doc.Close()

	errs := verifyOutputIntent(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong N, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.2.2" || errs[0].Check().Subclause() != 10 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// TestScalarLimitViolationsCarryOwnerRef asserts the generic walk reports
// 6.1.12/6.1.6 scalar violations against the nearest enclosing dict --
// threading through arrays -- so convert's targeted fixers can resolve them.
func TestScalarLimitViolationsCarryOwnerRef(t *testing.T) {
	owner := pdf.NewPDFDict()
	owner.Entries["_ref"] = pdf.PDFRef{ObjNum: 7}
	owner.Entries["Big"] = pdf.PDFInteger(3_000_000_000)
	owner.Entries["List"] = pdf.PDFArray{pdf.PDFName{Value: strings.Repeat("x", 130)}}
	owner.Entries["Hex"] = pdf.PDFHexString{Value: "abc"}
	graph := pdf.NewPDFDict()
	graph.Entries["Root"] = owner

	ctx := &ValidationContext{}
	verifyDocument(graph, ctx)

	want := map[pdf.Check]bool{
		pdf.Checks.Structure.IntegerOutOfRange:  false,
		pdf.Checks.Structure.NameTooLong:        false,
		pdf.Checks.Structure.HexStringOddLength: false,
	}
	for _, iss := range ctx.Issues() {
		if _, tracked := want[iss.Check()]; !tracked {
			continue
		}
		ref, ok := iss.ObjectRef()
		if !ok {
			t.Errorf("%s issue carries no ref, want owner ref 7", iss.Check().Name())
			continue
		}
		if ref.ObjNum != 7 {
			t.Errorf("%s issue ref = %d, want 7 (owning dict)", iss.Check().Name(), ref.ObjNum)
		}
		want[iss.Check()] = true
	}
	for c, seen := range want {
		if !seen {
			t.Errorf("check %s not reported with a resolvable ref", c.Name())
		}
	}
}

func TestCollectAPEntryUsage(t *testing.T) {
	xobj := pdf.NewPDFDict()
	xobj.Entries["Subtype"] = pdf.PDFName{Value: "Form"}
	xobj.HasStream = true
	xobj.RawStream = []byte("q Q\n")
	xobjPtr := pdf.ValuePointer(xobj.Entries)

	resources := pdf.NewPDFDict()
	xobjDict := pdf.NewPDFDict()
	xobjDict.Entries["X1"] = xobj
	resources.Entries["XObject"] = xobjDict

	doContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "X1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	direct := pdf.NewPDFDict()
	direct.HasStream = true
	direct.RawStream = doContent
	direct.Entries["Resources"] = resources

	ctx := &ValidationContext{}
	reachable := map[uintptr]bool{}
	fu := &fontUsage{visible: map[uintptr]bool{}, invisible: map[uintptr]bool{}, usedCodes: map[uintptr]map[int]bool{}, usedCIDs: map[uintptr]map[int]bool{}}
	collectAPEntryUsage(ctx, direct, reachable, fu)
	if !reachable[xobjPtr] {
		t.Error("expected the Do-invoked XObject to be marked reachable via a direct AP/N stream")
	}

	// Subdictionary-of-states form (Btn widget).
	states := pdf.NewPDFDict()
	states.Entries["On"] = direct
	states.Entries["Off"] = pdf.NewPDFDict() // no stream: skipped
	reachable2 := map[uintptr]bool{}
	collectAPEntryUsage(ctx, states, reachable2, fu)
	if !reachable2[xobjPtr] {
		t.Error("expected the Do-invoked XObject to be marked reachable via an appearance-state subdictionary")
	}

	// Neither a stream nor a dict: no-op, no panic.
	collectAPEntryUsage(ctx, pdf.PDFName{Value: "x"}, map[uintptr]bool{}, fu)
}

func TestShownStringBytes(t *testing.T) {
	got := ShownStringBytes("Tj", []pdf.PDFValue{pdf.PDFString{Value: "AB"}})
	if string(got) != "AB" {
		t.Errorf("ShownStringBytes(Tj) = %q, want AB", got)
	}

	got = ShownStringBytes("TJ", []pdf.PDFValue{pdf.PDFArray{
		pdf.PDFString{Value: "A"}, pdf.PDFInteger(-100), pdf.PDFHexString{Value: "42"},
	}})
	if string(got) != "AB" {
		t.Errorf("ShownStringBytes(TJ) = %q, want AB", got)
	}

	got = ShownStringBytes("'", []pdf.PDFValue{pdf.PDFString{Value: "X"}})
	if string(got) != "X" {
		t.Errorf(`ShownStringBytes(') = %q, want X`, got)
	}

	if got := ShownStringBytes("Tj", nil); got != nil {
		t.Errorf("ShownStringBytes(Tj, no operands) = %q, want nil", got)
	}
}

// buildValidICCProfile returns a minimal 128-byte ICC profile header with a
// valid version, device class, colour space and matching /N.
func buildValidICCProfile() []byte {
	data := make([]byte, 128)
	data[8] = 2 // major version 2
	copy(data[12:16], "mntr")
	copy(data[16:20], "RGB ")
	copy(data[36:40], "acsp")
	return data
}

func TestValidateICCProfileStream(t *testing.T) {
	good := pdf.NewPDFDict()
	good.HasStream = true
	good.RawStream = buildValidICCProfile()
	good.Entries["N"] = pdf.PDFInteger(3)
	if err := ValidateICCProfileStream(good); err != nil {
		t.Errorf("unexpected error for a valid ICC profile: %v", err)
	}

	notStream := pdf.NewPDFDict()
	if err := ValidateICCProfileStream(notStream); err == nil {
		t.Error("expected an error when the stream cannot be decoded")
	}

	tooShort := pdf.NewPDFDict()
	tooShort.HasStream = true
	tooShort.RawStream = make([]byte, 10)
	if err := ValidateICCProfileStream(tooShort); err == nil {
		t.Error("expected an error for a too-short ICC profile")
	}

	noSig := pdf.NewPDFDict()
	noSig.HasStream = true
	noSig.RawStream = buildValidICCProfile()
	noSig.RawStream[36] = 'x'
	if err := ValidateICCProfileStream(noSig); err == nil {
		t.Error("expected an error when the acsp signature is missing")
	}

	badVersion := pdf.NewPDFDict()
	badVersion.HasStream = true
	badVersion.RawStream = buildValidICCProfile()
	badVersion.RawStream[8] = 4
	if err := ValidateICCProfileStream(badVersion); err == nil {
		t.Error("expected an error for ICC version >= 3.0")
	}

	badClass := pdf.NewPDFDict()
	badClass.HasStream = true
	badClass.RawStream = buildValidICCProfile()
	copy(badClass.RawStream[12:16], "xxxx")
	if err := ValidateICCProfileStream(badClass); err == nil {
		t.Error("expected an error for an invalid device class")
	}

	badCS := pdf.NewPDFDict()
	badCS.HasStream = true
	badCS.RawStream = buildValidICCProfile()
	copy(badCS.RawStream[16:20], "xxxx")
	if err := ValidateICCProfileStream(badCS); err == nil {
		t.Error("expected an error for an invalid colour space")
	}

	noN := pdf.NewPDFDict()
	noN.HasStream = true
	noN.RawStream = buildValidICCProfile()
	if err := ValidateICCProfileStream(noN); err == nil {
		t.Error("expected an error when /N is missing")
	}

	badNType := pdf.NewPDFDict()
	badNType.HasStream = true
	badNType.RawStream = buildValidICCProfile()
	badNType.Entries["N"] = pdf.PDFName{Value: "3"}
	if err := ValidateICCProfileStream(badNType); err == nil {
		t.Error("expected an error when /N is not an integer")
	}

	mismatch := pdf.NewPDFDict()
	mismatch.HasStream = true
	mismatch.RawStream = buildValidICCProfile()
	mismatch.Entries["N"] = pdf.PDFInteger(4) // RGB colour space, N=4 mismatched
	if err := ValidateICCProfileStream(mismatch); err == nil {
		t.Error("expected an error when /N does not match the profile colour space")
	}
}

// TestVerifyCrossReferenceTablePrevChain walks a real incrementally-updated
// PDF (a /Prev-chained xref) through verifyCrossReferenceTable's loop, which
// single-generation fixtures elsewhere in this file never exercise.
func TestVerifyCrossReferenceTablePrevChain(t *testing.T) {
	path := "../../tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.10 Filters/veraPDF test suite 6-1-10-t01-fail-a.pdf"
	doc, err := pdf.Open(path)
	if err != nil {
		t.Skipf("corpus file not available: %v", err)
	}
	defer doc.Close()
	// Not asserting a specific outcome: this file's xref sections may or may
	// not themselves be spec-conformant. The point is exercising the Prev-walk
	// loop (and its visited-offset cycle guard) without panicking.
	_ = verifyCrossReferenceTable(doc)
}

func TestComputeContentUsageFullFlow(t *testing.T) {
	// A Type0/Identity-H composite font, a simple font, an XObject invoked
	// twice (second Do should hit the already-reachable skip), and text shown
	// under invisible rendering mode (Tr 3) for the simple font.
	cidDesc := pdf.NewPDFDict()
	cidDesc.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType2"}
	type0Font := pdf.NewPDFDict()
	type0Font.Entries["Subtype"] = pdf.PDFName{Value: "Type0"}
	type0Font.Entries["Encoding"] = pdf.PDFName{Value: "Identity-H"}
	type0Font.Entries["DescendantFonts"] = pdf.PDFArray{cidDesc}

	simpleFont := pdf.NewPDFDict()
	simpleFont.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}

	fonts := pdf.NewPDFDict()
	fonts.Entries["F0"] = type0Font
	fonts.Entries["F1"] = simpleFont

	xobj := pdf.NewPDFDict()
	xobj.Entries["Subtype"] = pdf.PDFName{Value: "Form"}
	xobj.HasStream = true
	xobj.RawStream = []byte("q Q\n")
	xobjs := pdf.NewPDFDict()
	xobjs.Entries["X1"] = xobj

	resources := pdf.NewPDFDict()
	resources.Entries["Font"] = fonts
	resources.Entries["XObject"] = xobjs

	ops := []writer.ContentOp{
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "X1"}}},
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "X1"}}}, // already reachable: hits the skip branch
		{Op: "BT", Operands: nil},
		{Op: "Tf", Operands: []pdf.PDFValue{pdf.PDFName{Value: "F0"}, pdf.PDFInteger(12)}},
		{Op: "TJ", Operands: []pdf.PDFValue{pdf.PDFArray{pdf.PDFHexString{Value: "0041"}}}},
		{Op: "Tr", Operands: []pdf.PDFValue{pdf.PDFInteger(3)}}, // invisible
		{Op: "Tf", Operands: []pdf.PDFValue{pdf.PDFName{Value: "F1"}, pdf.PDFInteger(12)}},
		{Op: "Tj", Operands: []pdf.PDFValue{pdf.PDFString{Value: "A"}}},
		{Op: "ET", Operands: nil},
		{Op: "Do", Operands: nil}, // Do with no operands: early return
	}
	content, err := writer.WriteContentStream(ops)
	if err != nil {
		t.Fatal(err)
	}

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Resources"] = resources
	page.Entries["Contents"] = pdf.PDFDict{HasStream: true, RawStream: content, Entries: pdf.NewPDFDict().Entries}

	ctx := &ValidationContext{}
	reachable, invisibleOnly, usedCodes, usedCIDs := ComputeContentUsage(page, ctx)

	xobjPtr := pdf.ValuePointer(xobj.Entries)
	if !reachable[xobjPtr] {
		t.Error("expected the XObject to be marked reachable")
	}
	cidPtr := pdf.ValuePointer(cidDesc.Entries)
	if len(usedCIDs[cidPtr]) == 0 {
		t.Error("expected a used CID recorded for the Identity-H composite font")
	}
	simplePtr := pdf.ValuePointer(simpleFont.Entries)
	if !invisibleOnly[simplePtr] {
		t.Error("expected the simple font to be invisible-only (shown only under Tr 3)")
	}
	if len(usedCodes[simplePtr]) == 0 {
		t.Error("expected a used char code recorded for the simple font")
	}
}

func TestCheckLinearizedFileID(t *testing.T) {
	mismatched := "/ID [<AABBCC>]\nsome bytes in between\n/ID [<DDEEFF>]\n"
	filename := t.TempDir() + "/lin-mismatch.pdf"
	if err := os.WriteFile(filename, []byte(mismatched), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc := pdf.NewRawReader(f, pdf.NewPDFDict(), int64(len(mismatched)), 0) // no /Root: linearized overflow
	errs := checkLinearizedFileID(doc)
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Structure.TrailerID {
		t.Errorf("checkLinearizedFileID(mismatched IDs) = %v, want a single TrailerID", errs)
	}

	matching := "/ID [<AABBCC>]\nsome bytes in between\n/ID [<AABBCC>]\n"
	filename2 := t.TempDir() + "/lin-match.pdf"
	if err := os.WriteFile(filename2, []byte(matching), 0644); err != nil {
		t.Fatal(err)
	}
	f2, err := os.Open(filename2)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	doc2 := pdf.NewRawReader(f2, pdf.NewPDFDict(), int64(len(matching)), 0)
	if errs := checkLinearizedFileID(doc2); errs != nil {
		t.Errorf("unexpected violation for matching IDs: %v", errs)
	}

	// A trailer with /Root is an ordinary (non-linearized-overflow) file: no-op.
	rootTrailer := pdf.NewPDFDict()
	rootTrailer.Entries["Root"] = pdf.NewPDFDict()
	doc3 := pdf.NewRawReader(f2, rootTrailer, int64(len(matching)), 0)
	if errs := checkLinearizedFileID(doc3); errs != nil {
		t.Errorf("unexpected check when trailer has /Root: %v", errs)
	}

	// Fewer than two /ID matches: no-op.
	single := "/ID [<AABBCC>]\n"
	filename3 := t.TempDir() + "/lin-single.pdf"
	os.WriteFile(filename3, []byte(single), 0644)
	f3, _ := os.Open(filename3)
	defer f3.Close()
	doc4 := pdf.NewRawReader(f3, pdf.NewPDFDict(), int64(len(single)), 0)
	if errs := checkLinearizedFileID(doc4); errs != nil {
		t.Errorf("unexpected check with only one /ID occurrence: %v", errs)
	}
}

func TestCollectAnnotAppearanceUsageAndContentUsage(t *testing.T) {
	xobj := pdf.NewPDFDict()
	xobj.Entries["Subtype"] = pdf.PDFName{Value: "Form"}
	xobj.HasStream = true
	xobj.RawStream = []byte("q Q\n")
	xobjPtr := pdf.ValuePointer(xobj.Entries)

	resources := pdf.NewPDFDict()
	xobjDict := pdf.NewPDFDict()
	xobjDict.Entries["X1"] = xobj
	resources.Entries["XObject"] = xobjDict

	doContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "X1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	apStream := pdf.NewPDFDict()
	apStream.HasStream = true
	apStream.RawStream = doContent
	apStream.Entries["Resources"] = resources

	ap := pdf.NewPDFDict()
	ap.Entries["N"] = apStream
	annot := pdf.NewPDFDict()
	annot.Entries["AP"] = ap
	page := pdf.NewPDFDict()
	page.Entries["Annots"] = pdf.PDFArray{annot}

	ctx := &ValidationContext{}
	reachable := map[uintptr]bool{}
	fu := &fontUsage{visible: map[uintptr]bool{}, invisible: map[uintptr]bool{}, usedCodes: map[uintptr]map[int]bool{}, usedCIDs: map[uintptr]map[int]bool{}}
	collectAnnotAppearanceUsage(ctx, page, reachable, fu)
	if !reachable[xobjPtr] {
		t.Error("expected the annotation's Do-invoked XObject to be marked reachable")
	}

	// No Annots entry: no-op, no panic.
	collectAnnotAppearanceUsage(ctx, pdf.NewPDFDict(), map[uintptr]bool{}, fu)

	// collectContentUsage: single stream dict.
	reachable2 := map[uintptr]bool{}
	collectContentUsage(ctx, apStream, resources, reachable2, fu)
	if !reachable2[xobjPtr] {
		t.Error("expected collectContentUsage to scan a single content stream dict")
	}

	// collectContentUsage: array of stream dicts.
	reachable3 := map[uintptr]bool{}
	collectContentUsage(ctx, pdf.PDFArray{apStream}, resources, reachable3, fu)
	if !reachable3[xobjPtr] {
		t.Error("expected collectContentUsage to scan an array of content streams")
	}

	// Neither a dict nor an array: no-op.
	collectContentUsage(ctx, pdf.PDFName{Value: "x"}, resources, map[uintptr]bool{}, fu)
}

func TestDeviceColourAllowedUnknownModel(t *testing.T) {
	ctx := &ValidationContext{}
	if !ctx.deviceColourAllowed("lab") {
		t.Error("deviceColourAllowed should default to true for an unrecognized model")
	}
}

func TestNewContext(t *testing.T) {
	ctx := NewContext(nil)
	if ctx == nil {
		t.Fatal("NewContext(nil) returned nil")
	}
	// A throwaway context (nil reader) still decodes streams uncached.
	dict := pdf.NewPDFDict()
	dict.HasStream = true
	dict.RawStream = []byte("hello")
	data, err := ctx.decodeStreamCached(dict)
	if err != nil || string(data) != "hello" {
		t.Errorf("decodeStreamCached via NewContext(nil) = %q, %v", data, err)
	}
}
