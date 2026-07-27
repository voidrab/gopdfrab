package pdf

import "unsafe"

// Footprint reports what a Reader is holding in the Go heap. It exists to
// attribute a conversion's memory between the resolved object graph and the
// derived caches, which is the measurement roadmap item 8 is built on.
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
	// ScannedBytes is an estimate: the operator structs, their names, and
	// their operand slice headers, but not the operand values, which are
	// shared with the graph.
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

// scannedOpsBytes estimates what a tokenized content stream costs on the heap.
// Operand values are excluded: they are shared with the resolved graph, which
// is accounted separately.
func scannedOpsBytes(ops []ScannedOp) int64 {
	const opSize = int64(unsafe.Sizeof(ScannedOp{}))
	const ifaceSize = 16 // a PDFValue slot in the operand slice
	n := int64(len(ops)) * opSize
	for _, op := range ops {
		n += int64(len(op.Op)) + int64(len(op.Operands))*ifaceSize
	}
	return n
}
