// Package corpustest describes the conformance corpora committed under tests/:
// where each one lives, which conformance level and profile it is verified
// against, which veraPDF flavour checks the same files, and how a file's name
// encodes the clause it targets and whether it is meant to fail.
//
// Every test that walks the corpora reads them from All, so a corpus added
// for a new level joins those walks by adding one row.
package corpustest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Naming is the filename convention a corpus follows.
type Naming int

const (
	// IsartorNames are names like "isartor-6-6-1-t01-fail-a.pdf". Every
	// Isartor file is a fail file.
	IsartorNames Naming = iota
	// VeraPDFNames are names like "veraPDF test suite 6-1-2-t01-fail-a.pdf"
	// or "6-3-5-t02-pass-a.pdf".
	VeraPDFNames
)

// Corpus is one conformance corpus.
type Corpus struct {
	// Name identifies the corpus in test names and logs, e.g. "veraPDF-1b".
	Name string
	// Dir is the corpus directory relative to the repository root.
	Dir string
	// Profile is the profile the corpus's conformance suite verifies with.
	// Its Level is the level the filename clauses are numbered in.
	Profile *pdf.Profile
	// Default is the level's default profile, tuned to veraPDF's reading of
	// the standard. The differential check against veraPDF and the convert
	// suites use it.
	Default *pdf.Profile
	// Flavour is the veraPDF --flavour that validates the same level.
	Flavour string
	// Naming is the corpus's filename convention.
	Naming Naming
}

// The committed corpora.
var (
	VeraPDF1B = Corpus{
		Name:    "veraPDF-1b",
		Dir:     "tests/veraPDF/PDF_A-1b",
		Profile: pdf.PDFA1B,
		Default: pdf.PDFA1B,
		Flavour: "1b",
		Naming:  VeraPDFNames,
	}
	// Isartor1B is verified with Legacy1B, the spec-literal profile whose
	// reading of the standard the Isartor suite shares.
	Isartor1B = Corpus{
		Name:    "Isartor-1b",
		Dir:     "tests/Isartor/PDFA-1b",
		Profile: pdf.Legacy1B,
		Default: pdf.PDFA1B,
		Flavour: "1b",
		Naming:  IsartorNames,
	}
)

// All lists every committed corpus.
var All = []Corpus{Isartor1B, VeraPDF1B}

// repoRoot is the repository root, two directories above this file.
var repoRoot = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}()

// Level is the conformance level the corpus targets.
func (c Corpus) Level() pdf.LevelType { return c.Profile.Level }

// Path returns the corpus directory as an absolute path.
func (c Corpus) Path() string { return filepath.Join(repoRoot, filepath.FromSlash(c.Dir)) }

// Present reports whether the corpus directory exists.
func (c Corpus) Present() bool {
	_, err := os.Stat(c.Path())
	return err == nil
}

// Files returns the absolute path of every PDF in the corpus, in lexical
// order. A corpus that is not present has no files.
func (c Corpus) Files() []string {
	var files []string
	_ = filepath.WalkDir(c.Path(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".pdf") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// Rel returns path relative to the corpus directory, with forward slashes.
func (c Corpus) Rel(path string) string {
	rel, err := filepath.Rel(c.Path(), path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// Fixture is one corpus file whose name follows the corpus's convention.
type Fixture struct {
	Corpus Corpus
	// Path is the file's absolute path.
	Path string
	// Clause is the clause the file targets, numbered in Corpus.Level().
	Clause string
	// WantFail is true for a file that must be reported non-conformant.
	WantFail bool
}

// Fixtures returns the corpus files whose names follow its convention, in
// lexical order.
func (c Corpus) Fixtures() []Fixture {
	var out []Fixture
	for _, path := range c.Files() {
		if clause, wantFail := c.Expect(path); clause != "" {
			out = append(out, Fixture{Corpus: c, Path: path, Clause: clause, WantFail: wantFail})
		}
	}
	return out
}

// AllFixtures returns the fixtures of every corpus in All, corpus by corpus.
// wantFail selects the fail files or the pass files.
func AllFixtures(wantFail bool) []Fixture {
	var out []Fixture
	for _, c := range All {
		for _, f := range c.Fixtures() {
			if f.WantFail == wantFail {
				out = append(out, f)
			}
		}
	}
	return out
}

var (
	isartorNameRe = regexp.MustCompile(`^isartor-((?:\d+-)+)t\d+`)
	veraPDFNameRe = regexp.MustCompile(`^(?:veraPDF test suite )?((?:\d+-)+)t\d+-(pass|fail)-`)
)

// Expect returns the clause a file targets, taken from its base name, and
// whether the file is meant to fail. clause is empty for a name that does not
// follow the corpus's convention.
func (c Corpus) Expect(name string) (clause string, wantFail bool) {
	name = filepath.Base(name)
	if c.Naming == IsartorNames {
		m := isartorNameRe.FindStringSubmatch(name)
		if m == nil {
			return "", false
		}
		return dashesToDots(m[1]), true
	}
	m := veraPDFNameRe.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return dashesToDots(m[1]), m[2] == "fail"
}

// dashesToDots turns "6-1-2-" into "6.1.2".
func dashesToDots(s string) string {
	return strings.ReplaceAll(strings.TrimSuffix(s, "-"), "-", ".")
}

// ClauseMatches reports whether a reported clause satisfies the expected one:
// the two are equal, or one is a dot-boundary prefix of the other, so "6.2.3"
// satisfies an expected "6.2.3.3" and the other way round.
func ClauseMatches(got, expected string) bool {
	return got == expected ||
		strings.HasPrefix(got+".", expected+".") ||
		strings.HasPrefix(expected+".", got+".")
}

// CheckMatches reports whether c enforces, under level, a clause that
// satisfies the expected one. A check that is not a rule of level never
// matches.
func CheckMatches(c pdf.Check, level pdf.LevelType, expected string) bool {
	clause, _, ok := c.ClauseIn(level)
	return ok && ClauseMatches(clause, expected)
}

// AnyMatches reports whether any issue's check matches the expected clause
// under level.
func AnyMatches(issues []pdf.PDFError, level pdf.LevelType, expected string) bool {
	for _, iss := range issues {
		if CheckMatches(iss.Check(), level, expected) {
			return true
		}
	}
	return false
}

// Clauses returns the clause each issue's check enforces under level, for
// diagnostics. A check that is not a rule of level shows as "?".
func Clauses(issues []pdf.PDFError, level pdf.LevelType) []string {
	out := make([]string, 0, len(issues))
	for _, iss := range issues {
		clause, _, ok := iss.Check().ClauseIn(level)
		if !ok {
			clause = "?"
		}
		out = append(out, clause)
	}
	return out
}
