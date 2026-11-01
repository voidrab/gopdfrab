package pdf

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"unsafe"
)

// Token represents a distinct piece of syntax from the PDF. Number tokens
// carry their parsed payload in Int/Num with Value left ""; Value holds the
// raw text only for malformed numbers (see IntValue/RealValue).
type Token struct {
	Type  TokenType
	Value string
	Int   int64
	Num   float64
}

// IntValue returns t's integer payload. Only a malformed-number token, whose
// Value is set, is re-parsed, and a parse error is ignored: the result is
// whatever strconv.Atoi returns for Value.
func (t Token) IntValue() int {
	if t.Value == "" {
		return int(t.Int)
	}
	i, _ := strconv.Atoi(t.Value)
	return i
}

// RealValue returns t's real payload. A malformed real, whose Value is set, is
// parsed from its raw text, and the parse error is returned.
func (t Token) RealValue() (float64, error) {
	if t.Value == "" {
		return t.Num, nil
	}
	return strconv.ParseFloat(t.Value, 64)
}

// Lexer holds the state of the current chunk being parsed. It lexes buf, which
// holds the bytes from file offset base on. When the file is read on demand,
// src is set and buf is a window that moves along the file with the position;
// otherwise buf is the whole slice or in-memory file.
type Lexer struct {
	buf  []byte
	base int64
	src  *source // the file a window is read from; nil when buf is all input
	pos  int64
	// err is why a read of the file failed, which ends the lexing early.
	err    error
	pushed []Token
	// arrScratch is parseArray's shared element stack (see parser.go).
	arrScratch []PDFValue
	// depth is the current array/dictionary nesting depth, bounded by
	// maxParseDepth so a pathologically nested object cannot recurse
	// parseArray/parseDictionary into a stack overflow (see parser.go).
	depth int
}

// NewLexerBytes creates a lexer over data, starting at startPos.
func NewLexerBytes(data []byte, startPos int64) *Lexer {
	return &Lexer{buf: data, pos: startPos}
}

// lexerWindow is how much of a file read on demand a Lexer holds at a time.
const lexerWindow = 4096

// lexer returns a Lexer over the file from offset on, lexing it in place when
// it is in memory.
func (s *source) lexer(offset int64) *Lexer {
	if s.mem != nil {
		return &Lexer{buf: s.mem, pos: offset}
	}
	return &Lexer{src: s, pos: offset}
}

// byteAt returns the byte at file offset pos, moving the window there when it
// lies outside, or false when the input has no byte there.
func (l *Lexer) byteAt(pos int64) (byte, bool) {
	if i := pos - l.base; i >= 0 && i < int64(len(l.buf)) {
		return l.buf[i], true
	}
	if !l.slide(pos, lexerWindow) {
		return 0, false
	}
	return l.buf[0], true
}

// slide moves the window to hold up to n bytes from pos on, and reports
// whether there are any. A slice has no bytes beyond itself.
func (l *Lexer) slide(pos int64, n int) bool {
	if l.src == nil {
		return false
	}
	b, err := l.src.window(pos, n)
	if err != nil {
		l.err = err
	}
	if len(b) == 0 {
		return false
	}
	l.buf, l.base = b, pos
	return true
}

// span returns the bytes from start up to the read position, which the
// window may have moved past.
func (l *Lexer) span(start int64) []byte {
	if start < l.base || l.pos-l.base > int64(len(l.buf)) {
		l.slide(start, int(l.pos-start))
	}
	return l.buf[start-l.base : l.pos-l.base]
}

// peekByte returns the next byte without advancing the position.
func (l *Lexer) peekByte() (byte, bool) {
	return l.byteAt(l.pos)
}

// NextToken returns the next distinct token from the stream.
func (l *Lexer) NextToken() Token {
	if len(l.pushed) > 0 {
		t := l.pushed[len(l.pushed)-1]
		l.pushed = l.pushed[:len(l.pushed)-1]
		return t
	}

	// Skip whitespace and comments in a loop: recursing once per comment
	// made stack depth grow with the number of consecutive comment lines.
	var ch byte
	for {
		l.skipWhitespace()
		var err error
		ch, err = l.readByte()
		if err == io.EOF {
			return Token{Type: TokenEOF}
		}
		if err != nil {
			return Token{Type: TokenError, Value: err.Error()}
		}
		if ch != '%' {
			break
		}
		for {
			b, err := l.readByte()
			if err != nil || b == '\n' || b == '\r' {
				break
			}
		}
	}

	switch ch {
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
// It returns the declared object number, or -1 when the header is not a
// well-formed int/int/obj triple, and the number of white space bytes skipped
// before the header. Leading white space is not a violation, as veraPDF reads
// the rule; the caller checks whether the number starts a line. It advances
// past the header even on violations, so resolution can continue.
func (l *Lexer) validateObjectStart() (int, int64, []error) {
	var errs []error

	var skipped int64
	for b, ok := l.peekByte(); ok && IsWhitespace(b); b, ok = l.peekByte() {
		l.readByte()
		skipped++
	}

	objTok := l.NextToken()
	if objTok.Type != TokenInteger {
		return -1, skipped, append(errs, fmt.Errorf("invalid object number: %q", objTok.Value))
	}

	if err := l.requireSingleSpace(); err != nil {
		errs = append(errs, err)
	}

	genTok := l.NextToken()
	if genTok.Type != TokenInteger {
		return -1, skipped, append(errs, fmt.Errorf("invalid generation number: %q", genTok.Value))
	}

	if err := l.requireSingleSpace(); err != nil {
		errs = append(errs, err)
	}

	objKw := l.NextToken()
	if objKw.Type != TokenObjectStart {
		return -1, skipped, append(errs, fmt.Errorf("expected 'obj' keyword, got %q", objKw.Value))
	}

	if err := l.requireEOL(); err != nil {
		errs = append(errs, fmt.Errorf("'obj' keyword not followed by single EOL: %v", err))
	}

	return objTok.IntValue(), skipped, errs
}

// validateEndObj validates the framing around an unconsumed endobj keyword
// (6.1.8).
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

// validateObjectEnd validates the EOL after an already-consumed endobj keyword
// (6.1.8).
func (l *Lexer) validateObjectEnd() []error {
	if err := l.requireEOL(); err != nil {
		return []error{fmt.Errorf("endobj not followed by single EOL: %v", err)}
	}
	return nil
}

// --- Helper Functions ---

func (l *Lexer) readByte() (byte, error) {
	b, ok := l.byteAt(l.pos)
	if !ok {
		if l.err != nil {
			return 0, l.err
		}
		return 0, io.EOF
	}
	l.pos++
	return b, nil
}

func (l *Lexer) unreadByte() {
	l.pos--
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

// readNumber handles integers and reals, parsing the value once into the
// token's Int/Num payload; only malformed numbers keep their raw text.
func (l *Lexer) readNumber() Token {
	start := l.pos
	for {
		b, ok := l.byteAt(l.pos)
		if !ok || !(b >= '0' && b <= '9') && b != '.' && b != '+' && b != '-' {
			break
		}
		l.pos++
	}
	raw := l.span(start)

	isReal := bytes.IndexByte(raw, '.') >= 0
	// Transient no-copy view; strconv does not retain its argument.
	s := unsafe.String(unsafe.SliceData(raw), len(raw))
	if isReal {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return Token{Type: TokenReal, Value: string(raw)}
		}
		return Token{Type: TokenReal, Num: f}
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return Token{Type: TokenInteger, Value: string(raw)}
	}
	return Token{Type: TokenInteger, Int: i, Num: float64(i)}
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
	val := internBytes(internedKeywords, buf)
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

// readStringLiteral handles string literals like (Hello World), decoding
// escape sequences and EOL normalization per ISO 32000-1 7.3.4.2 so
// Token.Value holds the bytes the string represents.
func (l *Lexer) readStringLiteral() Token {
	var buf []byte
	depth := 1

	for {
		b, err := l.readByte()
		if err != nil {
			return Token{Type: TokenError, Value: fmt.Sprintf("Unterminated String: %v", err)}
		}
		switch b {
		case '\\':
			nb, err := l.readByte()
			if err != nil {
				return Token{Type: TokenError, Value: fmt.Sprintf("Unterminated String: %v", err)}
			}
			switch nb {
			case 'n':
				buf = append(buf, '\n')
			case 'r':
				buf = append(buf, '\r')
			case 't':
				buf = append(buf, '\t')
			case 'b':
				buf = append(buf, '\b')
			case 'f':
				buf = append(buf, '\f')
			case '(', ')', '\\':
				buf = append(buf, nb)
			case '\r':
				// Line continuation: backslash + EOL vanish entirely.
				if next, ok := l.peekByte(); ok && next == '\n' {
					l.readByte()
				}
			case '\n':
			default:
				if nb >= '0' && nb <= '7' {
					v := int(nb - '0')
					for range 2 {
						nx, ok := l.peekByte()
						if !ok || nx < '0' || nx > '7' {
							break
						}
						l.readByte()
						v = v*8 + int(nx-'0')
					}
					buf = append(buf, byte(v))
				} else {
					// Unknown escape: the backslash is ignored.
					buf = append(buf, nb)
				}
			}
			continue
		case '\r':
			// Unescaped EOL inside a string reads as a single LF.
			if next, ok := l.peekByte(); ok && next == '\n' {
				l.readByte()
			}
			buf = append(buf, '\n')
			continue
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
// are preserved so callers (validateHexString) can report a precise 6.1.6
// violation.
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

	l.unreadByte()
	return nil
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

	l.unreadByte()
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

// internedKeywords holds canonical strings for structural keywords and every
// content-stream operator, so readKeyword rarely allocates.
var internedKeywords = func() map[string]string {
	words := []string{
		"R", "obj", "endobj", "stream", "endstream", "true", "false", "null",
		"xref", "trailer", "startxref", "n", "f",
		"q", "Q", "cm", "w", "J", "j", "M", "d", "ri", "i", "gs",
		"BT", "ET", "Td", "TD", "Tm", "T*", "Tc", "Tw", "Tz", "TL", "Tf", "Tr", "Ts",
		"Tj", "TJ", "'", "\"",
		"m", "l", "c", "v", "y", "h", "re",
		"S", "s", "F", "f*", "B", "B*", "b", "b*", "W", "W*",
		"Do", "sh", "cs", "CS", "sc", "scn", "SC", "SCN", "g", "G", "rg", "RG", "k", "K",
		"BMC", "BDC", "EMC", "MP", "DP", "BX", "EX", "d0", "d1", "BI", "ID", "EI",
	}
	m := make(map[string]string, len(words))
	for _, w := range words {
		m[w] = w
	}
	return m
}()

// internBytes returns a canonical string for b when m holds one, avoiding
// the copy; the probe key never escapes.
func internBytes(m map[string]string, b []byte) string {
	k := unsafe.String(unsafe.SliceData(b), len(b))
	if s, ok := m[k]; ok {
		return s
	}
	return string(b)
}

// internName returns a canonical string for s, reusing a pre-allocated
// backing when s is a common PDF name.
func internName(b []byte) string {
	return internBytes(internedNames, b)
}

// internedNameValues pre-boxes internedNames into PDFValues. Interning the
// string stops readName allocating; boxing the PDFName allocates again, and a
// 40,000-object document boxes /Type and /Subtype tens of thousands of times.
// Sound because PDFName is an immutable value type.
var internedNameValues = func() map[string]PDFValue {
	m := make(map[string]PDFValue, len(internedNames))
	for n := range internedNames {
		m[n] = PDFName{Value: n}
	}
	return m
}()

// boxName returns a shared box for a common name, else a fresh one.
func boxName(s string) PDFValue {
	if v, ok := internedNameValues[s]; ok {
		return v
	}
	return PDFName{Value: s}
}
