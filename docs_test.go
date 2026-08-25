package gopdfrab

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// TestNoticeCoversBundledAssets fails when a bundled asset is not named in
// NOTICE. Those files ship in the module zip and go:embed writes them into
// converted output, so each one needs its origin and licence recorded; a
// hand-written list drifts unless something reads it.
func TestNoticeCoversBundledAssets(t *testing.T) {
	notice, err := os.ReadFile("NOTICE")
	if err != nil {
		t.Fatalf("read NOTICE: %v", err)
	}
	err = filepath.WalkDir("internal/convert/assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		// README.md documents the directory rather than shipping in output.
		if name := d.Name(); name != "README.md" && !strings.Contains(string(notice), name) {
			t.Errorf("NOTICE does not name %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk assets: %v", err)
	}
}

// TestGoDirectivesAgree fails when the modules disagree on the minimum Go
// version, or when one names a patch release. A patch-level directive makes
// that exact patch the floor -- consumers on an earlier patch download a
// toolchain, and GOTOOLCHAIN=local cannot build at all.
func TestGoDirectivesAgree(t *testing.T) {
	mods := []string{
		"go.mod",
		"benchmarks/go.mod",
		"tests/go.mod",
		"internal/arlington/testdata/go.mod",
	}
	want := ""
	for _, mod := range mods {
		b, err := os.ReadFile(mod)
		if err != nil {
			t.Fatalf("read %s: %v", mod, err)
		}
		got := ""
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
				got = strings.TrimSpace(v)
				break
			}
		}
		switch {
		case got == "":
			t.Errorf("%s has no go directive", mod)
		case strings.Count(got, ".") != 1:
			t.Errorf("%s says go %s; drop the patch component", mod, got)
		case want == "":
			want = got
		case got != want:
			t.Errorf("%s says go %s, %s says go %s", mod, got, mods[0], want)
		}
	}
}

// exportedFuncs returns the package-level exported function names declared in
// this package's non-test sources.
func exportedFuncs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if ok && fd.Recv == nil && fd.Name.IsExported() {
				out = append(out, fd.Name.Name)
			}
		}
	}
	return out
}

// TestREADMENamesPublicAPI fails when a package-level exported function is not
// named in README.md. The godoc is the reference, but the README is where a
// reader starts, so a function missing from it is a function they never meet.
func TestREADMENamesPublicAPI(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	words := docWords(string(readme))
	for _, name := range exportedFuncs(t) {
		if !words[name] {
			t.Errorf("README.md does not name %s", name)
		}
	}
}

// cliFlags returns the flag names cmd/gopdfrab registers, taken from the string
// literal each fs.String/fs.Bool/fs.Int call names it with.
func cliFlags(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, "cmd/gopdfrab", nil, 0)
	if err != nil {
		t.Fatalf("parsing cmd/gopdfrab: %v", err)
	}
	seen := make(map[string]bool)
	var out []string
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "String", "Bool", "Int":
				default:
					return true
				}
				// Both subcommands register some of the same flags.
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil && !seen[v] {
						seen[v] = true
						out = append(out, v)
					}
				}
				return true
			})
		}
	}
	return out
}

// TestREADMEDocumentsCLIFlags fails when a registered CLI flag is not spelled
// out in README.md. The section listed four of the eight once.
func TestREADMEDocumentsCLIFlags(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	flags := cliFlags(t)
	if len(flags) == 0 {
		t.Fatal("no CLI flags found; the parse is broken, not the README")
	}
	for _, name := range flags {
		if !strings.Contains(string(readme), "`--"+name+"`") &&
			!strings.Contains(string(readme), "`-"+name+"`") {
			t.Errorf("README.md does not document the %s flag", name)
		}
	}
}
