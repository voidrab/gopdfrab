package pdf

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"unsafe"

	"github.com/klauspost/compress/zlib"
)

// filterNames returns the list of filter names applied to a stream.
func FilterNames(filter PDFValue) []string {
	switch f := filter.(type) {
	case PDFName:
		return []string{f.Value}
	case PDFArray:
		var out []string
		for _, item := range f {
			if name, ok := item.(PDFName); ok {
				out = append(out, name.Value)
			}
		}
		return out
	}
	return nil
}

var zlibReaderPool = sync.Pool{}

// inflateBufPool holds *bytes.Buffer scratch space for InflateZlib, reused
// across calls so its backing array grows to a working size once instead of
// reallocating/copying on every decode (unlike a fresh io.ReadAll per call).
var inflateBufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// inflateZlib decodes a zlib (FlateDecode/Fl) stream using a pooled decoder.
func InflateZlib(data []byte) ([]byte, error) {
	br := bytes.NewReader(data)

	var zr io.ReadCloser
	if pooled, ok := zlibReaderPool.Get().(io.ReadCloser); ok {
		if resetter, ok := pooled.(zlib.Resetter); ok && resetter.Reset(br, nil) == nil {
			zr = pooled
		}
	}
	if zr == nil {
		var err error
		zr, err = zlib.NewReader(br)
		if err != nil {
			return nil, err
		}
	}

	buf := inflateBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	// Pre-size for the typical ~4x Flate ratio, capped so pooled buffers
	// never pin large allocations.
	const maxInflatePrealloc = 16 << 20
	if need := min(len(data)*4, maxInflatePrealloc); buf.Cap() < need {
		buf.Grow(need - buf.Len())
	}
	_, err := buf.ReadFrom(zr)
	zlibReaderPool.Put(zr)

	// A truncated or checksum-broken zlib stream (common in malformed PDFs)
	// still yields a usable prefix; return what inflated rather than
	// discarding it, matching how lenient readers recover such streams. The
	// result must be copied out before the buffer is returned to the pool,
	// since Reset() on the next call reuses (and overwrites) its backing array.
	if err != nil && buf.Len() == 0 {
		inflateBufPool.Put(buf)
		return nil, err
	}
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	inflateBufPool.Put(buf)
	return out, nil
}

// decodeStream returns the decoded bytes of a stream dictionary, applying
// FlateDecode, ASCIIHexDecode, and ASCII85Decode filters as needed.
func DecodeStream(dict PDFDict) ([]byte, error) {
	if !dict.HasStream {
		return nil, fmt.Errorf("object is not a stream")
	}
	data := dict.RawStream
	for _, f := range FilterNames(dict.Entries["Filter"]) {
		switch f {
		case "FlateDecode", "Fl":
			out, err := InflateZlib(data)
			if err != nil {
				return nil, err
			}
			data = out
		case "ASCIIHexDecode", "AHx":
			out, err := DecodeASCIIHex(data)
			if err != nil {
				return nil, err
			}
			data = out
		case "ASCII85Decode", "A85":
			out, err := DecodeASCII85(data)
			if err != nil {
				return nil, err
			}
			data = out
		default:
			return nil, fmt.Errorf("unsupported filter %q", f)
		}
	}
	return data, nil
}

// StreamKey identifies a stream's raw (undecoded) bytes by content identity:
// the RawStream slice's data pointer and length. A fixer that rewrites a
// stream always assigns a fresh RawStream slice (SetStreamFlate et al.), so
// keying a decode cache on StreamKey makes invalidation automatic -- an
// unchanged stream keeps hitting, a rewritten one always misses.
type StreamKey struct {
	ptr uintptr
	len int
}

// StreamKeyOf returns dict's cache key and whether it is cacheable. Streams
// with no bytes (nil/empty RawStream) are not cacheable, since there is no
// address to key on and decoding them is trivial anyway.
func StreamKeyOf(dict PDFDict) (StreamKey, bool) {
	if !dict.HasStream || len(dict.RawStream) == 0 {
		return StreamKey{}, false
	}
	return StreamKey{ptr: uintptr(unsafe.Pointer(&dict.RawStream[0])), len: len(dict.RawStream)}, true
}

// DecodeCached decodes a stream using cache to avoid repeated decompression,
// keyed by StreamKeyOf so the cache stays correct across a convert run's
// fixer iterations even as streams are rewritten in place.
func DecodeCached(dict PDFDict, cache map[StreamKey][]byte) ([]byte, error) {
	key, ok := StreamKeyOf(dict)
	if !ok {
		return DecodeStream(dict)
	}
	if data, ok := cache[key]; ok {
		return data, nil
	}
	data, err := DecodeStream(dict)
	if err != nil {
		return nil, err
	}
	cache[key] = data
	return data, nil
}

// decodeASCIIHex decodes an ASCIIHexDecode stream: pairs of hex digits,
// terminated by '>'. Whitespace between pairs is ignored.
func DecodeASCIIHex(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)/2)
	i := 0
	for i < len(data) {
		for i < len(data) && isWhitespaceByte(data[i]) {
			i++
		}
		if i >= len(data) {
			break
		}
		if data[i] == '>' {
			break // EOD marker
		}
		hi := hexDigit(data[i])
		if hi < 0 {
			return nil, fmt.Errorf("invalid hex digit %q in ASCIIHexDecode", data[i])
		}
		i++
		for i < len(data) && isWhitespaceByte(data[i]) {
			i++
		}
		var lo int
		if i >= len(data) || data[i] == '>' {
			lo = 0 // odd number of digits: last nibble = 0
		} else {
			lo = hexDigit(data[i])
			if lo < 0 {
				return nil, fmt.Errorf("invalid hex digit %q in ASCIIHexDecode", data[i])
			}
			i++
		}
		out = append(out, byte(hi<<4|lo))
	}
	return out, nil
}

// DecodeASCII85 decodes an ASCII85Decode stream.
func DecodeASCII85(data []byte) ([]byte, error) {
	out := make([]byte, 0, len(data)*4/5)
	i := 0
	for i < len(data) {
		for i < len(data) && isWhitespaceByte(data[i]) {
			i++
		}
		if i >= len(data) {
			break
		}
		if data[i] == '~' {
			break // EOD '~>'
		}
		if data[i] == 'z' {
			// Special case: 5 zero bytes encoded as 'z'.
			out = append(out, 0, 0, 0, 0)
			i++
			continue
		}
		// Read up to 5 base-85 digits.
		var b [5]byte
		n := 0
		for n < 5 && i < len(data) && !isWhitespaceByte(data[i]) && data[i] != '~' {
			b[n] = data[i] - '!'
			if b[n] > 84 {
				return nil, fmt.Errorf("invalid ASCII85 byte %d", data[i])
			}
			n++
			i++
			for i < len(data) && isWhitespaceByte(data[i]) {
				i++
			}
		}
		if n == 0 {
			break
		}
		// Pad with 'u' (84) for partial groups.
		for n < 5 {
			b[n] = 84
			n++
		}
		v := uint32(b[0])*85*85*85*85 + uint32(b[1])*85*85*85 + uint32(b[2])*85*85 + uint32(b[3])*85 + uint32(b[4])
		// Determine how many output bytes to emit (partial group = n-1 bytes).
		switch {
		case n >= 5:
			out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		case n == 4:
			out = append(out, byte(v>>24), byte(v>>16), byte(v>>8))
		case n == 3:
			out = append(out, byte(v>>24), byte(v>>16))
		case n == 2:
			out = append(out, byte(v>>24))
		}
	}
	return out, nil
}

func isWhitespaceByte(b byte) bool {
	return b == 0x00 || b == 0x09 || b == 0x0A || b == 0x0C || b == 0x0D || b == 0x20
}

func hexDigit(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	}
	return -1
}

// InlineImageRaw carries an inline image's verbatim "BI...EI" byte span,
// appended as the last element of the "INLINEIMAGE" pseudo-operator's
// operands so writeContentStream can re-emit it unchanged. Existing
// consumers that scan operands in (key, value) pairs are unaffected: the
// trailing odd element falls outside every "i+1 < len(operands)" loop.
//
// Data holds just the image bytes between ID's separator and EI's separator
// (excluding both), for a fixer that needs to inspect or replace them (e.g.
// re-encoding LZW to Flate) without textually searching Bytes for "ID"/"EI"
// -- which would be unsafe, since arbitrary binary image data can itself
// contain those bytes. buildInlineImageBytes (content_writer.go) is the
// inverse: it rebuilds a fresh Bytes span from edited params and Data.
type InlineImageRaw struct {
	Bytes []byte
	Data  []byte
}

// ContentScanner walks a decoded content stream, exposing each operator together
// with its operands. It reuses the object Lexer for tokenising; bare operators
// arrive as keyword tokens.
type ContentScanner struct {
	lex   *Lexer
	stack []PDFValue
	data  []byte
}

func NewContentScanner(data []byte) *ContentScanner {
	return &ContentScanner{lex: NewLexerBytes(data, 0), data: data}
}

// ScannedOp is one content-stream operator paired with the operands collected
// before it, as an owned (non-aliasing) snapshot of what ContentScanner.Scan
// reports -- see TokenizeContent.
type ScannedOp struct {
	Op       string
	Operands []PDFValue
}

// TokenizeContent scans data once and returns every operator with its
// operands as an owned, replayable list. ContentScanner.Scan reuses one
// internal stack slice across operator callbacks, so operands are copied out
// here to remain valid after Scan returns (the PDFValues themselves, e.g. a
// TJ array, are still shared by reference -- consumers only read them).
func TokenizeContent(data []byte) []ScannedOp {
	var ops []ScannedOp
	NewContentScanner(data).Scan(func(op string, operands []PDFValue) {
		ops = append(ops, ScannedOp{Op: op, Operands: append([]PDFValue(nil), operands...)})
	})
	return ops
}

// ReplayOps invokes fn for each entry in ops in order, the same callback
// shape as ContentScanner.Scan, so a cached token list (see
// Reader.ScanStreamCached) can stand in for re-lexing an unchanged stream.
func ReplayOps(ops []ScannedOp, fn func(op string, operands []PDFValue)) {
	for _, o := range ops {
		fn(o.Op, o.Operands)
	}
}

// scan iterates the content stream, invoking fn for each operator with the
// operands collected since the previous operator.
func (cs *ContentScanner) Scan(fn func(op string, operands []PDFValue)) {
	defer cs.lex.Release()
	for {
		tok := cs.lex.NextToken()
		switch tok.Type {
		case TokenEOF, TokenError:
			return
		case TokenInteger:
			cs.stack = append(cs.stack, PDFInteger(tok.IntValue()))
		case TokenReal:
			f, _ := tok.RealValue()
			cs.stack = append(cs.stack, PDFReal(f))
		case TokenString:
			cs.stack = append(cs.stack, PDFString{Value: tok.Value})
		case TokenHexString:
			cs.stack = append(cs.stack, PDFHexString{Value: tok.Value})
		case TokenName:
			cs.stack = append(cs.stack, PDFName{Value: tok.Value})
		case TokenBoolean:
			cs.stack = append(cs.stack, PDFBoolean(tok.Value == "true"))
		case TokenArrayStart:
			if arr, err := parseArray(cs.lex); err == nil {
				cs.stack = append(cs.stack, arr)
			}
		case TokenDictStart:
			if dict, err := parseDictionary(cs.lex); err == nil {
				cs.stack = append(cs.stack, dict)
			}
		case TokenKeyword:
			op := tok.Value
			if op == "BI" {
				cs.scanInlineImage(fn)
				cs.stack = cs.stack[:0]
				continue
			}
			fn(op, cs.stack)
			cs.stack = cs.stack[:0]
		default:
			// obj/stream keywords do not appear in content streams.
			cs.stack = cs.stack[:0]
		}
	}
}

// scanInlineImage handles a BI ... ID <data> EI sequence: it reads the inline
// image parameters, then skips the binary data and reports the parameters,
// the verbatim "BI...EI" byte span, and the image data alone (via
// InlineImageRaw) through the "INLINEIMAGE" pseudo-operator.
func (cs *ContentScanner) scanInlineImage(fn func(op string, operands []PDFValue)) {
	start := cs.lex.pos - 2 // "BI" was already consumed by the caller's NextToken
	var params []PDFValue
	for {
		tok := cs.lex.NextToken()
		if tok.Type == TokenEOF || tok.Type == TokenError {
			return
		}
		if tok.Type == TokenKeyword && tok.Value == "ID" {
			break
		}
		switch tok.Type {
		case TokenName:
			params = append(params, PDFName{Value: tok.Value})
		case TokenInteger:
			params = append(params, PDFInteger(tok.IntValue()))
		case TokenReal:
			f, _ := tok.RealValue()
			params = append(params, PDFReal(f))
		case TokenBoolean:
			params = append(params, PDFBoolean(tok.Value == "true"))
		case TokenArrayStart:
			if arr, err := parseArray(cs.lex); err == nil {
				params = append(params, arr)
			}
		}
	}
	// Exactly one whitespace byte separates "ID" from the image data (PDF
	// 32000 Table 92); consume it so dataStart points at the data itself.
	if b, err := cs.lex.readByte(); err == nil && !IsWhitespace(b) {
		cs.lex.unreadByte()
	}
	dataStart := cs.lex.pos
	dataEnd := cs.skipToEI()
	if dataEnd < dataStart {
		dataEnd = dataStart
	}
	fn("INLINEIMAGE", append(params, InlineImageRaw{
		Bytes: cs.data[start:cs.lex.pos],
		Data:  cs.data[dataStart:dataEnd],
	}))
}

// skipToEI consumes raw bytes up to and including the EI operator that
// terminates inline image data, via cs.lex.readByte so cs.lex.pos -- which
// scanInlineImage uses to slice the verbatim image span -- stays accurate.
// It returns the offset of the single whitespace byte required immediately
// before "EI" (i.e. the end of the image data, exclusive).
func (cs *ContentScanner) skipToEI() (dataEnd int64) {
	var prev byte = ' '
	for {
		b, err := cs.lex.readByte()
		if err != nil {
			return cs.lex.pos
		}
		if IsWhitespace(prev) && b == 'E' {
			if next, ok := cs.lex.peekByte(); ok && next == 'I' {
				cs.lex.readByte() // consume 'I'
				if after, ok := cs.lex.peekByte(); !ok || IsWhitespace(after) || isDelimiter(after) {
					return cs.lex.pos - 3 // pos minus "I", "E", and the separator
				}
			}
		}
		prev = b
	}
}
