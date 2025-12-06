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
		return errors.New("expected 'xref' keyword (XRefStreams not supported in basic parser)")
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

// resolveObject takes a reference string like "1 0 R", finds the offset, and parses the object.
func (d *Document) resolveObject(ref string) (any, error) {
	var id, gen int
	var r string
	_, err := fmt.Sscanf(ref, "%d %d %s", &id, &gen, &r)
	if err != nil || r != "R" {
		return nil, errors.New("invalid reference format")
	}

	offset, ok := d.xrefTable[id]
	if !ok {
		return nil, fmt.Errorf("object %d not found in xref table", id)
	}

	chunk := make([]byte, 4096)
	n, err := d.file.ReadAt(chunk, offset)
	if err != nil && err != io.EOF {
		return nil, err
	}

	l := NewLexer(chunk[:n])

	t1 := l.NextToken() // ID
	l.NextToken()       // Gen
	t3 := l.NextToken() // obj

	if t1.Type != TokenInteger || t3.Value != "obj" {
		return nil, fmt.Errorf("expected '%d %d obj' at offset %d", id, gen, offset)
	}

	nextToken := l.NextToken()
	if nextToken.Type == TokenDictStart {
		l2 := NewLexer(chunk[l.pos-2 : n])
		return parseDictionary(l2)
	}

	return nextToken.Value, nil
}
