package pdf

import (
	"fmt"
	"math"
)

// xrefRange is one (start, count) pair from a cross-reference stream's
// /Index array, describing a contiguous run of object numbers it covers.
type xrefRange struct {
	start, count int
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
	v, err := d.parseBootstrapAt(offset)
	if err != nil {
		return PDFDict{}, err
	}
	dict, ok := v.(PDFDict)
	if !ok || !dict.IsStream() {
		return PDFDict{}, fmt.Errorf("object at offset %d has no stream body", offset)
	}
	if !EqualPDFValue(dict.Entries.Get("Type"), PDFName{Value: "XRef"}) {
		return PDFDict{}, fmt.Errorf("object at offset %d is not a cross-reference stream", offset)
	}

	data, err := DecodeStream(dict)
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
				// An 8-byte field can exceed int64; such an offset is past
				// any file and would wrap negative, so leave it unrecorded.
				if f2 > uint64(math.MaxInt64-d.pdfStart) {
					continue
				}
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
	arr, ok := dict.Entries.Get("W").(PDFArray)
	if !ok || len(arr) != 3 {
		return [3]int{}, fmt.Errorf("cross-reference stream: missing or malformed /W")
	}
	var w [3]int
	for i, v := range arr {
		n, ok := v.(PDFInteger)
		// Cap each width so entryLen (w0+w1+w2) cannot overflow to a bad slice.
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
	arr, ok := dict.Entries.Get("Index").(PDFArray)
	if !ok {
		size, ok := dict.Entries.Get("Size").(PDFInteger)
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
