package pdfrab

import (
	"bytes"
	"encoding/hex"
	"unicode"
)

type TokenType int

const (
	TokenError TokenType = iota
	TokenEOF
	TokenBoolean
	TokenInteger
	TokenReal
	TokenString // (literal) or <hex>
	TokenName   // /Name
	TokenKeyword
	TokenArrayStart // [
	TokenArrayEnd   // ]
	TokenDictStart  // <<
	TokenDictEnd    // >>
)

// Token represents a distinct piece of syntax from the PDF.
type Token struct {
	Type  TokenType
	Value string
}

// Lexer holds the state of the current chunk being parsed.
type Lexer struct {
	data []byte
	pos  int
}

// NewLexer creates a lexer for a specific chunk of data.
func NewLexer(data []byte) *Lexer {
	return &Lexer{data: data, pos: 0}
}

func (l *Lexer) JumpBack(n int) {
	if l.pos-n >= 0 {
		l.pos -= n
	}
}

// NextToken returns the next distinct token from the stream.
func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	if l.pos >= len(l.data) {
		return Token{Type: TokenEOF}
	}

	ch := l.data[l.pos]

	switch ch {
	case '%': // skip comment
		for l.pos < len(l.data) && l.data[l.pos] != '\r' && l.data[l.pos] != '\n' {
			l.pos++
		}
		return l.NextToken()
	case '/':
		return l.readName()
	case '(':
		return l.readStringLiteral()
	case '<':
		// Could be Dict Start (<<) or Hex String (<A0>)
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '<' {
			l.pos += 2
			return Token{Type: TokenDictStart, Value: "<<"}
		}
		return l.readHexString()
	case '>':
		if l.pos+1 < len(l.data) && l.data[l.pos+1] == '>' {
			l.pos += 2
			return Token{Type: TokenDictEnd, Value: ">>"}
		}
		l.pos++
		return Token{Type: TokenError, Value: ">"}
	case '[':
		l.pos++
		return Token{Type: TokenArrayStart, Value: "["}
	case ']':
		l.pos++
		return Token{Type: TokenArrayEnd, Value: "]"}
	}

	if unicode.IsDigit(rune(ch)) || ch == '+' || ch == '-' || ch == '.' {
		return l.readNumber()
	}

	if unicode.IsLetter(rune(ch)) {
		return l.readKeyword()
	}

	return Token{Type: TokenError, Value: string(ch)}
}

// --- Helper Functions ---

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.data) {
		ch := l.data[l.pos]
		// PDF WhiteSpace: Null, Tab, LF, FF, CR, Space
		if isWhitespace(ch) {
			l.pos++
			continue
		}
		break
	}
}

// readName handles /Name tokens (e.g., /Type, /Pages)
func (l *Lexer) readName() Token {
	l.pos++ // skip '/'
	start := l.pos
	for l.pos < len(l.data) {
		ch := l.data[l.pos]
		// Names end at whitespace or delimiters
		if isDelimiter(ch) || isWhitespace(ch) {
			break
		}
		l.pos++
	}
	return Token{Type: TokenName, Value: string(l.data[start:l.pos])}
}

// readNumber handles integers (123) and reals (12.34)
func (l *Lexer) readNumber() Token {
	start := l.pos
	isReal := false
	for l.pos < len(l.data) {
		ch := l.data[l.pos]
		if ch == '.' {
			isReal = true
		}
		if !unicode.IsDigit(rune(ch)) && ch != '.' && ch != '+' && ch != '-' {
			break
		}
		l.pos++
	}
	val := string(l.data[start:l.pos])
	if isReal {
		return Token{Type: TokenReal, Value: val}
	}
	return Token{Type: TokenInteger, Value: val}
}

// readKeyword handles true, false, R, obj, etc.
func (l *Lexer) readKeyword() Token {
	start := l.pos
	for l.pos < len(l.data) {
		ch := l.data[l.pos]
		if isDelimiter(ch) || isWhitespace(ch) {
			break
		}
		l.pos++
	}
	val := string(l.data[start:l.pos])

	if val == "true" || val == "false" {
		return Token{Type: TokenBoolean, Value: val}
	}
	return Token{Type: TokenKeyword, Value: val}
}

// readStringLiteral handles (Hello World)
func (l *Lexer) readStringLiteral() Token {
	l.pos++ // skip '('
	start := l.pos
	depth := 1
	for l.pos < len(l.data) {
		switch l.data[l.pos] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				val := string(l.data[start:l.pos])
				l.pos++ // skip closing ')'
				return Token{Type: TokenString, Value: val}
			}
		}
		l.pos++
	}
	return Token{Type: TokenError, Value: "Unterminated String"}
}

func (l *Lexer) readHexString() Token {
	l.pos++ // skip '<'

	var buf []byte

	for l.pos < len(l.data) {
		ch := l.data[l.pos]

		if ch == '>' {
			l.pos++ // skip '>'
			break
		}

		// Skip any PDF whitespace
		if isWhitespace(ch) {
			l.pos++
			continue
		}

		// Only allow hex digits
		if !isHexDigit(ch) {
			return Token{Type: TokenError, Value: "Invalid character in hex string"}
		}

		buf = append(buf, ch)
		l.pos++
	}

	// If we exited the loop without finding '>'
	if l.pos >= len(l.data) {
		return Token{Type: TokenError, Value: "Unterminated hex string"}
	}

	// if odd number of hex digits, pad with '0'
	if len(buf)%2 == 1 {
		buf = append(buf, '0')
	}

	decoded, err := hex.DecodeString(string(buf))
	if err != nil {
		return Token{Type: TokenError, Value: "Invalid hex data"}
	}

	return Token{Type: TokenString, Value: string(decoded)}
}

// --- Utilities ---

func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') ||
		(ch >= 'A' && ch <= 'F') ||
		(ch >= 'a' && ch <= 'f')
}

func isWhitespace(ch byte) bool {
	return ch == 0 || ch == 9 || ch == 10 || ch == 12 || ch == 13 || ch == 32
}

func isDelimiter(ch byte) bool {
	s := "()<>[]{}/%"
	return bytes.IndexByte([]byte(s), ch) != -1
}
