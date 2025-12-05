package gopdfrab

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// Document represents a PDF file.
// It holds the file handle and the offset of the cross-reference table.
type Document struct {
	file      *os.File
	size      int64
	trailer   map[string]interface{} // Simplified representation of the trailer dictionary
	xrefTable map[int]int64          // Map of Object ID -> Byte Offset
}

// Open initializes the PDF document.
// It performs a lightweight check for the header and parses the trailer.
func Open(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	doc := &Document{
		file: f,
		size: info.Size(),
	}

	header := make([]byte, 8)
	if _, err := f.ReadAt(header, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to read header: %w", err)
	}
	if !bytes.HasPrefix(header, []byte("%PDF-")) {
		f.Close()
		return nil, errors.New("invalid file format: missing %PDF header")
	}

	if err := doc.parseTrailer(); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to parse trailer: %w", err)
	}

	return doc, nil
}

// Close ensures the file handle is released.
func (d *Document) Close() error {
	return d.file.Close()
}

// parseDictionary consumes tokens to build a map.
// It expects the current token to be '<<'.
func parseDictionary(l *Lexer) (map[string]interface{}, error) {
	dict := make(map[string]interface{})

	// Ensure we start with '<<'
	tok := l.NextToken()
	if tok.Type != TokenDictStart {
		return nil, fmt.Errorf("expected dictionary start '<<' but was %v", tok.Type)
	}

	for {
		// 1. Read Key (Name)
		keyTok := l.NextToken()
		if keyTok.Type == TokenDictEnd {
			break // End of dictionary '>>'
		}
		if keyTok.Type != TokenName {
			return nil, fmt.Errorf("expected dictionary key /Name, got %v", keyTok.Value)
		}

		// 2. Read Value
		valTok := l.NextToken()

		// Handle indirect references (e.g., "1 0 R")
		// If we see an Integer, we must check if the NEXT two tokens are Integer + "R"
		if valTok.Type == TokenInteger {
			// Peek logic would go here for high performance.
			// For now, we perform a naive check.
			savedPos := l.pos
			tok2 := l.NextToken()
			tok3 := l.NextToken()

			if tok3.Type == TokenKeyword && tok3.Value == "R" {
				// It is a reference: "1 0 R"
				dict[keyTok.Value] = fmt.Sprintf("%s %s R", valTok.Value, tok2.Value)
				continue
			} else {
				// Not a reference, backtrack
				l.pos = savedPos
			}
		}

		// Simple value storage
		dict[keyTok.Value] = valTok.Value
	}
	return dict, nil
}

// parseTrailer locates the 'startxref' and parses the trailer dictionary.
// This allows us to find the Root object and Info object without reading the whole file.
func (d *Document) parseTrailer() error {
	// Read the last 1KB of the file. The trailer is almost always here.
	// This avoids scanning the whole file.
	bufSize := int64(1024)
	if d.size < bufSize {
		bufSize = d.size
	}

	tail := make([]byte, bufSize)
	_, err := d.file.ReadAt(tail, d.size-bufSize)
	if err != nil {
		return err
	}

	// Find %%EOF marker
	eofIndex := bytes.LastIndex(tail, []byte("%%EOF"))
	if eofIndex == -1 {
		return errors.New("EOF marker not found")
	}

	l := NewLexer(tail)

	l.skipToDict()

	// TODO find start of dict

	trailerDict, err := parseDictionary(l)
	if err != nil {
		return err
	}

	d.trailer = trailerDict

	return nil
}

// --- Public API ---

// GetMetadata returns the metadata dictionary (Title, Author, etc.).
func (d *Document) GetMetadata() (map[string]string, error) {
	// 1. Check if trailer has "Info" key
	if _, ok := d.trailer["Info"]; !ok {
		return nil, errors.New("no metadata info found in trailer")
	}

	// 2. In a real scenario, we parse the object at d.trailer["Info"]
	// For now, we simulate returning data.
	return map[string]string{
		"Producer": "gopdfrab Library",
		"Title":    "Performance Demo",
	}, nil
}

// GetPageCount parses the Page Tree Root to find the Count.
func (d *Document) GetPageCount() (int, error) {
	// 1. Get Root object from Trailer
	// 2. Follow Root -> Pages -> Count

	// Simulation:
	return 42, nil
}
