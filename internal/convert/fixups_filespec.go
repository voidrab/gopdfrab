package convert

import (
	"errors"
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// fileSpecFixer removes the embedded files and external file references that
// PDF/A-1 forbids. An embedded file is content the output no longer holds, so
// each one removed is reported to lost, when non-nil.
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

func (f fileSpecFixer) prepare(_ *pdf.PDFDict, _ []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
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
	return func(d pdf.PDFDict) {
		dropFile(pdf.Checks.Structure.EmbeddedFileSpec)(d)
		if tree, ok := d.Entries.Lookup("EmbeddedFiles"); ok {
			// The walk does not descend into a deleted entry, so the files
			// the name tree held are dropped and reported here.
			walkDicts(tree, map[uintptr]bool{}, dropFile(pdf.Checks.Structure.EmbeddedFiles))
			d.Entries.Del("EmbeddedFiles")
			*changed = true
		}
		if !d.IsStream() {
			return
		}
		for _, key := range []string{"F", "FFilter", "FDecodeParms"} {
			if _, ok := d.Entries.Lookup(key); ok {
				d.Entries.Del(key)
				*changed = true
			}
		}
	}, true
}

// embeddedFileName names the file that the file specification fs embeds, by
// its /UF or /F name when it has one.
func embeddedFileName(fs pdf.PDFDict) string {
	for _, key := range []string{"UF", "F"} {
		if name := pdf.DecodeInfoTextString(fs.Entries.Get(key)); name != "" {
			return fmt.Sprintf("embedded file %q", name)
		}
	}
	return "embedded file"
}

// reportLost appends to lost, when non-nil, the removal of d under check c,
// with d's object reference when it has one.
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
