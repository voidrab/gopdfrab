package pdfrab

import (
	"fmt"
	"os"
	"testing"
)

var test_dic = "test documents/"

func TestLexer_BasicDictionary(t *testing.T) {
	input := []byte("<< /Type /Catalog /Pages 1 0 R >>")
	l := NewLexer(input)

	expected := []string{"<<", "Type", "Catalog", "Pages", "1", "0", "R", ">>"}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Value != exp {
			t.Errorf("Token %d: Expected %q, got %q", i, exp, tok.Value)
		}
	}
}

func TestLexer_ArraysAndStrings(t *testing.T) {
	input := []byte("[ (Hello World) <41 41 42 42> ]")
	l := NewLexer(input)

	expectedTokens := []struct {
		Type  TokenType
		Value string
	}{
		{TokenArrayStart, "["},
		{TokenString, "Hello World"},
		{TokenString, "AABB"},
		{TokenArrayEnd, "]"},
	}

	for i, expected := range expectedTokens {
		tok := l.NextToken()
		if tok.Type != expected.Type {
			t.Errorf("Token %d: Expected Type %v, got %v", i, expected.Type, tok.Type)
		}
		if tok.Value != expected.Value {
			t.Errorf("Token %d: Expected Value %q, got %q", i, expected.Value, tok.Value)
		}
	}
}

func createValidPDF(filename string) error {
	header := "%PDF-1.7\n"

	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Count 5 >>\nendobj\n"
	obj3 := "3 0 obj\n<< /Title (Test PDF) /Producer (GoLib) >>\nendobj\n"

	offset1 := len(header)
	offset2 := offset1 + len(obj1)
	offset3 := offset2 + len(obj2)
	xrefOffset := offset3 + len(obj3)

	xref := fmt.Sprintf("xref\n0 4\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n",
		offset1, offset2, offset3)

	trailer := "trailer\n<< /Size 4 /Root 1 0 R /Info 3 0 R >>\n"
	startxref := fmt.Sprintf("startxref\n%d\n%%EOF", xrefOffset)

	content := header + obj1 + obj2 + obj3 + xref + trailer + startxref
	return os.WriteFile(filename, []byte(content), 0644)
}

func TestDocument_OpenAndRead(t *testing.T) {
	filename := "test_full.pdf"
	if err := createValidPDF(filename); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(filename)

	doc, err := Open(filename)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer doc.Close()

	if doc.info.Size() == 0 {
		t.Error("Document size reported as 0")
	}

	meta, err := doc.GetMetadata()
	if err != nil {
		t.Errorf("GetMetadata error: %v", err)
	}
	if meta["Title"] != "Test PDF" {
		t.Errorf("Expected Title 'Test PDF', got %v", meta["Title"])
	}

	count, err := doc.GetPageCount()
	if err != nil {
		t.Errorf("GetPageCount error: %v", err)
	}
	if count != 5 {
		t.Errorf("Expected 5 pages, got %d", count)
	}
}

func TestDocument_Open(t *testing.T) {
	filename := "test_min.pdf"

	if err := createValidPDF(filename); err != nil {
		t.Fatalf("Failed to create test PDF: %v", err)
	}
	defer os.Remove(filename) // Cleanup after test

	doc, err := Open(filename)
	if err != nil {
		t.Fatalf("Failed to open valid PDF: %v", err)
	}
	defer doc.Close()

	if doc.info.Size() == 0 {
		t.Error("Document size should not be 0")
	}

	fmt.Printf("Successfully opened PDF. Size: %d bytes\n", doc.info.Size())
}

func TestDocument_OpenReal(t *testing.T) {
	filename := "test.pdf"

	doc, err := Open(test_dic + filename)
	if err != nil {
		t.Fatalf("Failed to open valid PDF: %v", err)
	}
	defer doc.Close()

	if doc.info.Size() == 0 {
		t.Error("Document size should not be 0")
	}

	fmt.Printf("Successfully opened PDF. Size: %d bytes. Modified: %v\n", doc.info.Size(), doc.info.ModTime())

	count, err := doc.GetPageCount()
	if err != nil {
		t.Errorf("GetPageCount error: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 page, got %d", count)
	}
}

func TestDocument_GetVersion(t *testing.T) {
	filename := "test.pdf"

	doc, err := Open(test_dic + filename)
	if err != nil {
		t.Fatalf("Failed to open valid PDF: %v", err)
	}
	defer doc.Close()

	if doc.info.Size() == 0 {
		t.Error("Document size should not be 0")
	}

	version, err := doc.GetVersion()
	if err != nil {
		t.Errorf("GetVersion error: %v", err)
	}
	if version != "1.7" {
		t.Errorf("Expected PDF version 1.7, got %v", version)
	}
}

func TestDocument_OpenInvalid(t *testing.T) {
	filename := "test_invalid.pdf"
	os.WriteFile(filename, []byte("Not a PDF file"), 0644)
	defer os.Remove(filename)

	_, err := Open(filename)
	if err == nil {
		t.Error("Expected error when opening invalid PDF, got nil")
	}
}
