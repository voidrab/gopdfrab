package pdf

import "testing"

// catalogReader returns a Reader whose trailer's /Root is root and whose
// objects are objs, with no file behind it: a reference to any other object
// resolves to null.
func catalogReader(root PDFValue, objs map[int]PDFValue) *Reader {
	return &Reader{
		trailer:  PDFDict{Entries: DictOf(map[string]PDFValue{"Root": root})},
		objCache: objs,
	}
}

func TestCatalogResolvesOnlyTheNamedEntries(t *testing.T) {
	root := PDFDict{Entries: DictOf(map[string]PDFValue{
		"Pages":          PDFRef{ObjNum: 2},
		"StructTreeRoot": PDFRef{ObjNum: 3},
	})}
	d := catalogReader(PDFRef{ObjNum: 1}, map[int]PDFValue{
		1: root,
		2: PDFDict{Entries: DictOf(map[string]PDFValue{"Count": PDFRef{ObjNum: 4}})},
		4: PDFInteger(0),
	})
	got, err := d.Catalog("Pages", "Dests")
	if err != nil {
		t.Fatal(err)
	}
	pages, ok := got.Entries.Get("Pages").(PDFDict)
	if !ok || pages.Entries.Get("Count") != PDFInteger(0) {
		t.Errorf("Pages = %#v, want the page tree fully resolved", got.Entries.Get("Pages"))
	}
	// StructTreeRoot is not among the requested keys, so it is not resolved.
	if got.Entries.Get("StructTreeRoot") != (PDFRef{ObjNum: 3}) {
		t.Errorf("StructTreeRoot = %#v, want it left a reference", got.Entries.Get("StructTreeRoot"))
	}
}

func TestCatalogWithoutACatalogDictionary(t *testing.T) {
	for name, root := range map[string]PDFValue{"absent": nil, "not a dictionary": PDFInteger(1)} {
		got, err := catalogReader(root, nil).Catalog("Pages")
		if err != nil || got.Entries != nil {
			t.Errorf("%s: Catalog = %#v, %v; want the zero PDFDict", name, got, err)
		}
	}
}

func TestCatalogResolveFailures(t *testing.T) {
	defer SetMaxResolveDepth(1)()
	// A chain of references longer than the depth limit fails to resolve.
	chain := map[int]PDFValue{1: PDFRef{ObjNum: 2}, 2: PDFRef{ObjNum: 3}, 3: PDFRef{ObjNum: 4}, 4: PDFInteger(1)}
	if _, err := catalogReader(PDFRef{ObjNum: 1}, chain).Catalog("Pages"); err == nil {
		t.Error("an unresolvable /Root was not reported")
	}
	root := PDFDict{Entries: DictOf(map[string]PDFValue{"Pages": PDFRef{ObjNum: 1}})}
	if _, err := catalogReader(root, chain).Catalog("Pages"); err == nil {
		t.Error("an unresolvable named entry was not reported")
	}
}

func TestResolveShallowDepthLimit(t *testing.T) {
	defer SetMaxResolveDepth(1)()
	// 1 -> 2 -> 3 -> 4: a chain of references longer than the limit.
	d := &Reader{objCache: map[int]PDFValue{1: PDFRef{ObjNum: 2}, 2: PDFRef{ObjNum: 3}, 3: PDFRef{ObjNum: 4}, 4: PDFInteger(1)}}
	if _, err := d.resolveShallow(PDFRef{ObjNum: 1}); err == nil {
		t.Error("a reference chain past maxResolveDepth was followed")
	}
}

func TestObjStmEntryParsesOnce(t *testing.T) {
	e := objStmEntry{objNum: 1, raw: []byte("42")}
	e.parse(5)
	e.value = PDFInteger(7) // a second parse must not overwrite the value
	e.parse(5)
	if e.value != PDFInteger(7) || e.err != nil {
		t.Errorf("entry = %+v, want the first parse kept", e)
	}
}
