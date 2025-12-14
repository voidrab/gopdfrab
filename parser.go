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

// resolveObject recursively resolves nested references, dictionaries, and arrays.
func (d *Document) resolveObject(obj any) (any, error) {
	switch v := obj.(type) {

	case string:
		if isReference(v) {
			return d.resolveReference(v)
		}
		return v, nil

	case map[any]any:
		out := make(map[string]any)
		for k, val := range v {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("dictionary key is not a string: %v", k)
			}
			resolved, err := d.resolveObject(val)
			if err != nil {
				return nil, err
			}
			out[ks] = resolved
		}
		return out, nil

	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			resolved, err := d.resolveObject(elem)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil

	default:
		return v, nil
	}
}

func isReference(s string) bool {
	var id, gen int
	var r string
	_, err := fmt.Sscanf(s, "%d %d %s", &id, &gen, &r)
	return err == nil && r == "R"
}

// resolveReference resolves references
func (d *Document) resolveReference(ref string) (any, error) {
	var id, gen int
	var r string
	_, err := fmt.Sscanf(ref, "%d %d %s", &id, &gen, &r)
	if err != nil || r != "R" {
		return nil, fmt.Errorf("invalid reference: %s", ref)
	}

	offset, ok := d.xrefTable[id]
	if !ok {
		return nil, fmt.Errorf("object %d not found in xref table", id)
	}

	// Read chunk starting at offset
	chunk := make([]byte, 4096)
	n, err := d.file.ReadAt(chunk, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}

	l := NewLexer(bytes.NewReader(chunk[:n]))

	// Expect "<id> <gen> obj"
	t1 := l.NextToken()
	l.NextToken()
	t3 := l.NextToken()

	if t1.Type != TokenInteger || t3.Value != "obj" {
		return nil, fmt.Errorf("invalid object header %d %d", id, gen)
	}

	t := l.NextToken()

	switch t.Type {
	case TokenDictStart:
		m, err := parseDictionary(l)
		if err != nil {
			return nil, err
		}
		return d.resolveObject(m)

	case TokenArrayStart:
		arr, err := parseArray(l)
		if err != nil {
			return nil, err
		}
		return d.resolveObject(arr)

	default:
		return d.resolveObject(t.Value)
	}
}

// parseDictionary consumes tokens to build a map.
func parseDictionary(l *Lexer) (map[string]any, error) {
	dict := make(map[string]any)

	for {
		// get key
		keyTok := l.NextToken()
		if keyTok.Type == TokenDictEnd {
			break
		}
		if keyTok.Type == TokenEOF {
			return nil, errors.New("unexpected EOF while parsing dictionary")
		}
		if keyTok.Type == TokenDictStart { // skip dict start
			continue
		}
		if keyTok.Type != TokenName {
			return nil, fmt.Errorf("expected dictionary key but got %v (%q)", keyTok.Type, keyTok.Value)
		}

		key := keyTok.Value

		// get value
		valTok := l.NextToken()

		// Handle indirect references (e.g., "1 0 R")
		if valTok.Type == TokenInteger {
			t2 := l.NextToken()
			t3 := l.NextToken()

			if t2.Type == TokenInteger && t3.Type == TokenKeyword && t3.Value == "R" {
				dict[key] = fmt.Sprintf("%s %s R", valTok.Value, t2.Value)
				continue
			}

			// Not a reference, restore lexer position
			l.UnreadToken(t3)
			l.UnreadToken(t2)
		}

		if valTok.Type == TokenDictStart {
			subDict, err := parseDictionary(l)
			if err != nil {
				return nil, err
			}
			dict[key] = subDict
			continue
		}

		if valTok.Type == TokenArrayStart {
			arr, err := parseArray(l)
			if err != nil {
				return nil, err
			}
			dict[key] = arr
			continue
		}

		dict[key] = valTok.Value
	}
	return dict, nil
}

func parseArray(l *Lexer) ([]any, error) {
	var arr []any

	for {
		t := l.NextToken()

		if t.Type == TokenArrayEnd {
			return arr, nil
		}
		if t.Type == TokenEOF {
			return nil, errors.New("unexpected EOF while parsing array")
		}

		// Nested dictionary inside array
		if t.Type == TokenDictStart {
			sub, err := parseDictionary(l)
			if err != nil {
				return nil, err
			}
			arr = append(arr, sub)
			continue
		}

		// Nested array inside array
		if t.Type == TokenArrayStart {
			sub, err := parseArray(l)
			if err != nil {
				return nil, err
			}
			arr = append(arr, sub)
			continue
		}

		// Indirect reference inside array
		if t.Type == TokenInteger {
			t2 := l.NextToken()
			t3 := l.NextToken()

			if t2.Type == TokenInteger && t3.Type == TokenKeyword && t3.Value == "R" {
				ref := fmt.Sprintf("%s %s R", t.Value, t2.Value)
				arr = append(arr, ref)
				continue
			}

			l.UnreadToken(t3)
			l.UnreadToken(t2)
		}

		arr = append(arr, t.Value)
	}
}
