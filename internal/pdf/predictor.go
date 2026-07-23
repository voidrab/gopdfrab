package pdf

import "fmt"

// FilterDecodeParms returns the decode parameters for filter i of an n-filter
// chain. /DecodeParms (or the abbreviated /DP) may be:
//
//   - absent, giving an empty dict for every filter;
//   - an array, giving element i positionally (a null element is legal and
//     means "no parameters for this filter");
//   - a single dict, which strictly belongs to filter 0. Real-world writers
//     emit /Filter [/ASCII85Decode /FlateDecode] with a bare
//     /DecodeParms <</Predictor 12>>, so for n > 1 a lone dict is attached to
//     the chain's only predictor-taking filter when there is exactly one, and
//     to filter 0 otherwise.
//
// Reads dict.Entries directly with no reference resolution, so it stays safe
// on the xref/objstm bootstrap path where the resolver is not yet live.
func FilterDecodeParms(dict PDFDict, i, n int) PDFDict {
	parms := dict.Entries["DecodeParms"]
	if parms == nil {
		parms = dict.Entries["DP"]
	}
	switch p := parms.(type) {
	case PDFDict:
		if n <= 1 || i == loneParmsFilter(dict, n) {
			return p
		}
	case PDFArray:
		if i < len(p) {
			if d, ok := p[i].(PDFDict); ok {
				return d
			}
		}
	}
	return PDFDict{}
}

// loneParmsFilter returns the index a lone DecodeParms dict belongs to in an
// n-filter chain: the sole predictor-taking filter if there is exactly one,
// otherwise filter 0.
func loneParmsFilter(dict PDFDict, n int) int {
	names := FilterNames(dict.Entries["Filter"])
	idx, count := 0, 0
	for i, name := range names {
		if info, ok := LookupFilter(name); ok && info.Predictor {
			idx, count = i, count+1
		}
	}
	if count == 1 {
		return idx
	}
	return 0
}

// dictInt reads an integer-valued entry from dict, returning def if absent or
// not an integer.
func DictInt(dict PDFDict, key string, def int) int {
	if v, ok := dict.Entries[key].(PDFInteger); ok {
		return int(v)
	}
	return def
}

// UndoStreamPredictor reverses the PNG (Predictor >= 10) or TIFF
// (Predictor == 2) predictor described by parms, returning data unchanged when
// no predictor applies. Cross-reference and object streams commonly apply the
// PNG "Up" predictor to compress their tabular data; image streams may apply
// either. opts supplies the fallback Columns/Colors/BitsPerComponent for
// callers whose context implies different defaults from the spec's 1/1/8.
func UndoStreamPredictor(data []byte, parms PDFDict, opts DecodeOptions) ([]byte, error) {
	predictor := DictInt(parms, "Predictor", 1)
	if predictor == 1 {
		return data, nil
	}

	columns := DictInt(parms, "Columns", orDefault(opts.Columns, 1))
	colors := DictInt(parms, "Colors", orDefault(opts.Colors, 1))
	bpc := DictInt(parms, "BitsPerComponent", orDefault(opts.BitsPerComponent, 8))

	switch {
	case predictor == 2:
		return UndoTIFFPredictor(data, columns, colors, bpc)
	case predictor >= 10 || opts.LenientPredictor:
		return UndoPNGPredictor(data, columns, colors, bpc)
	default:
		return nil, fmt.Errorf("%w %d", ErrUnsupportedPredictor, predictor)
	}
}

// orDefault returns v when it is set (non-zero), else def.
func orDefault(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

// undoPNGPredictor reverses the PNG-style per-row predictor (ISO 32000-1
// 7.4.4.4 / RFC 2083 §6): each output row is prefixed by a one-byte filter
// type (None/Sub/Up/Average/Paeth) describing how it was encoded relative to
// the previous row and to already-decoded bytes within the same row.
func UndoPNGPredictor(data []byte, columns, colors, bpc int) ([]byte, error) {
	// Bound attacker-controlled params before sizing the per-row buffer.
	const maxPredictorColumns = 1 << 20
	if columns <= 0 || columns > maxPredictorColumns ||
		colors <= 0 || colors > 32 || bpc <= 0 || bpc > 32 {
		return nil, fmt.Errorf("invalid predictor parameters: columns=%d colors=%d bpc=%d", columns, colors, bpc)
	}
	bpp := max(1, (colors*bpc+7)/8)
	rowBytes := (columns*colors*bpc + 7) / 8
	if rowBytes <= 0 {
		return nil, fmt.Errorf("invalid predictor parameters: columns=%d colors=%d bpc=%d", columns, colors, bpc)
	}

	out := make([]byte, 0, len(data))
	prev := make([]byte, rowBytes)
	pos := 0
	for pos < len(data) {
		filterType := data[pos]
		pos++

		avail := min(rowBytes, len(data)-pos)
		row := make([]byte, rowBytes)
		copy(row, data[pos:pos+avail])
		pos += avail

		switch filterType {
		case 0: // None
		case 1: // Sub
			for i := range row {
				var left byte
				if i >= bpp {
					left = row[i-bpp]
				}
				row[i] += left
			}
		case 2: // Up
			for i := range row {
				row[i] += prev[i]
			}
		case 3: // Average
			for i := range row {
				var left int
				if i >= bpp {
					left = int(row[i-bpp])
				}
				row[i] = byte((int(row[i]) + (left+int(prev[i]))/2) & 0xFF)
			}
		case 4: // Paeth
			for i := range row {
				var left, upLeft int
				if i >= bpp {
					left = int(row[i-bpp])
					upLeft = int(prev[i-bpp])
				}
				row[i] = byte((int(row[i]) + paethPredictor(left, int(prev[i]), upLeft)) & 0xFF)
			}
		default:
			return nil, fmt.Errorf("unsupported PNG predictor filter type %d", filterType)
		}

		out = append(out, row...)
		prev = row
	}
	return out, nil
}

// paethPredictor implements the PNG Paeth predictor function (RFC 2083 §6.6).
func paethPredictor(a, b, c int) int {
	p := a + b - c
	pa, pb, pc := AbsInt(p-a), AbsInt(p-b), AbsInt(p-c)
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	default:
		return c
	}
}

// undoTIFFPredictor reverses TIFF predictor 2 (horizontal differencing),
// applied per sample within each row.
func UndoTIFFPredictor(data []byte, columns, colors, bpc int) ([]byte, error) {
	if bpc != 8 {
		return nil, fmt.Errorf("TIFF predictor only supported for 8-bit samples, got %d", bpc)
	}
	rowBytes := columns * colors
	if rowBytes <= 0 {
		return nil, fmt.Errorf("invalid predictor parameters: columns=%d colors=%d", columns, colors)
	}
	out := make([]byte, len(data))
	copy(out, data)
	for rowStart := 0; rowStart+rowBytes <= len(out); rowStart += rowBytes {
		row := out[rowStart : rowStart+rowBytes]
		for i := colors; i < len(row); i++ {
			row[i] += row[i-colors]
		}
	}
	return out, nil
}
