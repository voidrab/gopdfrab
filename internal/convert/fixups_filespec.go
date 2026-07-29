package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func init() {
	registerFixer(fileSpecFixer{})
}

type fileSpecFixer struct{}

func (fileSpecFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Structure.EmbeddedFileSpec, pdf.Checks.Structure.EmbeddedFiles,
		pdf.Checks.Structure.StreamFileSpec, pdf.Checks.Structure.StreamFileFilter,
		pdf.Checks.Structure.StreamFileDecodeParams:
		return true
	}
	return false
}

func (f fileSpecFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (fileSpecFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if _, ok := d.Entries.Lookup("EF"); ok {
			d.Entries.Del("EF")
			*changed = true
		}
		if _, ok := d.Entries.Lookup("EmbeddedFiles"); ok {
			d.Entries.Del("EmbeddedFiles")
			*changed = true
		}
		if !d.HasStream {
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
