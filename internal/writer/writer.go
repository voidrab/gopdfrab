package writer

import (
	"bytes"
	"compress/zlib"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"sort"
	"strconv"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// WritePDF serializes r's resolved object graph to w as a fresh PDF with a
// classic cross-reference table. See WriteDocument. (Named WritePDF rather
// than WriteTo since the latter is a reserved io.WriterTo method signature
// with a different return type.)
func WritePDF(r *pdf.Reader, w io.Writer) error {
	graph, err := r.ResolveGraph()
	if err != nil {
		return err
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return fmt.Errorf("writer: resolved graph is not a dictionary")
	}
	return WriteDocument(w, trailer)
}

// WriteDocument serializes a fully-resolved PDF object graph to w as a fresh,
// self-contained PDF with a cross-reference table.

func WriteDocument(w io.Writer, trailer pdf.PDFDict) error {
	wr := &pdfWriter{
		numbers: map[objectIdentity]int{},
		visited: map[uintptr]bool{},
	}
	wr.discover(trailer.Entries["Root"])
	wr.discover(trailer.Entries["Info"])

	cw := &countingWriter{w: w}

	// 6.1.2: version line followed by a binary-marker comment line (at least
	// four bytes, each > 127) on its own line.
	if _, err := fmt.Fprint(cw, "%PDF-1.4\n%\xc2\xb5\xc2\xb6\xc2\xb7\xc2\xb8\n"); err != nil {
		return err
	}

	offsets := make([]int64, len(wr.order)+1) // index 0 (the free-list head) is unused
	for i, obj := range wr.order {
		num := i + 1
		offsets[num] = cw.n
		if err := wr.writeIndirectObject(cw, num, obj); err != nil {
			return fmt.Errorf("writer: object %d: %w", num, err)
		}
	}

	xrefOffset := cw.n
	if _, err := fmt.Fprintf(cw, "xref\n0 %d\n0000000000 65535 f \n", len(wr.order)+1); err != nil {
		return err
	}
	for i := 1; i <= len(wr.order); i++ {
		if _, err := fmt.Fprintf(cw, "%010d 00000 n \n", offsets[i]); err != nil {
			return err
		}
	}

	newTrailer := map[string]pdf.PDFValue{
		"Size": pdf.PDFInteger(len(wr.order) + 1),
	}
	if root, ok := trailer.Entries["Root"]; ok {
		newTrailer["Root"] = root
	}
	if info, ok := trailer.Entries["Info"]; ok {
		newTrailer["Info"] = info
	}
	if id, ok := trailer.Entries["ID"]; ok {
		newTrailer["ID"] = id
	} else {
		// 6.1.3: the trailer shall contain an ID. Synthesize one deterministically
		// from content already fixed at this point (object count and xref offset)
		// rather than wall-clock time, so re-running WriteDocument on the same
		// input is reproducible; PDF/A permits ID[0] == ID[1].
		sum := md5.Sum(fmt.Appendf(nil, "gopdfrab:%d:%d", len(wr.order), xrefOffset))
		id := pdf.PDFHexString{Value: hex.EncodeToString(sum[:])}
		newTrailer["ID"] = pdf.PDFArray{id, id}
	}

	if _, err := fmt.Fprint(cw, "trailer\n"); err != nil {
		return err
	}
	if err := wr.writeDictEntries(cw, newTrailer); err != nil {
		return fmt.Errorf("writer: trailer: %w", err)
	}
	_, err := fmt.Fprintf(cw, "\nstartxref\n%d\n%%%%EOF", xrefOffset)
	return err
}

// MarkStreamDirty flags dict's stream as freshly-set decoded content (stored
// in dict.RawStream) rather than bytes read verbatim from disk, so
// WriteDocument Flate-encodes it instead of writing RawStream through
// unmodified. Future fixups that replace a stream's content (e.g.
// regenerating an XMP packet) should call this after setting RawStream.
func MarkStreamDirty(dict *pdf.PDFDict) {
	dict.Entries["_dirty"] = pdf.PDFBoolean(true)
}

// objectIdentity is the dedup key WriteDocument uses to recognise that two
// pdf.PDFValue occurrences refer to the same logical indirect object: either the
// object number it was read from disk under ("_ref"), or, for a dict with no
// "_ref" (synthesized fresh by a fixup, never read from disk), its Entries
// map's pointer identity.
type objectIdentity struct {
	hasRef bool
	objNum int
	ptr    uintptr
}

// isIndirectDict reports whether v must be written as its own indirect
// object rather than inlined where referenced.
func isIndirectDict(v pdf.PDFDict) bool {
	if _, ok := v.Entries["_ref"].(pdf.PDFRef); ok {
		return true
	}
	return v.HasStream
}

func identityOf(v pdf.PDFDict) objectIdentity {
	if ref, ok := v.Entries["_ref"].(pdf.PDFRef); ok {
		return objectIdentity{hasRef: true, objNum: ref.ObjNum}
	}
	return objectIdentity{ptr: pdf.ValuePointer(v.Entries)}
}

// pdfWriter accumulates the set of indirect objects reachable from the
// graph being serialized, in first-encounter order, and their assigned
// output object numbers (1-based, matching position in order).
type pdfWriter struct {
	numbers map[objectIdentity]int
	order   []pdf.PDFDict

	// visited guards against infinite recursion on any composite value
	// (dict or array) that participates in a cycle, indirect or not.
	visited map[uintptr]bool
}

// discover walks v, recording every indirect dict reachable from it (see
// isIndirectDict) in first-encounter order, and recursing into both indirect
// and inline composite values so nested indirect objects are found either way.
func (wr *pdfWriter) discover(v pdf.PDFValue) {
	switch val := v.(type) {
	case pdf.PDFDict:
		ptr := pdf.ValuePointer(val.Entries)
		if wr.visited[ptr] {
			return
		}
		wr.visited[ptr] = true

		if isIndirectDict(val) {
			id := identityOf(val)
			if _, ok := wr.numbers[id]; !ok {
				wr.numbers[id] = len(wr.order) + 1
				wr.order = append(wr.order, val)
			}
		}
		for k, child := range val.Entries {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			wr.discover(child)
		}

	case pdf.PDFArray:
		ptr := pdf.ValuePointer(val)
		if wr.visited[ptr] {
			return
		}
		wr.visited[ptr] = true
		for _, child := range val {
			wr.discover(child)
		}
	}
}

// writeIndirectObject writes "N 0 obj\n<body>\nendobj\n" for a previously
// discovered indirect object.
func (wr *pdfWriter) writeIndirectObject(cw *countingWriter, num int, val pdf.PDFDict) error {
	if _, err := fmt.Fprintf(cw, "%d 0 obj\n", num); err != nil {
		return err
	}

	if !val.HasStream {
		if err := wr.writeDictEntries(cw, val.Entries); err != nil {
			return err
		}
		_, err := fmt.Fprint(cw, "\nendobj\n")
		return err
	}

	raw := val.RawStream
	entries := val.Entries

	if dirty, _ := entries["_dirty"].(pdf.PDFBoolean); bool(dirty) {
		compressed, err := DeflateZlib(raw)
		if err != nil {
			return fmt.Errorf("re-encoding dirty stream: %w", err)
		}
		clone := make(map[string]pdf.PDFValue, len(entries)+1)
		maps.Copy(clone, entries)
		clone["Filter"] = pdf.PDFName{Value: "FlateDecode"}
		delete(clone, "DecodeParms")
		delete(clone, "DP")
		entries, raw = clone, compressed
	}

	// /Length is always recomputed from the bytes actually being written,
	// regardless of what the source declared (which may have been wrong, or
	// even a non-integer; see stream.go's tolerant Length handling).
	lengthClone := make(map[string]pdf.PDFValue, len(entries)+1)
	maps.Copy(lengthClone, entries)
	lengthClone["Length"] = pdf.PDFInteger(len(raw))

	if err := wr.writeDictEntries(cw, lengthClone); err != nil {
		return err
	}
	if _, err := fmt.Fprint(cw, "\nstream\n"); err != nil {
		return err
	}
	if _, err := cw.Write(raw); err != nil {
		return err
	}
	_, err := fmt.Fprint(cw, "\nendstream\nendobj\n")
	return err
}

// writeDictEntries writes "<< /Key value /Key2 value2 >>", skipping the
// synthetic "_ref"/"_dirty" bookkeeping keys and visiting real keys in sorted
// order for deterministic, diffable output.
func (wr *pdfWriter) writeDictEntries(cw *countingWriter, entries map[string]pdf.PDFValue) error {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		if k == "_ref" || k == "_dirty" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if _, err := fmt.Fprint(cw, "<<"); err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := fmt.Fprintf(cw, " /%s ", k); err != nil {
			return err
		}
		if err := wr.writeValue(cw, entries[k]); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(cw, " >>")
	return err
}

// writeValue serializes a single PDF value. An indirect dict (see
// isIndirectDict) is written as an "N 0 R" reference to its own
// already-discovered object instead of being inlined.
//
// pdf.PDFName, pdf.PDFString and pdf.PDFHexString are written by directly wrapping their
// stored Value in the appropriate delimiters with no further escaping: the
// lexer that produced Value (lexer.go's readName/readStringLiteral/
// readHexString) never interprets "#XX"/backslash/whitespace escapes either,
// so Value already holds exactly the bytes that belong between the
// delimiters, and this is the exact inverse operation.
func (wr *pdfWriter) writeValue(cw *countingWriter, v pdf.PDFValue) error {
	switch val := v.(type) {
	case nil:
		_, err := fmt.Fprint(cw, "null")
		return err

	case pdf.PDFDict:
		if isIndirectDict(val) {
			num, ok := wr.numbers[identityOf(val)]
			if !ok {
				return fmt.Errorf("internal error: indirect dict was not discovered before writing")
			}
			_, err := fmt.Fprintf(cw, "%d 0 R", num)
			return err
		}
		return wr.writeDictEntries(cw, val.Entries)

	case pdf.PDFArray:
		if _, err := fmt.Fprint(cw, "["); err != nil {
			return err
		}
		for i, child := range val {
			if i > 0 {
				if _, err := fmt.Fprint(cw, " "); err != nil {
					return err
				}
			}
			if err := wr.writeValue(cw, child); err != nil {
				return err
			}
		}
		_, err := fmt.Fprint(cw, "]")
		return err

	case pdf.PDFRef:
		return fmt.Errorf("encountered an unresolved reference %v; WriteDocument requires a fully-resolved graph (see Reader.ResolveGraph)", val)

	default:
		ok, err := writeScalar(cw, v)
		if !ok && err == nil {
			err = fmt.Errorf("unsupported value type %T", v)
		}
		return err
	}
}

// writeScalar serializes a pdf.PDFName/pdf.PDFString/pdf.PDFHexString/pdf.PDFInteger/
// pdf.PDFReal/pdf.PDFBoolean -- the value kinds with no indirect-object semantics,
// shared between writeValue (the full-document writer) and writeOperand
// (the content-stream writer, content_writer.go). ok is false for any other
// type, letting each caller apply its own array/dict/nil handling.
func writeScalar(w io.Writer, v pdf.PDFValue) (ok bool, err error) {
	switch val := v.(type) {
	case pdf.PDFName:
		_, err = fmt.Fprintf(w, "/%s", val.Value)
	case pdf.PDFString:
		_, err = fmt.Fprintf(w, "(%s)", val.Value)
	case pdf.PDFHexString:
		_, err = fmt.Fprintf(w, "<%s>", val.Value)
	case pdf.PDFInteger:
		_, err = fmt.Fprintf(w, "%d", int(val))
	case pdf.PDFReal:
		// Plain decimal, never scientific notation: lexer.go's readNumber
		// only accumulates digits/'.'/'+'/'-', so "1e+10" would not
		// round-trip through our own reader.
		_, err = fmt.Fprint(w, strconv.FormatFloat(float64(val), 'f', -1, 32))
	case pdf.PDFBoolean:
		s := "false"
		if bool(val) {
			s = "true"
		}
		_, err = fmt.Fprint(w, s)
	default:
		return false, nil
	}
	return true, err
}

// writeOperand serializes a content-stream operand: any value writeScalar
// handles, plus arrays and inline dictionaries (e.g. a BI inline-image
// parameter dict, or a BDC property list) -- operands are never indirect
// references, so unlike writeValue this never consults isIndirectDict.
func writeOperand(w io.Writer, v pdf.PDFValue) error {
	if ok, err := writeScalar(w, v); ok {
		return err
	}
	switch val := v.(type) {
	case pdf.PDFArray:
		if _, err := fmt.Fprint(w, "["); err != nil {
			return err
		}
		for i, child := range val {
			if i > 0 {
				if _, err := fmt.Fprint(w, " "); err != nil {
					return err
				}
			}
			if err := writeOperand(w, child); err != nil {
				return err
			}
		}
		_, err := fmt.Fprint(w, "]")
		return err

	case pdf.PDFDict:
		keys := make([]string, 0, len(val.Entries))
		for k := range val.Entries {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if _, err := fmt.Fprint(w, "<<"); err != nil {
			return err
		}
		for _, k := range keys {
			if _, err := fmt.Fprintf(w, " /%s ", k); err != nil {
				return err
			}
			if err := writeOperand(w, val.Entries[k]); err != nil {
				return err
			}
		}
		_, err := fmt.Fprint(w, " >>")
		return err

	case nil:
		_, err := fmt.Fprint(w, "null")
		return err

	default:
		return fmt.Errorf("unsupported value type %T", v)
	}
}

// countingWriter wraps an io.Writer, tracking the total number of bytes
// written so far, used to record each indirect object's byte offset for the
// cross-reference table.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// DeflateZlib encodes data as a zlib (FlateDecode) stream, the inverse of
// content.go's inflateZlib.
func DeflateZlib(data []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
