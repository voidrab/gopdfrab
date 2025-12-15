package pdfrab

import (
	"fmt"
	"io"
)

func (d *Document) resolveObject(obj PDFValue) (PDFValue, error) {
	switch v := obj.(type) {

	case PDFRef:
		return d.resolveReference(v)

	case PDFDict:
		out := make(PDFDict)
		for k, val := range v {
			resolved, err := d.resolveObject(val)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
		return out, nil

	case PDFArray:
		out := make(PDFArray, len(v))
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

func (d *Document) resolveReference(ref PDFRef) (PDFValue, error) {
	offset, ok := d.xrefTable[ref.ObjNum]
	if !ok {
		return nil, fmt.Errorf("object %d not found in xref table", ref.ObjNum)
	}

	if _, err := d.file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	l := NewLexerAt(d.file, offset)

	// Expect "<id> <gen> obj"
	t1 := l.NextToken()
	l.NextToken()
	t3 := l.NextToken()

	if t1.Type != TokenInteger || t3.Value != "obj" {
		return nil, fmt.Errorf("invalid object header %d %d", ref.ObjNum, ref.GenNum)
	}

	t := l.NextToken()

	switch t.Type {
	case TokenDictStart:
		m, err := parseDictionary(l)
		if err != nil {
			return nil, err
		}

		next := l.NextToken()
		if next.Type == TokenStreamStart {
			err := d.readStream(l, m)
			if err != nil {
				return nil, err
			}
			return PDFStreamDict(m), nil
		} else {
			l.UnreadToken(next)
		}

		return m, nil

	case TokenArrayStart:
		arr, err := parseArray(l)
		if err != nil {
			return nil, err
		}
		return arr, nil

	default:
		return PDFString{t.Value}, nil
	}
}
