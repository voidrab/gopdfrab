package pdfrab

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
func (d *Document) parseXRefTable(offset int64) error {
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

// parseXRefSectionAt parses the xref table and trailer dict at offset.
// If fillIn is true, only entries not already in d.xrefTable are added, preserving newer revisions.
func (d *Document) parseXRefSectionAt(offset int64, fillIn bool) (PDFDict, error) {
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

	trailerLine, _, err := reader.ReadLine()
	if err != nil {
		return PDFDict{}, fmt.Errorf("expected 'trailer' keyword: %w", err)
	}
	if strings.TrimRight(string(trailerLine), "\r\n") != "trailer" {
		return PDFDict{}, fmt.Errorf("expected 'trailer', got %q", string(trailerLine))
	}

	// Read up to 2 KB for the trailer dictionary (more than enough in practice).
	limited := io.LimitReader(reader, 2048)
	buf, _ := io.ReadAll(limited)
	l := NewLexer(bytes.NewReader(buf))
	defer l.Release()
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
			objNum, _ := strconv.Atoi(tok.Value)
			genNum, _ := strconv.Atoi(tok2.Value)
			return PDFRef{ObjNum: objNum, GenNum: genNum}, nil
		} else {
			l.UnreadToken(tok3)
			l.UnreadToken(tok2)
			i, _ := strconv.Atoi(tok.Value)
			return PDFInteger(i), nil
		}

	case TokenReal:
		f, err := strconv.ParseFloat(tok.Value, 64)
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

func parseArray(l *Lexer) (PDFArray, error) {
	var arr PDFArray

	for {
		t := l.NextToken()

		if t.Type == TokenArrayEnd {
			return arr, nil
		}
		if t.Type == TokenEOF {
			return nil, errors.New("unexpected EOF while parsing array")
		}

		elem, err := parseObject(l, t)
		if err != nil {
			return nil, err
		}
		arr = append(arr, elem)
	}
}
