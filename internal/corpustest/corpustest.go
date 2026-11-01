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
	// IsartorNames are names of the form isartor-<clause>-t<NN>-..., with the
	// clause numbers joined by dashes. Every Isartor file is a fail file.
	IsartorNames Naming = iota
	// VeraPDFNames are names of the form <clause>-t<NN>-pass-... or
	// <clause>-t<NN>-fail-..., optionally prefixed with "veraPDF test suite ".
	// The clause numbers are joined by dashes.
	VeraPDFNames
)

// Corpus is one conformance corpus.
type Corpus struct {
	// Name identifies the corpus in test names and logs, e.g. "veraPDF-1b".
	Name string
	// Dir is the corpus directory relative to the repository root.
	Dir string
	// Profile is the profile the corpus's conformance suite verifies with.
	// Profile.Level is the level the filename clauses are numbered in.
	Profile *pdf.Profile
	// Default is the level's default profile, tuned to veraPDF's reading of
	// the standard. The differential check against veraPDF and the convert
	// suites use it.
	Default *pdf.Profile
	// Flavour is the veraPDF --flavour that validates the same level.
	Flavour string
	// Naming is the corpus's filename convention.
	Naming Naming
	// Clauses, when not nil, lists the only clauses whose fail files the
	// corpus's tests expect to be caught. It is set for a level whose verifier
	// does not implement every clause. A fail file counts when the clause its
	// name targets is one of them or lies under one. Pass files always count.
	Clauses []string
	// Reclassified maps the base name of a fail file to the clause veraPDF
	// reports its violation under, where that differs from the clause its
	// name targets.
	Reclassified map[string]string
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
	// VeraPDF2B covers only the clauses the PDF/A-2b verifier implements.
	VeraPDF2B = Corpus{
		Name:    "veraPDF-2b",
		Dir:     "tests/veraPDF/PDF_A-2b",
		Profile: pdf.PDFA2B,
		Default: pdf.PDFA2B,
		Flavour: "2b",
		Naming:  VeraPDFNames,
		Clauses: []string{"6.1", "6.2.2", "6.2.3", "6.2.4", "6.2.5", "6.2.6", "6.2.8", "6.2.9", "6.2.10"},
		Reclassified: map[string]string{
			// The rendering intent of an image dictionary is checked under
			// 6.2.6, not 6.2.8.
			"veraPDF test suite 6-2-8-1-t03-fail-a.pdf": "6.2.6",
			// The transparent glyph of its Type 3 font is never shown, so
			// the page contains no transparency; the file fails only for
			// its uncovered DeviceGray.
			"veraPDF test suite 6-2-10-t04-fail-i.pdf": "6.2.4.3",
		},
	}
)

// All lists every committed corpus.
var All = []Corpus{Isartor1B, VeraPDF1B, VeraPDF2B}

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

// Covers reports whether the corpus's tests expect a fail file targeting the
// given clause to be caught (see Clauses).
func (c Corpus) Covers(clause string) bool {
	if c.Clauses == nil {
		return true
	}
	for _, covered := range c.Clauses {
		if strings.HasPrefix(clause+".", covered+".") {
			return true
		}
	}
	return false
}

// Fixtures returns the corpus files whose names follow its convention, in
// lexical order, leaving out the fail files of clauses the corpus does not
// cover.
func (c Corpus) Fixtures() []Fixture {
	var out []Fixture
	for _, path := range c.Files() {
		if clause, wantFail := c.Expect(path); clause != "" && (!wantFail || c.Covers(clause)) {
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
	if clause, ok := c.Reclassified[name]; ok {
		return clause, m[2] == "fail"
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
