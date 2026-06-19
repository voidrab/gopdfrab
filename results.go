package pdfrab

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Count returns the number of issues found.
func (r Result) Count() int {
	return len(r.Issues)
}

// Checks returns the distinct Checks violated in r.Issues, sorted by clause
// in numeric (dotted-segment) order, e.g. "6.2.9" before "6.2.10", then by
// subclause.
func (r Result) Checks() []Check {
	seen := make(map[Check]bool)
	var out []Check
	for _, issue := range r.Issues {
		if !seen[issue.check] {
			seen[issue.check] = true
			out = append(out, issue.check)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].clause != out[j].clause {
			return clauseLess(out[i].clause, out[j].clause)
		}
		return out[i].subclause < out[j].subclause
	})
	return out
}

// clauseLess compares two dotted clause numbers ("6.2.9", "6.2.10") segment by
// segment so they sort numerically rather than lexicographically.
func clauseLess(a, b string) bool {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		if aErr != nil || bErr != nil {
			if as[i] != bs[i] {
				return as[i] < bs[i]
			}
			continue
		}
		if an != bn {
			return an < bn
		}
	}
	return len(as) < len(bs)
}

// IssuesByCheck groups r.Issues by their violated Check.
func (r Result) IssuesByCheck() map[Check][]PDFError {
	out := make(map[Check][]PDFError)
	for _, issue := range r.Issues {
		out[issue.check] = append(out[issue.check], issue)
	}
	return out
}

// IssuesOnPage returns the issues found on the given 1-based page number. Pass
// 0 to get document-level issues (see PDFError.IsDocumentLevel).
func (r Result) IssuesOnPage(page int) []PDFError {
	var out []PDFError
	for _, issue := range r.Issues {
		if issue.page == page {
			out = append(out, issue)
		}
	}
	return out
}

// IssuesForCheck returns the issues that correspond to the given registered
// Check.
func (r Result) IssuesForCheck(c Check) []PDFError {
	var out []PDFError
	for _, issue := range r.Issues {
		if issue.check == c {
			out = append(out, issue)
		}
	}
	return out
}

// Summary returns a human-readable multi-line report: a validity line
// followed by one line per violated Check with its issue count, in clause
// order.
func (r Result) Summary() string {
	var b strings.Builder
	if r.Valid {
		fmt.Fprintf(&b, "%s: valid (no issues)", r.Type)
		return b.String()
	}

	fmt.Fprintf(&b, "%s: invalid (%d issue", r.Type, len(r.Issues))
	if len(r.Issues) != 1 {
		b.WriteString("s")
	}
	b.WriteString(")")

	byCheck := r.IssuesByCheck()
	for _, c := range r.Checks() {
		fmt.Fprintf(&b, "\n  %s/%d %s: %d", c.clause, c.subclause, c.name, len(byCheck[c]))
	}
	return b.String()
}
