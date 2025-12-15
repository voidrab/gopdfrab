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

	if res.Issues["6.1.6"] == nil {
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

	if err := doc.verifyFileHeader(); err == nil {
		t.Error("Expected error for invalid header, got nil")
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

	if err := doc.verifyFileHeader(); err == nil {
		t.Error("Expected error for short comment, got nil")
	}
}

func TestDocument_VerifyPDFAHeader_InvalidCommentContent(t *testing.T) {
	filename := "test.pdf"
	content := []byte("%PDF-1.7\n%CommentWithoutBinary\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}
	defer doc.Close()

	if err := doc.verifyFileHeader(); err == nil {
		t.Error("Expected error for non-binary comment, got nil")
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

	if err := doc.verifyFileTrailer(); err == nil {
		t.Error("Expected error for missing ID, got nil")
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

	if err := doc.verifyFileTrailer(); err == nil {
		t.Error("Expected error for disallowed Encrypt, got nil")
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

	if err := doc.verifyFileTrailer(); err == nil {
		t.Error("Expected error for invalid EOF, got nil")
	}
}

// 6.1.4

func TestDocument_VerifyPDFACrossReferenceTable_MultipleEOLSeperators(t *testing.T) {
	filename := "test.pdf"
	content := []byte("xref\n\n0 10\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f, xrefOffset: 0}
	defer doc.Close()

	if err := doc.verifyCrossReferenceTable(); err == nil {
		t.Error("Expected error for invalid EOL, got nil")
	}
}

func TestDocument_VerifyPDFACrossReferenceTable_SubsectionHeader(t *testing.T) {
	filename := "test.pdf"
	content := []byte("xref\r\n0 10 10\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f, xrefOffset: 0}
	defer doc.Close()

	if err := doc.verifyCrossReferenceTable(); err == nil {
		t.Error("Expected error for invalid subsection header, got nil")
	}
}

// 6.1.5

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

	if err := doc.verifyDocumentInformationDictionary(); err == nil {
		t.Error("Expected error for disallowed field, got nil")
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

	if err := doc.verifyDocumentInformationDictionary(); err == nil {
		t.Error("Expected error for empty field, got nil")
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

	if err := doc.verifyOptionalContent(); err == nil {
		t.Error("Expected error for invalid OCProperties, got nil")
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

	if err := doc.verifyOutputIntent(); err != nil {
		t.Errorf("Unexpected error: %v", err)
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

	if errs != nil {
		t.Errorf("Verification failed for conforming PDF: %v", err)
	}
}

func TestDocument_VerifyPDFAOutputIntent_NoOutputConditionIdentifier(t *testing.T) {
	filename := "test.pdf"
	content := []byte("")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	trailer := make(PDFDict)
	outputIntents := PDFArray{}
	outputIntent := make(PDFDict)

	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	if err := doc.verifyOutputIntent(); err == nil {
		t.Error("Expected error for no OutputConditionIdentifier, got nil")
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

	if err := doc.verifyOutputIntent(); err == nil {
		t.Error("Expected error for wrong type, got nil")
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

	destOutputProfile["N"] = PDFString{"5"}

	outputIntent["Type"] = PDFString{"OutputIntent"}
	outputIntent["S"] = PDFName{"GTS_PDFA1"}
	outputIntent["OutputConditionIdentifier"] = PDFString{"Test"}
	outputIntent["DestOutputProfile"] = destOutputProfile
	outputIntents = append(outputIntents, outputIntent)

	trailer["Root"] = outputIntents

	f, _ := os.Open(filename)
	doc := &Document{file: f, trailer: trailer}
	defer doc.Close()

	if err := doc.verifyOutputIntent(); err == nil {
		t.Error("Expected error for wrong N, got nil")
	}
}
