package convert

import (
	"errors"
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
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
