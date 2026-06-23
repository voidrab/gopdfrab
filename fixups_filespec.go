package pdfrab

func init() {
	registerFixer(fileSpecFixer{})
}

type fileSpecFixer struct{}

func (fileSpecFixer) Applies(c Check) bool {
	switch c {
	case Checks.Structure.EmbeddedFileSpec, Checks.Structure.EmbeddedFiles,
		Checks.Structure.StreamFileSpec, Checks.Structure.StreamFileFilter,
		Checks.Structure.StreamFileDecodeParams:
		return true
	}
	return false
}

func (f fileSpecFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	return runDictVisitor(trailer, f.prepare)
}

func (fileSpecFixer) prepare(_ *PDFDict, changed *bool) (func(PDFDict), bool) {
	return func(d PDFDict) {
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
