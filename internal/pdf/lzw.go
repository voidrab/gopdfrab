package pdf

import "fmt"

const (
	lzwClearTable = 256
	lzwEOD        = 257
	lzwFirstCode  = 258
	lzwMaxCode    = 4096
)

// maxLZWOutput caps decoded output so a crafted stream cannot OOM. Var, not
// const, only so tests can lower it.
var maxLZWOutput = 256 << 20

// DecodeLZW decodes a PDF LZWDecode stream into its uncompressed bytes, using
// the default /EarlyChange of 1.
func DecodeLZW(data []byte) ([]byte, error) { return DecodeLZWParams(data, 1) }

// DecodeLZWParams decodes a PDF LZWDecode stream. earlyChange selects when the
// code width grows: 1 (the /EarlyChange default) bumps one code before the
// table boundary, 0 bumps at the boundary. Any other value is treated as 1.
func DecodeLZWParams(data []byte, earlyChange int) ([]byte, error) {
	if earlyChange != 0 {
		earlyChange = 1
	}
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
		if len(out) > maxLZWOutput {
			return nil, ErrOutputTooLarge
		}

		if prev != nil && nextCode < lzwMaxCode {
			table[nextCode] = append(append([]byte{}, prev...), entry[0])
			nextCode++
			// The code width grows once the just-added entry's number reaches
			// the boundary; with EarlyChange 1 (the default) that happens one
			// code sooner than the table size alone would require.
			switch nextCode + earlyChange {
			case 512:
				codeWidth = 10
			case 1024:
				codeWidth = 11
			case 2048:
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
