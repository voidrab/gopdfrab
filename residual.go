package pdfrab

// ResidualCategory classifies a Check still present in a ConvertResult's
// Residual() as a hint toward what remediation it would need beyond this
// package's current fixups -- in particular, whether rasterizing the
// affected content is the only way to resolve it, since gopdfrab has no
// content-stream rewriter for these cases (see the converter plan's
// difficulty classification). Returns "" for anything not specifically
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

	case Checks.Structure.InlineImageLZWFilter, Checks.Structure.StringTooLong:
		// inlineImageLZWFixer (fixups_inline_image.go) handles the common
		// case but bails out when a /DP or /DecodeParms predictor is
		// present (no inline-image-aware predictor-undo exists), and the
		// q/Q-nesting-depth flavour of StringTooLong is a structural defect
		// contentLimitsFixer (fixups_content.go) deliberately leaves open --
		// both live in content this package doesn't rewrite for these cases.
		return "content-stream: requires re-tokenizing/re-encoding the content stream"

	case Checks.Structure.DeviceNColorants:
		// pruning colorant names to fit the 8-colorant maximum would leave
		// the DeviceN array shorter than the tint-transform function's
		// declared input arity -- fixing it means rewriting that function,
		// which this package has no machinery for.
		return "content-stream: requires rewriting the colour-space tint-transform function"
	}
	return ""
}
