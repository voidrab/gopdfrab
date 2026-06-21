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

func (fileSpecFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		if _, ok := d.Entries["EF"]; ok {
			delete(d.Entries, "EF")
			changed = true
		}
		if _, ok := d.Entries["EmbeddedFiles"]; ok {
			delete(d.Entries, "EmbeddedFiles")
			changed = true
		}
		if !d.HasStream {
			return
		}
		for _, key := range []string{"F", "FFilter", "FDecodeParms"} {
			if _, ok := d.Entries[key]; ok {
				delete(d.Entries, key)
				changed = true
			}
		}
	})
	return changed, nil
}
