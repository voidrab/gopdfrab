package pdfrab

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strconv"
)

// filterNames returns the list of filter names applied to a stream.
func filterNames(filter PDFValue) []string {
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

// decodeStream returns the decoded bytes of a stream dictionary. Only the
// FlateDecode filter (and unfiltered data) is supported, which is sufficient for
// the content and metadata streams PDF/A inspection requires.
func decodeStream(dict PDFDict) ([]byte, error) {
	if !dict.HasStream {
		return nil, fmt.Errorf("object is not a stream")
	}
	data := dict.RawStream
	for _, f := range filterNames(dict.Entries["Filter"]) {
		switch f {
		case "FlateDecode", "Fl":
			r, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			out, err := io.ReadAll(r)
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

// ContentScanner walks a decoded content stream, exposing each operator together
// with its operands. It reuses the object Lexer for tokenising; bare operators
// arrive as keyword tokens.
type ContentScanner struct {
	lex   *Lexer
	stack []PDFValue
}

func newContentScanner(data []byte) *ContentScanner {
	return &ContentScanner{lex: NewLexer(bytes.NewReader(data))}
}

// scan iterates the content stream, invoking fn for each operator with the
// operands collected since the previous operator.
func (cs *ContentScanner) scan(fn func(op string, operands []PDFValue)) {
	for {
		tok := cs.lex.NextToken()
		switch tok.Type {
		case TokenEOF, TokenError:
			return
		case TokenInteger:
			i, _ := strconv.Atoi(tok.Value)
			cs.stack = append(cs.stack, PDFInteger(i))
		case TokenReal:
			f, _ := strconv.ParseFloat(tok.Value, 64)
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
				// Inline image: collect parameters up to ID, then skip binary
				// image data up to EI.
				cs.scanInlineImage(fn)
				cs.stack = nil
				continue
			}
			fn(op, cs.stack)
			cs.stack = nil
		default:
			// obj/stream keywords do not appear in content streams.
			cs.stack = nil
		}
	}
}

// scanInlineImage handles a BI ... ID <data> EI sequence: it reads the inline
// image parameters, reports them via the "INLINEIMAGE" pseudo-operator, then
// skips the binary data.
func (cs *ContentScanner) scanInlineImage(fn func(op string, operands []PDFValue)) {
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
			i, _ := strconv.Atoi(tok.Value)
			params = append(params, PDFInteger(i))
		case TokenReal:
			f, _ := strconv.ParseFloat(tok.Value, 64)
			params = append(params, PDFReal(f))
		case TokenBoolean:
			params = append(params, PDFBoolean(tok.Value == "true"))
		case TokenArrayStart:
			if arr, err := parseArray(cs.lex); err == nil {
				params = append(params, arr)
			}
		}
	}
	fn("INLINEIMAGE", params)
	cs.skipToEI()
}

// skipToEI consumes raw bytes up to and including the EI operator that
// terminates inline image data.
func (cs *ContentScanner) skipToEI() {
	r := cs.lex.reader
	var prev byte = ' '
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		if isWhitespace(prev) && b == 'E' {
			next, err := r.Peek(1)
			if err == nil && len(next) == 1 && next[0] == 'I' {
				r.ReadByte() // consume 'I'
				after, err := r.Peek(1)
				if err != nil || isWhitespace(after[0]) || isDelimiter(after[0]) {
					return
				}
			}
		}
		prev = b
	}
}
