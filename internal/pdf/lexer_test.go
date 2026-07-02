package pdf

import (
	"bytes"
	"fmt"
	"testing"
)

func TestLexer_BasicDictionary(t *testing.T) {
	input := []byte("<< /Type /Catalog /Pages 1 0 R >>")
	l := NewLexer(bytes.NewReader(input))

	// Number tokens carry their payload in Int with an empty Value.
	expected := []struct {
		value string
		num   int
	}{
		{"<<", 0}, {"Type", 0}, {"Catalog", 0}, {"Pages", 0},
		{"", 1}, {"", 0}, {"R", 0}, {">>", 0},
	}

	for i, exp := range expected {
		tok := l.NextToken()
		if tok.Value != exp.value {
			t.Errorf("Token %d: Expected %q, got %q", i, exp.value, tok.Value)
		}
		if tok.Type == TokenInteger && tok.IntValue() != exp.num {
			t.Errorf("Token %d: IntValue = %d, want %d", i, tok.IntValue(), exp.num)
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

// TestLexer_StringLiteralDecoding confirms readStringLiteral decodes escape
// sequences and EOL normalization per ISO 32000-1 7.3.4.2, so TokenString
// (and thus PDFString.Value) holds the bytes the string represents.
func TestLexer_StringLiteralDecoding(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"escaped parens", `(Hello \(World\))`, "Hello (World)"},
		{"escaped backslash", `(A\\B)`, `A\B`},
		{"named escapes", `(a\nb\rc\td\be\ff)`, "a\nb\rc\td\be\ff"},
		{"octal escape", `(\101\102)`, "AB"},
		{"short octal terminated by non-digit", `(\12x)`, "\nx"},
		{"line continuation LF", "(ab\\\ncd)", "abcd"},
		{"line continuation CR", "(o\\\rdieresis)", "odieresis"},
		{"line continuation CRLF", "(q\\\r\n/question)", "q/question"},
		{"unescaped CR normalized to LF", "(a\rb)", "a\nb"},
		{"unescaped CRLF normalized to LF", "(a\r\nb)", "a\nb"},
		{"unknown escape drops backslash", `(a\zb)`, "azb"},
		{"nested parens", `(a(b)c)`, "a(b)c"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLexer(bytes.NewReader([]byte(tc.input)))
			tok := l.NextToken()
			if tok.Type != TokenString {
				t.Fatalf("token type = %v, want TokenString", tok.Type)
			}
			if tok.Value != tc.want {
				t.Errorf("decoded string = %q, want %q", tok.Value, tc.want)
			}
		})
	}
}
