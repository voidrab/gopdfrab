package verify

import (
	"os"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

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

	errs := verifyDocumentInformationDictionary(doc)
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

	errs := verifyDocumentInformationDictionary(doc)
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

	errs := verifyDocumentInformationDictionary(doc)
	if len(errs) != 1 {
		t.Errorf("Expected one error for empty metadata value, got %v", errs)
	}

	if errs[0].Check().Clause() != "6.1.5" || errs[0].Check().Subclause() != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// The empty-value check (6.1.5/3) only applies to the standard info dict
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

	errs := verifyDocumentInformationDictionary(doc)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
	verifyDocument(doc, graph, ctx)
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
