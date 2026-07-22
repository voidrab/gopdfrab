package pdf

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCheckMarshalJSON(t *testing.T) {
	c := Checks.Structure.TrailerEncrypt
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != c.Name() || got["clause"] != c.Clause() {
		t.Errorf("marshalled check = %s, want name/clause %q/%q", b, c.Name(), c.Clause())
	}
	if _, ok := got["description"]; !ok {
		t.Error("description missing")
	}
	if _, ok := got["id"]; ok {
		t.Error("internal id must not leak into JSON")
	}
}

func TestPDFErrorMarshalJSON(t *testing.T) {
	ref := PDFRef{ObjNum: 4, GenNum: 0}
	e := NewError(Checks.Structure.TrailerEncrypt, []error{errString("bad")}, 3, &ref).
		WithObjModelDetail(ObjModelDetail{TypeName: "Catalog", Key: "Version"})

	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) == "{}" {
		t.Fatal("PDFError still marshals to {} -- the bug item 17 fixes")
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["page"].(float64) != 3 || got["documentLevel"].(bool) {
		t.Errorf("page/documentLevel wrong: %s", b)
	}
	obj, ok := got["object"].(map[string]any)
	if !ok || obj["objNum"].(float64) != 4 {
		t.Errorf("object ref missing/wrong: %s", b)
	}
	om, ok := got["objModel"].(map[string]any)
	if !ok || om["typeName"] != "Catalog" || om["key"] != "Version" {
		t.Errorf("objModel missing/wrong: %s", b)
	}
	if msgs, ok := got["messages"].([]any); !ok || len(msgs) != 1 || msgs[0] != "bad" {
		t.Errorf("messages missing/wrong: %s", b)
	}
	if _, ok := got["text"].(string); !ok {
		t.Errorf("rendered text missing: %s", b)
	}
}

func TestPDFErrorMarshalJSONDocumentLevel(t *testing.T) {
	// A document-level error with no object ref omits "object" and "objModel".
	e := NewError(Checks.Structure.TrailerEncrypt, []error{errString("x")}, 0, nil)
	var got map[string]any
	b, _ := json.Marshal(e)
	json.Unmarshal(b, &got)
	if !got["documentLevel"].(bool) {
		t.Error("documentLevel should be true")
	}
	if _, ok := got["object"]; ok {
		t.Error("object must be omitted when absent")
	}
	if _, ok := got["objModel"]; ok {
		t.Error("objModel must be omitted when absent")
	}
}

func TestResultMarshalJSON(t *testing.T) {
	// Valid result: issues serializes as [], not null.
	valid, _ := json.Marshal(Result{Type: A_1B, Valid: true})
	var vg map[string]any
	json.Unmarshal(valid, &vg)
	if vg["type"] != "A-1b" || !vg["valid"].(bool) {
		t.Errorf("valid result wrong: %s", valid)
	}
	if vg["issueCount"].(float64) != 0 {
		t.Errorf("issueCount wrong: %s", valid)
	}
	if iss, ok := vg["issues"].([]any); !ok || len(iss) != 0 {
		t.Errorf("issues must be an empty array, got: %s", valid)
	}

	// Invalid result: each issue is a populated object, not {}.
	r := Result{Type: A_1B, Valid: false, Issues: []PDFError{
		NewError(Checks.Structure.TrailerEncrypt, []error{errString("e")}, 0, nil),
	}}
	b, _ := json.Marshal(r)
	var rg map[string]any
	json.Unmarshal(b, &rg)
	if rg["issueCount"].(float64) != 1 {
		t.Errorf("issueCount wrong: %s", b)
	}
	iss := rg["issues"].([]any)
	if len(iss) != 1 {
		t.Fatalf("want 1 issue: %s", b)
	}
	if m := iss[0].(map[string]any); m["check"] == nil || m["text"] == nil {
		t.Errorf("issue not populated: %s", b)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
