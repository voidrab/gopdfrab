package main

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/verify"
)

// The text output is line-oriented so other programs can parse it:
//
//	profile: pdfa1b
//
//	FAIL  issues=104 distinct=2  /path/to/file.pdf
//	  6.2.3.3/2  count=100  pages=1-3  objs=1  "device colour (gray) used ..."
//	  6.7.2/1    count=4    pages=doc  objs=0  "document catalog lacks a Metadata entry"
//	PASS  /path/to/other.pdf
//
//	summary: pass=1 fail=1 error=0 files=2
//
// A file line starts with its status and ends with the path, which is the rest
// of the line. Indented lines belong to the file above. An issue line's fields
// come in a fixed order, padded into columns, and end with the messages as one
// Go-quoted string. Issue lines are sorted by clause, then message.
//
// By default an issue line groups every report of one check with one message,
// whatever page or object it was found on. With --detail the lines are per
// location instead: clause, page=, obj= and count= of exact repeats.

// issueGroup is every report of one check with one message.
type issueGroup struct {
	first verify.PDFError
	count int
	pages []int // distinct pages, ascending; 0 is document-level
	objs  int   // distinct objects
}

// foldedIssue is one distinct issue at one location and how many times it was
// reported there.
type foldedIssue struct {
	issue verify.PDFError
	count int
}

// groupKey identifies an issue for grouping: its check and its messages.
type groupKey struct {
	clause    string
	subclause int
	messages  string
}

// issueKey identifies an issue for folding: two issues fold only when nothing
// a --detail line prints differs between them.
type issueKey struct {
	groupKey
	page   int
	ref    verify.PDFRef
	hasRef bool
}

func groupKeyOf(e verify.PDFError) groupKey {
	c := e.Check()
	return groupKey{
		clause:    c.Clause(),
		subclause: c.Subclause(),
		// NUL cannot be confused with text inside a message, so ["a; b"] and
		// ["a", "b"] stay distinct.
		messages: strings.Join(e.Messages(), "\x00"),
	}
}

func keyOf(e verify.PDFError) issueKey {
	ref, hasRef := e.ObjectRef()
	return issueKey{groupKey: groupKeyOf(e), page: e.Page(), ref: ref, hasRef: hasRef}
}

// groupIssues collapses every report of the same check and message into one
// group, sorted by clause and message.
func groupIssues(issues []verify.PDFError) []issueGroup {
	type acc struct {
		group issueGroup
		pages map[int]struct{}
		objs  map[verify.PDFRef]struct{}
	}
	var accs []*acc
	index := make(map[groupKey]*acc)
	for _, e := range issues {
		k := groupKeyOf(e)
		a, ok := index[k]
		if !ok {
			a = &acc{
				group: issueGroup{first: e},
				pages: make(map[int]struct{}),
				objs:  make(map[verify.PDFRef]struct{}),
			}
			index[k] = a
			accs = append(accs, a)
		}
		a.group.count++
		a.pages[e.Page()] = struct{}{}
		if ref, ok := e.ObjectRef(); ok {
			a.objs[ref] = struct{}{}
		}
	}

	out := make([]issueGroup, len(accs))
	for i, a := range accs {
		g := a.group
		for p := range a.pages {
			g.pages = append(g.pages, p)
		}
		slices.Sort(g.pages)
		g.objs = len(a.objs)
		out[i] = g
	}
	slices.SortFunc(out, func(a, b issueGroup) int {
		return compareGroupKeys(groupKeyOf(a.first), groupKeyOf(b.first))
	})
	return out
}

// foldIssues collapses exact repeats of an issue into one entry with a count,
// sorted by clause, message, page and object.
func foldIssues(issues []verify.PDFError) []foldedIssue {
	var out []foldedIssue
	index := make(map[issueKey]int, len(issues))
	for _, e := range issues {
		k := keyOf(e)
		if i, ok := index[k]; ok {
			out[i].count++
			continue
		}
		index[k] = len(out)
		out = append(out, foldedIssue{issue: e, count: 1})
	}
	slices.SortStableFunc(out, func(a, b foldedIssue) int {
		ka, kb := keyOf(a.issue), keyOf(b.issue)
		if c := compareGroupKeys(ka.groupKey, kb.groupKey); c != 0 {
			return c
		}
		if c := ka.page - kb.page; c != 0 {
			return c
		}
		if c := ka.ref.ObjNum - kb.ref.ObjNum; c != 0 {
			return c
		}
		return ka.ref.GenNum - kb.ref.GenNum
	})
	return out
}

func compareGroupKeys(a, b groupKey) int {
	if c := compareClause(a.clause, b.clause); c != 0 {
		return c
	}
	if c := a.subclause - b.subclause; c != 0 {
		return c
	}
	return strings.Compare(a.messages, b.messages)
}

// compareClause orders clauses by their dotted segments, numerically where a
// segment is a number, so 6.9 sorts before 6.10. A word segment such as
// "objmodel" sorts after every number.
func compareClause(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		var c int
		switch {
		case aErr == nil && bErr == nil:
			c = an - bn
		case aErr == nil:
			c = -1
		case bErr == nil:
			c = 1
		default:
			c = strings.Compare(as[i], bs[i])
		}
		if c != 0 {
			return c
		}
	}
	return len(as) - len(bs)
}

// clauseField renders a check's clause and subclause, "-" for none.
func clauseField(c verify.Check) string {
	clause := c.Clause()
	if clause == "" {
		clause = "-"
	}
	if c.Subclause() > 0 {
		clause += "/" + strconv.Itoa(c.Subclause())
	}
	return clause
}

// messageField renders an issue's messages as one Go-quoted string.
func messageField(e verify.PDFError) string {
	return strconv.Quote(strings.Join(e.Messages(), "; "))
}

// pageRanges renders ascending pages as "doc,1-4,7", page 0 being "doc".
func pageRanges(pages []int) string {
	var parts []string
	for i := 0; i < len(pages); {
		if pages[i] == 0 {
			parts = append(parts, "doc")
			i++
			continue
		}
		j := i
		for j+1 < len(pages) && pages[j+1] == pages[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.Itoa(pages[i]))
		} else {
			parts = append(parts, strconv.Itoa(pages[i])+"-"+strconv.Itoa(pages[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// writeGroups prints one indented line per issue group.
func writeGroups(w io.Writer, groups []issueGroup) {
	rows := make([][]string, len(groups))
	for i, g := range groups {
		rows[i] = []string{
			clauseField(g.first.Check()),
			"count=" + strconv.Itoa(g.count),
			"pages=" + pageRanges(g.pages),
			"objs=" + strconv.Itoa(g.objs),
			messageField(g.first),
		}
	}
	writeColumns(w, rows)
}

// writeIssues prints one indented line per folded issue, for --detail.
func writeIssues(w io.Writer, issues []foldedIssue) {
	rows := make([][]string, len(issues))
	for i, f := range issues {
		e := f.issue
		page := "page=doc"
		if !e.IsDocumentLevel() {
			page = "page=" + strconv.Itoa(e.Page())
		}
		obj := "obj=-"
		if ref, ok := e.ObjectRef(); ok {
			obj = fmt.Sprintf("obj=%d.%d", ref.ObjNum, ref.GenNum)
		}
		rows[i] = []string{
			clauseField(e.Check()),
			page,
			obj,
			"count=" + strconv.Itoa(f.count),
			messageField(e),
		}
	}
	writeColumns(w, rows)
}

// writeColumns prints rows indented by two spaces, every column but the last
// padded to line up. All rows have the same number of columns.
func writeColumns(w io.Writer, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	width := make([]int, len(rows[0])-1)
	for _, row := range rows {
		for col := range width {
			width[col] = max(width[col], len(row[col]))
		}
	}
	for _, row := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for col := range width {
			fmt.Fprintf(&b, "%-*s  ", width[col], row[col])
		}
		b.WriteString(row[len(row)-1])
		fmt.Fprintln(w, b.String())
	}
}

// writeIssueList prints a header line, then the issues grouped, or one line
// per location when detail is set. header is given the number of issue lines
// that follow.
func writeIssueList(w io.Writer, issues []verify.PDFError, detail bool, header func(distinct int) string) {
	if detail {
		folded := foldIssues(issues)
		fmt.Fprintln(w, header(len(folded)))
		writeIssues(w, folded)
		return
	}
	groups := groupIssues(issues)
	fmt.Fprintln(w, header(len(groups)))
	writeGroups(w, groups)
}
