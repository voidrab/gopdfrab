package pdf

import "fmt"

// streamDecodeParms returns the /DecodeParms (or abbreviated /DP) dictionary
// associated with dict's stream, or a zero-value dict if none is present.
// Handles both the common single-dict form and the per-filter array form,
// preferring the last array entry (the parameters for the last-applied
// filter), which covers the single-filter case used by cross-reference and
// object streams.
func StreamDecodeParms(dict PDFDict) PDFDict {
	parms := dict.Entries["DecodeParms"]
	if parms == nil {
		parms = dict.Entries["DP"]
	}
	switch p := parms.(type) {
	case PDFDict:
		return p
	case PDFArray:
		for i := len(p) - 1; i >= 0; i-- {
			if d, ok := p[i].(PDFDict); ok {
				return d
			}
		}
	}
	return PDFDict{}
}

// dictInt reads an integer-valued entry from dict, returning def if absent or
// not an integer.
func DictInt(dict PDFDict, key string, def int) int {
	if v, ok := dict.Entries[key].(PDFInteger); ok {
		return int(v)
	}
	return def
}

// decodeStreamPredicted decodes dict's stream like decodeStream, then undoes
// any PNG (Predictor >= 10) or TIFF (Predictor == 2) predictor recorded in its
// DecodeParms. Cross-reference and object streams commonly apply the PNG "Up"
// predictor to improve compression of their tabular data.
func decodeStreamPredicted(dict PDFDict) ([]byte, error) {
	data, err := DecodeStream(dict)
	if err != nil {
		return nil, err
	}

	parms := StreamDecodeParms(dict)
	predictor := DictInt(parms, "Predictor", 1)
	if predictor == 1 {
		return data, nil
	}

	columns := DictInt(parms, "Columns", 1)
	colors := DictInt(parms, "Colors", 1)
	bpc := DictInt(parms, "BitsPerComponent", 8)

	switch {
	case predictor == 2:
		return UndoTIFFPredictor(data, columns, colors, bpc)
	case predictor >= 10:
		return UndoPNGPredictor(data, columns, colors, bpc)
	default:
		return nil, fmt.Errorf("unsupported predictor %d", predictor)
	}
}

// undoPNGPredictor reverses the PNG-style per-row predictor (ISO 32000-1
// 7.4.4.4 / RFC 2083 §6): each output row is prefixed by a one-byte filter
// type (None/Sub/Up/Average/Paeth) describing how it was encoded relative to
// the previous row and to already-decoded bytes within the same row.
func UndoPNGPredictor(data []byte, columns, colors, bpc int) ([]byte, error) {
	// /Columns, /Colors and /BitsPerComponent come straight from the stream's
	// /DecodeParms and are attacker-controlled. Bound them before sizing any
	// per-row buffer so a value like /Columns 1000000000 on a tiny stream
	// cannot force a multi-gigabyte allocation (mirrors the CCITT column cap).
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
