package pdf

import (
	"errors"
	"testing"
)

// trailerReader returns a Reader whose trailer's /Root is root and whose
// objects are objs, with no file behind it: a reference to any other object
// resolves to null.
func trailerReader(root PDFValue, objs map[int]PDFValue) *Reader {
	return &Reader{
		trailer:  PDFDict{Entries: DictOf(map[string]PDFValue{"Root": root})},
		objCache: objs,
	}
}

func TestResolvePathResolvesOnlyThePath(t *testing.T) {
	root := PDFDict{Entries: DictOf(map[string]PDFValue{
		"Pages":          PDFRef{ObjNum: 2},
		"StructTreeRoot": PDFRef{ObjNum: 3},
	})}
	d := trailerReader(PDFRef{ObjNum: 1}, map[int]PDFValue{
		1: root,
		2: PDFDict{Entries: DictOf(map[string]PDFValue{"Count": PDFRef{ObjNum: 4}})},
		4: PDFInteger(0),
	})
	got, err := d.ResolveGraphByPath([]string{"Root", "Pages"})
	if err != nil {
		t.Fatal(err)
	}
	if pages, ok := got.(PDFDict); !ok || pages.Entries.Get("Count") != PDFInteger(0) {
		t.Errorf("Pages = %#v, want the page tree fully resolved", got)
	}
	// StructTreeRoot is off the path, so it is not resolved.
	if v := root.Entries.Get("StructTreeRoot"); v != (PDFRef{ObjNum: 3}) {
		t.Errorf("StructTreeRoot = %#v, want it left a reference", v)
	}
}

func TestResolvePathNoEntry(t *testing.T) {
	root := PDFDict{Entries: DictOf(map[string]PDFValue{"Kids": PDFArray{PDFInteger(1)}})}
	for _, path := range [][]string{{"Root", "Pages"}, {"Root", "Kids", "5"}} {
		if _, err := trailerReader(root, nil).ResolveGraphByPath(path); !errors.Is(err, ErrNoEntry) {
			t.Errorf("%v: err = %v, want ErrNoEntry", path, err)
		}
	}
}

func TestResolvePathDepthLimit(t *testing.T) {
	defer SetMaxResolveDepth(1)()
	// A chain of references longer than the depth limit fails to resolve.
	chain := map[int]PDFValue{1: PDFRef{ObjNum: 2}, 2: PDFRef{ObjNum: 3}, 3: PDFRef{ObjNum: 4}, 4: PDFInteger(1)}
	if _, err := trailerReader(PDFRef{ObjNum: 1}, chain).ResolveGraphByPath([]string{"Root"}); err == nil {
		t.Error("a reference chain past the depth limit was followed")
	}
}

func TestCompressedObjectParsedOnce(t *testing.T) {
	d := &Reader{objCache: map[int]PDFValue{5: buildObjStmDict()}}
	entry := compressedXrefEntry{streamObjNum: 5, index: 1}
	first, err := d.resolveCompressedObject(PDFRef{ObjNum: 2}, entry)
	if err != nil || first != PDFInteger(42) {
		t.Fatalf("first lookup = %v, %v; want 42", first, err)
	}
	d.objStmCache[5][1].value = PDFInteger(7) // a second parse would restore 42
	if again, err := d.resolveCompressedObject(PDFRef{ObjNum: 2}, entry); err != nil || again != PDFInteger(7) {
		t.Errorf("second lookup = %v, %v; want the object parsed once", again, err)
	}
}
