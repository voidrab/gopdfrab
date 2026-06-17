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
		out := NewPDFDict()
		out.HasStream = v.HasStream
		out.RawStream = v.RawStream
		for k, val := range v.Entries {
			resolved, err := d.resolveObject(val)
			if err != nil {
				return nil, err
			}
			out.Entries[k] = resolved
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

	d.recordFraming(ref.ObjNum, l.validateObjectStart())

	t := l.NextToken()

	switch t.Type {
	case TokenDictStart:
		m, err := parseDictionary(l)
		if err != nil {
			return nil, err
		}

		m.Entries["_ref"] = ref

		next := l.NextToken()

		switch next.Type {
		case TokenStreamStart:
			m.HasStream = true
			err := d.validateStream(l, &m, ref.ObjNum)
			if err != nil {
				return nil, err
			}
			d.recordFraming(ref.ObjNum, l.validateEndObj())
			return m, nil
		case TokenObjectEnd:
			d.recordFraming(ref.ObjNum, l.validateObjectEnd())
		default:
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
