package convert

import (
	"github.com/voidrab/gopdfrab/internal/check"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func init() {
	registerFixer(fileSpecFixer{})
}

type fileSpecFixer struct{}

func (fileSpecFixer) Applies(c check.Check) bool {
	switch c {
	case check.Checks.Structure.EmbeddedFileSpec, check.Checks.Structure.EmbeddedFiles,
		check.Checks.Structure.StreamFileSpec, check.Checks.Structure.StreamFileFilter,
		check.Checks.Structure.StreamFileDecodeParams:
		return true
	}
	return false
}

func (f fileSpecFixer) Fix(trailer *pdf.PDFDict, _ []check.PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (fileSpecFixer) prepare(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if _, ok := d.Entries["EF"]; ok {
			delete(d.Entries, "EF")
			*changed = true
		}
		if _, ok := d.Entries["EmbeddedFiles"]; ok {
			delete(d.Entries, "EmbeddedFiles")
			*changed = true
		}
		if !d.HasStream {
			return
		}
		for _, key := range []string{"F", "FFilter", "FDecodeParms"} {
			if _, ok := d.Entries[key]; ok {
				delete(d.Entries, key)
				*changed = true
			}
		}
	}, true
}
