package pdfrab

import (
	"os"
	"testing"
)

// -- PDF/A-1b

func TestDocument_VerifyPDFA(t *testing.T) {
	filename := "pdfa1b.pdf"
	doc, err := Open(test_dir + filename)
	if err != nil {
		t.Fatalf("Failed to open PDF: %v", err)
	}
	defer doc.Close()

	res, err := doc.Verify(LevelType(A1_B))

	if err != nil {
		t.Errorf("Verification failed for conforming PDF: %v", err)
	}

	if !res.Valid {
		t.Errorf("Verification failed for conforming PDF: %v", res.Issues)
	}
}

func TestDocument_VerifyPDFA_Invalid(t *testing.T) {
	filename := "pdfa1b_invalid.pdf"
	doc, err := Open(test_dir + filename)
	if err != nil {
		t.Fatalf("Failed to open PDF: %v", err)
	}
	defer doc.Close()

	res, err := doc.Verify(LevelType(A1_B))

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if res.Valid {
		t.Errorf("Verification succeeded for invalid PDF")
	}

	count := 0
	for _, issue := range res.Issues {
		if issue.clause == "6.1.6" {
			count++
		}
	}

	if count != 1 {
		t.Errorf("expected error due to odd number of hex digits")
	}
}

// 6.1.2

func TestDocument_VerifyPDFAHeader(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n%\xA0\xA1\xA2\xA3\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}

	defer doc.Close()

	if err := doc.verifyFileHeader(); err != nil {
		t.Errorf("Unexpected error while verifying header: %v", err)
	}
}

func TestDocument_VerifyPDFAHeader_InvalidHeader(t *testing.T) {
	filename := "test.pdf"
	content := []byte("1.7\n%\xA0\xA1\xA2\xA3\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}
	defer doc.Close()

	errs := doc.verifyFileHeader()

	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid header, got %v", errs)
	}

	if errs[0].clause != "6.1.2" || errs[0].subclause != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAHeader_NoComment(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}
	defer doc.Close()

	errs := doc.verifyFileHeader()

	if len(errs) != 1 {
		t.Errorf("Expected one error for missing comment, got %v", errs)
	}

	if errs[0].clause != "6.1.2" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAHeader_InvalidCommentLength(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n%\xA0\xA1\xA2\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}
	defer doc.Close()

	errs := doc.verifyFileHeader()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid comment length, got %v", errs)
	}

	if errs[0].clause != "6.1.2" || errs[0].subclause != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAHeader_InvalidCommentContent(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n%wrong\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}
	defer doc.Close()

	errs := doc.verifyFileHeader()
	if len(errs) != 1 {
		t.Errorf("Expected one error for non-binary characters in comment, got %v", errs)
	}

	// 5 errors expected, one for each invalid character
	if errs[0].clause != "6.1.2" || errs[0].subclause != 4 || len(errs[0].errs) != 5 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.3

func TestDocument_VerifyPDFATrailer_NoId(t *testing.T) {
	filename := "test.pdf"
	content := []byte("trailer\n<</ID a>>\nstartxref\n1111\n%%EOF")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)

	f, _ := os.Open(filename)
	info, _ := f.Stat()
	doc := &Document{file: f, trailer: trailer, info: info}
	defer doc.Close()

	errs := doc.verifyFileTrailer()
	if len(errs) != 1 {
		t.Errorf("Expected one error for missing ID key, got %v", errs)
	}

	if errs[0].clause != "6.1.3" || errs[0].subclause != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFATrailer_Encrypt(t *testing.T) {
	filename := "test.pdf"
	content := []byte("trailer\n<</Encrypt a>>\nstartxref\n1111\n%%EOF")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	trailer["ID"] = PDFString{"a"}
	trailer["Encrypt"] = PDFString{"a"}

	f, _ := os.Open(filename)
	info, _ := f.Stat()
	doc := &Document{file: f, trailer: trailer, info: info}
	defer doc.Close()

	errs := doc.verifyFileTrailer()
	if len(errs) != 1 {
		t.Errorf("Expected one error for forbidden Encrypt key, got %v", errs)
	}

	if errs[0].clause != "6.1.3" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFATrailer_InvalidEOF(t *testing.T) {
	filename := "test.pdf"
	content := []byte("trailer\n<</ID a>>\nstartxref\n1111\n%EOF")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	trailer["ID"] = PDFString{"a"}

	f, _ := os.Open(filename)
	info, _ := f.Stat()
	doc := &Document{file: f, trailer: trailer, info: info}
	defer doc.Close()

	errs := doc.verifyFileTrailer()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid EOF, got %v", errs)
	}

	if errs[0].clause != "6.1.3" || errs[0].subclause != 3 {
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
	doc := &Document{file: f, xrefOffset: 0}
	defer doc.Close()

	errs := doc.verifyCrossReferenceTable()
	if len(errs) != 1 {
		t.Errorf("Expected one error for missing xref keyword, got %v", errs)
	}

	if errs[0].clause != "6.1.4" || errs[0].subclause != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFACrossReferenceTable_MissingXrefHeader(t *testing.T) {
	filename := "test.pdf"
	content := []byte("xref")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f, xrefOffset: 0}
	defer doc.Close()

	errs := doc.verifyCrossReferenceTable()
	if len(errs) != 1 {
		t.Errorf("Expected one error for missing xref header, got %v", errs)
	}

	if errs[0].clause != "6.1.4" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFACrossReferenceTable_MultipleEOLSeperators(t *testing.T) {
	filename := "test.pdf"
	content := []byte("xref\r\n0 10 10\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f, xrefOffset: 0}
	defer doc.Close()

	errs := doc.verifyCrossReferenceTable()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid EOL, got %v", errs)
	}

	if errs[0].clause != "6.1.4" || errs[0].subclause != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.5

func TestDocument_VerifyPDFADocumentInformationDictionary_InvalidMetadata(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFArray, 0)

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyDocumentInformationDictionary()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid metadata type, got %v", errs)
	}

	if errs[0].clause != "6.1.5" || errs[0].subclause != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentInformationDictionary_DisallowedField(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["Title"] = PDFString{"Test"}
	info["Disallowed"] = PDFString{"Wrong"}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyDocumentInformationDictionary()
	if len(errs) != 1 {
		t.Errorf("Expected one error for disallowed field, got %v", errs)
	}

	if errs[0].clause != "6.1.5" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentInformationDictionary_EmptyValue(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["Title"] = PDFString{""}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyDocumentInformationDictionary()
	if len(errs) != 1 {
		t.Errorf("Expected one error for empty metadata value, got %v", errs)
	}

	if errs[0].clause != "6.1.5" || errs[0].subclause != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentInformationDictionary_DisallowedFieldAndEmptyValue(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["Wrong"] = PDFString{""}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyDocumentInformationDictionary()
	if len(errs) != 2 {
		t.Errorf("Expected two errors for invalid field and empty metadata value, got %v", errs)
	}

	if errs[0].clause != "6.1.5" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}

	if errs[1].clause != "6.1.5" || errs[1].subclause != 3 {
		t.Errorf("Got unexpected error %v", errs[1])
	}
}

// 6.1.6

func TestDocument_VerifyPDFADocumentHex_InvalidChar(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["Title"] = PDFHexString{"XXXX"}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.buildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	doc.verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid hex, got %v", errs)
	}

	// expect 4 errors for 4 invalid hex chars
	if errs[0].clause != "6.1.6" || errs[0].subclause != 1 || len(errs[0].errs) != 4 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentHex_InvalidLength(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["Title"] = PDFHexString{"AAA"}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.buildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	doc.verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for odd number of hex chars, got %v", errs)
	}

	if errs[0].clause != "6.1.6" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.7

func TestDocument_VerifyPDFADocumentHex_InvalidKeyF(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["F"] = PDFHexString{"aaaa"}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.buildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	doc.verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key F, got %v", errs)
	}

	if errs[0].clause != "6.1.7" || errs[0].subclause != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentHex_InvalidKeyFFilter(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["FFilter"] = PDFHexString{"aaaa"}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.buildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	doc.verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key FFilter, got %v", errs)
	}

	if errs[0].clause != "6.1.7" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFADocumentHex_InvalidKeyFDecodeParms(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	info := make(PDFDict)

	info["FDecodeParms"] = PDFHexString{"aaaa"}

	trailer["Info"] = info

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	graph, _ := doc.ResolveGraph()
	pageIndex, _ := doc.buildPageIndex(graph)
	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	doc.verifyDocument(graph, ctx)
	errs := ctx.errs
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid key FDecodeParms, got %v", errs)
	}

	if errs[0].clause != "6.1.7" || errs[0].subclause != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.1.13

func TestDocument_VerifyPDFAOptionalContent_OCProperties(t *testing.T) {
	filename := "test.pdf"
	if err := createValidPDF(filename); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(filename)

	doc, err := Open(filename)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer doc.Close()

	errs := doc.verifyOptionalContent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid OCProperties, got %v", errs)
	}

	if errs[0].clause != "6.1.13" || errs[0].subclause != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

// 6.2.2

func TestDocument_VerifyPDFAOutputIntent(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)

	outputIntent["Type"] = PDFString{"OutputIntent"}
	outputIntent["S"] = PDFName{"GTS_PDFA1"}
	outputIntent["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 0 {
		t.Errorf("Unexpected error: %v", errs)
	}
}

func TestDocument_VerifyPDFAOutputIntent_RealFile(t *testing.T) {
	filename := "pdfa1b.pdf"
	doc, err := Open(test_dir + filename)
	if err != nil {
		t.Fatalf("Failed to open PDF: %v", err)
	}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 0 {
		t.Errorf("Unexpected error: %v", errs)
	}
}

func TestDocument_VerifyPDFAOutputIntent_InvalidOutputIntents(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := make(PDFDict)
	outputIntent := make(PDFDict)

	outputIntents["OutputIntents"] = outputIntent

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid OutputIntents type, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 1 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_InvalidOutputIntent(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := PDFArray{}

	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid OutputIntent type, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 2 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_InvalidSType(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)
	outputIntent["S"] = PDFInteger(1)

	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for invalid S type, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 3 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongS(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)

	outputIntent["Type"] = PDFString{"OutputIntent"}
	outputIntent["S"] = PDFName{"Wrong"}
	outputIntent["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong S, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 4 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongOutputConditionIdentifier(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)

	outputIntent["Type"] = PDFString{"OutputIntent"}
	outputIntent["S"] = PDFName{"GTS_PDFA1"}
	outputIntent["OutputConditionIdentifier"] = nil
	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for nil OutputConditionIentifier, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 5 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_DifferingDestOutputProfiles(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent1 := make(PDFDict)
	destOutputProfile1 := make(PDFDict)

	destOutputProfile1["N"] = PDFInteger(1)

	outputIntent1["Type"] = PDFString{"OutputIntent"}
	outputIntent1["S"] = PDFName{"GTS_PDFA1"}
	outputIntent1["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntent1["DestOutputProfile"] = destOutputProfile1

	outputIntent2 := make(PDFDict)
	destOutputProfile2 := make(PDFDict)

	destOutputProfile2["N"] = PDFInteger(2)

	outputIntent2["Type"] = PDFString{"OutputIntent"}
	outputIntent2["S"] = PDFName{"GTS_PDFA1"}
	outputIntent2["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntent2["DestOutputProfile"] = destOutputProfile2

	outputIntents = append(outputIntents, outputIntent1, outputIntent2)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for differing DestOutputProfiles, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 6 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_DestOutputProfileWrongFormat(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)
	destOutputProfile := PDFInteger(1)

	outputIntent["Type"] = PDFString{"OutputIntent"}
	outputIntent["S"] = PDFName{"GTS_PDFA1"}
	outputIntent["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntent["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong DestOutputProfile format, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 8 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongNType(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)
	destOutputProfile := make(PDFDict)

	destOutputProfile["N"] = PDFString{"3"}

	outputIntent["Type"] = PDFString{"OutputIntent"}
	outputIntent["S"] = PDFName{"GTS_PDFA1"}
	outputIntent["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntent["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong N, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 9 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}

func TestDocument_VerifyPDFAOutputIntent_WrongN(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)
	destOutputProfile := make(PDFDict)

	destOutputProfile["N"] = PDFInteger(5)

	outputIntent["Type"] = PDFString{"OutputIntent"}
	outputIntent["S"] = PDFName{"GTS_PDFA1"}
	outputIntent["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntent["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	errs := doc.verifyOutputIntent()
	if len(errs) != 1 {
		t.Errorf("Expected one error for wrong N, got %v", errs)
	}

	if errs[0].clause != "6.2.2" || errs[0].subclause != 10 {
		t.Errorf("Got unexpected error %v", errs[0])
	}
}
