package gopdfrab

import (
	"fmt"
	"os"
	"testing"
)

func TestLexer_BasicDictionary(t *testing.T) {
	input := []byte("<< /Type /Catalog /Pages 1 0 R >>")
	l := NewLexer(input)

	expectedTokens := []struct {
		Type  TokenType
		Value string
	}{
		{TokenDictStart, "<<"},
		{TokenName, "Type"},
		{TokenName, "Catalog"},
		{TokenName, "Pages"},
		{TokenInteger, "1"},
		{TokenInteger, "0"},
		{TokenKeyword, "R"},
		{TokenDictEnd, ">>"},
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

func TestLexer_ArraysAndStrings(t *testing.T) {
	input := []byte("[ (Hello World) <AABB> ]")
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

func createDummyPDF(filename string) error {
	// A minimal PDF structure
	content := `%PDF-1.7
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Count 1 >>
endobj
trailer
<< /Size 3 /Root 1 0 R >>
startxref
123
%%EOF`

	return os.WriteFile(filename, []byte(content), 0644)
}

func TestDocument_Open(t *testing.T) {
	filename := "test_min.pdf"

	if err := createDummyPDF(filename); err != nil {
		t.Fatalf("Failed to create test PDF: %v", err)
	}
	defer os.Remove(filename) // Cleanup after test

	doc, err := Open(filename)
	if err != nil {
		t.Fatalf("Failed to open valid PDF: %v", err)
	}
	defer doc.Close()

	if doc.size == 0 {
		t.Error("Document size should not be 0")
	}

	fmt.Printf("Successfully opened PDF. Size: %d bytes\n", doc.size)
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
