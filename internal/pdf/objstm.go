package pdf

import (
	"bytes"
	"fmt"
	"strconv"
)

// compressedXrefEntry locates an object stored inside a compressed object
// stream (PDF 1.5+, ISO 32000-1 7.5.7): the object lives at position index
// within the object stream whose own object number is streamObjNum.
type compressedXrefEntry struct {
	streamObjNum int
	index        int
}

// objStmEntry is one decoded object from a compressed object stream, paired
// with the object number /N declares it as.
type objStmEntry struct {
	objNum int
	value  PDFValue
}

// resolveCompressedObject returns the value of a compressed object recorded
// in a type-2 cross-reference entry.
func (d *Reader) resolveCompressedObject(ref PDFRef, entry compressedXrefEntry) (PDFValue, error) {
	entries, err := d.decodeObjStm(entry.streamObjNum)
	if err != nil {
		return nil, fmt.Errorf("object %d: %w", ref.ObjNum, err)
	}
	if entry.index < 0 || entry.index >= len(entries) {
		return nil, fmt.Errorf("object %d: index %d out of range in object stream %d", ref.ObjNum, entry.index, entry.streamObjNum)
	}
	return entries[entry.index].value, nil
}

// decodeObjStm decodes the object stream with object number streamObjNum
// (ISO 32000-1 7.5.7), returning its contained objects in declaration order.
// Results are cached per stream object number.
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
	if !ok || !dict.HasStream {
		return nil, fmt.Errorf("object %d is not an object stream", streamObjNum)
	}

	data, err := decodeStreamPredicted(dict)
	if err != nil {
		return nil, fmt.Errorf("object stream %d: %w", streamObjNum, err)
	}

	n := DictInt(dict, "N", -1)
	first := DictInt(dict, "First", -1)
	if n < 0 || first < 0 {
		return nil, fmt.Errorf("object stream %d: missing /N or /First", streamObjNum)
	}

	headerLex := NewLexer(bytes.NewReader(data))
	type pair struct{ objNum, offset int }
	pairs := make([]pair, 0, n)
	for i := range n {
		numTok := headerLex.NextToken()
		offTok := headerLex.NextToken()
		if numTok.Type != TokenInteger || offTok.Type != TokenInteger {
			headerLex.Release()
			return nil, fmt.Errorf("object stream %d: malformed header at entry %d", streamObjNum, i)
		}
		objNum, _ := strconv.Atoi(numTok.Value)
		off, _ := strconv.Atoi(offTok.Value)
		pairs = append(pairs, pair{objNum, off})
	}
	headerLex.Release()

	entries := make([]objStmEntry, 0, len(pairs))
	for i, p := range pairs {
		start := first + p.offset
		end := len(data)
		if i+1 < len(pairs) {
			end = first + pairs[i+1].offset
		}
		if start < 0 || end > len(data) || start > end {
			return nil, fmt.Errorf("object stream %d: object %d has an out-of-range offset", streamObjNum, p.objNum)
		}

		objLex := NewLexer(bytes.NewReader(data[start:end]))
		tok := objLex.NextToken()
		val, err := parseObject(objLex, tok)
		objLex.Release()
		if err != nil {
			return nil, fmt.Errorf("object stream %d: object %d: %w", streamObjNum, p.objNum, err)
		}

		// Compressed objects may not themselves be streams (ISO 32000-1
		// 7.5.7), so there is no RawStream to capture here. Stamp the
		// synthetic _ref entry that the rest of the package relies on, the
		// same as resolver.go's parseClassicReference does for
		// classically-stored objects.
		if dictVal, ok := val.(PDFDict); ok {
			dictVal.Entries["_ref"] = PDFRef{ObjNum: p.objNum}
			val = dictVal
		}

		entries = append(entries, objStmEntry{objNum: p.objNum, value: val})
	}

	if d.objStmCache == nil {
		d.objStmCache = map[int][]objStmEntry{}
	}
	d.objStmCache[streamObjNum] = entries
	return entries, nil
}
