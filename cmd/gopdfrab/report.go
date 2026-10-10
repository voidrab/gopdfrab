package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab"
)

// The text output is line-oriented so other programs can parse it:
//
//	profile: pdfa1b
//
//	FAIL  issues=107 distinct=8  /path/to/file.pdf
//	  6.2.3.3/2  page=1    obj=4.0  count=98  "device colour (gray) used ..."
//	  6.7.2/1    page=doc  obj=-    count=1   "document catalog lacks a Metadata entry"
//	PASS  /path/to/other.pdf
//
//	summary: pass=1 fail=1 error=0 files=2
//
// A file line starts with its status and ends with the path, which is the rest
// of the line. Indented lines belong to the file above. An issue line's fields
// come in a fixed order, padded into columns, and end with the messages as one
// Go-quoted string.

// foldedIssue is one distinct issue and how many times it was reported.
type foldedIssue struct {
	issue gopdfrab.PDFError
	count int
}

// issueKey identifies an issue for folding: two issues fold only when nothing
// a line prints differs between them.
type issueKey struct {
	clause    string
	subclause int
	page      int
	ref       gopdfrab.PDFRef
	hasRef    bool
	messages  string
}

func keyOf(e gopdfrab.PDFError) issueKey {
	c := e.Check()
	ref, hasRef := e.ObjectRef()
	return issueKey{
		clause:    c.Clause(),
		subclause: c.Subclause(),
		page:      e.Page(),
		ref:       ref,
		hasRef:    hasRef,
		// NUL cannot be confused with text inside a message, so ["a; b"] and
		// ["a", "b"] stay distinct.
		messages: strings.Join(e.Messages(), "\x00"),
	}
}

// foldIssues collapses repeats of the same issue into one entry with a count,
// in order of first occurrence.
func foldIssues(issues []gopdfrab.PDFError) []foldedIssue {
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
	return out
}

// issueFields renders an issue line's columns: clause, page, object, count and
// the quoted messages.
func issueFields(f foldedIssue) [5]string {
	e := f.issue
	c := e.Check()

	clause := c.Clause()
	if clause == "" {
		clause = "-"
	}
	if c.Subclause() > 0 {
		clause += "/" + strconv.Itoa(c.Subclause())
	}

	page := "page=doc"
	if !e.IsDocumentLevel() {
		page = "page=" + strconv.Itoa(e.Page())
	}

	obj := "obj=-"
	if ref, ok := e.ObjectRef(); ok {
		obj = fmt.Sprintf("obj=%d.%d", ref.ObjNum, ref.GenNum)
	}

	return [5]string{
		clause,
		page,
		obj,
		"count=" + strconv.Itoa(f.count),
		strconv.Quote(strings.Join(e.Messages(), "; ")),
	}
}

// writeIssues prints one indented line per folded issue, with the columns
// padded to line up.
func writeIssues(w io.Writer, issues []foldedIssue) {
	rows := make([][5]string, len(issues))
	var width [4]int
	for i, f := range issues {
		rows[i] = issueFields(f)
		for col := range width {
			width[col] = max(width[col], len(rows[i][col]))
		}
	}
	for _, row := range rows {
		var b strings.Builder
		b.WriteString("  ")
		for col := range width {
			fmt.Fprintf(&b, "%-*s  ", width[col], row[col])
		}
		b.WriteString(row[4])
		fmt.Fprintln(w, b.String())
	}
}
