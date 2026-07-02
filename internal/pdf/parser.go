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

// parseXRefTable reads the 'xref' table starting at the given offset.
func (d *Reader) parseXRefTable(offset int64) error {
	d.xrefTable = make(map[int]int64)

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

// ParseXRefSectionAt parses the xref table and trailer dict at offset.
// If fillIn is true, only entries not already in d.xrefTable are added, preserving newer revisions.
func (d *Reader) ParseXRefSectionAt(offset int64, fillIn bool) (PDFDict, error) {
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

func parseObject(l *Lexer, tok Token) (PDFValue, error) {
	switch tok.Type {

	case TokenKeyword:
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

// parseDictionary consumes tokens to build a map.
func parseDictionary(l *Lexer) (PDFDict, error) {
	dict := NewPDFDict()

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
