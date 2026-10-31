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
}
