package pdf

// clauseRef is a clause and subclause of one ISO 19005 part, with the rule's
// description where it differs from the check's Part 1 one.
type clauseRef struct {
	clause      string
	subclause   int
	description string
}

// part2Clauses numbers, by check name, the checks that are rules of ISO 19005-2
// too. The subclause is the test number veraPDF gives the rule, so several
// checks that together enforce one veraPDF rule share its number; a check with
// no veraPDF counterpart has subclause 0. A check missing here is no rule of
// the Part 2 levels.
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
	"StreamLZWFilter": {"6.1.7.2", 1, ""},
	// 6.1.9 Indirect objects.
	"ObjectFraming": {"6.1.9", 1, ""},
	// 6.1.10 Inline image dictionaries.
	"InlineImageLZWFilter": {"6.1.10", 1, ""},
	// 6.1.13 Implementation limits.
	"IntegerOutOfRange":       {"6.1.13", 1, ""},
	"RealOutOfRange":          {"6.1.13", 2, "Real values must have an absolute value not exceeding 3.403 x 10^38"},
	"StringTooLong":           {"6.1.13", 3, "String objects must not exceed 32,767 bytes"},
	"NameTooLong":             {"6.1.13", 4, ""},
	"IndirectObjectsExceeded": {"6.1.13", 7, ""},
	"GraphicsStateNesting":    {"6.1.13", 8, ""},
	"DeviceNColorants":        {"6.1.13", 9, "DeviceN colour spaces must not exceed 32 colorants"},
	"CMapCIDOutOfRange":       {"6.1.13", 10, ""},
}
