package pdfrab

import (
	"bufio"
	"fmt"
	"io"
)

type TokenVisitor func(t Token, pos int64) error

type StreamVisitor func(r *bufio.Reader, pos int64) error

// TraverseTokens traverses all tokens in the file applying the visitor function
func (d *Document) TraverseTokens(tokenVisitor TokenVisitor, streamVisitor StreamVisitor) []error {
	if _, err := d.file.Seek(0, io.SeekStart); err != nil {
		return []error{fmt.Errorf("failed to seek: %w", err)}
	}

	l := NewLexer(d.file)

	errs := []error{}

	for {
		tok := l.NextToken()
		if tok.Type == TokenEOF {
			break
		}

		if tok.Type == TokenStreamStart && streamVisitor != nil {
			err := streamVisitor(l.reader, l.pos)
			if err != nil {
				errs = append(errs, err)
			}
		}

		if tok.Type == TokenError {
			err := fmt.Errorf("lexing error at offset %d: %v", l.pos, tok.Value)
			errs = append(errs, err)
			continue
		}

		err := tokenVisitor(tok, l.pos)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}
