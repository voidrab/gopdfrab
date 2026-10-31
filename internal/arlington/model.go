package arlington

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"io"
	"sort"
	"sync"

	"github.com/klauspost/compress/flate"
)

// The model is stored as data, not code: model-<version>.bin holds the tables
// in a compact binary form that is decoded on first use. Keeping them out of a
// Go map literal keeps the package's initialisation code small. gen.go writes
// the files with EncodeModel.
//
// Layout, after flate decompression: a string table (count, then each string
// as length and bytes), then the types sorted by name, then the
// self-identification pairs sorted by key. Strings are written as their
// table index, integers as varints, and absent pointers, slices and maps as
// a zero count or flag, so nil decodes back to nil.

//go:embed model-1.4.bin
var model14Data []byte

//go:embed model-1.7.bin
var model17Data []byte

// Model is the Arlington object model for one PDF version: the types that
// version defines, with version-gated constraints folded against it.
type Model struct {
	version string
	data    []byte

	once           sync.Once
	types          map[string]ObjectType
	selfIdentified map[[2]string]string
}

var (
	pdf14  = &Model{version: "1.4", data: model14Data}
	pdf17  = &Model{version: "1.7", data: model17Data}
	models = map[string]*Model{pdf14.version: pdf14, pdf17.version: pdf17}
)

// ModelFor returns the model for a PDF version ("1.4" or "1.7"), or nil when
// no model for that version is vendored.
func ModelFor(version string) *Model { return models[version] }

// Version returns the PDF version the model describes, such as "1.4".
func (m *Model) Version() string { return m.version }

// load decodes the model's data once, on the first lookup.
func (m *Model) load() {
	m.once.Do(func() {
		m.types, m.selfIdentified = decodeModel(m.data)
	})
}

// Types returns every type of the model, keyed by name. The map is shared and
// decoded once, on the first call; callers must not modify it.
func (m *Model) Types() map[string]ObjectType {
	m.load()
	return m.types
}

// Type looks up a type of the model by name (e.g. "Catalog", "ExtGState").
func (m *Model) Type(name string) (ObjectType, bool) {
	t, ok := m.Types()[name]
	return t, ok
}

// SelfIdentified returns the one type of the model an untyped dictionary's
// own /Type (and /Subtype) names unambiguously identify, or "" when the pair
// is unknown or ambiguous. The generated table only contains pairs exactly
// one type in the model is consistent with, so re-anchoring on the result
// never guesses.
func (m *Model) SelfIdentified(typeVal, subtypeVal string) string {
	m.load()
	if subtypeVal != "" {
		if n, ok := m.selfIdentified[[2]string{typeVal, subtypeVal}]; ok {
			return n
		}
	}
	return m.selfIdentified[[2]string{typeVal, ""}]
}

// Types returns every type of the PDF 1.4 model; see [Model.Types].
func Types() map[string]ObjectType { return pdf14.Types() }

// Type looks up a type of the PDF 1.4 model by name; see [Model.Type].
func Type(name string) (ObjectType, bool) { return pdf14.Type(name) }

// SelfIdentified returns the PDF 1.4 type a /Type and /Subtype pair
// identifies; see [Model.SelfIdentified].
func SelfIdentified(typeVal, subtypeVal string) string {
	return pdf14.SelfIdentified(typeVal, subtypeVal)
}

// encoder writes the body and interns strings; the table is written last
// but placed first.
type encoder struct {
	body    []byte
	strings []string
	index   map[string]int
}

func (e *encoder) uint(v int)  { e.body = binary.AppendUvarint(e.body, uint64(v)) }
func (e *encoder) int(v int)   { e.body = binary.AppendVarint(e.body, int64(v)) }
func (e *encoder) bool(v bool) { e.body = append(e.body, boolByte(v)) }

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func (e *encoder) str(s string) {
	i, ok := e.index[s]
	if !ok {
		i = len(e.strings)
		e.index[s] = i
		e.strings = append(e.strings, s)
	}
	e.uint(i)
}

func (e *encoder) strs(ss []string) {
	e.uint(len(ss))
	for _, s := range ss {
		e.str(s)
	}
}

func (e *encoder) valueTypes(vts []ValueType) {
	e.uint(len(vts))
	for _, vt := range vts {
		e.uint(int(vt))
	}
}

func (e *encoder) cond(c *Cond) {
	e.bool(c != nil)
	if c != nil {
		e.condBody(*c)
	}
}

func (e *encoder) condBody(c Cond) {
	e.uint(int(c.Op))
	e.str(c.Key)
	e.str(c.Value)
	e.uint(int(c.Fn))
	e.str(c.RHSKey)
	e.uint(int(c.RHSFn))
	e.int(c.RHSAdd)
	e.int(c.RHSMul)
	e.str(c.RHSKey2)
	e.int(c.Mod)
	e.int(c.BitLo)
	e.int(c.BitHi)
	e.uint(len(c.Kids))
	for _, k := range c.Kids {
		e.condBody(k)
	}
}

func (e *encoder) keyDef(kd *KeyDef) {
	e.str(kd.Name)
	e.valueTypes(kd.Types)
	e.bool(kd.Required)
	e.cond(kd.RequiredWhen)
	e.cond(kd.ValueCond)
	e.uint(int(kd.IndirectReference))
	e.str(kd.SinceVersion)
	e.str(kd.DeprecatedIn)
	e.strs(kd.PossibleValues)
	e.uint(len(kd.PinnedValues))
	for _, p := range kd.PinnedValues {
		e.cond(p.When)
		e.str(p.Value)
	}
	e.uint(len(kd.LinkGroups))
	for _, g := range kd.LinkGroups {
		e.valueTypes(g.ValueTypes)
		e.strs(g.Candidates)
		e.str(g.Discriminator)
		e.strs(sortedKeys(g.ByValue))
		for _, k := range sortedKeys(g.ByValue) {
			e.str(g.ByValue[k])
		}
	}
	e.cond(kd.SpecialCase)
	e.bool(kd.Inheritable)
	e.bool(kd.Predicated.Required)
	e.bool(kd.Predicated.Types)
	e.bool(kd.Predicated.Values)
	e.bool(kd.Predicated.Indirect)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// EncodeModel serializes ts and the self-identification table into the
// model-<version>.bin format that decodeModel reads. The output depends only
// on the content, not on map order, so regenerating an unchanged model is
// byte-identical.
func EncodeModel(ts map[string]ObjectType, self map[[2]string]string) []byte {
	e := &encoder{index: map[string]int{}}

	names := make([]string, 0, len(ts))
	for n := range ts {
		names = append(names, n)
	}
	sort.Strings(names)
	e.uint(len(names))
	for _, n := range names {
		ot := ts[n]
		e.str(ot.Name)
		e.uint(len(ot.Keys))
		for i := range ot.Keys {
			e.keyDef(&ot.Keys[i])
		}
		e.bool(ot.Wildcard != nil)
		if ot.Wildcard != nil {
			e.keyDef(ot.Wildcard)
		}
		e.strs(ot.LaterKeys)
	}

	pairs := make([][2]string, 0, len(self))
	for k := range self {
		pairs = append(pairs, k)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i][0] != pairs[j][0] {
			return pairs[i][0] < pairs[j][0]
		}
		return pairs[i][1] < pairs[j][1]
	})
	e.uint(len(pairs))
	for _, p := range pairs {
		e.str(p[0])
		e.str(p[1])
		e.str(self[p])
	}

	raw := binary.AppendUvarint(nil, uint64(len(e.strings)))
	for _, s := range e.strings {
		raw = binary.AppendUvarint(raw, uint64(len(s)))
		raw = append(raw, s...)
	}
	raw = append(raw, e.body...)

	var z bytes.Buffer
	// Neither call can fail: the level is valid and a bytes.Buffer never
	// returns a write error.
	w, _ := flate.NewWriter(&z, flate.BestCompression)
	_, _ = w.Write(raw)
	_ = w.Close()
	return z.Bytes()
}

// decoder reads what encoder wrote. The data is generated and embedded, so a
// malformed stream is a build defect, not an input error: reads past the end
// panic rather than being checked at every step.
type decoder struct {
	buf     []byte
	strings []string
}

func (d *decoder) uint() int {
	v, n := binary.Uvarint(d.buf)
	d.buf = d.buf[n:]
	return int(v)
}

func (d *decoder) int() int {
	v, n := binary.Varint(d.buf)
	d.buf = d.buf[n:]
	return int(v)
}

func (d *decoder) bool() bool {
	v := d.buf[0] == 1
	d.buf = d.buf[1:]
	return v
}

func (d *decoder) str() string { return d.strings[d.uint()] }

func (d *decoder) strs() []string {
	n := d.uint()
	if n == 0 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = d.str()
	}
	return out
}

func (d *decoder) valueTypes() []ValueType {
	n := d.uint()
	if n == 0 {
		return nil
	}
	out := make([]ValueType, n)
	for i := range out {
		out[i] = ValueType(d.uint())
	}
	return out
}

func (d *decoder) cond() *Cond {
	if !d.bool() {
		return nil
	}
	c := d.condBody()
	return &c
}

func (d *decoder) condBody() Cond {
	c := Cond{
		Op:      CondOp(d.uint()),
		Key:     d.str(),
		Value:   d.str(),
		Fn:      CondFn(d.uint()),
		RHSKey:  d.str(),
		RHSFn:   CondFn(d.uint()),
		RHSAdd:  d.int(),
		RHSMul:  d.int(),
		RHSKey2: d.str(),
		Mod:     d.int(),
		BitLo:   d.int(),
		BitHi:   d.int(),
	}
	if n := d.uint(); n > 0 {
		c.Kids = make([]Cond, n)
		for i := range c.Kids {
			c.Kids[i] = d.condBody()
		}
	}
	return c
}

func (d *decoder) keyDef() KeyDef {
	kd := KeyDef{
		Name:              d.str(),
		Types:             d.valueTypes(),
		Required:          d.bool(),
		RequiredWhen:      d.cond(),
		ValueCond:         d.cond(),
		IndirectReference: IndirectRule(d.uint()),
		SinceVersion:      d.str(),
		DeprecatedIn:      d.str(),
		PossibleValues:    d.strs(),
	}
	if n := d.uint(); n > 0 {
		kd.PinnedValues = make([]PinnedValue, n)
		for i := range kd.PinnedValues {
			kd.PinnedValues[i] = PinnedValue{When: d.cond(), Value: d.str()}
		}
	}
	if n := d.uint(); n > 0 {
		kd.LinkGroups = make([]LinkGroup, n)
		for i := range kd.LinkGroups {
			g := LinkGroup{ValueTypes: d.valueTypes(), Candidates: d.strs(), Discriminator: d.str()}
			if keys := d.strs(); keys != nil {
				g.ByValue = make(map[string]string, len(keys))
				for _, k := range keys {
					g.ByValue[k] = d.str()
				}
			}
			kd.LinkGroups[i] = g
		}
	}
	kd.SpecialCase = d.cond()
	kd.Inheritable = d.bool()
	kd.Predicated = Predication{Required: d.bool(), Types: d.bool(), Values: d.bool(), Indirect: d.bool()}
	return kd
}

// decodeModel reads the model-<version>.bin format: the types keyed by name,
// each with its key index built, and the self-identification table. It panics
// on data that EncodeModel did not write.
func decodeModel(data []byte) (map[string]ObjectType, map[[2]string]string) {
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
	if err != nil {
		panic("arlington: corrupt model data: " + err.Error())
	}
	d := &decoder{buf: raw}
	d.strings = make([]string, d.uint())
	for i := range d.strings {
		n := d.uint()
		d.strings[i] = string(d.buf[:n])
		d.buf = d.buf[n:]
	}

	n := d.uint()
	ts := make(map[string]ObjectType, n)
	for range n {
		ot := ObjectType{Name: d.str()}
		if k := d.uint(); k > 0 {
			ot.Keys = make([]KeyDef, k)
			for i := range ot.Keys {
				ot.Keys[i] = d.keyDef()
			}
		}
		if d.bool() {
			w := d.keyDef()
			ot.Wildcard = &w
		}
		ot.LaterKeys = d.strs()
		ot.keyByName = indexKeys(ot.Keys)
		ts[ot.Name] = ot
	}

	n = d.uint()
	self := make(map[[2]string]string, n)
	for range n {
		// Composite-literal and call operands evaluate left to right, so the
		// reads happen in the order EncodeModel wrote them.
		k := [2]string{d.str(), d.str()}
		self[k] = d.str()
	}
	return ts, self
}
