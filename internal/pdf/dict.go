package pdf

import (
	"iter"
	"slices"
)

// Dict is a PDF dictionary's entry set. It is pointer-shaped on purpose, and
// three properties follow from that -- all three are load-bearing:
//
//   - nil is the absent dictionary, so `d.Entries == nil` keeps working and
//     keeps meaning "this was not a dictionary".
//   - the pointer is a stable identity that survives mutation, which is what
//     ValuePointer keys on. A bare slice would not do: appending an entry can
//     move the backing array, and roughly eighty sites across this repo key
//     cycle guards, content-usage suppression sets, the writer's discovery
//     index and the fixers' parent index on that identity. An identity that
//     changed under Set would fail to dedupe or fail to suppress, silently.
//   - a PDFDict copied by value shares its entries, exactly as it did when
//     Entries was a map.
//
// The representation is a slice, not a map, because PDF dictionaries are
// small: measured over the 774 committed fixtures, 60,017 distinct
// dictionaries hold 3.84 entries on average, 95% have six or fewer and 99%
// have eight or fewer. At those sizes a Go map costs 392-440 bytes against a
// slice's 184-328, and the resolved graph is dominated by that scaffolding
// rather than by stream bytes (those alias the mmap).
//
// Above promoteThreshold entries a key->index map is built alongside, so a
// pathologically wide dictionary does not turn lookups quadratic.
type Dict = *DictBody

// DictBody is Dict's referent. Use Dict; this type is named only so the
// methods have somewhere to live and so godoc reads sensibly.
type DictBody struct {
	ent []DictEntry
	// idx is nil until the entry count passes promoteThreshold, at which
	// point it maps key -> index into ent and is maintained thereafter.
	idx map[string]int
}

// DictEntry is one key/value pair, in insertion order.
type DictEntry struct {
	Key string
	Val PDFValue
}

// promoteThreshold is the entry count past which a Dict also maintains an
// index map. Linear scan wins comfortably below it -- 99.8% of dictionaries
// in the committed corpora have twelve entries or fewer -- and the index
// bounds the tail.
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

// DictOf builds a dictionary from a map. Iteration order of a Go map is
// random, so the entries are inserted in sorted key order to keep the result
// reproducible; callers that need a specific order should Set in that order.
func DictOf(m map[string]PDFValue) Dict {
	d := NewDictCap(len(m))
	for _, k := range slices.Sorted(maps_Keys(m)) {
		d.Set(k, m[k])
	}
	return d
}

// maps_Keys avoids importing "maps" purely for one iterator.
func maps_Keys(m map[string]PDFValue) iter.Seq[string] {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
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

// Get returns the value stored under k, or nil when k is absent. Note that a
// present-but-null entry is also nil: null is a nil PDFValue (see PDFValue),
// and the spec treats a null entry and an absent one as equivalent. Use
// Lookup only when the distinction genuinely matters, which is rare.
//
// Safe on a nil Dict, matching a read from a nil map.
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
// It panics on a nil Dict, matching a write to a nil map.
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

// Del removes k if present. Removal preserves the order of the remaining
// entries, so a document's key order survives a fixer deleting one entry.
func (d Dict) Del(k string) {
	i := d.find(k)
	if i < 0 {
		return
	}
	d.ent = append(d.ent[:i], d.ent[i+1:]...)
	if d.idx != nil {
		d.reindex()
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

// All iterates the entries in insertion order. Safe on a nil Dict.
//
// The order being deterministic is a side effect, not a licence: every walk
// whose output must be reproducible still sorts explicitly (ctx.sortedKeys,
// pdfWriter.sortedEntryKeys, pruneUnusedResourceEntries). Relying on
// insertion order there would only hide a missing sort, and the writer's
// reproducibility argument has to stand on its own.
func (d Dict) All() iter.Seq2[string, PDFValue] {
	return func(yield func(string, PDFValue) bool) {
		if d == nil {
			return
		}
		for i := range d.ent {
			if !yield(d.ent[i].Key, d.ent[i].Val) {
				return
			}
		}
	}
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

// Clone returns a shallow copy: a new Dict with the same keys pointing at the
// same values.
func (d Dict) Clone() Dict {
	if d == nil {
		return nil
	}
	c := &DictBody{ent: slices.Clone(d.ent)}
	if d.idx != nil {
		c.reindex()
	}
	return c
}

// trim releases surplus capacity left by append's growth, so a freshly parsed
// dictionary retains only what it holds. Worth doing once at the end of
// parsing -- append grows 1,2,4,8, so a 3-entry dictionary otherwise carries
// a 4-entry array and a 6-entry one an 8-entry array.
func (d Dict) trim() {
	if d == nil || cap(d.ent) == len(d.ent) {
		return
	}
	d.ent = slices.Clip(d.ent)
}
