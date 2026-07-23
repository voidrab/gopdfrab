package pdf

import "encoding/json"

// This file defines the stable JSON encoding of the public result types. The
// shapes below are part of the API: field names and nesting will not change
// without a major version. The encoding is output-only -- there is no
// UnmarshalJSON, since a Check's identity is its registry entry, not free text.

// checkJSON is the wire shape of Check. The internal registration id is
// deliberately omitted; a check's public identity is its (clause, subclause).
type checkJSON struct {
	Name        string `json:"name"`
	Clause      string `json:"clause"`
	Subclause   int    `json:"subclause"`
	Description string `json:"description"`
}

// MarshalJSON encodes a Check as {name, clause, subclause, description}.
func (c Check) MarshalJSON() ([]byte, error) {
	return json.Marshal(checkJSON{
		Name:        c.name,
		Clause:      c.clause,
		Subclause:   c.subclause,
		Description: c.description,
	})
}

type objectRefJSON struct {
	ObjNum int `json:"objNum"`
	GenNum int `json:"genNum"`
}

type objModelJSON struct {
	TypeName string `json:"typeName"`
	Key      string `json:"key,omitempty"`
	Entry    string `json:"entry,omitempty"`
}

// pdfErrorJSON is the wire shape of PDFError. object and objModel are omitted
// when absent; messages and text are always present.
type pdfErrorJSON struct {
	Check         Check          `json:"check"`
	Page          int            `json:"page"`
	DocumentLevel bool           `json:"documentLevel"`
	Object        *objectRefJSON `json:"object,omitempty"`
	ObjModel      *objModelJSON  `json:"objModel,omitempty"`
	Messages      []string       `json:"messages"`
	Text          string         `json:"text"`
}

// MarshalJSON encodes a PDFError with its check, location, messages, and
// rendered text. Without it every field is unexported and json.Marshal yields {}.
func (e PDFError) MarshalJSON() ([]byte, error) {
	out := pdfErrorJSON{
		Check:         e.check,
		Page:          e.page,
		DocumentLevel: e.IsDocumentLevel(),
		Messages:      e.Messages(),
		Text:          e.String(),
	}
	if ref, ok := e.ObjectRef(); ok {
		out.Object = &objectRefJSON{ObjNum: ref.ObjNum, GenNum: ref.GenNum}
	}
	if d, ok := e.ObjModelDetail(); ok {
		out.ObjModel = &objModelJSON{TypeName: d.TypeName, Key: d.Key, Entry: d.Entry}
	}
	return json.Marshal(out)
}

type resultJSON struct {
	Type       LevelType  `json:"type"`
	Valid      bool       `json:"valid"`
	IssueCount int        `json:"issueCount"`
	Issues     []PDFError `json:"issues"`
}

// MarshalJSON encodes a Result as {type, valid, issueCount, issues}. issues is
// always an array (never null), so a valid document serializes to [].
func (r Result) MarshalJSON() ([]byte, error) {
	issues := r.Issues
	if issues == nil {
		issues = []PDFError{}
	}
	return json.Marshal(resultJSON{
		Type:       r.Type,
		Valid:      r.Valid,
		IssueCount: len(r.Issues),
		Issues:     issues,
	})
}
