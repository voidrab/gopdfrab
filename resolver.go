package pdfrab

import (
	"fmt"
	"io"
)

func (d *Document) resolveObject(obj PDFValue) (PDFValue, error) {
	return d.resolveInPlace(obj)
}

// resolveShallow dereferences obj if it is a PDFRef, without recursing into
// the result's own entries.
func (d *Document) resolveShallow(obj PDFValue) (PDFValue, error) {
	if ref, ok := obj.(PDFRef); ok {
		return d.resolveReference(ref)
	}
	return obj, nil
}

// resolveReference resolves an indirect reference to its object, parsing it
// from disk at most once per object number.
func (d *Document) resolveReference(ref PDFRef) (PDFValue, error) {
	if cached, ok := d.objCache[ref.ObjNum]; ok {
		return cached, nil
	}

	v, err := d.parseReference(ref)
	if err != nil {
		return nil, err
	}

	if d.objCache == nil {
		d.objCache = map[int]PDFValue{}
	}
	d.objCache[ref.ObjNum] = v
	return v, nil
}

// parseReference performs the actual disk read and parse for an indirect
// object. Callers should go through resolveReference, which caches the
// result. It dispatches between objects found at a classic byte offset (xref
// type 1) and objects packed inside a compressed object stream (xref type 2,
// PDF 1.5+; see objstm.go).
func (d *Document) parseReference(ref PDFRef) (PDFValue, error) {
	if offset, ok := d.xrefTable[ref.ObjNum]; ok {
		return d.parseClassicReference(ref, offset)
	}
	if entry, ok := d.compressedXref[ref.ObjNum]; ok {
		return d.resolveCompressedObject(ref, entry)
	}
	return nil, fmt.Errorf("object %d not found in xref table", ref.ObjNum)
}

// parseClassicReference performs the actual disk read and parse for an
// indirect object stored at a classic byte offset.
func (d *Document) parseClassicReference(ref PDFRef, offset int64) (PDFValue, error) {
	if _, err := d.file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}

	l := NewLexerAt(d.file, offset)
	defer l.Release()

	d.recordFraming(ref.ObjNum, l.validateObjectStart())

	t := l.NextToken()

	switch t.Type {
	case TokenDictStart:
		m, err := parseDictionary(l)
		if err != nil {
			return nil, err
		}

		m.Entries["_ref"] = ref

		// 6.1.8: capture EOL/whitespace right after '>>' before NextToken swallows it;
		// only used if next token is 'endobj'. Skipped when l.pushed is non-empty, since
		// parseDictionary's trailing-integer lookahead may have already read past '>>'.
		var preEOLErr error
		var leadingWS bool
		if len(l.pushed) == 0 {
			preEOLErr = l.requireEOL()
			if b, err := l.reader.Peek(1); err == nil && len(b) > 0 && isWhitespace(b[0]) {
				leadingWS = true
			}
		}

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
			var errs []error
			if preEOLErr != nil {
				errs = append(errs, fmt.Errorf("endobj not preceded by single EOL: %v", preEOLErr))
			}
			if leadingWS {
				errs = append(errs, fmt.Errorf("white space before endobj keyword"))
			}
			errs = append(errs, l.validateObjectEnd()...)
			d.recordFraming(ref.ObjNum, errs)
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
