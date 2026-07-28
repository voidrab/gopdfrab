package pdf

import (
	"errors"
	"fmt"
	"io"
	"strconv"
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
		v, err := d.parseClassicReference(ref, offset)
		if err != nil && d.degradeUnresolvable {
			return d.recoverOrDegradeClassic(ref, offset, err)
		}
		return v, err
	}
	if entry, ok := d.compressedXref[ref.ObjNum]; ok {
		v, err := d.resolveCompressedObject(ref, entry)
		if err != nil && d.degradeUnresolvable {
			d.recordDegraded(ref.ObjNum, err)
			return nil, nil
		}
		return v, err
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

// errWrongObjectHeader flags a classic xref offset landing on a well-formed
// header that declares a different object number.
var errWrongObjectHeader = errors.New("object header does not match xref entry")

// recoverOrDegradeClassic handles a classic object that failed to parse at its
// xref offset: retry once at the object's real "N G obj" header found by
// scanning the file, else resolve to null. Either way the failure is recorded
// as a diagnostic and resolution continues, so one bad offset cannot suppress
// checks on unrelated objects.
func (d *Reader) recoverOrDegradeClassic(ref PDFRef, badOffset int64, cause error) (PDFValue, error) {
	if off, ok := d.scanForObjectHeader(ref.ObjNum, badOffset); ok {
		if v, err := d.parseClassicReference(ref, off); err == nil {
			d.xrefTable[ref.ObjNum] = off
			d.recordRecovered(ref.ObjNum, badOffset, off, cause)
			return v, nil
		}
	}
	// The xref lists the object, but the whole-file scan found no header for it
	// anywhere: it is not damaged, it is undefined, and a reference to an
	// undefined object *is* the null object (ISO 32000-1 7.3.10) -- the same
	// conclusion parseReference already reaches for an object absent from every
	// xref section. Nothing was lost, so this is not degradation, and reporting
	// it as such wrongly failed the conversion of documents whose only defect
	// is an over-long xref (20 files in the real-world corpus, mostly slide
	// decks from one export pipeline).
	if exists, scanned := d.objectHeaderExists(ref.ObjNum); scanned && !exists {
		return nil, nil
	}
	d.recordDegraded(ref.ObjNum, cause)
	return nil, nil
}

// objectHeaderExists reports whether the file physically contains any
// "objNum G obj" header, and whether the question could be answered at all --
// scanned is false when the whole-file scan could not read the file, in which
// case absence proves nothing.
func (d *Reader) objectHeaderExists(objNum int) (exists, scanned bool) {
	d.scanForObjectHeader(objNum, -1) // -1 excludes nothing; ensures the scan ran
	if len(d.headerScan) == 0 {
		return false, false
	}
	return len(d.headerScan[objNum]) > 0, true
}

// scanForObjectHeader returns the last "objNum G obj" header offset in the
// file other than exclude. The whole-file scan runs once and is shared across
// lookups.
func (d *Reader) scanForObjectHeader(objNum int, exclude int64) (int64, bool) {
	if d.headerScan == nil {
		d.headerScan = map[int][]int64{}
		if raw, err := d.fullBytes(); err == nil {
			for _, loc := range bruteForceObjRe.FindAllSubmatchIndex(raw, -1) {
				n, err := strconv.Atoi(string(raw[loc[2]:loc[3]]))
				if err != nil {
					continue
				}
				d.headerScan[n] = append(d.headerScan[n], int64(loc[2]))
			}
		}
	}
	offs := d.headerScan[objNum]
	for i := len(offs) - 1; i >= 0; i-- {
		if offs[i] != exclude {
			return offs[i], true
		}
	}
	return 0, false
}

// parseClassicReference performs the actual disk read and parse for an
// indirect object stored at a classic byte offset. On failure, framing
// diagnostics recorded during the attempt are discarded so a parse at a bad
// offset leaves no trace once the object is recovered elsewhere or nulled.
func (d *Reader) parseClassicReference(ref PDFRef, offset int64) (PDFValue, error) {
	mark := len(d.parseDiagnostics)
	v, err := d.parseClassicAt(ref, offset)
	if err != nil {
		d.discardObjDiagnostics(mark, ref.ObjNum)
		return nil, err
	}
	return v, nil
}

func (d *Reader) parseClassicAt(ref PDFRef, offset int64) (PDFValue, error) {
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

	headerNum, headerErrs := l.validateObjectStart()
	d.recordFraming(ref.ObjNum, headerErrs)
	if headerNum >= 0 && headerNum != ref.ObjNum {
		return nil, fmt.Errorf("%w: found object %d", errWrongObjectHeader, headerNum)
	}

	t := l.NextToken()

	// Scalars dispatch exactly as they do inside a containing object (see
	// parseScalarToken); an object body only adds decryption on top.
	if v, ok, err := parseScalarToken(t); ok {
		if err != nil {
			return nil, err
		}
		if d.shouldDecrypt(ref) {
			v = d.decryptStrings(v, ref)
		}
		return v, nil
	}

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
			if d.shouldDecrypt(ref) {
				if err := d.decryptStream(&m, ref); err != nil {
					return nil, err
				}
				d.decryptStrings(m, ref)
			}
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

		if d.shouldDecrypt(ref) {
			d.decryptStrings(m, ref)
		}
		return m, nil

	case TokenArrayStart:
		arr, err := parseArray(l)
		if err != nil {
			return nil, err
		}
		if d.shouldDecrypt(ref) {
			d.decryptStrings(arr, ref)
		}
		return arr, nil

	// An object body is a value, not a reference chain, so a leading integer is
	// just an integer -- no "N G R" lookahead, unlike parseObject.
	case TokenInteger:
		return PDFInteger(t.IntValue()), nil

	default:
		return nil, fmt.Errorf("unexpected token %v in object %d", t.Type, ref.ObjNum)
	}
}
