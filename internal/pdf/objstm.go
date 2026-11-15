package pdf

import (
	"fmt"
)

// compressedXrefEntry locates an object stored inside a compressed object
// stream (PDF 1.5+, ISO 32000-1 7.5.7): the object lives at position index
// within the object stream whose own object number is streamObjNum.
type compressedXrefEntry struct {
	streamObjNum int
	index        int
}

// objStmEntry is one object of a compressed object stream, paired with the
// object number the stream's header declares it as. Until it is first looked
// up, raw holds its unparsed bytes; then value is set, or err when that one
// object failed to parse, and its neighbours are unaffected.
type objStmEntry struct {
	objNum int
	raw    []byte
	value  PDFValue
	err    error
}

// parse parses the entry's bytes, once, recording the object as declared
// under its number in the stream streamObjNum.
func (e *objStmEntry) parse(streamObjNum int) {
	if e.raw == nil {
		return
	}
	lex := NewLexerBytes(e.raw, 0)
	e.raw = nil
	val, err := parseObject(lex, lex.NextToken())
	if err != nil {
		e.err = fmt.Errorf("object stream %d: object %d: %w", streamObjNum, e.objNum, err)
		return
	}
	// Compressed objects may not themselves be streams (ISO 32000-1 7.5.7),
	// so there are no stream bytes to capture. Record the object's reference,
	// which the rest of the package relies on, as parseClassicReference in
	// resolver.go does for classic objects.
	if dict, ok := val.(PDFDict); ok {
		dict.SetRef(PDFRef{ObjNum: e.objNum})
		val = dict
	}
	e.value = val
}

// resolveCompressedObject returns the value of a compressed object recorded
// in a type-2 cross-reference entry. Only that object is parsed; the other
// objects of the stream stay unparsed.
func (d *Reader) resolveCompressedObject(ref PDFRef, entry compressedXrefEntry) (PDFValue, error) {
	entries, err := d.decodeObjStm(entry.streamObjNum)
	if err != nil {
		return nil, fmt.Errorf("object %d: %w", ref.ObjNum, err)
	}
	i := findObjStmEntry(entries, entry.index, ref.ObjNum)
	if i < 0 {
		return nil, fmt.Errorf("object %d: not in object stream %d", ref.ObjNum, entry.streamObjNum)
	}
	e := &entries[i]
	e.parse(entry.streamObjNum)
	return e.value, e.err
}

// findObjStmEntry returns the index of the entry for objNum, or -1: the one
// at index when its declared number agrees, else the first declaring objNum.
// A wrong xref index must not return, or stamp its reference onto, a
// different object.
func findObjStmEntry(entries []objStmEntry, index, objNum int) int {
	if index >= 0 && index < len(entries) && entries[index].objNum == objNum {
		return index
	}
	for i := range entries {
		if entries[i].objNum == objNum {
			return i
		}
	}
	return -1
}

// decodeObjStm decodes the object stream with object number streamObjNum
// (ISO 32000-1 7.5.7), returning its contained objects in declaration order,
// each still unparsed: objStmEntry.parse parses one. Results are cached per
// stream object number.
func (d *Reader) decodeObjStm(streamObjNum int) ([]objStmEntry, error) {
	if cached, ok := d.objStmCache[streamObjNum]; ok {
		return cached, nil
	}

	// The container is an ordinary indirect object; resolve it through the
	// normal (framing-checked) path like any other classically-stored object.
	v, err := d.ResolveReference(PDFRef{ObjNum: streamObjNum})
	if err != nil {
		return nil, fmt.Errorf("object stream %d: %w", streamObjNum, err)
	}
	dict, ok := v.(PDFDict)
	if !ok || !dict.IsStream() {
		return nil, fmt.Errorf("object %d is not an object stream", streamObjNum)
	}

	data, err := DecodeStream(dict)
	if err != nil {
		return nil, fmt.Errorf("object stream %d: %w", streamObjNum, err)
	}

	n := DictInt(dict, "N", -1)
	first := DictInt(dict, "First", -1)
	if n < 0 || first < 0 {
		return nil, fmt.Errorf("object stream %d: missing /N or /First", streamObjNum)
	}
	// Bound /N (each entry needs >=2 bytes) before pre-allocating pairs.
	const maxObjStmEntries = 1 << 20
	if n > len(data)/2 || n > maxObjStmEntries {
		return nil, fmt.Errorf("object stream %d: /N %d exceeds stream capacity", streamObjNum, n)
	}

	headerLex := NewLexerBytes(data, 0)
	type pair struct{ objNum, offset int }
	pairs := make([]pair, 0, n)
	for i := range n {
		numTok := headerLex.NextToken()
		offTok := headerLex.NextToken()
		if numTok.Type != TokenInteger || offTok.Type != TokenInteger {
			return nil, fmt.Errorf("object stream %d: malformed header at entry %d", streamObjNum, i)
		}
		pairs = append(pairs, pair{numTok.IntValue(), offTok.IntValue()})
	}

	entries := make([]objStmEntry, 0, len(pairs))
	for i, p := range pairs {
		start := first + p.offset
		end := len(data)
		if i+1 < len(pairs) {
			end = first + pairs[i+1].offset
		}
		// One malformed object fails only itself, not every object the
		// stream holds.
		if start < 0 || end > len(data) || start > end {
			entries = append(entries, objStmEntry{objNum: p.objNum,
				err: fmt.Errorf("object stream %d: object %d has an out-of-range offset", streamObjNum, p.objNum)})
			continue
		}

		entries = append(entries, objStmEntry{objNum: p.objNum, raw: data[start:end:end]})
	}

	if d.objStmCache == nil {
		d.objStmCache = map[int][]objStmEntry{}
	}
	d.objStmCache[streamObjNum] = entries
	return entries, nil
}
