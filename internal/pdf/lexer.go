package pdf

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync"
	"unsafe"
)

// Token represents a distinct piece of syntax from the PDF.
type Token struct {
	Type  TokenType
	Value string
}

// Lexer holds the state of the current chunk being parsed.
// data is non-nil when lexing a []byte directly (fast path); reader is used
// for the io.Reader fallback (trailer parsing, object streams).
type Lexer struct {
	data   []byte
	reader *bufio.Reader
	pos    int64
	pushed []Token
}

// NewLexerBytes creates a fast lexer that indexes data starting at startPos.
func NewLexerBytes(data []byte, startPos int64) *Lexer {
	if data == nil {
		data = []byte{}
	}
	return &Lexer{data: data, pos: startPos}
}

// peekByte returns the next byte without advancing the position.
func (l *Lexer) peekByte() (byte, bool) {
	if l.data != nil {
		if l.pos >= int64(len(l.data)) {
			return 0, false
		}
		return l.data[l.pos], true
	}
	b, err := l.reader.Peek(1)
	if err != nil || len(b) == 0 {
		return 0, false
	}
	return b[0], true
}

// bufioReaderPool reuses buffers across Lexer construction.
var bufioReaderPool = sync.Pool{
	New: func() any { return bufio.NewReaderSize(emptyLexerReader, 4096) },
}

// emptyLexerReader is a shared, never-mutated zero-length reader used to
// detach a released bufio.Reader from its previous source before returning
// it to the pool, so the pool doesn't pin large byte slices in memory.
var emptyLexerReader = bytes.NewReader(nil)

func acquireBufioReader(r io.Reader) *bufio.Reader {
	br := bufioReaderPool.Get().(*bufio.Reader)
	br.Reset(r)
	return br
}

// NewLexer creates a lexer for a specific chunk of data.
func NewLexer(r io.Reader) *Lexer {
	return &Lexer{reader: acquireBufioReader(r)}
}

func NewLexerAt(r io.Reader, offset int64) *Lexer {
	return &Lexer{reader: acquireBufioReader(r), pos: offset}
}

// Release returns l's underlying bufio.Reader to the pool for reuse by a
// later Lexer. Callers that construct a short-lived Lexer (the common case)
// should defer Release once the lexer is no longer needed.
func (l *Lexer) Release() {
	if l.reader == nil {
		return
	}
	l.reader.Reset(emptyLexerReader)
	bufioReaderPool.Put(l.reader)
	l.reader = nil
}

// NextToken returns the next distinct token from the stream.
func (l *Lexer) NextToken() Token {
	if len(l.pushed) > 0 {
		t := l.pushed[len(l.pushed)-1]
		l.pushed = l.pushed[:len(l.pushed)-1]
		return t
	}

	l.skipWhitespace()

	ch, err := l.readByte()
	if err == io.EOF {
		return Token{Type: TokenEOF}
	}
	if err != nil {
		return Token{Type: TokenError, Value: err.Error()}
	}

	switch ch {
	case '%':
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

	if (ch >= '0' && ch <= '9') || ch == '+' || ch == '-' || ch == '.' {
		l.unreadByte()
		return l.readNumber()
	}

	if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
		l.unreadByte()
		return l.readKeyword()
	}

	return Token{Type: TokenError, Value: string(ch)}
}

func (l *Lexer) UnreadToken(t Token) {
	l.pushed = append(l.pushed, t)
}

// validateObjectStart validates the "N G obj" header framing required by 6.1.8.
// Advances past the header even on violations, so resolution can continue.
func (l *Lexer) validateObjectStart() []error {
	var errs []error

	// The object shall begin at the cross-reference offset with no leading
	// white space.
	if b, ok := l.peekByte(); ok && IsWhitespace(b) {
		errs = append(errs, fmt.Errorf("object header preceded by white space"))
	}

	objTok := l.NextToken()
	if objTok.Type != TokenInteger {
		return append(errs, fmt.Errorf("invalid object number: %q", objTok.Value))
	}

	if err := l.requireSingleSpace(); err != nil {
		errs = append(errs, err)
	}

	genTok := l.NextToken()
	if genTok.Type != TokenInteger {
		return append(errs, fmt.Errorf("invalid generation number: %q", genTok.Value))
	}

	if err := l.requireSingleSpace(); err != nil {
		errs = append(errs, err)
	}

	objKw := l.NextToken()
	if objKw.Type != TokenObjectStart {
		return append(errs, fmt.Errorf("expected 'obj' keyword, got %q", objKw.Value))
	}

	if err := l.requireEOL(); err != nil {
		errs = append(errs, fmt.Errorf("'obj' keyword not followed by single EOL: %v", err))
	}

	return errs
}

// validateEndObj validates the framing around an unconsumed endobj keyword (6.1.8).
// The lexer must be positioned immediately after the object body.
func (l *Lexer) validateEndObj() []error {
	var errs []error

	if err := l.requireEOL(); err != nil {
		errs = append(errs, fmt.Errorf("endobj not preceded by single EOL: %v", err))
	}

	if b, ok := l.peekByte(); ok && IsWhitespace(b) {
		errs = append(errs, fmt.Errorf("white space before endobj keyword"))
	}

	tok := l.NextToken()
	if tok.Type != TokenObjectEnd {
		return append(errs, fmt.Errorf("expected endobj, got %q", tok.Value))
	}

	if err := l.requireEOL(); err != nil {
		errs = append(errs, fmt.Errorf("endobj not followed by single EOL: %v", err))
	}

	return errs
}

// validateObjectEnd validates the EOL after an already-consumed endobj keyword (6.1.8).
func (l *Lexer) validateObjectEnd() []error {
	if err := l.requireEOL(); err != nil {
		return []error{fmt.Errorf("endobj not followed by single EOL: %v", err)}
	}
	return nil
}

// --- Helper Functions ---

func (l *Lexer) readByte() (byte, error) {
	if l.data != nil {
		if l.pos >= int64(len(l.data)) {
			return 0, io.EOF
		}
		b := l.data[l.pos]
		l.pos++
		return b, nil
	}
	b, err := l.reader.ReadByte()
	if err == nil {
		l.pos++
	}
	return b, err
}

func (l *Lexer) unreadByte() error {
	if l.data != nil {
		if l.pos <= 0 {
			return io.ErrUnexpectedEOF
		}
		l.pos--
		return nil
	}
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
		if !IsWhitespace(b) {
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
		if err != nil || isDelimiter(b) || IsWhitespace(b) {
			if err == nil {
				l.unreadByte()
			}
			break
		}
		buf = append(buf, b)
	}
	return Token{Type: TokenName, Value: internName(buf)}
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
		if !(b >= '0' && b <= '9') && b != '.' && b != '+' && b != '-' {
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
		if err != nil || isDelimiter(b) || IsWhitespace(b) {
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

	if val == "obj" {
		return Token{Type: TokenObjectStart, Value: val}
	}
	if val == "endobj" {
		return Token{Type: TokenObjectEnd, Value: val}
	}

	if val == "stream" {
		return Token{Type: TokenStreamStart, Value: val}
	}
	if val == "endstream" {
		return Token{Type: TokenStreamEnd, Value: val}
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

// readHexString reads a hex string up to the closing '>'. Invalid hex digits
// are preserved so callers (validateHexString) can report a precise 6.1.6 violation.
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
		if IsWhitespace(b) {
			continue
		}
		buf = append(buf, b)
	}

	return Token{Type: TokenHexString, Value: string(buf)}
}

func (l *Lexer) skipEOL() error {
	b, err := l.readByte()
	if err != nil {
		return err
	}

	if b == '\r' {
		if next, ok := l.peekByte(); ok && next == '\n' {
			l.readByte()
		}
		return nil
	}

	if b == '\n' {
		return nil
	}

	return l.unreadByte()
}

func (l *Lexer) requireSingleSpace() error {
	b, err := l.readByte()
	if err != nil {
		return err
	}
	if !IsWhitespace(b) {
		return fmt.Errorf("expected single space, got 0x%02X", b)
	}

	if next, ok := l.peekByte(); ok && IsWhitespace(next) {
		return fmt.Errorf("multiple whitespace characters not allowed")
	}
	return nil
}

func (l *Lexer) requireEOL() error {
	b, err := l.readByte()
	if err != nil {
		return err
	}

	if b == '\r' {
		if next, ok := l.peekByte(); ok && next == '\n' {
			l.readByte()
		}
		return nil
	}

	if b == '\n' {
		return nil
	}

	err = l.unreadByte()
	if err != nil {
		return err
	}
	return fmt.Errorf("expected EOL, got 0x%02X", b)
}

// --- Utilities ---

func IsHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') ||
		(ch >= 'A' && ch <= 'F') ||
		(ch >= 'a' && ch <= 'f')
}

func IsWhitespace(ch byte) bool {
	return ch == 0 || ch == 9 || ch == 10 || ch == 12 || ch == 13 || ch == 32
}

func isDelimiter(ch byte) bool {
	switch ch {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// internedNames holds canonical strings for the most frequently seen PDF names.
var internedNames = func() map[string]string {
	names := []string{
		"Type", "Subtype", "Page", "Pages", "Kids", "Parent", "Count",
		"Contents", "MediaBox", "Resources", "Font", "XObject", "ExtGState",
		"ColorSpace", "Pattern", "Shading", "Properties",
		"Length", "Filter", "DecodeParms", "Width", "Height",
		"BitsPerComponent", "Columns", "Predictor",
		"FlateDecode", "DCTDecode", "CCITTFaxDecode", "JPXDecode",
		"ASCIIHexDecode", "ASCII85Decode", "LZWDecode",
		"Root", "Info", "Size", "Prev", "ID",
		"BaseFont", "Encoding", "Widths", "FirstChar", "LastChar",
		"ToUnicode", "FontDescriptor", "Flags", "FontBBox", "ItalicAngle",
		"Ascent", "Descent", "CapHeight", "StemV", "MissingWidth",
		"FontFile", "FontFile2", "FontFile3",
		"Annots", "Rotate", "CropBox", "TrimBox", "BleedBox", "ArtBox",
		"Image", "Form", "ProcSet", "Matrix", "BBox", "FormType",
		"DeviceRGB", "DeviceCMYK", "DeviceGray", "ICCBased", "Indexed",
		"BM", "Normal", "ca", "CA", "OP", "op", "SA",
		"CreationDate", "ModDate", "Producer", "Creator", "Author",
		"Title", "Subject", "Keywords", "Name", "ObjStm", "XRef",
	}
	m := make(map[string]string, len(names))
	for _, n := range names {
		m[n] = n
	}
	return m
}()

// internName returns a canonical string for s, reusing a pre-allocated
// backing when s is a common PDF name.
func internName(b []byte) string {
	// Zero-alloc map probe: the key is only used for hashing/comparison and
	// does not escape, so converting without a heap copy is safe here.
	k := unsafe.String(unsafe.SliceData(b), len(b))
	if s, ok := internedNames[k]; ok {
		return s
	}
	return string(b)
}
