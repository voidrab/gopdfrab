package pdfrab

import "fmt"

const (
	lzwClearTable = 256
	lzwEOD        = 257
	lzwFirstCode  = 258
	lzwMaxCode    = 4096
)

// decodeLZW decodes a PDF LZWDecode stream into its uncompressed bytes.
func decodeLZW(data []byte) ([]byte, error) {
	br := lzwBitReader{data: data}
	table := make([][]byte, lzwMaxCode)
	for i := range 256 {
		table[i] = []byte{byte(i)}
	}

	nextCode := lzwFirstCode
	codeWidth := 9
	var out, prev []byte

	for {
		code, ok := br.read(codeWidth)
		if !ok || code == lzwEOD {
			return out, nil
		}
		if code == lzwClearTable {
			nextCode, codeWidth, prev = lzwFirstCode, 9, nil
			continue
		}

		var entry []byte
		switch {
		case code < nextCode:
			entry = table[code]
		case code == nextCode && prev != nil:
			// The encoder may reference the very entry it's about to define.
			entry = append(append([]byte{}, prev...), prev[0])
		default:
			return nil, fmt.Errorf("lzw: invalid code %d", code)
		}
		out = append(out, entry...)

		if prev != nil && nextCode < lzwMaxCode {
			table[nextCode] = append(append([]byte{}, prev...), entry[0])
			nextCode++
			// the code width grows as soon as the just-added entry's
			// number reaches the boundary, one code sooner than the table
			// size alone would require.
			switch nextCode {
			case 511:
				codeWidth = 10
			case 1023:
				codeWidth = 11
			case 2047:
				codeWidth = 12
			}
		}
		prev = entry
	}
}

// lzwBitReader reads fixed-width codes from data, most-significant-bit first.
type lzwBitReader struct {
	data []byte
	pos  int
}

// read returns the next n-bit code, or ok=false if fewer than n bits remain.
func (r *lzwBitReader) read(n int) (code int, ok bool) {
	if r.pos+n > len(r.data)*8 {
		return 0, false
	}
	for range n {
		b := r.data[r.pos/8]
		bit := (b >> (7 - r.pos%8)) & 1
		code = code<<1 | int(bit)
		r.pos++
	}
	return code, true
}
