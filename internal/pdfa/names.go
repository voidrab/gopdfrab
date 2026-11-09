package pdfa

// Names is a read-only set of names that remembers the order they were given
// in, so a check walking it reports in a stable order. The zero Names is
// empty.
type Names struct {
	list []string
	set  map[string]bool
}

// NewNames returns the set of the given names, dropping repeats.
func NewNames(names ...string) Names {
	n := Names{set: make(map[string]bool, len(names))}
	for _, name := range names {
		if !n.set[name] {
			n.set[name] = true
			n.list = append(n.list, name)
		}
	}
	return n
}

// Has reports whether name is in the set.
func (n Names) Has(name string) bool { return n.set[name] }

// Len returns the number of names in the set.
func (n Names) Len() int { return len(n.list) }

// All returns the names in the order they were given, as a fresh slice.
func (n Names) All() []string { return append([]string(nil), n.list...) }
