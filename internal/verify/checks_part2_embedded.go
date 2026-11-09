package verify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// 6.8 Embedded files (ISO 19005-2)

// maxEmbedDepth bounds how deeply embedded files are verified inside one
// another. maxEmbedVerifies bounds how many embedded files one verify run
// judges in all, at every depth. Each judgement decodes and verifies a whole
// document, and a file can embed a copy of itself. The depth bound alone
// would allow a number of verifies exponential in the depth.
const (
	maxEmbedDepth    = 4
	maxEmbedVerifies = 64
)

// embedState is what a verify run and the verifies of the files it embeds
// share: the depth of the document being verified, and the verifies left.
type embedState struct {
	depth int
	left  *atomic.Int32
}

// embedStateKey is the context key of a nested verify's embedState.
type embedStateKey struct{}

// verifyEmbedded verifies an embedded file. It is VerifyContext, set in init
// because the rule tables VerifyContext reads refer back to it.
var verifyEmbedded func(context.Context, *pdf.Reader, *pdf.Profile) (pdf.Result, error)

func init() { verifyEmbedded = VerifyContext }

// embeddedFileKeys are the entries of an EF dictionary that hold an embedded
// file stream, as veraPDF reads them.
var embeddedFileKeys = []string{"F", "UF", "DOS", "Mac", "Unix"}

// IsFileSpec reports whether d is a file specification dictionary by its
// type, as veraPDF finds them in the object graph.
func IsFileSpec(d pdf.PDFDict) bool {
	return d.Entries.Get("Type") == pdf.PDFName{Value: "Filespec"}
}

// NamedFileSpec is a file specification of the EmbeddedFiles name tree with
// the name the tree gives it.
type NamedFileSpec struct {
	Name string
	Spec pdf.PDFDict
}

// EmbeddedFilesTree returns the file specification dictionaries of the
// EmbeddedFiles name tree in root's Names dictionary, in tree order. A value
// that is not a dictionary is skipped.
func EmbeddedFilesTree(root pdf.PDFDict) []NamedFileSpec {
	names, _ := root.Entries.Get("Names").(pdf.PDFDict)
	tree, ok := names.Entries.Get("EmbeddedFiles").(pdf.PDFDict)
	if !ok {
		return nil
	}
	var specs []NamedFileSpec
	seen := map[uintptr]bool{}
	var walk func(node pdf.PDFDict, depth int)
	walk = func(node pdf.PDFDict, depth int) {
		ptr := pdf.ValuePointer(node.Entries)
		if depth > pdf.MaxPageTreeDepth || seen[ptr] {
			return
		}
		seen[ptr] = true
		pairs, _ := node.Entries.Get("Names").(pdf.PDFArray)
		for i := 0; i+1 < len(pairs); i += 2 {
			if fs, ok := pairs[i+1].(pdf.PDFDict); ok {
				specs = append(specs, NamedFileSpec{Name: pdf.DecodeInfoTextString(pairs[i]), Spec: fs})
			}
		}
		for _, kid := range dictsOf(node.Entries.Get("Kids")) {
			walk(kid, depth+1)
		}
	}
	walk(tree, 0)
	return specs
}

// EmbeddedFileStreams returns the embedded file streams of the file
// specification fs: the streams its EF dictionary holds under F, UF, DOS, Mac
// and Unix, in that order, each once.
func EmbeddedFileStreams(fs pdf.PDFDict) []pdf.PDFDict {
	ef, _ := fs.Entries.Get("EF").(pdf.PDFDict)
	var streams []pdf.PDFDict
	for _, key := range embeddedFileKeys {
		s, ok := ef.Entries.Get(key).(pdf.PDFDict)
		if ok && s.IsStream() && !slices.ContainsFunc(streams, func(t pdf.PDFDict) bool { return t.Entries == s.Entries }) {
			streams = append(streams, s)
		}
	}
	return streams
}

// HasFileNames reports whether the file specification fs has both an F and a
// UF string, literal or hexadecimal, as test 2 of ISO 19005-2 6.8 requires of
// one with an EF entry.
func HasFileNames(fs pdf.PDFDict) bool {
	_, f := pdf.StringBytes(fs.Entries.Get("F"))
	_, uf := pdf.StringBytes(fs.Entries.Get("UF"))
	return f && uf
}

// EmbeddedFileJudge judges the embedded file streams a run finds against
// test 5 of ISO 19005-2 6.8, each stream once. The verifies it runs, and
// those of the files they embed in turn, share one budget for the run.
type EmbeddedFileJudge struct {
	run  context.Context
	seen map[pdf.Dict]embeddedVerdict
}

type embeddedVerdict struct {
	problem string
	decided bool
}

// NewEmbeddedFileJudge returns a judge for the run whose context is run; a nil
// run is treated as no run. Within a nested verify, the judge shares the budget
// of the run that verifies the enclosing file.
func NewEmbeddedFileJudge(run context.Context) *EmbeddedFileJudge {
	if run == nil {
		run = context.Background()
	}
	if _, ok := run.Value(embedStateKey{}).(embedState); !ok {
		state := embedState{left: new(atomic.Int32)}
		state.left.Store(maxEmbedVerifies)
		run = context.WithValue(run, embedStateKey{}, state)
	}
	return &EmbeddedFileJudge{run: run, seen: map[pdf.Dict]embeddedVerdict{}}
}

// Problem says why the embedded file stream ef does not comply with PDF/A-1b
// or PDF/A-2b, or returns "" when it does. As veraPDF judges it, the file
// must claim one of the two levels in its XMP metadata and pass every check of
// the level it claims. decided is false when the file could not be judged: it
// is nested too deeply, the run has verified as many embedded files as it
// may, or the run was cancelled. The problem then says so, and the file may
// comply.
func (j *EmbeddedFileJudge) Problem(ef pdf.PDFDict) (problem string, decided bool) {
	v, ok := j.seen[ef.Entries]
	if !ok {
		v.problem, v.decided = judgeEmbeddedFile(j.run, ef)
		j.seen[ef.Entries] = v
	}
	return v.problem, v.decided
}

// judgeEmbeddedFile is Problem without the memo. run carries an embedState.
func judgeEmbeddedFile(run context.Context, ef pdf.PDFDict) (problem string, decided bool) {
	state := run.Value(embedStateKey{}).(embedState)
	if state.depth >= maxEmbedDepth {
		return fmt.Sprintf("is embedded more than %d levels deep, too deep to verify", maxEmbedDepth), false
	}
	if state.left.Add(-1) < 0 {
		return fmt.Sprintf("was not verified: a document judges at most %d embedded files", maxEmbedVerifies), false
	}
	data, err := pdf.DecodeStream(ef)
	if err != nil {
		return "cannot be decoded: " + err.Error(), true
	}
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return "is not a readable PDF file", true
	}
	defer doc.Close()
	part, conformance, _ := doc.ClaimedConformance()
	claim := part + strings.ToUpper(conformance)
	var p *pdf.Profile
	switch claim {
	case "1B":
		p = pdf.PDFA1B
	case "2B":
		p = pdf.PDFA2B
	default:
		return "does not claim PDF/A-1b or PDF/A-2b", true
	}
	nested := embedState{depth: state.depth + 1, left: state.left}
	res, err := verifyEmbedded(context.WithValue(run, embedStateKey{}, nested), doc, p)
	if err != nil {
		return "cannot be verified: " + err.Error(), false
	}
	if !res.Valid {
		failed := map[pdf.Check]bool{}
		for _, is := range res.Issues {
			failed[is.Check()] = true
		}
		return fmt.Sprintf("claims PDF/A-%s but fails %d of its checks", strings.ToLower(claim), len(failed)), true
	}
	return "", true
}

// validateFileSpec checks a file specification dictionary, found by its type,
// against ISO 19005-2 6.8.
func validateFileSpec(v pdf.PDFDict, ctx *ValidationContext) {
	if IsFileSpec(v) {
		checkEmbeddedFileSpec(v, ctx)
	}
}

// embeddedFilesRule checks the file specifications of the EmbeddedFiles name
// tree that have no Filespec type, which the walk does not recognise as file
// specifications.
func embeddedFilesRule(d *pdf.Reader, _ pdf.PDFValue, ctx *ValidationContext) []pdf.PDFError {
	root, ok := catalog(d).(pdf.PDFDict)
	if !ok {
		return nil
	}
	for _, named := range EmbeddedFilesTree(root) {
		if !IsFileSpec(named.Spec) {
			checkEmbeddedFileSpec(named.Spec, ctx)
		}
	}
	return nil
}

// checkEmbeddedFileSpec reports the file specification fs when it has an EF
// entry but lacks an F or UF string, and each of its embedded files that does
// not comply with PDF/A-1b or PDF/A-2b.
func checkEmbeddedFileSpec(fs pdf.PDFDict, ctx *ValidationContext) {
	if _, ok := fs.Entries.Lookup("EF"); !ok {
		return
	}
	if !HasFileNames(fs) {
		ctx.Report(pdf.Checks.Structure.EmbeddedFileMissingNames, fs,
			"file specification of an embedded file lacks an F or UF string")
	}
	for _, ef := range EmbeddedFileStreams(fs) {
		if problem := ctx.embeddedFileProblem(ef); problem != "" {
			ctx.Report(pdf.Checks.Structure.EmbeddedFileNotPDFA, fs, "embedded file "+problem)
		}
	}
}

// embeddedFileProblem is the problem the run's EmbeddedFileJudge finds with
// ef, decided or not.
func (ctx *ValidationContext) embeddedFileProblem(ef pdf.PDFDict) string {
	if ctx.embeddedFiles == nil {
		ctx.embeddedFiles = NewEmbeddedFileJudge(ctx.run)
	}
	problem, _ := ctx.embeddedFiles.Problem(ef)
	return problem
}

// 6.10 Alternate presentations and transitions, 6.11 Document requirements
// (ISO 19005-2)

// presentationRule reports an AlternatePresentations entry in the name
// dictionary, a PresSteps entry in any page of the page tree and a
// Requirements entry in the catalog.
func presentationRule(d *pdf.Reader, _ pdf.PDFValue, _ *ValidationContext) []pdf.PDFError {
	root, ok := catalog(d).(pdf.PDFDict)
	if !ok {
		return nil
	}
	var errs []pdf.PDFError
	report := func(c pdf.Check, owner pdf.PDFDict, page int, msg string) {
		ref := refOf(owner)
		if ref == nil {
			ref = refOf(root)
		}
		errs = append(errs, pdf.NewError(c, []error{errors.New(msg)}, page, ref))
	}
	if names, ok := root.Entries.Get("Names").(pdf.PDFDict); ok {
		if _, ok := names.Entries.Lookup("AlternatePresentations"); ok {
			report(pdf.Checks.Structure.AlternatePresentations, names, 0,
				"name dictionary contains an AlternatePresentations entry")
		}
	}
	if pages, ok := root.Entries.Get("Pages").(pdf.PDFDict); ok {
		pdf.WalkPages(pages, func(page, _ pdf.PDFDict, _ [4]float64, n int) {
			if _, ok := page.Entries.Lookup("PresSteps"); ok {
				report(pdf.Checks.Structure.PresentationSteps, page, n, "page contains a PresSteps entry")
			}
		})
	}
	if _, ok := root.Entries.Lookup("Requirements"); ok {
		report(pdf.Checks.Structure.CatalogRequirements, root, 0, "catalog contains a Requirements entry")
	}
	return errs
}
