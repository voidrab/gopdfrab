package gopdfrab

import (
	"bytes"
	"unicode"
)

// TokenType identifies the type of data representing the token.
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
	Value string // In a hyper-optimized version, use []byte or start/end offsets
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

// NextToken returns the next distinct token from the stream.
func (l *Lexer) NextToken() Token {
	l.skipWhitespace()

	if l.pos >= len(l.data) {
		return Token{Type: TokenEOF}
	}

	ch := l.data[l.pos]

	switch ch {
	case '%':
		// Comment: skip until end of line
		for l.pos < len(l.data) && l.data[l.pos] != '\r' && l.data[l.pos] != '\n' {
			l.pos++
		}
		return l.NextToken() // Recurse to get the actual next token
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
		// Handle unexpected single '>' or end of hex string context
		l.pos++
		return Token{Type: TokenError, Value: ">"}
	case '[':
		l.pos++
		return Token{Type: TokenArrayStart, Value: "["}
	case ']':
		l.pos++
		return Token{Type: TokenArrayEnd, Value: "]"}
	}

	// Handle Numbers (Integers/Reals)
	if unicode.IsDigit(rune(ch)) || ch == '+' || ch == '-' || ch == '.' {
		return l.readNumber()
	}

	// Handle Keywords (true, false, null, obj, endobj, R, stream, etc.)
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
		if ch == 0 || ch == 9 || ch == 10 || ch == 12 || ch == 13 || ch == 32 {
			l.pos++
			continue
		}
		break
	}
}

func (l *Lexer) skipToDict() {
	for l.pos < len(l.data) {
		ch1 := l.data[l.pos]
		if ch1 != '<' {
			l.pos++
			continue
		}
		ch2 := l.data[l.pos]
		if ch2 != '<' {
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

// readNumber handles Integers (123) and Reals (12.34)
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
// Note: Does not currently handle nested parentheses or escaped chars for brevity
func (l *Lexer) readStringLiteral() Token {
	l.pos++ // skip '('
	start := l.pos
	depth := 1
	for l.pos < len(l.data) {
		if l.data[l.pos] == '(' {
			depth++
		} else if l.data[l.pos] == ')' {
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

// readHexString handles <AABB>
func (l *Lexer) readHexString() Token {
	l.pos++ // skip '<'
	start := l.pos
	for l.pos < len(l.data) {
		if l.data[l.pos] == '>' {
			val := string(l.data[start:l.pos])
			l.pos++                                     // skip '>'
			return Token{Type: TokenString, Value: val} // In reality, decode hex here
		}
		l.pos++
	}
	return Token{Type: TokenError, Value: "Unterminated Hex String"}
}

// --- Utilities ---

func isWhitespace(ch byte) bool {
	return ch == 0 || ch == 9 || ch == 10 || ch == 12 || ch == 13 || ch == 32
}

func isDelimiter(ch byte) bool {
	s := "()<>[]{}/%"
	return bytes.IndexByte([]byte(s), ch) != -1
}
