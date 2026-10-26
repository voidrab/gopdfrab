package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestFlattenWorkersDoNotEditWhatOthersRender pins that the flatten
// workers leave shared objects alone until every render is done. Form A and
// the soft-masked image are page targets of their own and are also drawn
// inside form B, another target: a worker that rewrote A or stripped the
// image's mask in place raced with the worker rendering B. Run with -race.
func TestFlattenWorkersDoNotEditWhatOthersRender(t *testing.T) {
	black := grayImage(2, 2, []byte{0, 0, 0, 0})
	formA := groupFormDrawingIm0(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im0": black})},
	})})
	masked := grayImage(2, 2, []byte{0, 0, 0, 0})
	masked.Entries.Set("SMask", grayImage(2, 2, []byte{0, 255, 0, 255}))

	formB := groupFormDrawingIm0(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im0": masked, "FmA": formA})},
	})})
	formB.RawStream = []byte("q /GS0 gs 20 0 0 20 0 0 cm /Im0 Do Q /FmA Do")

	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"FmA": formA, "FmB": formB, "Im1": masked})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"XObject": xobjects})},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Pages"}, "Kids": pdf.PDFArray{page}})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})},
	})}

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix: changed=%v err=%v", changed, err)
	}
	for _, name := range []string{"FmA", "FmB"} {
		fm := xobjects.Entries.Get(name).(pdf.PDFDict)
		if fm.Entries.Get("Group") != nil {
			t.Errorf("%s still carries /Group", name)
		}
		if ink := flattenedInk(t, fm); ink < 0.5 {
			t.Errorf("%s flattened to %.3f ink, want its picture", name, ink)
		}
	}
	// The edits reached the shared objects, not a worker's private copy: the
	// original values see them too.
	if formA.Entries.Get("Group") != nil {
		t.Error("form A's own entries still carry /Group: the flatten was not published")
	}
	if masked.Entries.Get("SMask") != nil {
		t.Error("the image's own entries still carry /SMask: the bake was not published")
	}
}
