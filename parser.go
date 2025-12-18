package pdfrab

import (
	"bufio"
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
			entryLine := make([]byte, 20) // each row is 20 bytes
			if _, err := io.ReadFull(reader, entryLine); err != nil {
				return err
			}

			if entryLine[17] == 'n' { // flag ('n' = used) is usually at index 17
				offsetStr := string(entryLine[:10])
				offsetVal, _ := strconv.ParseInt(offsetStr, 10, 64)
				d.xrefTable[startObjID+i] = offsetVal
			}
		}
	}

	return nil
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
		// get key
		keyTok := l.NextToken()
		if keyTok.Type == TokenDictEnd {
			break
		}
		if keyTok.Type == TokenEOF {
			return dict, errors.New("unexpected EOF while parsing dictionary")
		}
		if keyTok.Type == TokenDictStart { // skip dict start
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
