package pdfrab

// ResidualCategory classifies a Check still present in a ConvertResult's
// Residual() as a hint toward what remediation it would need beyond this
// package's current fixups -- in particular, whether rasterizing the
// affected content is the only way to resolve it, since gopdfrab has no
// content-stream rewriter or font re-subsetter (see the converter plan's
// difficulty classification). Returns "" for anything not specifically
// classified here; that covers both genuinely novel violations and ones
// theoretically fixable by a future dictionary-level fixup that just
// doesn't exist yet (e.g. CIDToGIDMapMissing, TrueTypeEncoding) -- being
// unclassified is not itself evidence that rasterization is needed.
func ResidualCategory(c Check) string {
	switch c {
	case Checks.Font.SimpleNotEmbedded, Checks.Font.CIDNotEmbedded,
		Checks.Font.SubsetGlyphCoverage, Checks.Font.InvalidProgram,
		Checks.Font.CMapNotEmbedded:
		// The glyph/program/width data this needs simply isn't in the file
		// (or is corrupt); fixing it means re-subsetting/re-embedding the
		// original font, which gopdfrab cannot do, or rasterizing the
		// affected text.
		return "font: requires re-embedding/re-subsetting the original font, or rasterizing the affected text"

	case Checks.Structure.InlineImageLZWFilter, Checks.Structure.StringTooLong,
		Checks.Structure.ArrayTooLarge, Checks.Structure.DictTooLarge,
		Checks.Structure.NameTooLong, Checks.Structure.CMapCIDOutOfRange:
		// inlineImageLZWFixer (fixups_inline_image.go) handles the common
		// case but bails out when a /DP or /DecodeParms predictor is
		// present (no inline-image-aware predictor-undo exists), and the
		// q/Q-nesting-depth flavour of StringTooLong is a structural defect
		// contentLimitsFixer (fixups_content.go) deliberately leaves open;
		// the rest (Array/Dict/Name/CMapCID) live in content or
		// dictionaries this package doesn't yet rewrite.
		return "content-stream: requires re-tokenizing/re-encoding the content stream"

	case Checks.Transparency.TransparencyGroup, Checks.Transparency.ImageWithSoftMask:
		// Removing the offending key (/Group, /SMask) is trivial, but doing
		// so changes the document's rendered appearance; a faithful fix
		// needs flattening the transparency, or rasterizing the result.
		return "transparency: requires flattening or rasterizing the affected content"
	}
	return ""
}
