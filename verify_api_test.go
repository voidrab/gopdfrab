package gopdfrab

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/verify"
)

// isConvertOnly reports whether a root identifier belongs to conversion, and
// so has no counterpart in the verify package.
func isConvertOnly(name string) bool {
	switch name {
	case "PageFidelity", "RasterDrop", "ErrUnresolvableGraph":
		return true
	}
	return strings.Contains(name, "Convert")
}

// apiSurface returns the exported package-level identifiers declared in the
// non-test sources of the package in dir, and the exported methods declared
// on its Document type.
func apiSurface(t *testing.T, dir string) (idents, docMethods []string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	fset := token.NewFileSet()
	for _, fn := range files {
		if strings.HasSuffix(fn, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, fn, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", fn, err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv == nil {
					idents = append(idents, d.Name.Name)
				} else if star, ok := d.Recv.List[0].Type.(*ast.StarExpr); ok {
					if id, ok := star.X.(*ast.Ident); ok && id.Name == "Document" {
						docMethods = append(docMethods, d.Name.Name)
					}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							idents = append(idents, s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								idents = append(idents, n.Name)
							}
						}
					}
				}
			}
		}
	}
	slices.Sort(idents)
	slices.Sort(docMethods)
	return idents, docMethods
}

// withoutConvert drops the conversion identifiers from names.
func withoutConvert(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), isConvertOnly)
}

// TestVerifyAPIParity fails when the verify package and the root package's
// verification half drift apart: a name added to one and not the other, or a
// Document method present on only one side. The verify package promises that
// switching to it is an import swap, which holds only while the two match.
func TestVerifyAPIParity(t *testing.T) {
	rootIdents, rootMethods := apiSurface(t, ".")
	verIdents, verMethods := apiSurface(t, "verify")
	if want := withoutConvert(rootIdents); !slices.Equal(want, verIdents) {
		t.Errorf("package-level API differs:\n root (less conversion): %v\n verify:                 %v", want, verIdents)
	}
	if want := withoutConvert(rootMethods); !slices.Equal(want, verMethods) {
		t.Errorf("Document methods differ:\n root (less conversion): %v\n verify:                 %v", want, verMethods)
	}
}

// TestVerifySharesRootTypes pins that the verify package's data types are the
// root package's, not look-alikes: a value must pass between the two without
// conversion.
func TestVerifySharesRootTypes(t *testing.T) {
	pairs := []struct {
		name       string
		root, ours reflect.Type
	}{
		{"Result", reflect.TypeOf(Result{}), reflect.TypeOf(verify.Result{})},
		{"FileResult", reflect.TypeOf(FileResult[Result]{}), reflect.TypeOf(verify.FileResult[verify.Result]{})},
		{"Profile", reflect.TypeOf(Profile{}), reflect.TypeOf(verify.Profile{})},
		{"LevelType", reflect.TypeOf(LevelType("")), reflect.TypeOf(verify.LevelType(""))},
		{"Check", reflect.TypeOf(Check{}), reflect.TypeOf(verify.Check{})},
		{"PDFError", reflect.TypeOf(PDFError{}), reflect.TypeOf(verify.PDFError{})},
		{"PDFRef", reflect.TypeOf(PDFRef{}), reflect.TypeOf(verify.PDFRef{})},
		{"ObjModelDetail", reflect.TypeOf(ObjModelDetail{}), reflect.TypeOf(verify.ObjModelDetail{})},
		{"Limits", reflect.TypeOf(Limits{}), reflect.TypeOf(verify.Limits{})},
	}
	for _, p := range pairs {
		if p.root != p.ours {
			t.Errorf("%s: root is %v, verify is %v", p.name, p.root, p.ours)
		}
	}

	// The profiles start out shared, and the registry is the same value.
	if PDFA1B != verify.PDFA1B || Legacy1B != verify.Legacy1B || PDF != verify.PDF {
		t.Error("the default profiles differ between gopdfrab and verify")
	}
	if !reflect.DeepEqual(Checks, verify.Checks) {
		t.Error("the Checks registry differs between gopdfrab and verify")
	}

	// verify.Options is the root Options' verification subset, field for field.
	rootOpts := reflect.TypeOf(Options{})
	verOpts := reflect.TypeOf(verify.Options{})
	for i := range verOpts.NumField() {
		f := verOpts.Field(i)
		rf, ok := rootOpts.FieldByName(f.Name)
		if !ok || rf.Type != f.Type {
			t.Errorf("verify.Options.%s (%v) has no matching field in gopdfrab.Options", f.Name, f.Type)
		}
	}
}
