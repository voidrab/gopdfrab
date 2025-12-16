package pdfrab

import (
	"bytes"
	"fmt"
	"testing"
)

func TestLexer_BasicDictionary(t *testing.T) {
	input := []byte("<< /Type /Catalog /Pages 1 0 R >>")
	l := NewLexer(bytes.NewReader(input))

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
	l := NewLexer(bytes.NewReader(input))

	expectedTokens := []struct {
		Type  TokenType
		Value string
	}{
		{TokenArrayStart, "["},
		{TokenString, "Hello World"},
		{TokenHexString, fmt.Sprintf("%X", "AABB")},
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
