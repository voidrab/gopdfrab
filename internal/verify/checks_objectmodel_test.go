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
	pages.Entries["_ref"] = pdf.PDFRef{ObjNum: 2}
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

func TestValidateAgainstSchema_DisallowedValue(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries["NonFullScreenPageMode"] = pdf.PDFName{Value: "Bogus"}
	ctx := &ValidationContext{}
	validateAgainstSchema(vp, "ViewerPreferences", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for a NonFullScreenPageMode value outside its enum")
	}
}

func TestValidateAgainstSchema_AllowedValueNotFlagged(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries["NonFullScreenPageMode"] = pdf.PDFName{Value: "UseOutlines"}
	ctx := &ValidationContext{}
	validateAgainstSchema(vp, "ViewerPreferences", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("a value within the enum must not be flagged, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_DisallowedValueSkipsNonScalar(t *testing.T) {
	// PossibleValues enforcement only covers name/integer scalars; an array
	// value must never be flagged even though it can't match a string enum.
	vp := pdf.NewPDFDict()
	vp.Entries["NonFullScreenPageMode"] = pdf.PDFArray{}
	ctx := &ValidationContext{}
	validateAgainstSchema(vp, "ViewerPreferences", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("a non-scalar value must not be checked against PossibleValues, got %v", ctx.errs)
	}
}

func TestScalarEnumString(t *testing.T) {
	cases := []struct {
		name    string
		val     pdf.PDFValue
		wantStr string
		wantOK  bool
	}{
		{"name", pdf.PDFName{Value: "Foo"}, "Foo", true},
		{"integer", pdf.PDFInteger(3), "3", true},
		{"real not enforced", pdf.PDFReal(1.5), "", false},
		{"string not enforced", pdf.PDFString{Value: "x"}, "", false},
		{"nil not enforced", nil, "", false},
	}
	for _, c := range cases {
		s, ok := scalarEnumString(c.val)
		if s != c.wantStr || ok != c.wantOK {
			t.Errorf("%s: scalarEnumString(%#v) = (%q, %v), want (%q, %v)", c.name, c.val, s, ok, c.wantStr, c.wantOK)
		}
	}
}

func TestValidateAgainstSchema_IndirectRequired(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	pages := pdf.NewPDFDict() // no _ref: inlined directly rather than referenced
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	catalog.Entries["Pages"] = pages

	ctx := &ValidationContext{}
	validateAgainstSchema(catalog, "Catalog", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.IndirectRequired) {
		t.Error("expected IndirectRequired for a Catalog.Pages inlined without _ref")
	}
}

func TestValidateAgainstSchema_IndirectRequiredSatisfied(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	pages.Entries["_ref"] = pdf.PDFRef{ObjNum: 2}
	catalog.Entries["Pages"] = pages

	ctx := &ValidationContext{}
	validateAgainstSchema(catalog, "Catalog", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.IndirectRequired) {
		t.Errorf("a Pages value carrying _ref must not be flagged, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_KeyIntroducedAfterPDF14(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries["PrintScaling"] = pdf.PDFName{Value: "AppDefault"}
	ctx := &ValidationContext{}
	validateAgainstSchema(vp, "ViewerPreferences", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14) {
		t.Error("expected KeyIntroducedAfterPDF14 for ViewerPreferences.PrintScaling (introduced in PDF 1.6)")
	}
}

func TestValidateAgainstSchema_CustomKeyNotPost14(t *testing.T) {
	vp := pdf.NewPDFDict()
	vp.Entries["ACustomVendorKey"] = pdf.PDFInteger(1)
	ctx := &ValidationContext{}
	validateAgainstSchema(vp, "ViewerPreferences", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14) {
		t.Errorf("a custom key absent from both the 1.4 and post-1.4 schema must not be flagged, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_WildcardTypeSkipsKeyIntroducedAfterPDF14(t *testing.T) {
	// RoleMap has a "*" wildcard entry, so it accepts arbitrary keys; the
	// post-1.4 check must not run against it at all.
	rm := pdf.NewPDFDict()
	rm.Entries["AnyRoleName"] = pdf.PDFName{Value: "Note"}
	ctx := &ValidationContext{}
	validateAgainstSchema(rm, "RoleMap", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14) {
		t.Errorf("a wildcard type must never trigger KeyIntroducedAfterPDF14, got %v", ctx.errs)
	}
}

func TestIsIndirect(t *testing.T) {
	ref := pdf.NewPDFDict()
	ref.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}
	if !isIndirect(ref) {
		t.Error("a dict carrying _ref must be indirect")
	}
	if isIndirect(pdf.NewPDFDict()) {
		t.Error("a dict without _ref must not be indirect")
	}
	if !isIndirect(pdf.PDFArray{}) {
		t.Error("a non-dict value must be treated as satisfied (no _ref marker exists for arrays)")
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
