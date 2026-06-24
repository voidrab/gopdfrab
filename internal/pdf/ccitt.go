package pdf

import "fmt"

// This file implements a CCITT Group 3/Group 4 (ITU-T T.4/T.6) fax decoder,
// the codec behind PDF's CCITTFaxDecode filter. It is used only by the
// rasterizer (raster_image.go) to reproduce bilevel fax images faithfully
// when a page is flattened; a decode failure falls back to a placeholder, so
// it never affects conformance, only fidelity.

// CCITTParams holds the CCITTFaxDecode parameters that drive decoding.
type CCITTParams struct {
	Columns   int
	Rows      int
	K         int
	ByteAlign bool
	BlackIs1  bool
}

type ccittError string

func (e ccittError) Error() string { return string(e) }

const (
	errCCITTEOF ccittError = "ccitt: unexpected end of data"
	errCCITTEOL ccittError = "ccitt: end-of-line"
)

// decodeCCITT decodes a CCITT-encoded bitstream into packed 1-bit-per-pixel
// Rows (row-padded to a byte boundary), with each pixel's bit set per the
// BlackIs1 convention. K<0 is pure 2D (Group 4), K==0 pure 1D (Group 3 1D),
// K>0 mixed (Group 3 2D).
func DecodeCCITT(data []byte, p CCITTParams) ([]byte, error) {
	cols := p.Columns
	if cols <= 0 {
		cols = 1728
	}
	rowBytes := (cols + 7) / 8
	br := &ccittBitReader{data: data}
	ref := []int{cols, cols}
	pureG4 := p.K < 0

	var out []byte
	rowsDone := 0
	for {
		if p.Rows > 0 && rowsDone >= p.Rows {
			break
		}
		if br.eof() {
			break
		}
		if p.K >= 0 {
			br.skipEOL()
			if br.eof() {
				break
			}
		}

		is2D := pureG4
		if p.K > 0 {
			bit, ok := br.readBit()
			if !ok {
				break
			}
			is2D = bit == 0
		}

		var (
			cur []int
			err error
		)
		if is2D {
			cur, err = decode2DRow(br, ref, cols)
		} else {
			cur, err = decode1DRow(br, cols)
		}
		if err == errCCITTEOF || err == errCCITTEOL {
			break
		}
		if err != nil {
			return nil, err
		}

		out = append(out, packCCITTRow(cur, cols, p.BlackIs1)...)
		rowsDone++
		ref = append(cur[:len(cur):len(cur)], cols, cols)
		if p.ByteAlign {
			br.align()
		}
	}

	if rowsDone == 0 {
		return nil, fmt.Errorf("ccitt: no Rows decoded")
	}
	for p.Rows > 0 && rowsDone < p.Rows {
		out = append(out, packCCITTRow(nil, cols, p.BlackIs1)...)
		rowsDone++
	}
	_ = rowBytes
	return out, nil
}

// ccittBitReader reads bits MSB-first from a byte slice.
type ccittBitReader struct {
	data []byte
	pos  int
}

func (r *ccittBitReader) readBit() (int, bool) {
	if r.pos >= len(r.data)*8 {
		return 0, false
	}
	b := r.data[r.pos>>3]
	bit := int((b >> (7 - uint(r.pos&7))) & 1)
	r.pos++
	return bit, true
}

func (r *ccittBitReader) eof() bool { return r.pos >= len(r.data)*8 }

func (r *ccittBitReader) align() {
	if rem := r.pos % 8; rem != 0 {
		r.pos += 8 - rem
	}
}

// skipEOL consumes an EOL code (>=11 zero bits followed by a 1) when one is
// next in the stream, returning whether it did. The reader is left unmoved if
// the next bits are not an EOL.
func (r *ccittBitReader) skipEOL() bool {
	save := r.pos
	zeros := 0
	for {
		bit, ok := r.readBit()
		if !ok {
			r.pos = save
			return false
		}
		if bit == 0 {
			zeros++
			continue
		}
		if zeros >= 11 {
			return true
		}
		r.pos = save
		return false
	}
}

// readRun reads one complete white or black run length, summing make-up codes
// (>=64) until a terminating code (<64) ends the run.
func (r *ccittBitReader) readRun(white bool) (int, error) {
	total := 0
	for {
		n, err := r.readCode(white)
		if err != nil {
			return 0, err
		}
		total += n
		if n < 64 {
			return total, nil
		}
	}
}

// readCode reads a single run-length code (terminating or make-up) for the
// given colour, matching the canonical T.4 prefix codes bit by bit.
func (r *ccittBitReader) readCode(white bool) (int, error) {
	m := blackRunCodes
	if white {
		m = whiteRunCodes
	}
	code := 0
	for length := 1; length <= 14; length++ {
		bit, ok := r.readBit()
		if !ok {
			return 0, errCCITTEOF
		}
		code = code<<1 | bit
		if v, ok := m[uint32(length)<<16|uint32(code)]; ok {
			return v, nil
		}
	}
	return 0, fmt.Errorf("ccitt: invalid run code")
}

// 2D mode codes.
const (
	modePass = iota
	modeHoriz
	modeV0
	modeVR1
	modeVR2
	modeVR3
	modeVL1
	modeVL2
	modeVL3
)

// readMode reads one T.6 2D mode code. It returns errCCITTEOL on an EOL/EOFB
// marker and an error on a 2D extension code (unsupported), so callers stop
// cleanly and fall back to a placeholder rather than mis-decode.
func (r *ccittBitReader) readMode() (int, error) {
	bit, ok := r.readBit()
	if !ok {
		return 0, errCCITTEOF
	}
	if bit == 1 {
		return modeV0, nil // 1
	}
	if bit, ok = r.readBit(); !ok {
		return 0, errCCITTEOF
	}
	if bit == 1 { // 01x
		if bit, _ = r.readBit(); bit == 1 {
			return modeVR1, nil // 011
		}
		return modeVL1, nil // 010
	}
	if bit, ok = r.readBit(); !ok {
		return 0, errCCITTEOF
	}
	if bit == 1 {
		return modeHoriz, nil // 001
	}
	if bit, ok = r.readBit(); !ok {
		return 0, errCCITTEOF
	}
	if bit == 1 {
		return modePass, nil // 0001
	}
	if bit, ok = r.readBit(); !ok {
		return 0, errCCITTEOF
	}
	if bit == 1 { // 00001x
		if bit, _ = r.readBit(); bit == 1 {
			return modeVR2, nil // 000011
		}
		return modeVL2, nil // 000010
	}
	if bit, ok = r.readBit(); !ok {
		return 0, errCCITTEOF
	}
	if bit == 1 { // 000001x
		if bit, _ = r.readBit(); bit == 1 {
			return modeVR3, nil // 0000011
		}
		return modeVL3, nil // 0000010
	}
	// Six leading zeros: either a 2D extension (0000001...) or an EOL
	// (>=11 zeros then 1). Read on to disambiguate.
	if bit, ok = r.readBit(); !ok {
		return 0, errCCITTEOF
	}
	if bit == 1 {
		return 0, fmt.Errorf("ccitt: unsupported 2D extension code")
	}
	zeros := 7
	for {
		bit, ok = r.readBit()
		if !ok {
			return 0, errCCITTEOF
		}
		if bit == 1 {
			if zeros >= 11 {
				return 0, errCCITTEOL
			}
			return 0, fmt.Errorf("ccitt: invalid 2D mode code")
		}
		zeros++
	}
}

// vDelta maps a vertical mode to its offset from b1.
func vDelta(mode int) int {
	switch mode {
	case modeV0:
		return 0
	case modeVR1:
		return 1
	case modeVR2:
		return 2
	case modeVR3:
		return 3
	case modeVL1:
		return -1
	case modeVL2:
		return -2
	case modeVL3:
		return -3
	}
	return 0
}

// decode1DRow decodes one Group 3 1D row into its list of colour-change
// positions (transitions), starting from white at column 0.
func decode1DRow(br *ccittBitReader, Columns int) ([]int, error) {
	var cur []int
	pos, color := 0, 0
	for pos < Columns {
		run, err := br.readRun(color == 0)
		if err != nil {
			if len(cur) == 0 {
				return nil, err
			}
			break
		}
		pos += run
		if pos > Columns {
			pos = Columns
		}
		cur = append(cur, pos)
		color ^= 1
	}
	return cur, nil
}

// decode2DRow decodes one T.6 2D row relative to the reference line's
// transitions ref, returning the current line's transitions.
func decode2DRow(br *ccittBitReader, ref []int, Columns int) ([]int, error) {
	var cur []int
	a0, color := -1, 0
	for a0 < Columns {
		b1, b2 := findB1B2(ref, a0, color, Columns)
		mode, err := br.readMode()
		if err != nil {
			return nil, err
		}
		switch mode {
		case modePass:
			a0 = b2
		case modeHoriz:
			r1, err := br.readRun(color == 0)
			if err != nil {
				return nil, err
			}
			r2, err := br.readRun(color != 0)
			if err != nil {
				return nil, err
			}
			start := a0
			if start < 0 {
				start = 0
			}
			a1 := ClampInt(start+r1, 0, Columns)
			a2 := ClampInt(a1+r2, 0, Columns)
			cur = append(cur, a1, a2)
			a0 = a2
		default:
			a1 := ClampInt(b1+vDelta(mode), 0, Columns)
			cur = append(cur, a1)
			a0 = a1
			color ^= 1
		}
	}
	return cur, nil
}

// findB1B2 locates b1 (the first changing element on the reference line right
// of a0 with colour opposite a0's) and b2 (the next changing element after
// b1), per T.6. ref is the reference line's transitions terminated by two
// Columns sentinels.
func findB1B2(ref []int, a0, color, Columns int) (int, int) {
	i := 0
	for i < len(ref) && ref[i] <= a0 {
		i++
	}
	if i < len(ref) && (i%2) != color {
		i++
	}
	b1, b2 := Columns, Columns
	if i < len(ref) {
		b1 = ref[i]
	}
	if i+1 < len(ref) {
		b2 = ref[i+1]
	}
	return b1, b2
}

// packCCITTRow packs a row described by its transitions into 1-bpc bytes,
// setting each pixel's bit per the BlackIs1 convention (white -> !BlackIs1,
// black -> BlackIs1).
func packCCITTRow(trans []int, Columns int, BlackIs1 bool) []byte {
	row := make([]byte, (Columns+7)/8)
	set := func(from, to, color int) {
		black := color == 1
		if black != BlackIs1 {
			return
		}
		for x := from; x < to; x++ {
			row[x>>3] |= 1 << (7 - uint(x&7))
		}
	}
	prev, color := 0, 0
	for _, t := range trans {
		if t > Columns {
			t = Columns
		}
		set(prev, t, color)
		prev = t
		color ^= 1
	}
	set(prev, Columns, color)
	return row
}

// Canonical T.4 run-length codes, keyed (length<<16 | code) -> run length.
// Built once from the white/black terminating, make-up and shared make-up
// tables in ITU-T T.4 Tables 1-3.
var (
	whiteRunCodes = map[uint32]int{}
	blackRunCodes = map[uint32]int{}
)

type ccittCode struct {
	run, code, bits int
}

func init() {
	for _, c := range whiteCodeTable {
		whiteRunCodes[uint32(c.bits)<<16|uint32(c.code)] = c.run
	}
	for _, c := range sharedMakeupTable {
		whiteRunCodes[uint32(c.bits)<<16|uint32(c.code)] = c.run
	}
	for _, c := range blackCodeTable {
		blackRunCodes[uint32(c.bits)<<16|uint32(c.code)] = c.run
	}
	for _, c := range sharedMakeupTable {
		blackRunCodes[uint32(c.bits)<<16|uint32(c.code)] = c.run
	}
}

var whiteCodeTable = []ccittCode{
	{0, 0x35, 8}, {1, 0x7, 6}, {2, 0x7, 4}, {3, 0x8, 4}, {4, 0xB, 4}, {5, 0xC, 4},
	{6, 0xE, 4}, {7, 0xF, 4}, {8, 0x13, 5}, {9, 0x14, 5}, {10, 0x7, 5}, {11, 0x8, 5},
	{12, 0x8, 6}, {13, 0x3, 6}, {14, 0x34, 6}, {15, 0x35, 6}, {16, 0x2A, 6}, {17, 0x2B, 6},
	{18, 0x27, 7}, {19, 0xC, 7}, {20, 0x8, 7}, {21, 0x17, 7}, {22, 0x3, 7}, {23, 0x4, 7},
	{24, 0x28, 7}, {25, 0x2B, 7}, {26, 0x13, 7}, {27, 0x24, 7}, {28, 0x18, 7}, {29, 0x2, 8},
	{30, 0x3, 8}, {31, 0x1A, 8}, {32, 0x1B, 8}, {33, 0x12, 8}, {34, 0x13, 8}, {35, 0x14, 8},
	{36, 0x15, 8}, {37, 0x16, 8}, {38, 0x17, 8}, {39, 0x28, 8}, {40, 0x29, 8}, {41, 0x2A, 8},
	{42, 0x2B, 8}, {43, 0x2C, 8}, {44, 0x2D, 8}, {45, 0x4, 8}, {46, 0x5, 8}, {47, 0xA, 8},
	{48, 0xB, 8}, {49, 0x52, 8}, {50, 0x53, 8}, {51, 0x54, 8}, {52, 0x55, 8}, {53, 0x24, 8},
	{54, 0x25, 8}, {55, 0x58, 8}, {56, 0x59, 8}, {57, 0x5A, 8}, {58, 0x5B, 8}, {59, 0x4A, 8},
	{60, 0x4B, 8}, {61, 0x32, 8}, {62, 0x33, 8}, {63, 0x34, 8},
	{64, 0x1B, 5}, {128, 0x12, 5}, {192, 0x17, 6}, {256, 0x37, 7}, {320, 0x36, 8},
	{384, 0x37, 8}, {448, 0x64, 8}, {512, 0x65, 8}, {576, 0x68, 8}, {640, 0x67, 8},
	{704, 0xCC, 9}, {768, 0xCD, 9}, {832, 0xD2, 9}, {896, 0xD3, 9}, {960, 0xD4, 9},
	{1024, 0xD5, 9}, {1088, 0xD6, 9}, {1152, 0xD7, 9}, {1216, 0xD8, 9}, {1280, 0xD9, 9},
	{1344, 0xDA, 9}, {1408, 0xDB, 9}, {1472, 0x98, 9}, {1536, 0x99, 9}, {1600, 0x9A, 9},
	{1664, 0x18, 6}, {1728, 0x9B, 9},
}

var blackCodeTable = []ccittCode{
	{0, 0x37, 10}, {1, 0x2, 3}, {2, 0x3, 2}, {3, 0x2, 2}, {4, 0x3, 3}, {5, 0x3, 4},
	{6, 0x2, 4}, {7, 0x3, 5}, {8, 0x5, 6}, {9, 0x4, 6}, {10, 0x4, 7}, {11, 0x5, 7},
	{12, 0x7, 7}, {13, 0x4, 8}, {14, 0x7, 8}, {15, 0x18, 9}, {16, 0x17, 10}, {17, 0x18, 10},
	{18, 0x8, 10}, {19, 0x67, 11}, {20, 0x68, 11}, {21, 0x6C, 11}, {22, 0x37, 11}, {23, 0x28, 11},
	{24, 0x17, 11}, {25, 0x18, 11}, {26, 0xCA, 12}, {27, 0xCB, 12}, {28, 0xCC, 12}, {29, 0xCD, 12},
	{30, 0x68, 12}, {31, 0x69, 12}, {32, 0x6A, 12}, {33, 0x6B, 12}, {34, 0xD2, 12}, {35, 0xD3, 12},
	{36, 0xD4, 12}, {37, 0xD5, 12}, {38, 0xD6, 12}, {39, 0xD7, 12}, {40, 0x6C, 12}, {41, 0x6D, 12},
	{42, 0xDA, 12}, {43, 0xDB, 12}, {44, 0x54, 12}, {45, 0x55, 12}, {46, 0x56, 12}, {47, 0x57, 12},
	{48, 0x64, 12}, {49, 0x65, 12}, {50, 0x52, 12}, {51, 0x53, 12}, {52, 0x24, 12}, {53, 0x37, 12},
	{54, 0x38, 12}, {55, 0x27, 12}, {56, 0x28, 12}, {57, 0x58, 12}, {58, 0x59, 12}, {59, 0x2B, 12},
	{60, 0x2C, 12}, {61, 0x5A, 12}, {62, 0x66, 12}, {63, 0x67, 12},
	{64, 0xF, 10}, {128, 0xC8, 12}, {192, 0xC9, 12}, {256, 0x5B, 12}, {320, 0x33, 12},
	{384, 0x34, 12}, {448, 0x35, 12}, {512, 0x6C, 13}, {576, 0x6D, 13}, {640, 0x4A, 13},
	{704, 0x4B, 13}, {768, 0x4C, 13}, {832, 0x4D, 13}, {896, 0x72, 13}, {960, 0x73, 13},
	{1024, 0x74, 13}, {1088, 0x75, 13}, {1152, 0x76, 13}, {1216, 0x77, 13}, {1280, 0x52, 13},
	{1344, 0x53, 13}, {1408, 0x54, 13}, {1472, 0x55, 13}, {1536, 0x5A, 13}, {1600, 0x5B, 13},
	{1664, 0x64, 13}, {1728, 0x65, 13},
}

var sharedMakeupTable = []ccittCode{
	{1792, 0x8, 11}, {1856, 0xC, 11}, {1920, 0xD, 11}, {1984, 0x12, 12}, {2048, 0x13, 12},
	{2112, 0x14, 12}, {2176, 0x15, 12}, {2240, 0x16, 12}, {2304, 0x17, 12}, {2368, 0x1C, 12},
	{2432, 0x1D, 12}, {2496, 0x1E, 12}, {2560, 0x1F, 12},
}
