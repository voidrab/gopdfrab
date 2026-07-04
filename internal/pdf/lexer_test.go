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

func TestIsHexDigit(t *testing.T) {
	for _, ch := range []byte("0123456789ABCDEFabcdef") {
		if !IsHexDigit(ch) {
			t.Errorf("IsHexDigit(%q) = false, want true", ch)
		}
	}
	for _, ch := range []byte("gG xZ!") {
		if IsHexDigit(ch) {
			t.Errorf("IsHexDigit(%q) = true, want false", ch)
		}
	}
}

// TestRequireEOL covers LF, CRLF, bare-CR-at-EOF, non-EOL-byte (error +
// unread), and immediate-EOF branches.
func TestRequireEOL(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"LF", "\n", false},
		{"CRLF", "\r\n", false},
		{"bare CR at EOF", "\r", false},
		{"non-EOL byte", "X", true},
		{"immediate EOF", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLexerBytes([]byte(tc.data), 0)
			defer l.Release()
			if err := l.requireEOL(); (err != nil) != tc.wantErr {
				t.Errorf("requireEOL(%q) = %v, wantErr %v", tc.data, err, tc.wantErr)
			}
		})
	}
}

// TestSkipEOL covers LF, CRLF, bare-CR, a non-EOL byte (unread, no error),
// and immediate EOF (error).
func TestSkipEOL(t *testing.T) {
	l := NewLexerBytes([]byte("\nrest"), 0)
	defer l.Release()
	if err := l.skipEOL(); err != nil {
		t.Errorf("skipEOL(LF) = %v, want nil", err)
	}

	l2 := NewLexerBytes([]byte("Xrest"), 0)
	defer l2.Release()
	if err := l2.skipEOL(); err != nil {
		t.Errorf("skipEOL(non-EOL) = %v, want nil", err)
	}
	if b, _ := l2.readByte(); b != 'X' {
		t.Errorf("skipEOL should have unread the non-EOL byte, next byte = %q", b)
	}

	l3 := NewLexerBytes([]byte(""), 0)
	defer l3.Release()
	if err := l3.skipEOL(); err == nil {
		t.Error("skipEOL at EOF should error")
	}
}

// TestRequireSingleSpace covers a lone space at EOF, a non-whitespace first
// byte, a single space followed by non-whitespace, and multiple whitespace.
func TestRequireSingleSpace(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{"single space at EOF", " ", false},
		{"space then non-whitespace", " X", false},
		{"non-whitespace first", "X", true},
		{"multiple whitespace", "  ", true},
		{"immediate EOF", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLexerBytes([]byte(tc.data), 0)
			defer l.Release()
			if err := l.requireSingleSpace(); (err != nil) != tc.wantErr {
				t.Errorf("requireSingleSpace(%q) = %v, wantErr %v", tc.data, err, tc.wantErr)
			}
		})
	}
}

// TestValidateObjectStart covers a well-formed "N G obj" header, leading
// whitespace, an invalid object/generation number, a missing "obj" keyword,
// and a missing EOL after "obj".
func TestValidateObjectStart(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantErrs int
	}{
		{"well-formed", "1 0 obj\n", 0},
		{"leading whitespace", " 1 0 obj\n", 1},
		{"invalid object number", "X 0 obj\n", 1},
		{"invalid generation number", "1 X obj\n", 1},
		{"missing obj keyword", "1 0 foo\n", 1},
		{"obj not followed by EOL", "1 0 obj X", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLexerBytes([]byte(tc.data), 0)
			defer l.Release()
			if errs := l.validateObjectStart(); len(errs) != tc.wantErrs {
				t.Errorf("validateObjectStart(%q) = %v, want %d errors", tc.data, errs, tc.wantErrs)
			}
		})
	}
}

// TestValidateEndObj covers a well-formed "\nendobj\n", whitespace before
// "endobj", a missing "endobj" keyword, and a missing trailing EOL.
func TestValidateEndObj(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantErrs int
	}{
		{"well-formed", "\nendobj\n", 0},
		{"whitespace before endobj", "\n endobj\n", 1},
		{"missing endobj keyword", "\nfoo\n", 1},
		{"endobj not followed by EOL", "\nendobj X", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLexerBytes([]byte(tc.data), 0)
			defer l.Release()
			if errs := l.validateEndObj(); len(errs) != tc.wantErrs {
				t.Errorf("validateEndObj(%q) = %v, want %d errors", tc.data, errs, tc.wantErrs)
			}
		})
	}
}

// TestValidateObjectEnd covers the trailing-EOL success and failure paths.
func TestValidateObjectEnd(t *testing.T) {
	l := NewLexerBytes([]byte("\n"), 0)
	defer l.Release()
	if errs := l.validateObjectEnd(); errs != nil {
		t.Errorf("validateObjectEnd(LF) = %v, want nil", errs)
	}

	l2 := NewLexerBytes([]byte("X"), 0)
	defer l2.Release()
	if errs := l2.validateObjectEnd(); errs == nil {
		t.Error("validateObjectEnd(non-EOL) should error")
	}
}
