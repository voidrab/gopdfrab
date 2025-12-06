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

// 6.1.2

func TestDocument_VerifyPDFAHeader(t *testing.T) {
	filename := "test_pdfa.pdf"
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
	filename := "test_invalid_pdfa.pdf"
	content := []byte("1.7\n%\xA0\xA1\xA2\xA3\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}
	defer doc.Close()

	if err := doc.verifyFileHeader(); err == nil {
		t.Error("Expected error for non-binary comment, got nil")
	}
}

func TestDocument_VerifyPDFAHeader_InvalidCommentLength(t *testing.T) {
	filename := "test_invalid_pdfa.pdf"
	content := []byte("%PDF-1.7\n%\xA0\xA1\xA2\n")
	os.WriteFile(filename, content, 0644)
	defer os.Remove(filename)

	f, _ := os.Open(filename)
	doc := &Document{file: f}
	defer doc.Close()

	if err := doc.verifyFileHeader(); err == nil {
		t.Error("Expected error for non-binary comment, got nil")
	}
}

func TestDocument_VerifyPDFAHeader_InvalidCommentContent(t *testing.T) {
	filename := "test_invalid_pdfa.pdf"
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
