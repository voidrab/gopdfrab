package pdfrab

// ResidualCategory classifies a Check still present in a ConvertResult's
// Residual() as a hint toward what remediation it would need beyond this
// package's targeted fixups -- in particular, whether the content is
// genuinely irreducible (no glyph/mapping data survives to repair) or whether
// the opt-in raster fallback (Convert with WithRasterFallback) is the only
// remaining in-process route. Returns "" for anything not specifically
// classified here; that covers both genuinely novel violations and ones
// theoretically fixable by a future dictionary-level fixup that just
// doesn't exist yet -- being unclassified is not itself evidence that
// rasterization is needed.
func ResidualCategory(c Check) string {
	switch c {
	case Checks.Font.SimpleNotEmbedded, Checks.Font.CIDNotEmbedded,
		Checks.Font.SubsetGlyphCoverage, Checks.Font.InvalidProgram,
		Checks.Font.CMapNotEmbedded:
		// fontSubstitutionFixer (fixups_font_subst.go) clears most of these
		// by substituting a bundled face, but only when a code/CID->Unicode
		// mapping is recoverable (Identity-H/V plus /ToUnicode for composite
		// fonts); a CID-keyed font with neither -- typically a CJK font with
		// no /ToUnicode -- has no tractable fix: there's nothing left in the
		// file to tell it what glyph any given CID was supposed to be, so a
		// remaining instance still means re-embedding the original (which
		// gopdfrab cannot do without it) or rasterizing the affected text.
		return "font: requires re-embedding/re-subsetting the original font, or rasterizing the affected text"

	case Checks.Structure.StringTooLong:
		// The q/Q-nesting-depth flavour of StringTooLong is a structural
		// defect contentLimitsFixer (fixups_content.go) deliberately leaves
		// open: rebalancing the nesting can't be done as a scalar clamp.
		// The whole-page rasterization backstop (Convert with
		// WithRasterFallback) is the only in-process remediation.
		return "content-stream: requires re-tokenizing the content stream, or rasterizing the affected page"
	}
	return ""
}
