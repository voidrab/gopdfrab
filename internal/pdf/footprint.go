package pdf

import "unsafe"

// Footprint reports what a Reader is holding in the Go heap. It exists to
// attribute a conversion's memory between the resolved object graph and the
// derived caches.
//
// Byte totals are accumulated as entries go in, so reading a Footprint is
// O(1). Objects and Nodes are counts only: a resolved object's size is not
// knowable without walking it, and the walk would cost more than the number is
// worth.
type Footprint struct {
	// Objects is the number of resolved objects held in the object cache.
	Objects int
	// Nodes is the number of graph nodes (dict Entries maps and array
	// backing arrays) marked resolved.
	Nodes int
	// DecodedStreams / DecodedBytes describe the decoded-stream cache. A
	// stream whose filter chain is a no-op is counted even though its bytes
	// alias the mapping rather than the heap.
	DecodedStreams int
	DecodedBytes   int64
	// ScannedStreams / ScannedBytes describe the tokenized-content cache.
	// ScannedBytes is an estimate: the operator structs, their names, their
	// operand slice headers, and the operand values themselves (see
	// scannedOpsBytes).
	ScannedStreams int
	ScannedBytes   int64
	// ObjStreams / ObjStmObjects describe the object-stream cache, which
	// holds every object of every object stream in addition to the object
	// cache.
	ObjStreams    int
	ObjStmObjects int
}

// Total is the byte total Footprint can actually account for: the two stream
// caches. It is the quantity a resident-set budget is measured against.
func (f Footprint) Total() int64 { return f.DecodedBytes + f.ScannedBytes }

// Footprint reports the Reader's current cache occupancy.
func (d *Reader) Footprint() Footprint {
	f := Footprint{
		Objects:        len(d.objCache),
		Nodes:          len(d.resolvedPtrs),
		DecodedStreams: len(d.decodedCache),
		DecodedBytes:   d.decodedBytes,
		ScannedStreams: len(d.scanCache),
		ScannedBytes:   d.scanBytes,
		ObjStreams:     len(d.objStmCache),
	}
	for _, entries := range d.objStmCache {
		f.ObjStmObjects += len(entries)
	}
	return f
}

// scannedOpsBytes estimates what a tokenized content stream costs on the heap:
// the operator structs, their names, the operand slice headers, and the operand
// values. Operands are built fresh per operator by the content lexer -- they are
// not shared with the resolved graph -- so leaving them out under-counted a
// vector-heavy stream by about a quarter, and the resident budget under-bound by
// the same margin.
func scannedOpsBytes(ops []ScannedOp) int64 {
	var n int64
	for _, op := range ops {
		n += scannedOpBytes(op.Op, op.Operands)
	}
	return n
}

// scannedOpBytes is scannedOpsBytes for a single operator, so a scan that
// accounts as it goes (see Reader.scanAndMaybeCache) and one that accounts a
// finished list agree by construction.
func scannedOpBytes(op string, operands []PDFValue) int64 {
	const opSize = int64(unsafe.Sizeof(ScannedOp{}))
	return opSize + int64(len(op)) + scannedOperandsBytes(operands)
}

// ifaceSize is one PDFValue slot: a (type, data) pair.
const ifaceSize = 16

func scannedOperandsBytes(operands []PDFValue) int64 {
	n := int64(len(operands)) * ifaceSize
	for _, v := range operands {
		n += scannedValueBytes(v)
	}
	return n
}

// scannedValueBytes estimates the heap a content-stream operand owns beyond its
// interface slot. Only what the lexer allocates fresh counts: names are interned
// and an inline image's byte spans alias the decoded stream, so both are charged
// for their box alone.
func scannedValueBytes(v PDFValue) int64 {
	const boxSize = 8 // a boxed scalar
	switch t := v.(type) {
	case PDFInteger, PDFReal, PDFBoolean:
		return boxSize
	case PDFName:
		return int64(unsafe.Sizeof(t))
	case PDFString:
		return int64(unsafe.Sizeof(t)) + int64(len(t.Value))
	case PDFHexString:
		return int64(unsafe.Sizeof(t)) + int64(len(t.Value))
	case PDFArray:
		return int64(unsafe.Sizeof(t)) + scannedOperandsBytes(t)
	case PDFDict:
		n := int64(unsafe.Sizeof(t))
		for k, e := range t.Entries.All() {
			n += int64(len(k)) + ifaceSize + scannedValueBytes(e)
		}
		return n
	case InlineImageRaw:
		return int64(unsafe.Sizeof(t))
	}
	return boxSize
}
