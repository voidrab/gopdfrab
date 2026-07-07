package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestValidateAgainstSchema_MissingRequiredKey(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	// Pages omitted: Catalog.Pages is required and not inheritable.
	ctx := &ValidationContext{}
	validateAgainstSchema(catalog, "Catalog", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("expected MissingRequiredKey for a Catalog missing Pages")
	}
}

func TestValidateAgainstSchema_InheritableKeyNotFlagged(t *testing.T) {
	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	// Resources/MediaBox omitted: both are Inheritable on PageObject, so their
	// absence here is not itself a violation.
	ctx := &ValidationContext{}
	validateAgainstSchema(page, "PageObject", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("inheritable keys must not trigger MissingRequiredKey when absent")
	}
}

func TestValidateAgainstSchema_WrongValueType(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFInteger(1) // Catalog.Type must be a name
	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	catalog.Entries["Pages"] = pages

	ctx := &ValidationContext{}
	validateAgainstSchema(catalog, "Catalog", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.WrongValueType) {
		t.Error("expected WrongValueType for a Catalog.Type that is an integer, not a name")
	}
}

func TestValidateAgainstSchema_CorrectTypeNotFlagged(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	catalog.Entries["Pages"] = pages

	ctx := &ValidationContext{}
	validateAgainstSchema(catalog, "Catalog", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.WrongValueType) || hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Errorf("unexpected violation for a conformant Catalog: %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_NullValueNotFlagged(t *testing.T) {
	// A key resolving to PDF null (Go nil) -- e.g. an indirect reference to a
	// null object -- is structurally equivalent to the key being absent
	// (ISO 32000 7.3.9), so it must never trigger WrongValueType.
	info := pdf.NewPDFDict()
	info.Entries["Title"] = nil
	ctx := &ValidationContext{}
	validateAgainstSchema(info, "DocInfo", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.WrongValueType) {
		t.Error("a null value must not trigger WrongValueType")
	}
}

func TestValidateAgainstSchema_CustomKeyNotFlagged(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	catalog.Entries["Pages"] = pages
	catalog.Entries["ACustomVendorKey"] = pdf.PDFInteger(42)

	ctx := &ValidationContext{}
	validateAgainstSchema(catalog, "Catalog", ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("a key absent from the schema must not be flagged, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_LengthSkippedOnStream(t *testing.T) {
	meta := pdf.NewPDFDict()
	meta.HasStream = true
	meta.Entries["Type"] = pdf.PDFName{Value: "Metadata"}
	meta.Entries["Subtype"] = pdf.PDFName{Value: "XML"}
	// Length omitted: the writer always recomputes it from RawStream at
	// serialization time, so its in-memory absence must not be flagged.
	ctx := &ValidationContext{}
	validateAgainstSchema(meta, "Metadata", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Errorf("Length must never trigger MissingRequiredKey on a stream dict, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_UnknownTypeIgnored(t *testing.T) {
	d := pdf.NewPDFDict()
	ctx := &ValidationContext{}
	validateAgainstSchema(d, "NotAnArlingtonType", ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("an unknown Arlington type name must report nothing, got %v", ctx.errs)
	}
}

func TestMatchesValueType(t *testing.T) {
	cases := []struct {
		name    string
		val     pdf.PDFValue
		allowed []arlington.ValueType
		want    bool
	}{
		{"empty allowed list is unconstrained", pdf.PDFInteger(1), nil, true},
		{"null always matches", nil, []arlington.ValueType{arlington.Name}, true},
		{"integer matches bitmask", pdf.PDFInteger(3), []arlington.ValueType{arlington.Bitmask}, true},
		{"integer matches number", pdf.PDFInteger(3), []arlington.ValueType{arlington.Number}, true},
		{"real matches number", pdf.PDFReal(1.5), []arlington.ValueType{arlington.Number}, true},
		{"real does not match integer", pdf.PDFReal(1.5), []arlington.ValueType{arlington.Integer}, false},
		{"boolean matches boolean", pdf.PDFBoolean(true), []arlington.ValueType{arlington.Boolean}, true},
		{"name matches name", pdf.PDFName{Value: "X"}, []arlington.ValueType{arlington.Name}, true},
		{"string matches string-text", pdf.PDFString{Value: "x"}, []arlington.ValueType{arlington.StringText}, true},
		{"hex string matches string", pdf.PDFHexString{Value: "aa"}, []arlington.ValueType{arlington.String}, true},
		{"array matches rectangle", pdf.PDFArray{}, []arlington.ValueType{arlington.Rectangle}, true},
		{"dict matches dictionary", pdf.NewPDFDict(), []arlington.ValueType{arlington.Dictionary}, true},
		{"unresolved ref always matches", pdf.PDFRef{ObjNum: 1}, []arlington.ValueType{arlington.Name}, true},
		{"name does not match integer", pdf.PDFName{Value: "X"}, []arlington.ValueType{arlington.Integer}, false},
	}
	for _, c := range cases {
		if got := matchesValueType(c.val, c.allowed); got != c.want {
			t.Errorf("%s: matchesValueType(%#v, %v) = %v, want %v", c.name, c.val, c.allowed, got, c.want)
		}
	}

	streamDict := pdf.NewPDFDict()
	streamDict.HasStream = true
	if !matchesValueType(streamDict, []arlington.ValueType{arlington.Stream}) {
		t.Error("a stream dict must match Stream")
	}
	if matchesValueType(streamDict, []arlington.ValueType{arlington.Dictionary}) {
		t.Error("a stream dict must not match plain Dictionary")
	}
}

func TestArlingtonChildType(t *testing.T) {
	if got := arlingtonChildType("Catalog", "Pages"); got != "PageTreeNodeRoot" {
		t.Errorf("arlingtonChildType(Catalog, Pages) = %q, want PageTreeNodeRoot", got)
	}
	if got := arlingtonChildType("Catalog", "OpenAction"); got != "" {
		t.Errorf("arlingtonChildType(Catalog, OpenAction) = %q, want \"\" (ambiguous Link)", got)
	}
	if got := arlingtonChildType("NotAType", "Pages"); got != "" {
		t.Errorf("arlingtonChildType(unknown type) = %q, want \"\"", got)
	}
	if got := arlingtonChildType("Catalog", "NoSuchKey"); got != "" {
		t.Errorf("arlingtonChildType(Catalog, NoSuchKey) = %q, want \"\"", got)
	}
}

func TestArlingtonElementType(t *testing.T) {
	if got := arlingtonElementType("ArrayOfOutputIntents"); got != "OutputIntents" {
		t.Errorf("arlingtonElementType(ArrayOfOutputIntents) = %q, want OutputIntents", got)
	}
	if got := arlingtonElementType("NotAType"); got != "" {
		t.Errorf("arlingtonElementType(unknown type) = %q, want \"\"", got)
	}
	// ArrayOf_2Numbers has fixed-position keys ("0", "1"), not a wildcard.
	if got := arlingtonElementType("ArrayOf_2Numbers"); got != "" {
		t.Errorf("arlingtonElementType(ArrayOf_2Numbers) = %q, want \"\" (no wildcard)", got)
	}
}
