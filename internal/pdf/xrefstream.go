package pdf

import (
	"fmt"
	"io"
)

// xrefRange is one (start, count) pair from a cross-reference stream's
// /Index array, describing a contiguous run of object numbers it covers.
type xrefRange struct {
	start, count int
}

// parseIndirectObjectAt performs a minimal, framing-check-free parse of the
// indirect object at offset: "N G obj <object> [stream ... ] endobj". It is
// used for bootstrap parsing (cross-reference streams, and the object stream
// container itself) before d.xrefTable exists, where the normal 6.1.8-checked
// path (parseClassicReference) cannot run yet. Per ISO 32000-1 7.5.8.2, a
// cross-reference stream's own /Length must be a direct integer, which this
// function relies on to read the stream body without resolving anything.
func parseIndirectObjectAt(file fileSource, offset int64) (objNum int, dict PDFDict, err error) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, PDFDict{}, err
	}
	l := NewLexerAt(file, offset)
	defer l.Release()

	objTok := l.NextToken()
	if objTok.Type != TokenInteger {
		return 0, PDFDict{}, fmt.Errorf("expected object number at offset %d, got %q", offset, objTok.Value)
	}
	objNum = objTok.IntValue()

	genTok := l.NextToken()
	if genTok.Type != TokenInteger {
		return 0, PDFDict{}, fmt.Errorf("expected generation number at offset %d", offset)
	}

	kwTok := l.NextToken()
	if kwTok.Type != TokenObjectStart {
		return 0, PDFDict{}, fmt.Errorf("expected 'obj' keyword at offset %d, got %q", offset, kwTok.Value)
	}

	dictTok := l.NextToken()
	if dictTok.Type != TokenDictStart {
		return 0, PDFDict{}, fmt.Errorf("expected dictionary at offset %d", offset)
	}
	dict, err = parseDictionary(l)
	if err != nil {
		return 0, PDFDict{}, err
	}

	next := l.NextToken()
	if next.Type != TokenStreamStart {
		l.UnreadToken(next)
		return objNum, dict, nil
	}
	dict.HasStream = true

	lengthVal, ok := dict.Entries["Length"].(PDFInteger)
	if !ok {
		return 0, PDFDict{}, fmt.Errorf("object %d: stream /Length must be a direct integer for bootstrap parsing", objNum)
	}
	length := int(lengthVal)
	if length < 0 {
		return 0, PDFDict{}, fmt.Errorf("object %d: negative stream /Length", objNum)
	}

	if err := l.skipEOL(); err != nil {
		return 0, PDFDict{}, fmt.Errorf("object %d: %w", objNum, err)
	}

	data := make([]byte, length)
	if _, err := file.ReadAt(data, l.pos); err != nil {
		return 0, PDFDict{}, fmt.Errorf("object %d: %w", objNum, err)
	}
	dict.RawStream = data

	return objNum, dict, nil
}

// tryParseXRefStream attempts to parse the object at offset as a
// cross-reference stream (ISO 32000-1 7.5.8), the PDF 1.5+ replacement for a
// classic "xref" table that folds the cross-reference and trailer roles into
// one stream object. On success it populates d.xrefTable (type-1 entries,
// direct byte offsets) and d.compressedXref (type-2 entries, objects packed
// inside an object stream), and returns the stream's own dictionary, which
// doubles as the trailer exactly like a classic trailer dict (it carries
// /Root, /Info, /ID, /Prev). fillIn mirrors ParseXRefSectionAt: when true,
// only object numbers not already recorded are added, so newer revisions
// (already parsed) win.
//
// PDF/A-1b (based on PDF 1.4) does not permit cross-reference streams, so a
// conformant file never reaches this path; it exists purely so arbitrary
// modern input PDFs (e.g. for conversion) using one can still be read.
func (d *Reader) tryParseXRefStream(offset int64, fillIn bool) (PDFDict, error) {
	_, dict, err := parseIndirectObjectAt(d.file, offset)
	if err != nil {
		return PDFDict{}, err
	}
	if !dict.HasStream {
		return PDFDict{}, fmt.Errorf("object at offset %d has no stream body", offset)
	}
	if !EqualPDFValue(dict.Entries["Type"], PDFName{Value: "XRef"}) {
		return PDFDict{}, fmt.Errorf("object at offset %d is not a cross-reference stream", offset)
	}

	data, err := decodeStreamPredicted(dict)
	if err != nil {
		return PDFDict{}, fmt.Errorf("cross-reference stream: %w", err)
	}

	widths, err := xrefFieldWidths(dict)
	if err != nil {
		return PDFDict{}, err
	}
	ranges, err := xrefIndexRanges(dict)
	if err != nil {
		return PDFDict{}, err
	}

	entryLen := widths[0] + widths[1] + widths[2]
	if entryLen == 0 {
		return PDFDict{}, fmt.Errorf("cross-reference stream: invalid /W field widths")
	}

	if d.xrefTable == nil {
		d.xrefTable = map[int]int64{}
	}

	pos := 0
	for _, rng := range ranges {
		for i := range rng.count {
			if pos+entryLen > len(data) {
				return PDFDict{}, fmt.Errorf("cross-reference stream: truncated entry table")
			}
			field := data[pos : pos+entryLen]
			pos += entryLen

			objNum := rng.start + i
			if objNum == 0 {
				continue // object 0 is always the head of the linked free list
			}

			typ := 1 // ISO 32000-1 Table 18: a zero-width type field defaults to type 1.
			if widths[0] > 0 {
				typ = int(beUint(field[:widths[0]]))
				field = field[widths[0]:]
			}
			f2 := beUint(field[:widths[1]])
			f3 := beUint(field[widths[1] : widths[1]+widths[2]])

			if fillIn {
				if _, ok := d.xrefTable[objNum]; ok {
					continue
				}
				if _, ok := d.compressedXref[objNum]; ok {
					continue
				}
			}

			switch typ {
			case 0: // free entry
				continue
			case 1: // in use: f2 = byte offset, f3 = generation
				d.xrefTable[objNum] = int64(f2) + d.pdfStart
			case 2: // compressed: f2 = container object-stream number, f3 = index within it
				if d.compressedXref == nil {
					d.compressedXref = map[int]compressedXrefEntry{}
				}
				d.compressedXref[objNum] = compressedXrefEntry{streamObjNum: int(f2), index: int(f3)}
			}
		}
	}

	return dict, nil
}

// xrefFieldWidths reads /W [w1 w2 w3] from a cross-reference stream dict.
func xrefFieldWidths(dict PDFDict) ([3]int, error) {
	arr, ok := dict.Entries["W"].(PDFArray)
	if !ok || len(arr) != 3 {
		return [3]int{}, fmt.Errorf("cross-reference stream: missing or malformed /W")
	}
	var w [3]int
	for i, v := range arr {
		n, ok := v.(PDFInteger)
		// Each field width is a small byte count (real streams use 1-4, the
		// spec tops out well under 8). Cap it so the sum computed by the caller
		// (entryLen = w0+w1+w2) cannot overflow int and turn `data[pos:pos+
		// entryLen]` into a panicking low>high slice.
		if !ok || n < 0 || n > 8 {
			return [3]int{}, fmt.Errorf("cross-reference stream: /W[%d] is not an integer in [0,8]", i)
		}
		w[i] = int(n)
	}
	return w, nil
}

// xrefIndexRanges reads /Index [start1 count1 start2 count2 ...], defaulting
// to the single range [0, Size) when /Index is absent (ISO 32000-1 7.5.8.2).
func xrefIndexRanges(dict PDFDict) ([]xrefRange, error) {
	arr, ok := dict.Entries["Index"].(PDFArray)
	if !ok {
		size, ok := dict.Entries["Size"].(PDFInteger)
		if !ok {
			return nil, fmt.Errorf("cross-reference stream: missing /Size")
		}
		return []xrefRange{{start: 0, count: int(size)}}, nil
	}
	if len(arr)%2 != 0 {
		return nil, fmt.Errorf("cross-reference stream: /Index has an odd number of elements")
	}
	ranges := make([]xrefRange, 0, len(arr)/2)
	for i := 0; i < len(arr); i += 2 {
		start, ok1 := arr[i].(PDFInteger)
		count, ok2 := arr[i+1].(PDFInteger)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("cross-reference stream: /Index entries must be integers")
		}
		ranges = append(ranges, xrefRange{start: int(start), count: int(count)})
	}
	return ranges, nil
}

// beUint decodes a big-endian unsigned integer from a byte slice of length
// 0-8, per the variable per-field widths recorded in /W.
func beUint(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}
