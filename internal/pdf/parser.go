package pdf

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// xrefEntryOffset parses the 10-byte offset field of a classic xref entry
// without allocating. When trimPadding is true, spaces around the digits are
// ignored; otherwise every byte of the field must be a digit, as in
// readXRefSection's strict mode. A malformed field yields 0.
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

// parseXRefTable reads the classic cross-reference section at offset as
// the document's table, strictly: see readXRefSection.
func (d *Reader) parseXRefTable(offset int64) error {
	d.xrefTable = make(map[int]int64)
	_, err := d.readXRefSection(offset, true, false)
	return err
}

// ParseXRefSectionAt parses the xref table and trailer dict at offset.
// If fillIn is true, only entries not already in d.xrefTable are added,
// preserving newer revisions.
func (d *Reader) ParseXRefSectionAt(offset int64, fillIn bool) (PDFDict, error) {
	c, err := d.readXRefSection(offset, false, fillIn)
	if err != nil {
		return PDFDict{}, err
	}
	// Lex the trailer keyword and dictionary together so the keyword may sit on
	// its own line or share one with the dict ("trailer << ... >>").
	l := NewLexerBytes(c.Rest(8192), 0)
	defer l.Release()

	if tok := l.NextToken(); tok.Value != "trailer" {
		return PDFDict{}, fmt.Errorf("expected 'trailer', got %q", tok.Value)
	}
	return parseDictionary(l)
}

// readXRefSection reads the subsections of the classic cross-reference
// section at offset into d.xrefTable, and returns a cursor just past them.
// When strict, a section the file ends inside is an error, and an entry's
// offset must be ten digits. Otherwise reading stops at the first damage
// without error, and an offset padded with spaces is read. When fillIn, an
// object the table already holds keeps its entry, as a newer revision's.
func (d *Reader) readXRefSection(offset int64, strict, fillIn bool) (*SectionCursor, error) {
	c := d.SectionAt(offset)
	line, ok := c.ReadLine()
	if !ok {
		return nil, cmp.Or(c.Err(), io.EOF)
	}
	if string(line) != "xref" {
		if strict {
			return nil, errors.New("expected 'xref' keyword")
		}
		return nil, fmt.Errorf("expected 'xref' at offset %d, got %q", offset, string(line))
	}

	for {
		next, ok := c.Peek()
		if !ok {
			if strict {
				return nil, cmp.Or(c.Err(), io.EOF)
			}
			break
		}
		if next == 't' { // trailer keyword
			break
		}

		// Peek found a byte, so there is a line to read.
		subHeader, _ := c.ReadLine()
		parts := strings.Fields(string(subHeader))
		if len(parts) != 2 {
			break
		}

		startObjID, _ := strconv.Atoi(parts[0])
		numObjs, _ := strconv.Atoi(parts[1])

		for i := range numObjs {
			entry, ok := c.Next(20)
			if !ok {
				if strict {
					return nil, cmp.Or(c.Err(), io.ErrUnexpectedEOF)
				}
				break
			}
			if entry[17] == 'n' { // 'n' = used entry
				objNum := startObjID + i
				if !fillIn || d.xrefTable[objNum] == 0 {
					d.xrefTable[objNum] = xrefEntryOffset(entry[:10], !strict) + d.pdfStart
				}
			}
		}
	}
	return c, nil
}

// parseScalarToken converts a token that stands for a complete value on its own
// into that value. ok is false for tokens whose meaning depends on what follows
// or on where they appear -- integers, arrays, dictionaries -- which each caller
// handles itself.
//
// Both dispatchers share this: parseObject below and resolver.go's
// parseClassicAt, which reads an indirect object's body. Keeping the scalar
// cases here ensures that both agree on scalars and null; the callers handle
// only what differs between them.
func parseScalarToken(tok Token) (v PDFValue, ok bool, err error) {
	switch tok.Type {
	case TokenKeyword:
		// 'null' is the null object (ISO 32000-1 7.3.10), a nil PDFValue.
		if tok.Value == "null" {
			return nil, true, nil
		}
		return boxName(tok.Value), true, nil

	case TokenBoolean:
		return PDFBoolean(tok.Value == "true"), true, nil

	case TokenReal:
		f, err := tok.RealValue()
		if err != nil {
			return nil, true, err
		}
		return PDFReal(f), true, nil

	case TokenString:
		return PDFString{Value: tok.Value}, true, nil

	case TokenHexString:
		return PDFHexString{Value: tok.Value}, true, nil

	case TokenName:
		return boxName(tok.Value), true, nil
	}
	return nil, false, nil
}

func parseObject(l *Lexer, tok Token) (PDFValue, error) {
	if v, ok, err := parseScalarToken(tok); ok {
		return v, err
	}

	switch tok.Type {
	// An integer inside a containing object may open an "N G R" reference; the
	// object-body dispatcher has no such case, which is the one difference
	// between the two.
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
		dict.Entries.Set(key, elem)
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
