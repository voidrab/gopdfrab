package pdf

import (
	"errors"
	"testing"
)

func TestObjModelDetail(t *testing.T) {
	base := NewError(Checks.ObjectModel.MissingRequiredKey, []error{errors.New("x")}, 0, nil)
	if _, ok := base.ObjModelDetail(); ok {
		t.Error("a plain PDFError must not carry an object-model detail")
	}

	d := ObjModelDetail{TypeName: "Catalog", Key: "Pages"}
	with := base.WithObjModelDetail(d)
	got, ok := with.ObjModelDetail()
	if !ok || got != d {
		t.Errorf("ObjModelDetail() = (%v, %v), want (%v, true)", got, ok, d)
	}
	if _, ok := base.ObjModelDetail(); ok {
		t.Error("WithObjModelDetail must not mutate its receiver")
	}
}
