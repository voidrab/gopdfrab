package gopdfrab

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// allowedInternalImports is the layering ARCHITECTURE.md describes: each
// internal package and the internal packages its non-test code may import.
// verify and raster sit on the same layer and must not import each other;
// anything both need belongs one layer down.
var allowedInternalImports = map[string][]string{
	"arlington": nil,
	"content":   {"pdf"},
	"convert":   {"arlington", "content", "fontedit", "fontprog", "fontsubst", "pdf", "pdfa", "raster", "verify", "writer"},
	"fontedit":  {"fontprog"},
	"fontprog":  {"pdf"},
	"fontsubst": {"pdf"},
	"pdf":       nil,
	"pdfa":      {"pdf"},
	"pdfgen":    nil,
	"raster":    {"content", "fontprog", "fontsubst", "pdf"},
	"verify":    {"arlington", "fontprog", "pdf", "pdfa"},
	"writer":    {"pdf"},
}

// TestPackageLayering fails when an internal package imports one it sits
// above, or when a new internal package appears without a place in the
// layering. Build tags are ignored on purpose: every file counts.
func TestPackageLayering(t *testing.T) {
	dirs, err := os.ReadDir("internal")
	if err != nil {
		t.Fatalf("read internal: %v", err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		pkg := d.Name()
		allowed, known := allowedInternalImports[pkg]
		if !known {
			t.Errorf("internal/%s is not in allowedInternalImports; add it with the packages it may import", pkg)
			continue
		}
		got := internalImports(t, filepath.Join("internal", pkg))
		var bad []string
		for _, dep := range got {
			if !slices.Contains(allowed, dep) {
				bad = append(bad, dep)
			}
		}
		sort.Strings(bad)
		if len(bad) > 0 {
			t.Errorf("internal/%s imports %v, which its layer does not allow (see ARCHITECTURE.md)", pkg, bad)
		}
	}
}

// internalImports returns the internal packages, by name under internal/, that
// the non-test Go files in dir import.
func internalImports(t *testing.T, dir string) []string {
	t.Helper()
	const prefix = "github.com/voidrab/gopdfrab/internal/"
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	seen := map[string]bool{}
	var out []string
	for _, fn := range files {
		if strings.HasSuffix(fn, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), fn, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", fn, err)
		}
		// A package main beside a library is a go:generate tool (arlington's
		// gen.go), built on its own and never linked into the package.
		if f.Name.Name == "main" {
			continue
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if dep, ok := strings.CutPrefix(p, prefix); ok && !seen[dep] {
				seen[dep] = true
				out = append(out, dep)
			}
		}
	}
	return out
}

// convertOnly is every internal package that exists for conversion alone.
// The verify package must not reach any of them, directly or transitively:
// keeping them out of verify-only binaries is that package's reason to exist.
var convertOnly = []string{"convert", "fontedit", "fontsubst", "raster", "writer"}

// TestVerifyPackageIsLight fails when the public verify package comes to
// depend, through any chain of internal imports, on a conversion-only package.
func TestVerifyPackageIsLight(t *testing.T) {
	via := map[string]string{} // internal package -> the package that pulled it in
	queue := []string{}
	for _, dep := range internalImports(t, "verify") {
		via[dep] = "verify"
		queue = append(queue, dep)
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for _, dep := range internalImports(t, filepath.Join("internal", pkg)) {
			if _, ok := via[dep]; !ok {
				via[dep] = "internal/" + pkg
				queue = append(queue, dep)
			}
		}
	}
	for _, pkg := range convertOnly {
		if from, ok := via[pkg]; ok {
			t.Errorf("verify reaches internal/%s (imported by %s); it must stay free of conversion code", pkg, from)
		}
	}
	if len(via) == 0 {
		t.Fatal("verify imports no internal package; the walk found nothing to check")
	}
}
