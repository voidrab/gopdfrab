package pdf

// clauseRef is a clause and subclause of one ISO 19005 part.
type clauseRef struct {
	clause    string
	subclause int
}

// part2Clauses numbers, by check ID, the checks that are rules of ISO 19005-2
// too. The subclause is veraPDF's test number for the rule, so a corpus file
// named after a Part 2 rule maps to exactly one check. A check missing here is
// no rule of the Part 2 levels.
var part2Clauses = map[int]clauseRef{}
