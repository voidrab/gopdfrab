package corpustest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestExpect(t *testing.T) {
	tests := []struct {
		c          Corpus
		name       string
		wantClause string
		wantFail   bool
	}{
		{Isartor1B, "isartor-6-6-1-t01-fail-a.pdf", "6.6.1", true},
		{Isartor1B, "dir/isartor-6-1-12-t02-fail-b.pdf", "6.1.12", true},
		{Isartor1B, "readme.pdf", "", false},
		{VeraPDF1B, "veraPDF test suite 6-1-2-t01-fail-a.pdf", "6.1.2", true},
		{VeraPDF1B, "6-3-5-t02-pass-a.pdf", "6.3.5", false},
		{VeraPDF1B, "isartor-6-6-1-t01-fail-a.pdf", "", false},
	}
	for _, tt := range tests {
		clause, wantFail := tt.c.Expect(tt.name)
		if clause != tt.wantClause || wantFail != tt.wantFail {
			t.Errorf("%s.Expect(%q) = %q, %v; want %q, %v",
				tt.c.Name, tt.name, clause, wantFail, tt.wantClause, tt.wantFail)
		}
	}
}

func TestClauseMatches(t *testing.T) {
	tests := []struct {
		got, expected string
		want          bool
	}{
		{"6.1.2", "6.1.2", true},
		{"6.2.3", "6.2.3.3", true},
		{"6.2.3.3", "6.2.3", true},
		{"6.1.1", "6.1.12", false},
		{"6.1.12", "6.1.1", false},
	}
	for _, tt := range tests {
		if got := ClauseMatches(tt.got, tt.expected); got != tt.want {
			t.Errorf("ClauseMatches(%q, %q) = %v, want %v", tt.got, tt.expected, got, tt.want)
		}
	}
}

func TestCheckMatchesIsLevelAware(t *testing.T) {
	c := pdf.Checks.Structure.TrailerEOF
	clause := c.Clause()
	if !CheckMatches(c, pdf.A1B, clause) {
		t.Errorf("CheckMatches(%s, A1B, %s) = false", c.Name(), clause)
	}
	if CheckMatches(c, pdf.LevelType("no such level"), clause) {
		t.Errorf("CheckMatches matched under a level the check is not a rule of")
	}

	issues := []pdf.PDFError{pdf.NewError(c, nil, 0, nil)}
	if !AnyMatches(issues, pdf.A1B, clause) {
		t.Error("AnyMatches = false for a matching issue")
	}
	if AnyMatches(issues, pdf.A1B, "9.9") {
		t.Error("AnyMatches = true for an unrelated clause")
	}
	if got := Clauses(issues, pdf.A1B); !slices.Equal(got, []string{clause}) {
		t.Errorf("Clauses(A1B) = %v, want [%s]", got, clause)
	}
	if got := Clauses(issues, pdf.LevelType("no such level")); !slices.Equal(got, []string{"?"}) {
		t.Errorf("Clauses(unknown level) = %v, want [?]", got)
	}
}

func TestCorpusFiles(t *testing.T) {
	// Corpus.Dir is relative to the repository root, so the fixture corpus
	// has to sit under it: t.TempDir can be on another drive on Windows.
	dir, err := os.MkdirTemp(".", "testcorpus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if dir, err = filepath.Abs(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b/x.PDF", "a.pdf", "notes.txt", "6-1-2-t01-fail-a.pdf", "6-1-3-t01-pass-a.pdf"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rel, err := filepath.Rel(repoRoot, dir)
	if err != nil {
		t.Fatal(err)
	}
	c := Corpus{Name: "tmp", Dir: filepath.ToSlash(rel), Profile: pdf.PDFA1B, Naming: VeraPDFNames}

	if !c.Present() {
		t.Fatal("Present = false for an existing directory")
	}
	var got []string
	for _, f := range c.Files() {
		got = append(got, c.Rel(f))
	}
	if want := []string{"6-1-2-t01-fail-a.pdf", "6-1-3-t01-pass-a.pdf", "a.pdf", "b/x.PDF"}; !slices.Equal(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
	if c.Level() != pdf.A1B {
		t.Errorf("Level = %v, want %v", c.Level(), pdf.A1B)
	}
	if got := c.Rel("relative/x.pdf"); got != "relative/x.pdf" {
		t.Errorf("Rel of a relative path = %q, want it unchanged", got)
	}

	fx := c.Fixtures()
	if len(fx) != 2 || fx[0].Clause != "6.1.2" || !fx[0].WantFail || fx[1].Clause != "6.1.3" || fx[1].WantFail {
		t.Errorf("Fixtures = %+v, want the 6.1.2 fail file and the 6.1.3 pass file", fx)
	}

	saved := All
	defer func() { All = saved }()
	All = []Corpus{c}
	if got := AllFixtures(true); len(got) != 1 || got[0].Clause != "6.1.2" {
		t.Errorf("AllFixtures(true) = %+v, want the fail file", got)
	}
	if got := AllFixtures(false); len(got) != 1 || got[0].Clause != "6.1.3" {
		t.Errorf("AllFixtures(false) = %+v, want the pass file", got)
	}

	missing := Corpus{Dir: "tests/no-such-corpus"}
	if missing.Present() || len(missing.Files()) != 0 {
		t.Error("a missing corpus is reported present or has files")
	}
}

// TestAllCorporaAreNamedUniquely guards the keys tests build from a corpus:
// names and directories must not collide.
func TestAllCorporaAreNamedUniquely(t *testing.T) {
	names, dirs := map[string]bool{}, map[string]bool{}
	for _, c := range All {
		if names[c.Name] || dirs[c.Dir] {
			t.Errorf("corpus %s (%s) repeats a name or directory", c.Name, c.Dir)
		}
		names[c.Name], dirs[c.Dir] = true, true
		if c.Profile == nil || c.Default == nil || c.Default.Level != c.Profile.Level || c.Flavour == "" || strings.Contains(c.Dir, "\\") {
			t.Errorf("corpus %s is incomplete: %+v", c.Name, c)
		}
	}
}
