package pdfrab

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"unicode"
)

type TokenType int

const (
	TokenError TokenType = iota
	TokenEOF
	TokenBoolean
	TokenInteger
	TokenReal
	TokenString    // (literal)
	TokenHexString // <hex>
	TokenName      // /Name
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
	reader *bufio.Reader
	pos    int
	pushed []Token
}

// NewLexer creates a lexer for a specific chunk of data.
func NewLexer(r io.Reader) *Lexer {
	return &Lexer{reader: bufio.NewReader(r)}
}

// NextToken returns the next distinct token from the stream.
func (l *Lexer) NextToken() Token {
	if len(l.pushed) > 0 {
		t := l.pushed[len(l.pushed)-1]
		l.pushed = l.pushed[:len(l.pushed)-1]
		return t
	}

	// add stream support
	// skip content when stream is encountered?

	l.skipWhitespace()

	ch, err := l.readByte()
	if err == io.EOF {
		return Token{Type: TokenEOF}
	}
	if err != nil {
		return Token{Type: TokenError, Value: err.Error()}
	}

	switch ch {
	case '%': // comment
		for {
			b, err := l.readByte()
			if err != nil || b == '\n' || b == '\r' {
				break
			}
		}
		return l.NextToken()
	case '/':
		return l.readName()
	case '(':
		return l.readStringLiteral()
	case '<':
		b, err := l.readByte()
		if err == nil && b == '<' {
			return Token{Type: TokenDictStart, Value: "<<"}
		}
		if err == nil {
			l.unreadByte()
		}
		return l.readHexString()
	case '>':
		b, err := l.readByte()
		if err == nil && b == '>' {
			return Token{Type: TokenDictEnd, Value: ">>"}
		}
		if err == nil {
			l.unreadByte()
		}
		return Token{Type: TokenError, Value: ">"}
	case '[':
		return Token{Type: TokenArrayStart, Value: "["}
	case ']':
		return Token{Type: TokenArrayEnd, Value: "]"}
	}

	if unicode.IsDigit(rune(ch)) || ch == '+' || ch == '-' || ch == '.' {
		l.unreadByte()
		return l.readNumber()
	}

	if unicode.IsLetter(rune(ch)) {
		l.unreadByte()
		return l.readKeyword()
	}

	return Token{Type: TokenError, Value: string(ch)}
}

func (l *Lexer) UnreadToken(t Token) {
	l.pushed = append(l.pushed, t)
}

// --- Helper Functions ---

func (l *Lexer) readByte() (byte, error) {
	b, err := l.reader.ReadByte()
	if err == nil {
		l.pos++
	}
	return b, err
}

func (l *Lexer) unreadByte() error {
	err := l.reader.UnreadByte()
	if err == nil {
		l.pos--
	}
	return err
}

func (l *Lexer) skipWhitespace() {
	for {
		b, err := l.readByte()
		if err != nil {
			return
		}
		if !isWhitespace(b) {
			l.unreadByte()
			return
		}
	}
}

// readName handles name tokens like /Name
func (l *Lexer) readName() Token {
	var buf []byte
	for {
		b, err := l.readByte()
		if err != nil || isDelimiter(b) || isWhitespace(b) {
			if err == nil {
				l.unreadByte()
			}
			break
		}
		buf = append(buf, b)
	}
	return Token{Type: TokenName, Value: string(buf)}
}

// readNumber handles integers and reals
func (l *Lexer) readNumber() Token {
	var buf []byte
	isReal := false

	for {
		b, err := l.readByte()
		if err != nil {
			break
		}
		if b == '.' {
			isReal = true
		}
		if !unicode.IsDigit(rune(b)) && b != '.' && b != '+' && b != '-' {
			l.unreadByte()
			break
		}
		buf = append(buf, b)
	}

	if isReal {
		return Token{Type: TokenReal, Value: string(buf)}
	}
	return Token{Type: TokenInteger, Value: string(buf)}
}

// readKeyword handles keywords like true, false, obj, etc.
func (l *Lexer) readKeyword() Token {
	var buf []byte
	for {
		b, err := l.readByte()
		if err != nil || isDelimiter(b) || isWhitespace(b) {
			if err == nil {
				l.unreadByte()
			}
			break
		}
		buf = append(buf, b)
	}
	val := string(buf)
	if val == "true" || val == "false" {
		return Token{Type: TokenBoolean, Value: val}
	}
	return Token{Type: TokenKeyword, Value: val}
}

// readStringLiteral handles string literals like (Hello World)
func (l *Lexer) readStringLiteral() Token {
	var buf []byte
	depth := 1

	for {
		b, err := l.readByte()
		if err != nil {
			return Token{Type: TokenError, Value: fmt.Sprintf("Unterminated String: %v", err)}
		}
		switch b {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return Token{Type: TokenString, Value: string(buf)}
			}
		}
		buf = append(buf, b)
	}
}

func (l *Lexer) readHexString() Token {
	var buf []byte

	for {
		b, err := l.readByte()
		if err != nil {
			return Token{Type: TokenError, Value: "Unterminated hex string"}
		}
		if b == '>' {
			break
		}
		if isWhitespace(b) {
			continue
		}
		if !isHexDigit(b) {
			return Token{Type: TokenError, Value: "Invalid character in hex string"}
		}
		buf = append(buf, b)
	}

	return Token{Type: TokenHexString, Value: string(buf)}
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
