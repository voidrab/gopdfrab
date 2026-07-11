package pdf

import (
	"fmt"
	"io"
)

func (d *Reader) ResolveObject(obj PDFValue) (PDFValue, error) {
	return d.resolveInPlace(obj)
}

// resolveShallow dereferences obj if it is a PDFRef, without recursing into
// the result's own entries.
func (d *Reader) resolveShallow(obj PDFValue) (PDFValue, error) {
	if ref, ok := obj.(PDFRef); ok {
		return d.ResolveReference(ref)
	}
	return obj, nil
}

// ResolveReference resolves an indirect reference to its object, parsing it
// from disk at most once per object number.
func (d *Reader) ResolveReference(ref PDFRef) (PDFValue, error) {
	if cached, ok := d.objCache[ref.ObjNum]; ok {
		return cached, nil
	}

	// Re-entering an object still being parsed is a cycle (e.g. a stream whose
	// /Length references itself); resolve it to null instead of overflowing.
	if d.resolvingInProgress[ref.ObjNum] {
		return nil, nil
	}
	if d.resolvingInProgress == nil {
		d.resolvingInProgress = map[int]bool{}
	}
	d.resolvingInProgress[ref.ObjNum] = true
	v, err := d.parseReference(ref)
	delete(d.resolvingInProgress, ref.ObjNum)
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
// object. Callers should go through ResolveReference, which caches the
// result. It dispatches between objects found at a classic byte offset (xref
// type 1) and objects packed inside a compressed object stream (xref type 2,
// PDF 1.5+; see objstm.go).
func (d *Reader) parseReference(ref PDFRef) (PDFValue, error) {
	if offset, ok := d.xrefTable[ref.ObjNum]; ok {
		return d.parseClassicReference(ref, offset)
	}
	if entry, ok := d.compressedXref[ref.ObjNum]; ok {
		return d.resolveCompressedObject(ref, entry)
	}
	// Absent from every xref section: scan once for physically present but
	// unlisted objects, then treat a still-missing target as the null object
	// (ISO 32000-1 7.3.10).
	if !d.danglingScanRan {
		d.danglingScanRan = true
		if d.recoverXRefByBruteForceScan(true) == nil {
			return d.parseReference(ref)
		}
	}
	return nil, nil
}

// parseClassicReference performs the actual disk read and parse for an
// indirect object stored at a classic byte offset.
func (d *Reader) parseClassicReference(ref PDFRef, offset int64) (PDFValue, error) {
	var l *Lexer
	if d.data != nil {
		l = NewLexerBytes(d.data, offset)
	} else {
		if _, err := d.file.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		l = NewLexerAt(d.file, offset)
		defer l.Release()
	}

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
			if b, ok := l.peekByte(); ok && IsWhitespace(b) {
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

	case TokenInteger:
		return PDFInteger(t.IntValue()), nil

	case TokenReal:
		f, err := t.RealValue()
		if err != nil {
			return nil, err
		}
		return PDFReal(f), nil

	case TokenBoolean:
		return PDFBoolean(t.Value == "true"), nil

	case TokenName:
		return PDFName{Value: t.Value}, nil

	case TokenKeyword:
		// The only bare keyword valid as an object body is 'null', which is the
		// null object (ISO 32000-1 7.3.10), represented as a nil PDFValue.
		if t.Value == "null" {
			return nil, nil
		}
		return PDFName{Value: t.Value}, nil

	case TokenString:
		return PDFString{Value: t.Value}, nil

	case TokenHexString:
		return PDFHexString{Value: t.Value}, nil

	default:
		return nil, fmt.Errorf("unexpected token %v in object %d", t.Type, ref.ObjNum)
	}
}
