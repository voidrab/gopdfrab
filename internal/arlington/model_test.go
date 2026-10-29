package arlington

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestModelRoundTrip re-encodes the decoded model and requires the embedded
// bytes back: every field decodeModel reads, EncodeModel writes, in the same
// order, and neither drops anything the data carries.
func TestModelRoundTrip(t *testing.T) {
	load()
	if again := EncodeModel(Types(), selfIdentifiedMap); !bytes.Equal(again, modelData) {
		t.Fatal("EncodeModel(decodeModel(model.bin)) differs from model.bin")
	}
	ts, self := decodeModel(modelData)
	if !reflect.DeepEqual(ts, Types()) || !reflect.DeepEqual(self, selfIdentifiedMap) {
		t.Fatal("decoding model.bin twice gives different models")
	}
}

// TestModelCodecCoversEveryField pins the field counts of the types the codec
// serializes. A new field is silently dropped by EncodeModel until the codec
// learns it, so adding one must fail here first.
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

// TestDecodeModelCorrupt: data that is not a flate stream is a build defect
// and panics with a message naming the file.
func TestDecodeModelCorrupt(t *testing.T) {
	defer func() {
		r, _ := recover().(string)
		if !strings.Contains(r, "model.bin") {
			t.Errorf("recover() = %q, want a corrupt model.bin panic", r)
		}
	}()
	decodeModel([]byte{0xff, 0xff, 0xff})
}
