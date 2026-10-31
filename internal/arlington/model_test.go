package arlington

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestModelRoundTrip verifies that re-encoding each decoded model yields the
// embedded bytes. Every field decodeModel reads is written back by EncodeModel
// in the same order, so neither drops anything the data carries.
func TestModelRoundTrip(t *testing.T) {
	for _, m := range []*Model{pdf14, pdf17} {
		m.load()
		if again := EncodeModel(m.Types(), m.selfIdentified); !bytes.Equal(again, m.data) {
			t.Fatalf("EncodeModel(decodeModel(model-%s.bin)) differs from the file", m.Version())
		}
		ts, self := decodeModel(m.data)
		if !reflect.DeepEqual(ts, m.Types()) || !reflect.DeepEqual(self, m.selfIdentified) {
			t.Fatalf("decoding model-%s.bin twice gives different models", m.Version())
		}
	}
}

// TestModelCodecCoversEveryField verifies that the types the codec serializes
// have the field counts it handles. A new field is silently dropped by
// EncodeModel until the codec learns it, so adding one must fail this test
// first.
func TestModelCodecCoversEveryField(t *testing.T) {
	for typ, want := range map[reflect.Type]int{
		reflect.TypeOf(ObjectType{}):  5, // keyByName is rebuilt, not serialized
		reflect.TypeOf(KeyDef{}):      14,
		reflect.TypeOf(Cond{}):        13,
		reflect.TypeOf(LinkGroup{}):   4,
		reflect.TypeOf(PinnedValue{}): 2,
		reflect.TypeOf(Predication{}): 4,
	} {
		if got := typ.NumField(); got != want {
			t.Errorf("%s has %d fields, the model codec handles %d: update EncodeModel, decodeModel and this test", typ.Name(), got, want)
		}
	}
}

// TestDecodeModelCorrupt verifies that data that is not a flate stream is a
// build defect, and that decodeModel panics with a message reporting corrupt
// model data.
func TestDecodeModelCorrupt(t *testing.T) {
	defer func() {
		r, _ := recover().(string)
		if !strings.Contains(r, "corrupt model data") {
			t.Errorf("recover() = %q, want a corrupt model data panic", r)
		}
	}()
	decodeModel([]byte{0xff, 0xff, 0xff})
}

// TestModelFor verifies that ModelFor serves the vendored versions, nil for
// any other, and that the package-level lookups read the PDF 1.4 model.
func TestModelFor(t *testing.T) {
	for _, v := range []string{"1.4", "1.7"} {
		m := ModelFor(v)
		if m == nil || m.Version() != v {
			t.Fatalf("ModelFor(%q) = %v, want the PDF %s model", v, m, v)
		}
	}
	for _, v := range []string{"", "1.5", "2.0"} {
		if m := ModelFor(v); m != nil {
			t.Errorf("ModelFor(%q) = %v, want nil", v, m)
		}
	}
	m14 := ModelFor("1.4")
	if !reflect.DeepEqual(Types(), m14.Types()) {
		t.Error("Types() is not the PDF 1.4 model's")
	}
	if got, want := SelfIdentified("Annot", "Widget"), m14.SelfIdentified("Annot", "Widget"); got != want {
		t.Errorf("SelfIdentified(Annot, Widget) = %q, want %q", got, want)
	}
}

// TestPDF17Model verifies what the PDF 1.7 model adds over PDF 1.4: JPXDecode
// as an image filter, the object and cross-reference stream types, and the
// ViewerPreferences keys that PDF 1.4 lacks. Those keys must not appear in the
// PDF 1.7 model's LaterKeys.
func TestPDF17Model(t *testing.T) {
	m14, m17 := ModelFor("1.4"), ModelFor("1.7")

	filters := func(m *Model) []string {
		img, ok := m.Type("XObjectImage")
		if !ok {
			t.Fatalf("PDF %s: no XObjectImage", m.Version())
		}
		return img.KeyDefByName("Filter").PossibleValues
	}
	if slices.Contains(filters(m14), "JPXDecode") {
		t.Error("PDF 1.4: XObjectImage /Filter allows JPXDecode")
	}
	if !slices.Contains(filters(m17), "JPXDecode") {
		t.Errorf("PDF 1.7: XObjectImage /Filter = %v, want JPXDecode among them", filters(m17))
	}

	for _, name := range []string{"ObjectStream", "XRefStream"} {
		if _, ok := m14.Type(name); ok {
			t.Errorf("PDF 1.4 has type %s", name)
		}
		if _, ok := m17.Type(name); !ok {
			t.Errorf("PDF 1.7 lacks type %s", name)
		}
	}
	if got := m17.SelfIdentified("ObjStm", ""); got != "ObjectStream" {
		t.Errorf("PDF 1.7: SelfIdentified(ObjStm) = %q, want ObjectStream", got)
	}

	vp, _ := m17.Type("ViewerPreferences")
	if !vp.HasNamedKey("PrintScaling") || slices.Contains(vp.LaterKeys, "PrintScaling") {
		t.Errorf("PDF 1.7: PrintScaling must be a ViewerPreferences key, not a later one (LaterKeys %v)", vp.LaterKeys)
	}
}
