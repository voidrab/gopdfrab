package verify

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// packageRefs is a name-level reference graph of this package's production
// code: for each top-level function, method and variable, the package-level
// names it mentions and the checks it names as pdf.Checks.Group.Name.
type packageRefs struct {
	names  map[string][]string // declaration -> names it mentions
	checks map[string][]string // declaration -> "Group.Name" checks
}

// loadPackageRefs parses the package's non-test files. A method is keyed by
// its bare name, so a call through any receiver reaches every method of that
// name: the graph over-approximates, which is the safe direction for a
// declaration of what a rule may report.
func loadPackageRefs(t *testing.T) packageRefs {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	refs := packageRefs{names: map[string][]string{}, checks: map[string][]string{}}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				refs.collect(d.Name.Name, d.Body)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, n := range vs.Names {
							for _, v := range vs.Values {
								refs.collect(n.Name, v)
							}
						}
					}
				}
			}
		}
	}
	return refs
}

func (r packageRefs) collect(decl string, node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			// pdf.Checks.Group.Name
			if group, ok := x.X.(*ast.SelectorExpr); ok {
				if checks, ok := group.X.(*ast.SelectorExpr); ok && checks.Sel.Name == "Checks" {
					if pkg, ok := checks.X.(*ast.Ident); ok && pkg.Name == "pdf" {
						r.checks[decl] = append(r.checks[decl], group.Sel.Name+"."+x.Sel.Name)
						return false
					}
				}
			}
			r.names[decl] = append(r.names[decl], x.Sel.Name)
		case *ast.Ident:
			r.names[decl] = append(r.names[decl], x.Name)
		}
		return true
	})
}

// reachableChecks returns the checks that decl names, directly or through
// the package declarations it mentions.
func (r packageRefs) reachableChecks(decl string) map[string]bool {
	seen := map[string]bool{}
	out := map[string]bool{}
	var visit func(string)
	visit = func(d string) {
		if seen[d] {
			return
		}
		seen[d] = true
		for _, c := range r.checks[d] {
			out[c] = true
		}
		for _, n := range r.names[d] {
			if _, isDecl := r.names[n]; isDecl || r.checks[n] != nil {
				visit(n)
			}
		}
	}
	visit(decl)
	return out
}

// checkKey names c the way the source does, as "Group.Name".
func checkKey(t *testing.T, c pdf.Check) string {
	t.Helper()
	groups := reflect.ValueOf(pdf.Checks)
	for i := range groups.NumField() {
		group := groups.Field(i)
		for j := range group.NumField() {
			if group.Field(j).Interface() == c {
				return groups.Type().Field(i).Name + "." + group.Type().Field(j).Name
			}
		}
	}
	t.Fatalf("check %s is not in pdf.Checks", c.Name())
	return ""
}

// funcName is the bare name of the package function fn.
func funcName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}

// assertDeclaredChecks verifies that a rule declares exactly the checks its
// function can reach. A missing check would let a profile that enables only
// that check skip the rule and lose its findings; an extra one keeps the
// rule running for nothing.
func assertDeclaredChecks(t *testing.T, refs packageRefs, fn any, declared []pdf.Check) {
	t.Helper()
	name := funcName(fn)
	want := refs.reachableChecks(name)
	got := map[string]bool{}
	for _, c := range declared {
		got[checkKey(t, c)] = true
	}
	var missing, extra []string
	for c := range want {
		if !got[c] {
			missing = append(missing, c)
		}
	}
	for c := range got {
		if !want[c] {
			extra = append(extra, c)
		}
	}
	slices.Sort(missing)
	slices.Sort(extra)
	if len(missing) > 0 {
		t.Errorf("%s reaches undeclared checks %v", name, missing)
	}
	if len(extra) > 0 {
		t.Errorf("%s declares checks it never reaches: %v", name, extra)
	}
}

// TestDictRulesDeclareTheirChecks verifies each dict rule's check list
// against the checks its function reaches.
func TestDictRulesDeclareTheirChecks(t *testing.T) {
	refs := loadPackageRefs(t)
	for _, r := range dictRules {
		assertDeclaredChecks(t, refs, r.fn, r.checks)
	}
}

// TestDocRulesDeclareTheirChecks verifies each document rule's check list
// against the checks its function reaches.
func TestDocRulesDeclareTheirChecks(t *testing.T) {
	refs := loadPackageRefs(t)
	for _, r := range append(slices.Clone(preWalkDocRules), postWalkDocRules...) {
		assertDeclaredChecks(t, refs, r.fn, r.checks)
	}
}

// TestEnabledRules verifies that enabledRules keeps exactly the rules with an
// enabled check, and returns the table itself when every rule is enabled.
func TestEnabledRules(t *testing.T) {
	checks := func(r dictRule) []pdf.Check { return r.checks }
	if got := enabledRules(dictRules, pdf.PDFA1B, checks); &got[0] != &dictRules[0] || len(got) != len(dictRules) {
		t.Error("with every rule enabled, enabledRules did not return the table itself")
	}

	none := pdf.PDFA1B.Clone().Clear()
	if got := enabledRules(dictRules, none, checks); got == nil || len(got) != 0 {
		t.Errorf("with no check enabled, enabledRules = %v, want an empty non-nil slice", got)
	}

	fonts := pdf.PDFA1B.Clone().Clear().AddCheck(pdf.Checks.Font.FontBaseFont)
	got := enabledRules(dictRules, fonts, checks)
	if len(got) != 1 || funcName(got[0].fn) != "ValidateFontDict" {
		t.Errorf("with one font check enabled, enabledRules kept %d rules, want ValidateFontDict alone", len(got))
	}
}

// TestRunDocRulesStopsWhenCancelled verifies that no document rule runs once
// the verify is cancelled.
func TestRunDocRulesStopsWhenCancelled(t *testing.T) {
	ran := false
	rules := []docRule{{fn: func(*pdf.Reader, pdf.PDFValue, *ValidationContext) []pdf.PDFError {
		ran = true
		return nil
	}}}
	ctx := &ValidationContext{cancelled: true}
	runDocRules(rules, nil, nil, ctx)
	if ran || ctx.errs != nil {
		t.Errorf("a cancelled run ran a document rule (issues %v)", ctx.errs)
	}
}

// TestNarrowProfileReportsWhatTheFullOneDoes verifies, over the reference
// corpora, that skipping a rule loses nothing: a profile enabling only one
// rule's checks reports what the full profile reports for those checks.
func TestNarrowProfileReportsWhatTheFullOneDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("verifies every corpus file several times")
	}
	var paths []string
	for _, dir := range []string{isartorDir, veraPDFDir} {
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".pdf") {
				paths = append(paths, path)
			}
			return nil
		})
	}
	if len(paths) == 0 {
		t.Skip("no corpora present")
	}
	var all [][]pdf.Check
	for _, r := range dictRules {
		all = append(all, r.checks)
	}
	for _, r := range append(slices.Clone(preWalkDocRules), postWalkDocRules...) {
		all = append(all, r.checks)
	}
	describe := func(issues []pdf.PDFError, p *pdf.Profile) []string {
		var out []string
		for _, iss := range issues {
			if p.Has(iss.Check()) {
				out = append(out, fmt.Sprintf("p%d %s", iss.Page(), iss.String()))
			}
		}
		return out
	}
	compared := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		full, err := VerifyBytes(data, pdf.PDFA1B, nil)
		if err != nil {
			continue
		}
		for _, checks := range all {
			narrow := pdf.PDFA1B.Clone().Clear()
			for _, c := range checks {
				if pdf.PDFA1B.Has(c) {
					narrow = narrow.AddCheck(c)
				}
			}
			want := describe(full.Issues, narrow)
			if len(want) == 0 {
				continue
			}
			res, err := VerifyBytes(data, narrow, nil)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			compared++
			if got := describe(res.Issues, narrow); !slices.Equal(got, want) {
				t.Errorf("%s, narrowed to %v:\n got %q\nwant %q", path, checks, got, want)
			}
		}
	}
	if compared == 0 {
		t.Fatal("no corpus file reported a rule's check")
	}
}

// TestDocRulesNameTheObject verifies that each document-level family reports
// against the object it judged: document-level, so on no page, but with that
// object's reference.
func TestDocRulesNameTheObject(t *testing.T) {
	const catalog = "/Type /Catalog /Pages 2 0 R"
	for _, tc := range []struct {
		name    string
		check   pdf.Check
		catalog string            // extra catalog entries
		objs    map[int]string    // extra objects
		streams map[int][2]string // extra stream objects: dict, data
		info    bool
		want    int
	}{
		{name: "info", check: pdf.Checks.Structure.InfoDictEmptyValues,
			objs: map[int]string{5: "<< /Title () >>"}, info: true, want: 5},
		{name: "optional content", check: pdf.Checks.Structure.OptionalContent,
			catalog: "/OCProperties << /OCGs [] /D << >> >>", want: 1},
		{name: "forms", check: pdf.Checks.Form.NeedAppearances,
			catalog: "/AcroForm 5 0 R", objs: map[int]string{5: "<< /Fields [] /NeedAppearances true >>"}, want: 5},
		{name: "output intent", check: pdf.Checks.Colour.OutputIntentWrongS,
			catalog: "/OutputIntents [5 0 R]", objs: map[int]string{5: "<< /Type /OutputIntent /S /GTS_PDFX /OutputConditionIdentifier (x) >>"}, want: 5},
		{name: "missing metadata", check: pdf.Checks.Metadata.MetadataMissing, want: 1},
		{name: "metadata", check: pdf.Checks.Metadata.XMPNotWellFormed,
			catalog: "/Metadata 5 0 R", streams: map[int][2]string{5: {"<< /Type /Metadata /Subtype /XML", "<x:xmpmeta"}}, want: 5},
		{name: "object metadata", check: pdf.Checks.Metadata.ObjectXMPNoXPacket,
			objs:    map[int]string{3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Metadata 5 0 R >>"},
			streams: map[int][2]string{5: {"<< /Type /Metadata /Subtype /XML", "<x:xmpmeta/>"}}, want: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := pdfgen.NewBuilder("%PDF-1.4\n")
			b.Obj(1, "<< "+catalog+" "+tc.catalog+" >>")
			b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
			if _, ok := tc.objs[3]; !ok {
				b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] >>")
			}
			for _, n := range []int{3, 5} {
				if body, ok := tc.objs[n]; ok {
					b.Obj(n, body)
				}
				if s, ok := tc.streams[n]; ok {
					b.StreamObj(n, s[0], []byte(s[1]))
				}
			}
			trailer := "<< /Size 6 /Root 1 0 R >>"
			if tc.info {
				trailer = "<< /Size 6 /Root 1 0 R /Info 5 0 R >>"
			}
			res, err := VerifyBytes(b.FinishClassic(trailer), pdf.PDFA1B, nil)
			if err != nil {
				t.Fatal(err)
			}
			issues := res.IssuesForCheck(tc.check)
			if len(issues) == 0 {
				t.Fatalf("no %s issue; got %v", tc.check.Name(), res.Issues)
			}
			for _, iss := range issues {
				ref, ok := iss.ObjectRef()
				if !ok || ref.ObjNum != tc.want || iss.Page() != 0 {
					t.Errorf("%v: ref %v (%v), page %d; want object %d on no page", iss, ref, ok, iss.Page(), tc.want)
				}
			}
		})
	}
}
