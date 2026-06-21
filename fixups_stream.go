package pdfrab

import "fmt"

func init() {
	registerFixer(lzwStreamFixer{})
}

type lzwStreamFixer struct{}

func (lzwStreamFixer) Applies(c Check) bool {
	return c == Checks.Structure.StreamLZWFilter
}

func (lzwStreamFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) (PDFDict, bool) {
		if !d.HasStream || !hasLZWFilter(d.Entries["Filter"]) {
			return d, false
		}
		plaintext, err := lzwStreamPlaintext(d)
		if err != nil {
			return d, false
		}
		delete(d.Entries, "Filter")
		delete(d.Entries, "DecodeParms")
		delete(d.Entries, "DP")
		d.RawStream = plaintext
		MarkStreamDirty(&d)
		changed = true
		return d, true
	})
	return changed, nil
}

// hasLZWFilter reports whether filter (a stream's /Filter entry, name or
// array) includes the forbidden LZWDecode filter.
func hasLZWFilter(filter PDFValue) bool {
	for _, f := range filterNames(filter) {
		if f == "LZWDecode" || f == "LZW" {
			return true
		}
	}
	return false
}

// lzwStreamPlaintext decodes a stream using LZWDecode, running all filters and predictors
// to return the uncompressed plaintext. It duplicates the standard decoder chain logic so that
// verification paths remain unaffected by this fixer-specific decoder.
func lzwStreamPlaintext(dict PDFDict) ([]byte, error) {
	data := dict.RawStream
	for _, f := range filterNames(dict.Entries["Filter"]) {
		switch f {
		case "FlateDecode", "Fl":
			out, err := inflateZlib(data)
			if err != nil {
				return nil, err
			}
			data = out
		case "ASCIIHexDecode", "AHx":
			out, err := decodeASCIIHex(data)
			if err != nil {
				return nil, err
			}
			data = out
		case "ASCII85Decode", "A85":
			out, err := decodeASCII85(data)
			if err != nil {
				return nil, err
			}
			data = out
		case "LZWDecode", "LZW":
			out, err := decodeLZW(data)
			if err != nil {
				return nil, err
			}
			data = out
		default:
			return nil, fmt.Errorf("unsupported filter %q", f)
		}
	}

	parms := streamDecodeParms(dict)
	predictor := dictInt(parms, "Predictor", 1)
	if predictor == 1 {
		return data, nil
	}
	columns := dictInt(parms, "Columns", 1)
	colors := dictInt(parms, "Colors", 1)
	bpc := dictInt(parms, "BitsPerComponent", 8)
	switch {
	case predictor == 2:
		return undoTIFFPredictor(data, columns, colors, bpc)
	case predictor >= 10:
		return undoPNGPredictor(data, columns, colors, bpc)
	default:
		return nil, fmt.Errorf("unsupported predictor %d", predictor)
	}
}

// walkStreamDicts calls fix for every PDFDict found within v's dictionary entries or array elements,
// using cycle protection. Unlike walkDicts, it writes modified dictionaries back to the parent structure
// so that changes to stream fields take effect.
func walkStreamDicts(v PDFValue, visited map[uintptr]bool, fix func(PDFDict) (PDFDict, bool)) {
	switch val := v.(type) {
	case PDFDict:
		ptr := pdfValuePointer(val.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for k, child := range val.Entries {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			if cd, ok := child.(PDFDict); ok {
				if updated, ok := fix(cd); ok {
					val.Entries[k] = updated
					child = updated
				}
			}
			walkStreamDicts(child, visited, fix)
		}

	case PDFArray:
		ptr := pdfValuePointer(val)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for i, child := range val {
			if cd, ok := child.(PDFDict); ok {
				if updated, ok := fix(cd); ok {
					val[i] = updated
					child = updated
				}
			}
			walkStreamDicts(child, visited, fix)
		}
	}
}
