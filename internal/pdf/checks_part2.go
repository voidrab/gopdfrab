package pdf

// clauseRef is a clause and subclause of one ISO 19005 part, with the rule's
// description where it differs from the check's Part 1 one.
type clauseRef struct {
	clause      string
	subclause   int
	description string
}

// part2Clauses numbers, by check name, the checks that are rules of ISO 19005-2
// too. The subclause is the number of the rule's test, so several checks that
// together enforce one test share its number; a check with no test of its own,
// or one that enforces several, has subclause 0. A check missing here is no
// rule of the Part 2 levels.
var part2Clauses = map[string]clauseRef{
	// 6.1.2 File header.
	"FileHeaderSignature":     {"6.1.2", 1, "The file header must be %PDF-1.n, n from 0 to 7, at the start of the file"},
	"FileHeaderComment":       {"6.1.2", 2, ""},
	"FileHeaderCommentLength": {"6.1.2", 2, ""},
	"FileHeaderCommentBytes":  {"6.1.2", 2, ""},
	// 6.1.3 File trailer.
	"TrailerID":      {"6.1.3", 1, "The trailer dictionary must contain an ID entry"},
	"TrailerEncrypt": {"6.1.3", 2, ""},
	"TrailerEOF":     {"6.1.3", 3, ""},
	// 6.1.4 Cross reference table.
	"XRefKeyword":          {"6.1.4", 2, ""},
	"XRefSubsectionHeader": {"6.1.4", 2, ""},
	// 6.1.6 String objects.
	"GraphResolutionFailure": {"6.1.6", 0, ""},
	"HexStringOddLength":     {"6.1.6", 1, ""},
	"HexStringInvalidChar":   {"6.1.6", 2, ""},
	// 6.1.7.1 Stream objects.
	"StreamUndecodable":       {"6.1.7.1", 0, ""},
	"StreamLengthMismatch":    {"6.1.7.1", 1, ""},
	"StreamLengthIncludesEOL": {"6.1.7.1", 1, ""},
	"StreamKeywordEOL":        {"6.1.7.1", 2, ""},
	"EndstreamEOL":            {"6.1.7.1", 2, ""},
	"StreamFileSpec":          {"6.1.7.1", 3, ""},
	"StreamFileFilter":        {"6.1.7.1", 3, ""},
	"StreamFileDecodeParams":  {"6.1.7.1", 3, ""},
	// 6.1.7.2 Filters.
	"StreamLZWFilter":        {"6.1.7.2", 1, ""},
	"StreamFilterNotAllowed": {"6.1.7.2", 1, ""},
	// 6.1.8 Name objects.
	"NameNotUTF8": {"6.1.8", 1, ""},
	// 6.1.9 Indirect objects.
	"ObjectFraming": {"6.1.9", 1, ""},
	// 6.1.10 Inline image dictionaries.
	"InlineImageLZWFilter":        {"6.1.10", 1, ""},
	"InlineImageFilterNotAllowed": {"6.1.10", 1, ""},
	// 6.1.12 Permissions.
	"PermissionsKeys":          {"6.1.12", 1, ""},
	"SignatureReferenceDigest": {"6.1.12", 2, ""},
	// 6.1.13 Implementation limits.
	"IntegerOutOfRange":       {"6.1.13", 1, ""},
	"RealOutOfRange":          {"6.1.13", 2, "Real values must have an absolute value not exceeding 3.403 x 10^38"},
	"StringTooLong":           {"6.1.13", 3, "String objects must not exceed 32,767 bytes"},
	"NameTooLong":             {"6.1.13", 4, ""},
	"RealTooCloseToZero":      {"6.1.13", 5, ""},
	"IndirectObjectsExceeded": {"6.1.13", 7, ""},
	"GraphicsStateNesting":    {"6.1.13", 8, ""},
	"DeviceNColorants":        {"6.1.13", 9, "DeviceN colour spaces must not exceed 32 colorants"},
	"CMapCIDOutOfRange":       {"6.1.13", 10, ""},
	"PageBoundarySize":        {"6.1.13", 11, ""},
	// 6.2.2 Content streams.
	"UndefinedOperator":  {"6.2.2", 1, "Content streams must not use operators not defined in ISO 32000-1, even between BX and EX"},
	"RenderingIntent":    {"6.2.2", 0, ""},
	"InheritedResources": {"6.2.2", 2, ""},
	// 6.2.3 Output intent.
	"OutputIntentNotArray":             {"6.2.3", 0, ""},
	"OutputIntentNotDict":              {"6.2.3", 0, ""},
	"OutputIntentInvalidS":             {"6.2.3", 0, ""},
	"OutputIntentMissingIdentifier":    {"6.2.3", 0, ""},
	"OutputIntentUnresolvedProfile":    {"6.2.3", 0, ""},
	"DeviceColourWithoutPDFAIntent":    {"6.2.3", 0, ""},
	"OutputIntentInvalidProfile":       {"6.2.3", 1, ""},
	"OutputIntentMissingN":             {"6.2.3", 1, ""},
	"OutputIntentInvalidN":             {"6.2.3", 1, ""},
	"OutputIntentICCVersion":           {"6.2.3", 1, "The OutputIntent ICC profile must be an output or monitor profile of version 4.x or earlier"},
	"OutputIntentMultipleProfiles":     {"6.2.3", 2, ""},
	"OutputIntentDestOutputProfileRef": {"6.2.3", 3, ""},
	// 6.2.4.2 ICCBased colour spaces.
	"ICCBasedComponentsMismatch": {"6.2.4.2", 1, ""},
	"ICCBasedProfileInvalid":     {"6.2.4.2", 1, "An ICCBased colour space's embedded ICC profile must be of version 4.x or earlier, with a device class and colour space ISO 19005-2 allows"},
	"ICCBasedCMYKOverprint":      {"6.2.4.2", 2, ""},
	// 6.2.4.3 Uncalibrated device colour spaces.
	"DeviceColourSpaceUsage":    {"6.2.4.3", 0, ""},
	"DeviceColourContentStream": {"6.2.4.3", 0, ""},
	// 6.2.4.4 Separation and DeviceN colour spaces.
	"SeparationAlternateColour": {"6.2.4.4", 0, ""},
	"DeviceNColorantsMissing":   {"6.2.4.4", 1, ""},
	"SeparationInconsistent":    {"6.2.4.4", 2, ""},
	// 6.2.4.5 Indexed and Pattern colour spaces.
	"DeviceColourBase": {"6.2.4.5", 0, ""},
	// 6.2.5 Extended graphics state.
	"TransferFunction":         {"6.2.5", 1, ""},
	"DefaultTransferFunction":  {"6.2.5", 2, ""},
	"HalftoneExtGState":        {"6.2.5", 3, ""},
	"HalftoneType":             {"6.2.5", 4, ""},
	"HalftoneName":             {"6.2.5", 5, ""},
	"HalftoneTransferFunction": {"6.2.5", 6, ""},
	"ExtGStateRenderingIntent": {"6.2.5", 0, ""},
	// 6.2.6 Rendering intents.
	"ImageRenderingIntent": {"6.2.6", 1, ""},
	// 6.2.8 Image dictionaries.
	"ImageAlternates":           {"6.2.8", 1, ""},
	"ImageOPI":                  {"6.2.8", 2, ""},
	"ImageInterpolate":          {"6.2.8", 3, ""},
	"ImageBitsPerComponent":     {"6.2.8", 4, "An image XObject BitsPerComponent value must be 1, 2, 4, 8 or 16"},
	"ImageMaskBitsPerComponent": {"6.2.8", 5, ""},
	// 6.2.8.3 JPEG2000.
	"JPXColourChannels": {"6.2.8.3", 1, ""},
	"JPXColourSpecs":    {"6.2.8.3", 2, ""},
	"JPXColourMethod":   {"6.2.8.3", 3, ""},
	"JPXCIEJab":         {"6.2.8.3", 4, ""},
	"JPXBitDepth":       {"6.2.8.3", 5, ""},
	// 6.2.9 XObjects.
	"FormOPI":           {"6.2.9", 1, ""},
	"FormSubtype2PS":    {"6.2.9", 1, ""},
	"FormPSEntry":       {"6.2.9", 1, ""},
	"ReferenceXObject":  {"6.2.9", 2, "Reference XObjects (/Ref) are not permitted"},
	"PostScriptXObject": {"6.2.9", 3, "PostScript XObjects (/Subtype /PS) are not permitted"},
	// 6.2.10 Transparency.
	"BlendMode":                        {"6.2.10", 1, "ExtGState blend mode (BM) must be one ISO 32000-1 defines"},
	"TransparencyWithoutBlendingSpace": {"6.2.10", 2, ""},
	"DeviceBlendingColourSpace":        {"6.2.10", 0, ""},
}
