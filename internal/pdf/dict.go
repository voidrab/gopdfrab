package pdf

import (
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
)

// Dict is a PDF dictionary's entry set: a slice, since dictionaries are small
// and a Go map costs roughly twice the memory at that size.
type Dict = *DictBody

// DictBody is Dict's referent. Use Dict.
type DictBody struct {
	ent []DictEntry
	idx map[string]int // nil below promoteThreshold; key -> index into ent
}

// DictEntry is one key/value pair, in insertion order.
type DictEntry struct {
	Key string
	Val PDFValue
}

// promoteThreshold is where a Dict starts maintaining an index map. Linear
// scan wins below it; 99.8% of corpus dictionaries stay under.
const promoteThreshold = 12

// NewDict returns an empty dictionary.
func NewDict() Dict { return &DictBody{} }

// NewDictCap returns an empty dictionary sized for n entries.
func NewDictCap(n int) Dict {
	if n <= 0 {
		return &DictBody{}
	}
	d := &DictBody{ent: make([]DictEntry, 0, n)}
	return d
}

// DictOf builds a dictionary from a map, inserting in sorted key order so the
// result does not depend on map iteration order.
func DictOf(m map[string]PDFValue) Dict {
	d := NewDictCap(len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		d.Set(k, m[k])
	}
	return d
}

// find returns the index of key k, or -1.
func (d Dict) find(k string) int {
	if d == nil {
		return -1
	}
	if d.idx != nil {
		if i, ok := d.idx[k]; ok {
			return i
		}
		return -1
	}
	for i := range d.ent {
		if d.ent[i].Key == k {
			return i
		}
	}
	return -1
}

// Get returns the value under k, or nil when absent. A present-but-null entry
// is also nil -- see PDFValue. Safe on a nil Dict.
func (d Dict) Get(k string) PDFValue {
	if i := d.find(k); i >= 0 {
		return d.ent[i].Val
	}
	return nil
}

// Lookup is Get with the key-presence flag.
func (d Dict) Lookup(k string) (PDFValue, bool) {
	if i := d.find(k); i >= 0 {
		return d.ent[i].Val, true
	}
	return nil, false
}

// Set stores v under k, replacing any existing value and otherwise appending.
//
// Set panics on a nil Dict: there is no receiver to allocate into. The zero
// PDFDict has nil Entries, so a PDFDict returned alongside an error must not
// be written to; build a writable one with NewPDFDict. (A panic reaching a
// public entry point is returned as ErrInternal, not a crash.)
func (d Dict) Set(k string, v PDFValue) {
	if d == nil {
		panic("pdf: Set on a nil Dict")
	}
	if i := d.find(k); i >= 0 {
		d.ent[i].Val = v
		return
	}
	d.ent = append(d.ent, DictEntry{Key: k, Val: v})
	if d.idx != nil {
		d.idx[k] = len(d.ent) - 1
	} else if len(d.ent) > promoteThreshold {
		d.reindex()
	}
}

// Del removes k if present, preserving the order of the rest. It costs
// O(entries after k); to remove several keys, DeleteFunc does it in one pass.
func (d Dict) Del(k string) {
	i := d.find(k)
	if i < 0 {
		return
	}
	last := len(d.ent) - 1
	copy(d.ent[i:], d.ent[i+1:])
	d.ent[last] = DictEntry{} // do not keep the shifted-out value alive
	d.ent = d.ent[:last]
	if d.idx != nil {
		// Only the entries after i moved; renumber those in place rather
		// than rebuilding the whole index map.
		delete(d.idx, k)
		for j := i; j < len(d.ent); j++ {
			d.idx[d.ent[j].Key] = j
		}
	}
}

// reindex rebuilds idx from ent.
func (d Dict) reindex() {
	d.idx = make(map[string]int, len(d.ent))
	for i := range d.ent {
		d.idx[d.ent[i].Key] = i
	}
}

// Len reports the entry count. Safe on a nil Dict.
func (d Dict) Len() int {
	if d == nil {
		return 0
	}
	return len(d.ent)
}

// Clone returns a dictionary holding d's entries in a body of its own, so
// editing the clone leaves d -- and everything reading it -- as it was. The
// values themselves are shared, not copied.
func (d Dict) Clone() Dict {
	return &DictBody{ent: slices.Clone(d.ent), idx: maps.Clone(d.idx)}
}

// Assign makes d hold exactly src's entries. Every value sharing d's body,
// which is every reference to the object, sees the change: the way to publish
// an edit prepared on a Clone.
func (d Dict) Assign(src Dict) {
	*d = DictBody{ent: slices.Clone(src.ent), idx: maps.Clone(src.idx)}
}

// All iterates in insertion order. Safe on a nil Dict.
//
// Do not Set a new key or Del during iteration: unlike a Go map, removing an
// entry shifts its successors. Use DeleteFunc instead. Callers needing a
// reproducible order must still sort; insertion order is not a substitute.
func (d Dict) All() iter.Seq2[string, PDFValue] {
	return func(yield func(string, PDFValue) bool) {
		if d == nil {
			return
		}
		for i := 0; i < len(d.ent); i++ {
			if !yield(d.ent[i].Key, d.ent[i].Val) {
				return
			}
		}
	}
}

// DeleteFunc removes every entry pred selects, in one pass, keeping order.
// This is the safe way to remove while inspecting; All plus Del is not.
func (d Dict) DeleteFunc(pred func(k string, v PDFValue) bool) {
	if d == nil {
		return
	}
	out := d.ent[:0]
	for _, e := range d.ent {
		if !pred(e.Key, e.Val) {
			out = append(out, e)
		}
	}
	// Clear the tail so dropped values are not kept alive.
	for i := len(out); i < len(d.ent); i++ {
		d.ent[i] = DictEntry{}
	}
	d.ent = out
	if d.idx != nil {
		d.reindex()
	}
}

// String formats the dictionary in map[...] form with its keys sorted. Without
// it, fmt prints the pointer, which puts a heap address into every check
// message.
func (d Dict) String() string {
	if d == nil {
		return "map[]"
	}
	keys := d.Keys()
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("map[")
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s:%v", k, d.Get(k))
	}
	b.WriteByte(']')
	return b.String()
}

// Keys returns the keys in insertion order. The slice is freshly allocated;
// callers needing sorted order should sort it themselves.
func (d Dict) Keys() []string {
	if d == nil {
		return nil
	}
	out := make([]string, len(d.ent))
	for i := range d.ent {
		out[i] = d.ent[i].Key
	}
	return out
}
