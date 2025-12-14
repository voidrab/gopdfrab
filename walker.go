package pdfrab

import (
	"fmt"
	"io"
)

type TokenVisitor func(t Token, pos int, errs []error) error

// TraverseTokens travers all tokens in the file applying visitor
func (d *Document) TraverseTokens(visitor TokenVisitor) []error {
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
		// skip streams
		if tok.Type == TokenKeyword && tok.Value == "stream" {
			for {
				line, _, err := l.reader.ReadLine()
				if err != nil {
					errs = append(errs, err)
					continue
				}
				if string(line) == "endstream" {
					break
				}
			}
		}

		if tok.Type == TokenError {
			err := fmt.Errorf("lexing error at offset %d: %v", l.pos, tok.Value)
			errs = append(errs, err)
			continue
		}

		err := visitor(tok, l.pos, errs)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}
