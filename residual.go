package pdfrab

// ResidualCategory classifies a Check by what remediation it needs beyond
// this package's targeted fixups -- whether it can only be cleared by the
// whole-page rasterization Convert now applies automatically as a last resort
// (see convert.go). Returns "" for anything not specifically classified here;
// that covers both genuinely novel violations and ones theoretically fixable
// by a future dictionary-level fixup that just doesn't exist yet -- being
// unclassified is not itself evidence that rasterization is needed. Convert
// rasterizes any such page on a resolvable graph, so these checks should not
// survive in a ConvertResult's Residual(); the classification documents the
// remediation each one required.
func ResidualCategory(c Check) string {
	switch c {
	case Checks.Font.SimpleNotEmbedded, Checks.Font.CIDNotEmbedded,
		Checks.Font.SubsetGlyphCoverage, Checks.Font.InvalidProgram,
		Checks.Font.CMapNotEmbedded:
		// fontSubstitutionFixer (fixups_font_subst.go) clears most of these
		// by substituting a bundled face, but only when a code/CID->Unicode
		// mapping is recoverable (Identity-H/V plus /ToUnicode for composite
		// fonts); a CID-keyed font with neither -- typically a CJK font with
		// no /ToUnicode -- has no in-place fix: there's nothing left in the
		// file to tell it what glyph any given CID was supposed to be, so the
		// affected text can only be reproduced by rasterizing its page (which
		// Convert now does automatically).
		return "font: requires re-embedding/re-subsetting the original font, or rasterizing the affected text"

	case Checks.Structure.StringTooLong:
		// The q/Q-nesting-depth flavour of StringTooLong is a structural
		// defect contentLimitsFixer (fixups_content.go) deliberately leaves
		// open: rebalancing the nesting can't be done as a scalar clamp.
		// Convert's automatic whole-page rasterization is the only in-process
		// remediation.
		return "content-stream: requires re-tokenizing the content stream, or rasterizing the affected page"
	}
	return ""
}
