package pdf

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// readLineBytes returns the line starting at pos (terminated by '\n', with a
// single trailing '\r' stripped, matching bufio.Reader.ReadLine) and the
// position just past the terminator.
func readLineBytes(data []byte, pos int) (line []byte, next int, ok bool) {
	if pos >= len(data) {
		return nil, pos, false
	}
	i := bytes.IndexByte(data[pos:], '\n')
	if i < 0 {
		return data[pos:], len(data), true
	}
	line = data[pos : pos+i]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, pos + i + 1, true
}

// xrefEntryOffset parses the 10-byte offset field of a classic xref entry
// without allocating. trimPadding mirrors ParseXRefSectionAt's historical
// TrimSpace tolerance for space-padded fields; without it all ten bytes must
// be digits (parseXRefTable's strictness). A malformed field yields 0,
// matching the ignored strconv errors this replaces.
func xrefEntryOffset(field []byte, trimPadding bool) int64 {
	if trimPadding {
		field = bytes.TrimSpace(field)
	}
	if len(field) == 0 {
		return 0
	}
	var v int64
	for _, c := range field {
		if c < '0' || c > '9' {
			return 0
		}
		v = v*10 + int64(c-'0')
	}
	return v
}

// parseXRefTable reads the 'xref' table starting at the given offset.
func (d *Reader) parseXRefTable(offset int64) error {
	d.xrefTable = make(map[int]int64)

	if d.data != nil {
		return d.parseXRefTableBytes(offset)
	}

	_, err := d.file.Seek(offset, io.SeekStart)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(d.file)

	line, _, err := reader.ReadLine()
	if err != nil {
		return err
	}
	if string(line) != "xref" {
		return errors.New("expected 'xref' keyword")
	}

	for {
		peekBytes, err := reader.Peek(1)
		if err != nil {
			return err
		}
		if peekBytes[0] == 't' { // stop when reaching 't' for trailer
			break
		}

		line, _, err := reader.ReadLine()
		if err != nil {
			return err
		}
		parts := strings.Fields(string(line))
		if len(parts) != 2 {
			break
		}

		startObjID, _ := strconv.Atoi(parts[0])
		numObjs, _ := strconv.Atoi(parts[1])

		for i := range numObjs {
			entryLine := make([]byte, 20)
			if _, err := io.ReadFull(reader, entryLine); err != nil {
				return err
			}

			if entryLine[17] == 'n' { // 'n' = used entry
				offsetStr := string(entryLine[:10])
				offsetVal, _ := strconv.ParseInt(offsetStr, 10, 64)
				d.xrefTable[startObjID+i] = offsetVal + d.pdfStart
			}
		}
	}

	return nil
}

// parseXRefTableBytes is parseXRefTable's fast path over the in-memory file
// bytes (mmap or OpenBytes): no Seek, no bufio.Reader, and no per-entry or
// per-field allocation. Same tolerances and stop conditions as the reader
// path.
func (d *Reader) parseXRefTableBytes(offset int64) error {
	data := d.data
	if offset < 0 || offset >= int64(len(data)) {
		return io.EOF
	}
	line, pos, ok := readLineBytes(data, int(offset))
	if !ok {
		return io.EOF
	}
	if string(line) != "xref" {
		return errors.New("expected 'xref' keyword")
	}

	for {
		if pos >= len(data) {
			return io.EOF
		}
		if data[pos] == 't' { // stop when reaching 't' for trailer
			break
		}

		line, next, ok := readLineBytes(data, pos)
		if !ok {
			return io.EOF
		}
		pos = next
		parts := strings.Fields(string(line))
		if len(parts) != 2 {
			break
		}

		startObjID, _ := strconv.Atoi(parts[0])
		numObjs, _ := strconv.Atoi(parts[1])

		for i := range numObjs {
			if pos+20 > len(data) {
				return io.ErrUnexpectedEOF
			}
			entry := data[pos : pos+20]
			pos += 20

			if entry[17] == 'n' { // 'n' = used entry
				d.xrefTable[startObjID+i] = xrefEntryOffset(entry[:10], false) + d.pdfStart
			}
		}
	}

	return nil
}

// ParseXRefSectionAt parses the xref table and trailer dict at offset.
// If fillIn is true, only entries not already in d.xrefTable are added, preserving newer revisions.
func (d *Reader) ParseXRefSectionAt(offset int64, fillIn bool) (PDFDict, error) {
	if d.data != nil {
		return d.parseXRefSectionAtBytes(offset, fillIn)
	}

	if _, err := d.file.Seek(offset, io.SeekStart); err != nil {
		return PDFDict{}, err
	}

	reader := bufio.NewReader(d.file)

	line, _, err := reader.ReadLine()
	if err != nil {
		return PDFDict{}, err
	}
	if strings.TrimRight(string(line), "\r\n") != "xref" {
		return PDFDict{}, fmt.Errorf("expected 'xref' at offset %d, got %q", offset, string(line))
	}

	for {
		peekBytes, err := reader.Peek(1)
		if err != nil || len(peekBytes) == 0 {
			break
		}
		if peekBytes[0] == 't' { // trailer keyword
			break
		}

		subHeader, _, err := reader.ReadLine()
		if err != nil {
			break
		}
		parts := strings.Fields(string(subHeader))
		if len(parts) != 2 {
			break
		}

		startObjID, _ := strconv.Atoi(parts[0])
		numObjs, _ := strconv.Atoi(parts[1])

		for i := range numObjs {
			entryLine := make([]byte, 20)
			if _, err := io.ReadFull(reader, entryLine); err != nil {
				break
			}
			if entryLine[17] == 'n' {
				offsetStr := strings.TrimSpace(string(entryLine[:10]))
				objOffset, _ := strconv.ParseInt(offsetStr, 10, 64)
				objNum := startObjID + i
				if !fillIn || d.xrefTable[objNum] == 0 {
					d.xrefTable[objNum] = objOffset + d.pdfStart
				}
			}
		}
	}

	// Lex the trailer keyword and dictionary together so the keyword may sit on
	// its own line or share one with the dict ("trailer << ... >>").
	limited := io.LimitReader(reader, 8192)
	buf, _ := io.ReadAll(limited)
	l := NewLexer(bytes.NewReader(buf))
	defer l.Release()

	if tok := l.NextToken(); tok.Value != "trailer" {
		return PDFDict{}, fmt.Errorf("expected 'trailer', got %q", tok.Value)
	}
	return parseDictionary(l)
}

// parseXRefSectionAtBytes is ParseXRefSectionAt's fast path over the
// in-memory file bytes: no Seek, no bufio.Reader, no per-entry allocation,
// and the trailer dict is lexed straight from the backing slice instead of
// being copied out through io.ReadAll. Same tolerances and stop conditions
// as the reader path, including its 8192-byte trailer window.
func (d *Reader) parseXRefSectionAtBytes(offset int64, fillIn bool) (PDFDict, error) {
	data := d.data
	if offset < 0 || offset >= int64(len(data)) {
		return PDFDict{}, io.EOF
	}
	line, pos, ok := readLineBytes(data, int(offset))
	if !ok {
		return PDFDict{}, io.EOF
	}
	if strings.TrimRight(string(line), "\r\n") != "xref" {
		return PDFDict{}, fmt.Errorf("expected 'xref' at offset %d, got %q", offset, string(line))
	}

	for {
		if pos >= len(data) || data[pos] == 't' { // trailer keyword
			break
		}

		subHeader, next, ok := readLineBytes(data, pos)
		if !ok {
			break
		}
		pos = next
		parts := strings.Fields(string(subHeader))
		if len(parts) != 2 {
			break
		}

		startObjID, _ := strconv.Atoi(parts[0])
		numObjs, _ := strconv.Atoi(parts[1])

		for i := range numObjs {
			if pos+20 > len(data) {
				break
			}
			entry := data[pos : pos+20]
			pos += 20
			if entry[17] == 'n' {
				objNum := startObjID + i
				if !fillIn || d.xrefTable[objNum] == 0 {
					d.xrefTable[objNum] = xrefEntryOffset(entry[:10], true) + d.pdfStart
				}
			}
		}
	}

	// Lex the trailer keyword and dictionary together so the keyword may sit on
	// its own line or share one with the dict ("trailer << ... >>").
	window := data[pos:]
	if len(window) > 8192 {
		window = window[:8192]
	}
	l := NewLexerBytes(window, 0)
	defer l.Release()

	if tok := l.NextToken(); tok.Value != "trailer" {
		return PDFDict{}, fmt.Errorf("expected 'trailer', got %q", tok.Value)
	}
	return parseDictionary(l)
}

func parseObject(l *Lexer, tok Token) (PDFValue, error) {
	switch tok.Type {

	case TokenKeyword:
		// 'null' is the null object (ISO 32000-1 7.3.10), a nil PDFValue.
		if tok.Value == "null" {
			return nil, nil
		}
		return PDFName{Value: tok.Value}, nil

	case TokenBoolean:
		return PDFBoolean(tok.Value == "true"), nil

	case TokenInteger:
		tok2 := l.NextToken()
		tok3 := l.NextToken()

		if tok2.Type == TokenInteger && tok3.Type == TokenKeyword && tok3.Value == "R" {
			return PDFRef{ObjNum: tok.IntValue(), GenNum: tok2.IntValue()}, nil
		} else {
			l.UnreadToken(tok3)
			l.UnreadToken(tok2)
			return PDFInteger(tok.IntValue()), nil
		}

	case TokenReal:
		f, err := tok.RealValue()
		if err != nil {
			return nil, err
		}
		return PDFReal(f), nil

	case TokenString:
		return PDFString{Value: tok.Value}, nil

	case TokenHexString:
		return PDFHexString{Value: tok.Value}, nil

	case TokenName:
		return PDFName{Value: tok.Value}, nil

	case TokenArrayStart:
		return parseArray(l)

	case TokenDictStart:
		return parseDictionary(l)

	default:
		return nil, fmt.Errorf("unexpected token %v with value %v", tok.Type, tok.Value)
	}
}

// maxParseDepth bounds array/dictionary nesting so a crafted object with
// pathologically deep nesting (e.g. thousands of nested arrays) cannot recurse
// parseObject/parseArray/parseDictionary into an unrecoverable stack overflow.
// Real documents nest only a handful of levels deep.
const maxParseDepth = 1000

// parseDictionary consumes tokens to build a map.
func parseDictionary(l *Lexer) (PDFDict, error) {
	dict := NewPDFDict()

	if l.depth >= maxParseDepth {
		return dict, errors.New("maximum nesting depth exceeded")
	}
	l.depth++
	defer func() { l.depth-- }()

	for {
		keyTok := l.NextToken()
		if keyTok.Type == TokenDictEnd {
			break
		}
		if keyTok.Type == TokenEOF {
			return dict, errors.New("unexpected EOF while parsing dictionary")
		}
		if keyTok.Type == TokenDictStart {
			continue
		}
		if keyTok.Type != TokenName {
			return dict, fmt.Errorf("expected dictionary key but got %v (%q)", keyTok.Type, keyTok.Value)
		}

		key := keyTok.Value

		tok := l.NextToken()

		elem, err := parseObject(l, tok)
		if err != nil {
			return dict, err
		}
		dict.Entries[key] = elem
	}
	return dict, nil
}

// parseArray builds an array from the object stream, collecting elements on
// the lexer's shared scratch stack (nesting-safe via base offsets) and
// copying out an exact-size array, so element growth never reallocates per
// array.
func parseArray(l *Lexer) (PDFArray, error) {
	if l.depth >= maxParseDepth {
		return nil, errors.New("maximum nesting depth exceeded")
	}
	l.depth++
	defer func() { l.depth-- }()

	base := len(l.arrScratch)

	for {
		t := l.NextToken()

		if t.Type == TokenArrayEnd {
			arr := make(PDFArray, len(l.arrScratch)-base)
			copy(arr, l.arrScratch[base:])
			clear(l.arrScratch[base:])
			l.arrScratch = l.arrScratch[:base]
			return arr, nil
		}
		if t.Type == TokenEOF {
			clear(l.arrScratch[base:])
			l.arrScratch = l.arrScratch[:base]
			return nil, errors.New("unexpected EOF while parsing array")
		}

		elem, err := parseObject(l, t)
		if err != nil {
			clear(l.arrScratch[base:])
			l.arrScratch = l.arrScratch[:base]
			return nil, err
		}
		l.arrScratch = append(l.arrScratch, elem)
	}
}
