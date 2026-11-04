package verify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// 6.8 Embedded files (ISO 19005-2)

// maxEmbedDepth bounds how deeply embedded files are verified inside one
// another. Each level decodes and verifies a whole document, and a file can
// embed a copy of itself.
const maxEmbedDepth = 4

// embedDepthKey is the context key under which a nested verify records how
// many embedded files enclose the document it verifies.
type embedDepthKey struct{}

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

// EmbeddedFileProblem says why the embedded file stream ef does not comply
// with PDF/A-1b or PDF/A-2b, as test 5 of ISO 19005-2 6.8 requires, or
// returns "" when it does. As veraPDF judges it, the file must claim one of
// the two levels in its XMP metadata and pass every check of the level it
// claims. run is the context of the run that found ef, or nil for none.
func EmbeddedFileProblem(run context.Context, ef pdf.PDFDict) string {
	if run == nil {
		run = context.Background()
	}
	depth, _ := run.Value(embedDepthKey{}).(int)
	if depth >= maxEmbedDepth {
		return fmt.Sprintf("is embedded more than %d levels deep, too deep to verify", maxEmbedDepth)
	}
	data, err := pdf.DecodeStream(ef)
	if err != nil {
		return "cannot be decoded: " + err.Error()
	}
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return "is not a readable PDF file"
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
		return "does not claim PDF/A-1b or PDF/A-2b"
	}
	res, err := verifyEmbedded(context.WithValue(run, embedDepthKey{}, depth+1), doc, p)
	if err != nil {
		return "cannot be verified: " + err.Error()
	}
	if !res.Valid {
		return fmt.Sprintf("claims PDF/A-%s but fails %d of its checks", strings.ToLower(claim), len(res.Issues))
	}
	return ""
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

// embeddedFileProblem is EmbeddedFileProblem, memoized per stream so that a
// stream several EF entries or file specifications share is verified once.
func (ctx *ValidationContext) embeddedFileProblem(ef pdf.PDFDict) string {
	ptr := pdf.ValuePointer(ef.Entries)
	if problem, ok := ctx.embeddedFiles[ptr]; ok {
		return problem
	}
	if ctx.embeddedFiles == nil {
		ctx.embeddedFiles = map[uintptr]string{}
	}
	problem := EmbeddedFileProblem(ctx.run, ef)
	ctx.embeddedFiles[ptr] = problem
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
		ref := refOf(root)
		if r, ok := owner.Ref(); ok {
			ref = &r
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
