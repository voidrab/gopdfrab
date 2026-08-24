package gopdfrab

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
)

// publicTypes is every public data-model type that is an alias into internal/.
// An alias carries none of the aliased type's fields or methods into this
// package's documentation, so the doc comment on the alias is the whole
// reference a consumer gets. These tests hold it to that.
var publicTypes = []struct {
	name string
	typ  reflect.Type
}{
	{"Result", reflect.TypeOf(Result{})},
	{"FileResult", reflect.TypeOf(FileResult[Result]{})},
	{"Profile", reflect.TypeOf(Profile{})},
	{"LevelType", reflect.TypeOf(LevelType(""))},
	{"Check", reflect.TypeOf(Check{})},
	{"PDFError", reflect.TypeOf(PDFError{})},
	{"PDFRef", reflect.TypeOf(PDFRef{})},

	{"ObjModelDetail", reflect.TypeOf(ObjModelDetail{})},
	{"ConvertResult", reflect.TypeOf(ConvertResult{})},
	{"PageFidelity", reflect.TypeOf(PageFidelity{})},
	{"RasterDrop", reflect.TypeOf(RasterDrop{})},
}

// packageDocs parses this package's non-test sources and returns each declared
// type's doc comment.
func packageDocs(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, f)
	}
	d, err := doc.NewFromFiles(fset, files, "github.com/voidrab/gopdfrab")
	if err != nil {
		t.Fatalf("building package docs: %v", err)
	}
	out := make(map[string]string, len(d.Types))
	for _, dt := range d.Types {
		out[dt.Name] = dt.Doc
	}
	return out
}

// docWords splits a doc comment into identifier-shaped words, so a mention has
// to be the name itself and not a longer name containing it.
func docWords(text string) map[string]bool {
	out := make(map[string]bool)
	for _, w := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		out[w] = true
	}
	return out
}

// exportedNames returns the exported field and method names of typ, looking at
// both the value and the pointer method sets.
func exportedNames(typ reflect.Type) []string {
	var out []string
	if typ.Kind() == reflect.Struct {
		for _, f := range reflect.VisibleFields(typ) {
			if f.IsExported() {
				out = append(out, f.Name)
			}
		}
	}
	seen := make(map[string]bool)
	for _, t := range []reflect.Type{typ, reflect.PointerTo(typ)} {
		for i := range t.NumMethod() {
			if m := t.Method(i); !seen[m.Name] {
				seen[m.Name] = true
				out = append(out, m.Name)
			}
		}
	}
	return out
}

// TestPublicTypesDocumented fails when an exported field or method of a public
// type is not named in that type's doc comment, since nothing else documents
// it: godoc shows an alias's comment and nothing of what it points at.
func TestPublicTypesDocumented(t *testing.T) {
	docs := packageDocs(t)
	for _, pt := range publicTypes {
		text, ok := docs[pt.name]
		if !ok || strings.TrimSpace(text) == "" {
			t.Errorf("%s has no doc comment", pt.name)
			continue
		}
		words := docWords(text)
		for _, name := range exportedNames(pt.typ) {
			if !words[name] {
				t.Errorf("%s: doc comment does not mention %s", pt.name, name)
			}
		}
	}
}

// TestNoUnnameableInternalTypes fails when a public type's field or method
// signature reaches an internal type this package does not alias. A consumer
// can receive such a value but cannot name it.
func TestNoUnnameableInternalTypes(t *testing.T) {
	aliased := make(map[string]bool, len(publicTypes))
	for _, pt := range publicTypes {
		aliased[pt.typ.PkgPath()+"."+pt.typ.Name()] = true
	}

	reported := make(map[string]bool)
	var check func(owner string, typ reflect.Type, depth int)
	check = func(owner string, typ reflect.Type, depth int) {
		if typ == nil || depth > 4 {
			return
		}
		switch typ.Kind() {
		case reflect.Slice, reflect.Array, reflect.Pointer, reflect.Map, reflect.Chan:
			check(owner, typ.Elem(), depth+1)
		}
		if typ.Name() == "" || !strings.Contains(typ.PkgPath(), "/internal/") {
			return
		}
		key := owner + " " + typ.PkgPath() + "." + typ.Name()
		if !aliased[typ.PkgPath()+"."+typ.Name()] && !reported[key] {
			reported[key] = true
			t.Errorf("%s exposes %s.%s, which is internal and has no alias here",
				owner, typ.PkgPath(), typ.Name())
		}
	}

	for _, pt := range publicTypes {
		if pt.typ.Kind() == reflect.Struct {
			for _, f := range reflect.VisibleFields(pt.typ) {
				if f.IsExported() {
					check(pt.name+"."+f.Name, f.Type, 0)
				}
			}
		}
		for _, typ := range []reflect.Type{pt.typ, reflect.PointerTo(pt.typ)} {
			for i := range typ.NumMethod() {
				m := typ.Method(i)
				for j := range m.Type.NumIn() {
					check(pt.name+"."+m.Name, m.Type.In(j), 0)
				}
				for j := range m.Type.NumOut() {
					check(pt.name+"."+m.Name, m.Type.Out(j), 0)
				}
			}
		}
	}
}
