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

// TestLexerTokenTypes tokenizes a stream spanning every scalar token kind,
// covering the NextToken dispatch branches.
func TestLexerTokenTypes(t *testing.T) {
	src := []byte("<< /Name 42 -3.14 (lit) <4869> true false null [ 1 2 ] >> keyword")
	want := []TokenType{
		TokenDictStart, TokenName, TokenInteger, TokenReal, TokenString,
		TokenHexString, TokenBoolean, TokenBoolean, TokenKeyword,
		TokenArrayStart, TokenInteger, TokenInteger, TokenArrayEnd,
		TokenDictEnd, TokenKeyword,
	}
	lex := NewLexerBytes(src, 0)
	var got []TokenType
	for {
		tok := lex.NextToken()
		if tok.Type == TokenEOF {
			break
		}
		got = append(got, tok.Type)
	}
	if len(got) != len(want) {
		t.Fatalf("token count = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestTokenIntRealValue covers IntValue/RealValue for both the fast (parsed)
// path and the malformed-number (raw Value) fallback.
func TestTokenIntRealValue(t *testing.T) {
	lex := NewLexerBytes([]byte("42 3.5"), 0)
	if v := lex.NextToken().IntValue(); v != 42 {
		t.Errorf("IntValue = %d, want 42", v)
	}
	if v, err := lex.NextToken().RealValue(); err != nil || v != 3.5 {
		t.Errorf("RealValue = %v, %v; want 3.5", v, err)
	}

	// Malformed real "1.2.3": kept as a raw-Value token.
	m := NewLexerBytes([]byte("1.2.3"), 0).NextToken()
	if _, err := m.RealValue(); err == nil {
		t.Error("RealValue(1.2.3) should error")
	}
	if v := m.IntValue(); v != 0 {
		t.Errorf("IntValue(1.2.3) = %d, want 0", v)
	}
}

// TestNewLexerBytesNil covers the nil-data guard.
func TestNewLexerBytesNil(t *testing.T) {
	if tok := NewLexerBytes(nil, 0).NextToken(); tok.Type != TokenEOF {
		t.Errorf("NextToken(nil lexer) = %v, want EOF", tok.Type)
	}
}

// TestLexerReaderPath drives the io.Reader-backed lexer, covering peekByte and
// NextToken's reader branches.
func TestLexerReaderPath(t *testing.T) {
	lex := NewLexer(bytes.NewReader([]byte("<< /K 7 >>")))
	defer lex.Release()
	want := []TokenType{TokenDictStart, TokenName, TokenInteger, TokenDictEnd}
	for i, w := range want {
		if tok := lex.NextToken(); tok.Type != w {
			t.Errorf("reader token[%d] = %v, want %v", i, tok.Type, w)
		}
	}
	if tok := lex.NextToken(); tok.Type != TokenEOF {
		t.Errorf("final token = %v, want EOF", tok.Type)
	}
}
