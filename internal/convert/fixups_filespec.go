package convert

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// fileSpecFixer removes the embedded files and external file references that
// PDF/A-1 forbids, each only when its own check was reported. An embedded
// file is content the output no longer holds, so each one removed is reported
// to lost, when non-nil.
type fileSpecFixer struct {
	lost *[]pdf.PDFError
}

func (fileSpecFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Structure.EmbeddedFileSpec, pdf.Checks.Structure.EmbeddedFiles,
		pdf.Checks.Structure.StreamFileSpec, pdf.Checks.Structure.StreamFileFilter,
		pdf.Checks.Structure.StreamFileDecodeParams:
		return true
	}
	return false
}

func (f fileSpecFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (f fileSpecFixer) prepare(_ *pdf.PDFDict, issues []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	reported := reportedChecks(issues)
	st := pdf.Checks.Structure
	dropFile := func(c pdf.Check) func(pdf.PDFDict) {
		return func(d pdf.PDFDict) {
			if _, ok := d.Entries.Lookup("EF"); !ok {
				return
			}
			d.Entries.Del("EF")
			*changed = true
			reportLost(f.lost, c, d, embeddedFileName(d)+" removed")
		}
	}
	streamKeys := map[string]pdf.Check{
		"F": st.StreamFileSpec, "FFilter": st.StreamFileFilter, "FDecodeParms": st.StreamFileDecodeParams,
	}
	return func(d pdf.PDFDict) {
		if reported[st.EmbeddedFileSpec] {
			dropFile(st.EmbeddedFileSpec)(d)
		}
		if tree, ok := d.Entries.Lookup("EmbeddedFiles"); ok && reported[st.EmbeddedFiles] {
			// The walk does not descend into a deleted entry, so the files
			// the name tree held are dropped and reported here.
			walkDicts(tree, map[uintptr]bool{}, dropFile(st.EmbeddedFiles))
			d.Entries.Del("EmbeddedFiles")
			*changed = true
		}
		if !d.IsStream() {
			return
		}
		for key, c := range streamKeys {
			if _, ok := d.Entries.Lookup(key); ok && reported[c] {
				d.Entries.Del(key)
				*changed = true
			}
		}
	}, true
}

// embeddedFileName names the file that the file specification fs embeds, by
// its Unicode file name (/UF) or else its file name (/F) when it has one.
func embeddedFileName(fs pdf.PDFDict) string {
	for _, key := range []string{"UF", "F"} {
		if name := pdf.DecodeInfoTextString(fs.Entries.Get(key)); name != "" {
			return fmt.Sprintf("embedded file %q", name)
		}
	}
	return "embedded file"
}

// reportLost appends a record for the removal of d under check c to lost,
// when lost is non-nil. It includes d's object reference if d has one.
func reportLost(lost *[]pdf.PDFError, c pdf.Check, d pdf.PDFDict, msg string) {
	if lost == nil {
		return
	}
	var ref *pdf.PDFRef
	if r, ok := d.Ref(); ok {
		ref = &r
	}
	*lost = append(*lost, pdf.NewError(c, []error{errors.New(msg)}, 0, ref))
}

// --- 6.8 Embedded files (PDF/A-2) ---

// embeddedFileFixer repairs the file specifications of embedded files, those
// found by their Filespec type and those of the EmbeddedFiles name tree, each
// repair only when its own check was reported. A file specification with an
// embedded file that does not comply with PDF/A-1b or PDF/A-2b loses its EF
// entry, and the removal is reported to lost, when non-nil. One that keeps its
// EF entry but lacks an F or UF string gets the one from the other, or both
// from its name in the tree, or else a generic name.
type embeddedFileFixer struct {
	ctx  context.Context
	lost *[]pdf.PDFError
}

func (embeddedFileFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.EmbeddedFileMissingNames || c == pdf.Checks.Structure.EmbeddedFileNotPDFA
}

func (f embeddedFileFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	reported := reportedChecks(issues)
	st := pdf.Checks.Structure
	treeNames := map[pdf.Dict]string{}
	tree := verify.EmbeddedFilesTree(root)
	for _, named := range tree {
		if _, ok := treeNames[named.Spec.Entries]; !ok {
			treeNames[named.Spec.Entries] = named.Name
		}
	}
	problems := map[pdf.Dict]string{}
	dropped := map[pdf.Dict]bool{}
	changed := false
	fix := func(fs pdf.PDFDict) {
		if _, ok := fs.Entries.Lookup("EF"); !ok {
			return
		}
		if reported[st.EmbeddedFileNotPDFA] {
			for _, ef := range verify.EmbeddedFileStreams(fs) {
				problem, ok := problems[ef.Entries]
				if !ok {
					problem = verify.EmbeddedFileProblem(f.ctx, ef)
					problems[ef.Entries] = problem
				}
				if problem != "" {
					reportLost(f.lost, st.EmbeddedFileNotPDFA, fs, embeddedFileName(fs)+" removed: it "+problem)
					fs.Entries.Del("EF")
					dropped[fs.Entries] = true
					changed = true
					return
				}
			}
		}
		if reported[st.EmbeddedFileMissingNames] && !verify.HasFileNames(fs) {
			fillFileNames(fs, treeNames[fs.Entries])
			changed = true
		}
	}
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if verify.IsFileSpec(d) {
			fix(d)
		}
	})
	for _, named := range tree {
		fix(named.Spec)
	}
	if len(dropped) > 0 {
		dropAssociatedFiles(*trailer, dropped)
	}
	return changed, nil
}

// dropAssociatedFiles removes the file specifications in dropped from every
// AF array, and deletes an AF entry it leaves empty. An associated file must
// be embedded, so one that lost its embedded file is no longer one.
func dropAssociatedFiles(trailer pdf.PDFDict, dropped map[pdf.Dict]bool) {
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		af, ok := d.Entries.Get("AF").(pdf.PDFArray)
		if !ok {
			return
		}
		kept := slices.DeleteFunc(slices.Clone(af), func(v pdf.PDFValue) bool {
			fs, ok := v.(pdf.PDFDict)
			return ok && dropped[fs.Entries]
		})
		switch {
		case len(kept) == 0:
			d.Entries.Del("AF")
		case len(kept) < len(af):
			d.Entries.Set("AF", kept)
		}
	})
}

// fillFileNames gives the file specification fs the F or UF string it lacks:
// the other one's text, or else treeName, or else a generic name, for both.
func fillFileNames(fs pdf.PDFDict, treeName string) {
	f, hasF := pdf.StringBytes(fs.Entries.Get("F"))
	uf, hasUF := pdf.StringBytes(fs.Entries.Get("UF"))
	var name string
	switch {
	case hasF:
		name = pdf.DecodePDFTextString(f)
	case hasUF:
		name = pdf.DecodePDFTextString(uf)
	case treeName != "":
		name = treeName
	default:
		name = "attachment"
	}
	text := pdf.PDFString{Value: string(pdf.EncodePDFTextString(name))}
	if !hasF {
		fs.Entries.Set("F", text)
	}
	if !hasUF {
		fs.Entries.Set("UF", text)
	}
}
