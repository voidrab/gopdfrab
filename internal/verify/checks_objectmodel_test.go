package verify

import (
	"strings"
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
	parent := pdf.NewPDFDict()
	parent.Entries["_ref"] = pdf.PDFRef{ObjNum: 9}
	page.Entries["Parent"] = parent
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
	pagesDict := pdf.NewPDFDict()
	if got := arlingtonChildType("Catalog", "Pages", pagesDict); got != "PageTreeNodeRoot" {
		t.Errorf("arlingtonChildType(Catalog, Pages) = %q, want PageTreeNodeRoot", got)
	}

	// OpenAction's dict alternative resolves via its S discriminator, including the
	// JavaScript -> ActionECMAScript case (not a naive "Action"+S concatenation).
	goTo := pdf.NewPDFDict()
	goTo.Entries["S"] = pdf.PDFName{Value: "GoTo"}
	if got := arlingtonChildType("Catalog", "OpenAction", goTo); got != "ActionGoTo" {
		t.Errorf("arlingtonChildType(Catalog, OpenAction, S=GoTo) = %q, want ActionGoTo", got)
	}
	js := pdf.NewPDFDict()
	js.Entries["S"] = pdf.PDFName{Value: "JavaScript"}
	if got := arlingtonChildType("Catalog", "OpenAction", js); got != "ActionECMAScript" {
		t.Errorf("arlingtonChildType(Catalog, OpenAction, S=JavaScript) = %q, want ActionECMAScript", got)
	}
	unknownAction := pdf.NewPDFDict()
	unknownAction.Entries["S"] = pdf.PDFName{Value: "NotARealActionType"}
	if got := arlingtonChildType("Catalog", "OpenAction", unknownAction); got != "" {
		t.Errorf("arlingtonChildType(Catalog, OpenAction, unrecognized S) = %q, want \"\" (no guess)", got)
	}
	noDiscriminator := pdf.NewPDFDict()
	if got := arlingtonChildType("Catalog", "OpenAction", noDiscriminator); got != "" {
		t.Errorf("arlingtonChildType(Catalog, OpenAction, no S) = %q, want \"\" (no guess)", got)
	}

	if got := arlingtonChildType("NotAType", "Pages", pagesDict); got != "" {
		t.Errorf("arlingtonChildType(unknown type) = %q, want \"\"", got)
	}
	if got := arlingtonChildType("Catalog", "NoSuchKey", pagesDict); got != "" {
		t.Errorf("arlingtonChildType(Catalog, NoSuchKey) = %q, want \"\"", got)
	}
	if got := arlingtonChildType("Catalog", "Pages", nil); got != "" {
		t.Errorf("arlingtonChildType(Catalog, Pages, nil) = %q, want \"\" (null never resolves)", got)
	}
}

func TestArlingtonElementType(t *testing.T) {
	oi := pdf.NewPDFDict()
	if got := arlingtonElementType("ArrayOfOutputIntents", oi); got != "OutputIntents" {
		t.Errorf("arlingtonElementType(ArrayOfOutputIntents) = %q, want OutputIntents", got)
	}

	// ArrayOfAnnots' wildcard resolves each element via its own Subtype.
	widget := pdf.NewPDFDict()
	widget.Entries["Subtype"] = pdf.PDFName{Value: "Widget"}
	if got := arlingtonElementType("ArrayOfAnnots", widget); got != "AnnotWidget" {
		t.Errorf("arlingtonElementType(ArrayOfAnnots, Subtype=Widget) = %q, want AnnotWidget", got)
	}
	noSubtype := pdf.NewPDFDict()
	if got := arlingtonElementType("ArrayOfAnnots", noSubtype); got != "" {
		t.Errorf("arlingtonElementType(ArrayOfAnnots, no Subtype) = %q, want \"\" (no guess)", got)
	}

	if got := arlingtonElementType("NotAType", oi); got != "" {
		t.Errorf("arlingtonElementType(unknown type) = %q, want \"\"", got)
	}
	// ArrayOf_2Numbers has fixed-position keys ("0", "1"), not a wildcard.
	if got := arlingtonElementType("ArrayOf_2Numbers", oi); got != "" {
		t.Errorf("arlingtonElementType(ArrayOf_2Numbers) = %q, want \"\" (no wildcard)", got)
	}
	if got := arlingtonElementType("ArrayOfOutputIntents", nil); got != "" {
		t.Errorf("arlingtonElementType(ArrayOfOutputIntents, nil) = %q, want \"\" (null never resolves)", got)
	}
}

func TestResolveLinkGroups(t *testing.T) {
	// A group whose ValueTypes doesn't match val's kind is skipped entirely.
	next, _ := arlington.Type("ActionGoTo")
	var nextKey *arlington.KeyDef
	for i := range next.Keys {
		if next.Keys[i].Name == "Next" {
			nextKey = &next.Keys[i]
		}
	}
	if nextKey == nil {
		t.Fatal("ActionGoTo.Next not found")
	}
	// Next's Array-typed group has a single candidate (ArrayOfActions); a Name value matches
	// neither the Array nor the Dictionary group, so it must stay unresolved.
	if got := resolveLinkGroups(nextKey.LinkGroups, nextKey.Types, pdf.PDFName{Value: "X"}); got != "" {
		t.Errorf("resolveLinkGroups(Next, a name) = %q, want \"\" (no matching group)", got)
	}
	if got := resolveLinkGroups(nextKey.LinkGroups, nextKey.Types, pdf.PDFArray{}); got != "ArrayOfActions" {
		t.Errorf("resolveLinkGroups(Next, an array) = %q, want ArrayOfActions", got)
	}
}

func TestLinkGroupMatchesKind(t *testing.T) {
	streamDict := pdf.NewPDFDict()
	streamDict.HasStream = true

	cases := []struct {
		name       string
		val        pdf.PDFValue
		valueTypes []arlington.ValueType
		want       bool
	}{
		{"nil ValueTypes always matches", pdf.NewPDFDict(), nil, true},
		{"array matches Array", pdf.PDFArray{}, []arlington.ValueType{arlington.Array}, true},
		{"dict matches Dictionary", pdf.NewPDFDict(), []arlington.ValueType{arlington.Dictionary}, true},
		{"stream dict matches Stream", streamDict, []arlington.ValueType{arlington.Stream}, true},
		{"non-stream dict does not match Stream", pdf.NewPDFDict(), []arlington.ValueType{arlington.Stream}, false},
		{"stream dict does not match Dictionary", streamDict, []arlington.ValueType{arlington.Dictionary}, false},
		{"name matches Name", pdf.PDFName{Value: "X"}, []arlington.ValueType{arlington.Name}, true},
		{"integer matches Integer", pdf.PDFInteger(1), []arlington.ValueType{arlington.Integer}, true},
		{"real matches Number", pdf.PDFReal(1.5), []arlington.ValueType{arlington.Number}, true},
		{"boolean matches Boolean", pdf.PDFBoolean(true), []arlington.ValueType{arlington.Boolean}, true},
		{"string matches StringText", pdf.PDFString{Value: "x"}, []arlington.ValueType{arlington.StringText}, true},
		{"hex string matches String", pdf.PDFHexString{Value: "aa"}, []arlington.ValueType{arlington.String}, true},
		{"integer does not match Name (fails closed)", pdf.PDFInteger(1), []arlington.ValueType{arlington.Name}, false},
		{"boolean does not match any of a multi-type list", pdf.PDFBoolean(true), []arlington.ValueType{arlington.Array, arlington.Name}, false},
	}
	for _, c := range cases {
		if got := linkGroupMatchesKind(c.val, c.valueTypes); got != c.want {
			t.Errorf("%s: linkGroupMatchesKind(%#v, %v) = %v, want %v", c.name, c.val, c.valueTypes, got, c.want)
		}
	}
}

// TestResolveLinkGroupsZeroCandidates covers a matching group with no candidates at all (a
// Type alternative with no sub-schema, e.g. a plain scalar type alongside a dict alternative).
func TestResolveLinkGroupsZeroCandidates(t *testing.T) {
	groups := []arlington.LinkGroup{
		{ValueTypes: []arlington.ValueType{arlington.Name}}, // no Candidates
	}
	if got := resolveLinkGroups(groups, nil, pdf.PDFName{Value: "X"}); got != "" {
		t.Errorf("resolveLinkGroups(zero-candidate group) = %q, want \"\"", got)
	}
}

// TestSchemaCheckAfterUntypedFirstVisit builds a dict shared between an untyped custom
// trailer key (sorted first, so the untyped path wins the initial visit) and the typed
// Catalog.Pages edge: the typed re-descent must still schema-check it.
func TestSchemaCheckAfterUntypedFirstVisit(t *testing.T) {
	shared := pdf.NewPDFDict() // empty: missing PageTreeNodeRoot's required Type/Kids/Count

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["Pages"] = shared

	trailer := pdf.NewPDFDict()
	trailer.Entries["AAACustom"] = shared
	trailer.Entries["Root"] = catalog
	trailer.Entries["Size"] = pdf.PDFInteger(3)

	ctx := &ValidationContext{}
	verifyDocument(trailer, ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("expected MissingRequiredKey for a shared dict reached untyped first and typed later")
	}
}

// TestSchemaCheckUnderEveryReachableType shares one dict between two differently-typed
// edges (Catalog.AcroForm -> InteractiveForm, Catalog.Pages -> PageTreeNodeRoot); it must
// be schema-checked under both types, regardless of visit order.
func TestSchemaCheckUnderEveryReachableType(t *testing.T) {
	shared := pdf.NewPDFDict()

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["AcroForm"] = shared // InteractiveForm requires Fields
	catalog.Entries["Pages"] = shared    // PageTreeNodeRoot requires Type/Kids/Count

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog
	trailer.Entries["Size"] = pdf.PDFInteger(3)

	ctx := &ValidationContext{}
	verifyDocument(trailer, ctx)

	var gotForm, gotPages bool
	for _, e := range ctx.errs {
		if e.Check() != pdf.Checks.ObjectModel.MissingRequiredKey {
			continue
		}
		for _, m := range e.Messages() {
			if strings.Contains(m, "InteractiveForm") {
				gotForm = true
			}
			if strings.Contains(m, "PageTreeNodeRoot") {
				gotPages = true
			}
		}
	}
	if !gotForm || !gotPages {
		t.Errorf("expected MissingRequiredKey under both InteractiveForm and PageTreeNodeRoot, got form=%v pages=%v (%v)", gotForm, gotPages, ctx.errs)
	}
}

// TestSchemaCheckOncePerType asserts a (node, type) pair is validated exactly once even
// when the same typed edge is reachable twice, so re-descents cannot duplicate findings.
func TestSchemaCheckOncePerType(t *testing.T) {
	form := pdf.NewPDFDict() // InteractiveForm missing required Fields

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["AcroForm"] = form
	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	pages.Entries["Kids"] = pdf.PDFArray{}
	pages.Entries["Count"] = pdf.PDFInteger(0)
	catalog.Entries["Pages"] = pages

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog
	trailer.Entries["AAAAlias"] = catalog // second, untyped route to the same catalog
	trailer.Entries["Size"] = pdf.PDFInteger(3)

	ctx := &ValidationContext{}
	verifyDocument(trailer, ctx)

	count := 0
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.ObjectModel.MissingRequiredKey {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one MissingRequiredKey (Fields), got %d: %v", count, ctx.errs)
	}
}

func TestValidateArrayAgainstSchema_MissingRequiredElement(t *testing.T) {
	arr := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(612)}
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(arr, "ArrayOf_4Numbers", pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("expected MissingRequiredKey for a 3-element ArrayOf_4Numbers")
	}
}

func TestValidateArrayAgainstSchema_FixedIndexWrongType(t *testing.T) {
	arr := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFString{Value: "x"}, pdf.PDFInteger(612), pdf.PDFInteger(792)}
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(arr, "ArrayOf_4Numbers", pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.WrongValueType) {
		t.Error("expected WrongValueType for a string in ArrayOf_4Numbers")
	}
}

func TestValidateArrayAgainstSchema_ConformantFixedArray(t *testing.T) {
	arr := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFReal(0.5), pdf.PDFInteger(612), pdf.PDFInteger(792)}
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(arr, "ArrayOf_4Numbers", pdf.NewPDFDict(), ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("unexpected violations for a conformant ArrayOf_4Numbers: %v", ctx.errs)
	}
}

func TestValidateArrayAgainstSchema_DisallowedValue(t *testing.T) {
	// Dest1Array element 1 is enumerated (FitH/FitV/FitBH/FitBV); element 2 is
	// null|number, so a PDF null (Go nil) there must not be flagged.
	arr := pdf.PDFArray{pdf.PDFInteger(2), pdf.PDFName{Value: "XYZ"}, nil}
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(arr, "Dest1Array", pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for Dest1Array element 1 = XYZ")
	}
	if hasCheck(ctx, pdf.Checks.ObjectModel.WrongValueType) {
		t.Errorf("null element must never be a WrongValueType: %v", ctx.errs)
	}
}

func TestValidateArrayAgainstSchema_WildcardElement(t *testing.T) {
	// ArrayOfPageTreeNodeKids' wildcard requires indirect dictionary elements.
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFInteger(5)}, "ArrayOfPageTreeNodeKids", pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.WrongValueType) {
		t.Error("expected WrongValueType for an integer kid")
	}

	direct := pdf.NewPDFDict()
	direct.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	ctx = &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{direct}, "ArrayOfPageTreeNodeKids", pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.IndirectRequired) {
		t.Error("expected IndirectRequired for a direct kid dict")
	}

	indirect := pdf.NewPDFDict()
	indirect.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	indirect.Entries["Kids"] = pdf.PDFArray{}
	indirect.Entries["Count"] = pdf.PDFInteger(0)
	indirect.Entries["_ref"] = pdf.PDFRef{ObjNum: 7}
	ctx = &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{indirect}, "ArrayOfPageTreeNodeKids", pdf.NewPDFDict(), ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("unexpected violations for an indirect kid: %v", ctx.errs)
	}
}

// TestArraySchemaCheckedInWalk asserts verifyDocument types and checks arrays reached
// through Link edges: a direct (non-indirect) kid inside Pages.Kids must be flagged.
func TestArraySchemaCheckedInWalk(t *testing.T) {
	kid := pdf.NewPDFDict()
	kid.Entries["Type"] = pdf.PDFName{Value: "Page"}

	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	pages.Entries["Kids"] = pdf.PDFArray{kid}
	pages.Entries["Count"] = pdf.PDFInteger(1)
	pages.Entries["_ref"] = pdf.PDFRef{ObjNum: 2}

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["Pages"] = pages
	catalog.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog
	trailer.Entries["Size"] = pdf.PDFInteger(3)

	ctx := &ValidationContext{}
	verifyDocument(trailer, ctx)

	found := false
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.ObjectModel.IndirectRequired {
			for _, m := range e.Messages() {
				if strings.Contains(m, "ArrayOfPageTreeNodeKids element 0") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected IndirectRequired for the direct kid via the walk, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_WildcardDictEntries(t *testing.T) {
	// XObjectMap's wildcard requires every value to be an indirect stream.
	m := pdf.NewPDFDict()
	m.Entries["Im1"] = pdf.PDFName{Value: "NotAStream"}
	ctx := &ValidationContext{}
	validateAgainstSchema(m, "XObjectMap", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.WrongValueType) {
		t.Error("expected WrongValueType for a name in an XObject resource map")
	}

	direct := pdf.NewPDFDict()
	direct.HasStream = true
	m = pdf.NewPDFDict()
	m.Entries["Im1"] = direct
	ctx = &ValidationContext{}
	validateAgainstSchema(m, "XObjectMap", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.IndirectRequired) {
		t.Error("expected IndirectRequired for a direct stream in an XObject resource map")
	}

	indirect := pdf.NewPDFDict()
	indirect.HasStream = true
	indirect.Entries["_ref"] = pdf.PDFRef{ObjNum: 9}
	m = pdf.NewPDFDict()
	m.Entries["Im1"] = indirect
	ctx = &ValidationContext{}
	validateAgainstSchema(m, "XObjectMap", ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("unexpected violations for a conformant XObject resource map: %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_WildcardSkipsNamedRows(t *testing.T) {
	// DocInfo's wildcard types custom keys as string-text; named rows (Trapped
	// is a name) must stay governed by their own row, not the wildcard.
	info := pdf.NewPDFDict()
	info.Entries["Trapped"] = pdf.PDFName{Value: "True"}
	info.Entries["Custom"] = pdf.PDFInteger(7)
	ctx := &ValidationContext{}
	validateAgainstSchema(info, "DocInfo", ctx)
	count := 0
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.ObjectModel.WrongValueType {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one WrongValueType (Custom), got %d: %v", count, ctx.errs)
	}
}

// TestChildTypeRequiresMatchingKind: a single-alternative Link (nil ValueTypes) must not
// propagate its type to a value whose kind contradicts the key's declared types, e.g. a
// stream where FileTrailer.Info declares a dictionary.
func TestChildTypeRequiresMatchingKind(t *testing.T) {
	stream := pdf.NewPDFDict()
	stream.HasStream = true
	if got := arlingtonChildType("FileTrailer", "Info", stream); got != "" {
		t.Errorf("arlingtonChildType(FileTrailer, Info, stream) = %q, want \"\"", got)
	}
	plain := pdf.NewPDFDict()
	if got := arlingtonChildType("FileTrailer", "Info", plain); got != "DocInfo" {
		t.Errorf("arlingtonChildType(FileTrailer, Info, dict) = %q, want DocInfo", got)
	}
}

func TestValidateArrayAgainstSchema_NonArrayTypeNames(t *testing.T) {
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{}, "NoSuchType", pdf.NewPDFDict(), ctx)
	// Catalog has only named (non-index) rows and no wildcard: nothing applies positionally.
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFInteger(1)}, "Catalog", pdf.NewPDFDict(), ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("expected no findings for non-array type names, got %v", ctx.errs)
	}
}

func TestValidateArrayAgainstSchema_WildcardEnum(t *testing.T) {
	// ArrayOfFilterNames enumerates the legal stream filter names.
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFName{Value: "BogusDecode"}}, "ArrayOfFilterNames", pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for an unknown filter name")
	}
}

func TestEvalCond(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries["R"] = pdf.PDFInteger(5)
	d.Entries["Type"] = pdf.PDFName{Value: "Page"}
	d.Entries["Null"] = nil
	d.Entries["Dict"] = pdf.NewPDFDict()

	cases := []struct {
		name    string
		cond    arlington.Cond
		val, ok bool
	}{
		{"present", arlington.Cond{Op: arlington.CondPresent, Key: "R"}, true, true},
		{"absent", arlington.Cond{Op: arlington.CondPresent, Key: "X"}, false, true},
		{"null is absent", arlington.Cond{Op: arlington.CondPresent, Key: "Null"}, false, true},
		{"eq match", arlington.Cond{Op: arlington.CondEq, Key: "R", Value: "5"}, true, true},
		{"eq mismatch", arlington.Cond{Op: arlington.CondEq, Key: "R", Value: "6"}, false, true},
		{"eq absent", arlington.Cond{Op: arlington.CondEq, Key: "X", Value: "5"}, false, true},
		{"ne name", arlington.Cond{Op: arlington.CondNe, Key: "Type", Value: "Template"}, true, true},
		{"ne absent is true", arlington.Cond{Op: arlington.CondNe, Key: "X", Value: "5"}, true, true},
		{"eq non-scalar fails closed", arlington.Cond{Op: arlington.CondEq, Key: "Dict", Value: "5"}, false, false},
		{"not", arlington.Cond{Op: arlington.CondNot, Kids: []arlington.Cond{{Op: arlington.CondPresent, Key: "X"}}}, true, true},
		{"or decisive beats bad kid", arlington.Cond{Op: arlington.CondOr, Kids: []arlington.Cond{
			{Op: arlington.CondEq, Key: "Dict", Value: "5"}, {Op: arlington.CondPresent, Key: "R"}}}, true, true},
		{"or all false", arlington.Cond{Op: arlington.CondOr, Kids: []arlington.Cond{
			{Op: arlington.CondPresent, Key: "X"}, {Op: arlington.CondPresent, Key: "Y"}}}, false, true},
		{"and decisive beats bad kid", arlington.Cond{Op: arlington.CondAnd, Kids: []arlington.Cond{
			{Op: arlington.CondEq, Key: "Dict", Value: "5"}, {Op: arlington.CondPresent, Key: "X"}}}, false, true},
		{"and bad kid fails closed", arlington.Cond{Op: arlington.CondAnd, Kids: []arlington.Cond{
			{Op: arlington.CondEq, Key: "Dict", Value: "5"}, {Op: arlington.CondPresent, Key: "R"}}}, false, false},
	}
	for _, tc := range cases {
		val, ok := evalCond(&tc.cond, d)
		if val != tc.val || ok != tc.ok {
			t.Errorf("%s: evalCond = (%v, %v), want (%v, %v)", tc.name, val, ok, tc.val, tc.ok)
		}
	}
}

func TestEvalCondModAndUnknown(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries["Rotate"] = pdf.PDFInteger(270)
	d.Entries["Skew"] = pdf.PDFInteger(45)
	d.Entries["Neg"] = pdf.PDFInteger(-90)
	d.Entries["Real"] = pdf.PDFReal(90)

	cases := []struct {
		name    string
		cond    arlington.Cond
		val, ok bool
	}{
		{"mod multiple", arlington.Cond{Op: arlington.CondEq, Key: "Rotate", Value: "0", Mod: 90}, true, true},
		{"mod remainder", arlington.Cond{Op: arlington.CondEq, Key: "Skew", Value: "0", Mod: 90}, false, true},
		{"mod negative multiple", arlington.Cond{Op: arlington.CondEq, Key: "Neg", Value: "0", Mod: 90}, true, true},
		{"mod non-integer fails closed", arlington.Cond{Op: arlington.CondEq, Key: "Real", Value: "0", Mod: 90}, false, false},
		{"mod absent fails closed", arlington.Cond{Op: arlington.CondEq, Key: "X", Value: "0", Mod: 90}, false, false},
		{"unknown is unresolvable", arlington.Cond{Op: arlington.CondUnknown}, false, false},
		{"or true beats unknown", arlington.Cond{Op: arlington.CondOr, Kids: []arlington.Cond{
			{Op: arlington.CondPresent, Key: "Rotate"}, {Op: arlington.CondUnknown}}}, true, true},
		{"or false with unknown stays unknown", arlington.Cond{Op: arlington.CondOr, Kids: []arlington.Cond{
			{Op: arlington.CondPresent, Key: "X"}, {Op: arlington.CondUnknown}}}, false, false},
		{"and false beats unknown", arlington.Cond{Op: arlington.CondAnd, Kids: []arlington.Cond{
			{Op: arlington.CondPresent, Key: "X"}, {Op: arlington.CondUnknown}}}, false, true},
	}
	for _, tc := range cases {
		val, ok := evalCond(&tc.cond, d)
		if val != tc.val || ok != tc.ok {
			t.Errorf("%s: evalCond = (%v, %v), want (%v, %v)", tc.name, val, ok, tc.val, tc.ok)
		}
	}
}

func TestEvalCondOperandsAndContains(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries["TI"] = pdf.PDFInteger(2)
	d.Entries["Opt"] = pdf.PDFArray{pdf.PDFName{Value: "a"}, pdf.PDFName{Value: "b"}, pdf.PDFName{Value: "c"}}
	d.Entries["S"] = pdf.PDFString{Value: "12345678"}
	d.Entries["Filter"] = pdf.PDFArray{pdf.PDFName{Value: "FlateDecode"}, pdf.PDFName{Value: "JPXDecode"}}
	d.Entries["One"] = pdf.PDFName{Value: "DCTDecode"}
	d.Entries["Mask"] = pdf.PDFBoolean(true)
	d.Entries["Unmask"] = pdf.PDFBoolean(false)
	d.Entries["Mixed"] = pdf.PDFArray{pdf.NewPDFDict()}

	cases := []struct {
		name    string
		cond    arlington.Cond
		val, ok bool
	}{
		{"lt array length", arlington.Cond{Op: arlington.CondLt, Key: "TI", RHSKey: "Opt", RHSFn: arlington.FnArrayLength}, true, true},
		{"ge array length", arlington.Cond{Op: arlington.CondGe, Key: "TI", Value: "3", RHSKey: "Opt", RHSFn: arlington.FnArrayLength}, false, true},
		{"array length eq", arlington.Cond{Op: arlington.CondEq, Key: "Opt", Fn: arlington.FnArrayLength, Value: "3"}, true, true},
		{"string length eq", arlington.Cond{Op: arlington.CondEq, Key: "S", Fn: arlington.FnStringLength, Value: "8"}, true, true},
		{"length of non-array fails closed", arlington.Cond{Op: arlington.CondEq, Key: "S", Fn: arlington.FnArrayLength, Value: "8"}, false, false},
		{"length of absent fails closed", arlington.Cond{Op: arlington.CondEq, Key: "X", Fn: arlington.FnArrayLength, Value: "0"}, false, false},
		{"length mod", arlington.Cond{Op: arlington.CondEq, Key: "Opt", Fn: arlington.FnArrayLength, Value: "1", Mod: 2}, true, true},
		{"contains in array", arlington.Cond{Op: arlington.CondContains, Key: "Filter", Value: "JPXDecode"}, true, true},
		{"contains scalar", arlington.Cond{Op: arlington.CondContains, Key: "One", Value: "DCTDecode"}, true, true},
		{"contains no match", arlington.Cond{Op: arlington.CondContains, Key: "Filter", Value: "DCTDecode"}, false, true},
		{"contains absent is definite", arlington.Cond{Op: arlington.CondContains, Key: "X", Value: "JPXDecode"}, false, true},
		{"contains unresolvable element fails closed", arlington.Cond{Op: arlington.CondContains, Key: "Mixed", Value: "JPXDecode"}, false, false},
		{"boolean eq", arlington.Cond{Op: arlington.CondEq, Key: "Mask", Value: "true"}, true, true},
		{"boolean false eq", arlington.Cond{Op: arlington.CondEq, Key: "Unmask", Value: "false"}, true, true},
		{"contains skips null elements", arlington.Cond{Op: arlington.CondContains, Key: "Sparse", Value: "X"}, true, true},
		{"string length of non-string fails closed", arlington.Cond{Op: arlington.CondEq, Key: "Opt", Fn: arlington.FnStringLength, Value: "8"}, false, false},
		{"non-numeric bound fails closed", arlington.Cond{Op: arlington.CondLt, Key: "TI", Value: "abc"}, false, false},
		{"malformed not fails closed", arlington.Cond{Op: arlington.CondNot}, false, false},
		{"unrecognized op fails closed", arlington.Cond{Op: arlington.CondOp(99)}, false, false},
	}
	d.Entries["Sparse"] = pdf.PDFArray{nil, pdf.PDFName{Value: "X"}}
	for _, tc := range cases {
		val, ok := evalCond(&tc.cond, d)
		if val != tc.val || ok != tc.ok {
			t.Errorf("%s: evalCond = (%v, %v), want (%v, %v)", tc.name, val, ok, tc.val, tc.ok)
		}
	}

	// A multi-candidate LinkGroup can only discriminate dicts and arrays; any other kind
	// fails closed to untyped.
	got := resolveLinkGroups([]arlington.LinkGroup{{
		Candidates:    []string{"A", "B"},
		Discriminator: "0",
		ByValue:       map[string]string{"X": "A"},
	}}, []arlington.ValueType{arlington.String}, pdf.PDFString{Value: "X"})
	if got != "" {
		t.Errorf("resolveLinkGroups on a scalar: want \"\", got %q", got)
	}
}

func TestValidateAgainstSchema_ImageRequirements(t *testing.T) {
	// A plain (non-JPX, non-mask) image stream must carry ColorSpace and BitsPerComponent.
	img := pdf.NewPDFDict()
	img.HasStream = true
	img.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	img.Entries["Width"] = pdf.PDFInteger(1)
	img.Entries["Height"] = pdf.PDFInteger(1)
	ctx := &ValidationContext{}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("expected MissingRequiredKey for an image without ColorSpace/BitsPerComponent")
	}

	// An image mask needs neither.
	img.Entries["ImageMask"] = pdf.PDFBoolean(true)
	ctx = &ValidationContext{}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Errorf("an image mask must not require ColorSpace/BitsPerComponent, got %v", ctx.errs)
	}

	// A JPX-encoded image self-describes both.
	delete(img.Entries, "ImageMask")
	img.Entries["Filter"] = pdf.PDFName{Value: "JPXDecode"}
	ctx = &ValidationContext{}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Errorf("a JPX image must not require ColorSpace/BitsPerComponent, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_PinnedValues(t *testing.T) {
	// EncryptionStandard.R must be 3 when V is 2.
	enc := pdf.NewPDFDict()
	enc.Entries["Filter"] = pdf.PDFName{Value: "Standard"}
	enc.Entries["V"] = pdf.PDFInteger(2)
	enc.Entries["R"] = pdf.PDFInteger(2)
	enc.Entries["O"] = pdf.PDFString{Value: "o"}
	enc.Entries["U"] = pdf.PDFString{Value: "u"}
	enc.Entries["P"] = pdf.PDFInteger(-4)
	ctx := &ValidationContext{}
	validateAgainstSchema(enc, "EncryptionStandard", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for R=2 with V=2")
	}

	enc.Entries["R"] = pdf.PDFInteger(3)
	ctx = &ValidationContext{}
	validateAgainstSchema(enc, "EncryptionStandard", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("R=3 with V=2 must not be flagged, got %v", ctx.errs)
	}

	// A DCT-encoded image must use 8 bits per component; the pin fires on 4, and 16 is
	// outside the (now unpredicated) enum regardless of filter.
	img := pdf.NewPDFDict()
	img.HasStream = true
	img.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	img.Entries["Width"] = pdf.PDFInteger(1)
	img.Entries["Height"] = pdf.PDFInteger(1)
	img.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceGray"}
	img.Entries["Filter"] = pdf.PDFName{Value: "DCTDecode"}
	img.Entries["BitsPerComponent"] = pdf.PDFInteger(4)
	ctx = &ValidationContext{}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for a 4-bit DCT image")
	}

	img.Entries["BitsPerComponent"] = pdf.PDFInteger(8)
	ctx = &ValidationContext{}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("an 8-bit DCT image must not be flagged, got %v", ctx.errs)
	}

	// An unresolvable pin condition (Filter as an array makes @Filter== unknown) never flags.
	img.Entries["Filter"] = pdf.PDFArray{pdf.PDFName{Value: "DCTDecode"}}
	img.Entries["BitsPerComponent"] = pdf.PDFInteger(4)
	ctx = &ValidationContext{}
	validateAgainstSchema(img, "XObjectImage", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("an unknown pin condition must fail closed, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_SpecialCaseConstraint(t *testing.T) {
	// A stream's DecodeParms array must be as long as its Filter array.
	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.Entries["Filter"] = pdf.PDFArray{pdf.PDFName{Value: "ASCIIHexDecode"}, pdf.PDFName{Value: "FlateDecode"}}
	stream.Entries["DecodeParms"] = pdf.PDFArray{pdf.NewPDFDict()}
	ctx := &ValidationContext{}
	validateAgainstSchema(stream, "Stream", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Error("expected ConstraintViolated for mismatched DecodeParms/Filter lengths")
	}

	stream.Entries["DecodeParms"] = pdf.PDFArray{pdf.NewPDFDict(), nil}
	ctx = &ValidationContext{}
	validateAgainstSchema(stream, "Stream", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Errorf("matched lengths must not be flagged, got %v", ctx.errs)
	}

	// A single name Filter makes ArrayLength(Filter) unknown: no flag either way.
	stream.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
	stream.Entries["DecodeParms"] = pdf.PDFArray{pdf.NewPDFDict()}
	ctx = &ValidationContext{}
	validateAgainstSchema(stream, "Stream", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Errorf("a scalar Filter must leave the coupling unknown, got %v", ctx.errs)
	}

	// Bit predicates: reserved FontDescriptor flag bits are an unconditional ISO rule and
	// flag; annotation F bits are only constrained through version gates, which are dropped
	// (real files carry post-1.4 flag bits harmlessly -- the KeyIntroducedAfterPDF14 stance).
	desc := pdf.NewPDFDict()
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["Flags"] = pdf.PDFInteger(32 + 1<<14) // Nonsymbolic + reserved bit 15
	ctx = &ValidationContext{}
	validateAgainstSchema(desc, "FontDescriptorType1", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Error("expected ConstraintViolated for a reserved FontDescriptor flag bit")
	}

	annot := pdf.NewPDFDict()
	annot.Entries["Subtype"] = pdf.PDFName{Value: "Text"}
	annot.Entries["Rect"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(1)}
	annot.Entries["F"] = pdf.PDFInteger(1 << 9) // bit 10, LockedContents (PDF 1.7)
	ctx = &ValidationContext{}
	validateAgainstSchema(annot, "AnnotText", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Errorf("post-1.4 annotation flag bits must not be flagged, got %v", ctx.errs)
	}

	// A negative flag word keeps its two's-complement pattern: bits 13..32 of a standard
	// permissions value like -44 are set, so CondBitsSet on them holds.
	perms := pdf.NewPDFDict()
	perms.Entries["P"] = pdf.PDFInteger(-44)
	set, ok := evalCond(&arlington.Cond{Op: arlington.CondBitsSet, Key: "P", BitLo: 13, BitHi: 32}, perms)
	if !set || !ok {
		t.Errorf("bits 13..32 of -44 must read as set, got (%v, %v)", set, ok)
	}
	clear, ok := evalCond(&arlington.Cond{Op: arlington.CondBitsClear, Key: "P", BitLo: 3, BitHi: 3}, perms)
	if clear != false || !ok {
		t.Errorf("bit 3 of -44 (print allowed) must read as set, got (%v, %v)", clear, ok)
	}
	if _, ok := evalCond(&arlington.Cond{Op: arlington.CondBitsClear, Key: "X", BitLo: 1, BitHi: 1}, perms); ok {
		t.Error("a bit test on an absent key must fail closed")
	}

	// Fixed-index rows carry constraints too: an annotation colour array with exactly two
	// components is malformed (element 1 present requires element 2).
	ctx = &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFReal(0.5), pdf.PDFReal(0.5)}, "ArrayOf_4NumbersColorAnnotation", pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Error("expected ConstraintViolated for a two-component colour annotation array")
	}
	ctx = &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFReal(0.5), pdf.PDFReal(0.5), pdf.PDFReal(0.5)}, "ArrayOf_4NumbersColorAnnotation", pdf.NewPDFDict(), ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Errorf("a three-component colour array must not be flagged, got %v", ctx.errs)
	}

	// An odd-length function Domain violates the mod-2 coupling.
	fn := pdf.NewPDFDict()
	fn.HasStream = true
	fn.Entries["FunctionType"] = pdf.PDFInteger(0)
	fn.Entries["Domain"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(2)}
	fn.Entries["Range"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)}
	fn.Entries["Size"] = pdf.PDFArray{pdf.PDFInteger(2)}
	fn.Entries["BitsPerSample"] = pdf.PDFInteger(8)
	ctx = &ValidationContext{}
	validateAgainstSchema(fn, "FunctionType0", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Error("expected ConstraintViolated for an odd-length Domain")
	}
}

func TestValidateAgainstSchema_NotStandard14Font(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "ABCDEF+SomeFont"}
	ctx := &ValidationContext{}
	validateAgainstSchema(font, "FontType1", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("expected MissingRequiredKey for a non-standard Type1 font without Widths")
	}

	// A standard-14 base font carries its own metrics; nothing further is required.
	font.Entries["BaseFont"] = pdf.PDFName{Value: "Helvetica"}
	ctx = &ValidationContext{}
	validateAgainstSchema(font, "FontType1", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Errorf("a standard-14 font must not require Widths, got %v", ctx.errs)
	}

	// Without BaseFont the standard-14 test is unknowable: the conditional requirements are
	// skipped, only BaseFont's own absence is flagged.
	delete(font.Entries, "BaseFont")
	ctx = &ValidationContext{}
	validateAgainstSchema(font, "FontType1", ctx)
	count := 0
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.ObjectModel.MissingRequiredKey {
			count++
		}
	}
	if count != 1 {
		t.Errorf("want exactly one MissingRequiredKey (BaseFont itself), got %d: %v", count, ctx.errs)
	}
}

func TestValidateArrayAgainstSchema_ElementComparison(t *testing.T) {
	// LabRangeArray requires amin <= amax and bmin <= bmax.
	owner := pdf.NewPDFDict()
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFInteger(5), pdf.PDFInteger(-5), pdf.PDFInteger(-100), pdf.PDFInteger(100)}, "LabRangeArray", owner, ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for amin > amax")
	}

	ctx = &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFInteger(-100), pdf.PDFInteger(100), pdf.PDFInteger(-100), pdf.PDFInteger(100)}, "LabRangeArray", owner, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("unexpected violations for an ordered Lab range: %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_RotateMod90(t *testing.T) {
	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Parent"] = pdf.PDFRef{ObjNum: 2}
	page.Entries["Rotate"] = pdf.PDFInteger(45)
	ctx := &ValidationContext{}
	validateAgainstSchema(page, "PageObject", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for /Rotate 45")
	}

	page.Entries["Rotate"] = pdf.PDFInteger(-270)
	ctx = &ValidationContext{}
	validateAgainstSchema(page, "PageObject", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("a multiple of 90 must not be flagged, got %v", ctx.errs)
	}
}

func TestEvalCondOrdering(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries["CA"] = pdf.PDFReal(0.5)
	d.Entries["CA1"] = pdf.PDFReal(1.0000001)
	d.Entries["LC"] = pdf.PDFInteger(2)
	d.Entries["Name"] = pdf.PDFName{Value: "X"}

	cases := []struct {
		name    string
		cond    arlington.Cond
		val, ok bool
	}{
		{"ge real", arlington.Cond{Op: arlington.CondGe, Key: "CA", Value: "0.0"}, true, true},
		{"le real", arlington.Cond{Op: arlington.CondLe, Key: "CA", Value: "1.0"}, true, true},
		{"gt fails", arlington.Cond{Op: arlington.CondGt, Key: "CA", Value: "0.5"}, false, true},
		{"lt int", arlington.Cond{Op: arlington.CondLt, Key: "LC", Value: "3"}, true, true},
		{"absent fails closed", arlington.Cond{Op: arlington.CondGe, Key: "X", Value: "0"}, false, false},
		{"non-numeric fails closed", arlington.Cond{Op: arlington.CondGe, Key: "Name", Value: "0"}, false, false},
		// Rounding tolerance: values within 1e-5 of the bound compare equal.
		{"le tolerates rounding", arlington.Cond{Op: arlington.CondLe, Key: "CA1", Value: "1"}, true, true},
		{"gt respects tolerance", arlington.Cond{Op: arlington.CondGt, Key: "CA1", Value: "1"}, false, true},
	}
	for _, tc := range cases {
		val, ok := evalCond(&tc.cond, d)
		if val != tc.val || ok != tc.ok {
			t.Errorf("%s: evalCond = (%v, %v), want (%v, %v)", tc.name, val, ok, tc.val, tc.ok)
		}
	}
}

func TestValidateAgainstSchema_ValueCondRange(t *testing.T) {
	// GraphicsStateParameter.CA must satisfy 0 <= CA <= 1.
	gs := pdf.NewPDFDict()
	gs.Entries["CA"] = pdf.PDFReal(1.5)
	ctx := &ValidationContext{}
	validateAgainstSchema(gs, "GraphicsStateParameter", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for CA outside [0,1]")
	}

	gs = pdf.NewPDFDict()
	gs.Entries["CA"] = pdf.PDFReal(0.5)
	ctx = &ValidationContext{}
	validateAgainstSchema(gs, "GraphicsStateParameter", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("a CA within [0,1] must not be flagged, got %v", ctx.errs)
	}

	// Real files carry rounded values like 1.0000001; validators accept them as 1.0.
	gs = pdf.NewPDFDict()
	gs.Entries["CA"] = pdf.PDFReal(1.0000001)
	ctx = &ValidationContext{}
	validateAgainstSchema(gs, "GraphicsStateParameter", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("a CA within rounding tolerance of 1 must not be flagged, got %v", ctx.errs)
	}

	// A value of the wrong shape is the type check's business, not the range check's.
	gs = pdf.NewPDFDict()
	gs.Entries["CA"] = pdf.PDFName{Value: "NotANumber"}
	ctx = &ValidationContext{}
	validateAgainstSchema(gs, "GraphicsStateParameter", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("a non-numeric CA must fail closed in the range check, got %v", ctx.errs)
	}
}

func TestEvalCondArray(t *testing.T) {
	arr := pdf.PDFArray{pdf.PDFReal(0.9505), nil, pdf.PDFName{Value: "X"}}

	cases := []struct {
		name    string
		cond    arlington.Cond
		val, ok bool
	}{
		{"element present", arlington.Cond{Op: arlington.CondPresent, Key: "0"}, true, true},
		{"null element is absent", arlington.Cond{Op: arlington.CondPresent, Key: "1"}, false, true},
		{"out of range is absent", arlington.Cond{Op: arlington.CondPresent, Key: "9"}, false, true},
		{"gt element", arlington.Cond{Op: arlington.CondGt, Key: "0", Value: "0"}, true, true},
		{"gt out of range fails closed", arlington.Cond{Op: arlington.CondGt, Key: "9", Value: "0"}, false, false},
		{"gt non-numeric fails closed", arlington.Cond{Op: arlington.CondGt, Key: "2", Value: "0"}, false, false},
		{"eq name element", arlington.Cond{Op: arlington.CondEq, Key: "2", Value: "X"}, true, true},
		{"non-numeric key is absent", arlington.Cond{Op: arlington.CondPresent, Key: "R"}, false, true},
	}
	for _, tc := range cases {
		val, ok := evalCondArray(&tc.cond, arr)
		if val != tc.val || ok != tc.ok {
			t.Errorf("%s: evalCondArray = (%v, %v), want (%v, %v)", tc.name, val, ok, tc.val, tc.ok)
		}
	}
}

func TestValidateArrayAgainstSchema_ValueCondRange(t *testing.T) {
	// WhitepointArray elements 0 and 2 must be > 0.
	owner := pdf.NewPDFDict()
	ctx := &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFReal(-1), pdf.PDFInteger(1), pdf.PDFReal(1.089)}, "WhitepointArray", owner, ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for a non-positive whitepoint X")
	}

	ctx = &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFReal(0.9505), pdf.PDFInteger(1), pdf.PDFReal(1.089)}, "WhitepointArray", owner, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("unexpected violations for a conformant whitepoint: %v", ctx.errs)
	}

	// A null element matches everything: the range check must not fire on it.
	ctx = &ValidationContext{}
	validateArrayAgainstSchema(pdf.PDFArray{nil, pdf.PDFInteger(1), pdf.PDFReal(1.089)}, "WhitepointArray", owner, ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Errorf("a null element must not be range-checked, got %v", ctx.errs)
	}
}

func TestValidateAgainstSchema_ConditionallyRequired(t *testing.T) {
	// PageObject.Parent is required when @Type!=Template.
	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	ctx := &ValidationContext{}
	validateAgainstSchema(page, "PageObject", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("expected MissingRequiredKey for a Page without Parent")
	}

	tmpl := pdf.NewPDFDict()
	tmpl.Entries["Type"] = pdf.PDFName{Value: "Template"}
	ctx = &ValidationContext{}
	validateAgainstSchema(tmpl, "PageObject", ctx)
	if hasCheck(ctx, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Error("a Template page must not require Parent")
	}

	// FileTrailer.ID is required when Encrypt is present.
	trailer := pdf.NewPDFDict()
	enc := pdf.NewPDFDict()
	enc.Entries["_ref"] = pdf.PDFRef{ObjNum: 9}
	trailer.Entries["Encrypt"] = enc
	ctx = &ValidationContext{}
	validateAgainstSchema(trailer, "FileTrailer", ctx)
	found := false
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.ObjectModel.MissingRequiredKey {
			found = true
		}
	}
	if !found {
		t.Error("expected MissingRequiredKey for an encrypted trailer without ID")
	}
}

func TestValidateAgainstSchema_WildcardDictEnum(t *testing.T) {
	// ColorSpaceMap's wildcard enumerates the legal name-valued entries.
	m := pdf.NewPDFDict()
	m.Entries["CS0"] = pdf.PDFName{Value: "BogusColorSpace"}
	ctx := &ValidationContext{}
	validateAgainstSchema(m, "ColorSpaceMap", ctx)
	if !hasCheck(ctx, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Error("expected DisallowedValue for an unknown colour space name")
	}

	m = pdf.NewPDFDict()
	m.Entries["CS0"] = pdf.PDFName{Value: "DeviceRGB"}
	ctx = &ValidationContext{}
	validateAgainstSchema(m, "ColorSpaceMap", ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("unexpected violations for a conformant colour space map: %v", ctx.errs)
	}
}

// TestSelfIdentifiedType covers the re-anchor lookup: unambiguous /Type (+/Subtype) pairs
// resolve, ambiguous or malformed ones stay "".
func TestSelfIdentifiedType(t *testing.T) {
	mk := func(typ, sub string) pdf.PDFDict {
		d := pdf.NewPDFDict()
		if typ != "" {
			d.Entries["Type"] = pdf.PDFName{Value: typ}
		}
		if sub != "" {
			d.Entries["Subtype"] = pdf.PDFName{Value: sub}
		}
		return d
	}
	cases := []struct {
		d    pdf.PDFDict
		want string
	}{
		{mk("Page", ""), "PageObject"},
		{mk("Annot", "Text"), "AnnotText"},
		{mk("FontDescriptor", ""), ""}, // four types collide on this pair
		{mk("", ""), ""},               // no /Type at all
	}
	for _, c := range cases {
		if got := selfIdentifiedType(c.d); got != c.want {
			t.Errorf("selfIdentifiedType(%v) = %q, want %q", c.d.Entries, got, c.want)
		}
	}

	// A non-name /Type never re-anchors.
	d := pdf.NewPDFDict()
	d.Entries["Type"] = pdf.PDFInteger(3)
	if got := selfIdentifiedType(d); got != "" {
		t.Errorf("selfIdentifiedType(non-name Type) = %q, want \"\"", got)
	}
}

// TestWalkReanchorsSelfIdentifyingDict reaches a /Type /Page dict only through an untyped
// custom trailer key: the walk must re-anchor it as PageObject and flag its missing Parent.
func TestWalkReanchorsSelfIdentifyingDict(t *testing.T) {
	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}

	trailer := pdf.NewPDFDict()
	trailer.Entries["XOrphan"] = page
	trailer.Entries["Size"] = pdf.PDFInteger(2)

	ctx := &ValidationContext{}
	verifyDocument(trailer, ctx)

	found := false
	for _, e := range ctx.errs {
		if e.Check() != pdf.Checks.ObjectModel.MissingRequiredKey {
			continue
		}
		for _, m := range e.Messages() {
			if strings.Contains(m, "PageObject") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected a PageObject MissingRequiredKey via re-anchoring, got %v", ctx.errs)
	}
}

// TestArlingtonChildTypeColourSpaceArray covers array-first-element discrimination: the
// name at index 0 of a colour-space array picks the candidate type.
func TestArlingtonChildTypeColourSpaceArray(t *testing.T) {
	stream := pdf.NewPDFDict()
	stream.HasStream = true

	icc := pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream}
	if got := arlingtonChildType("ColorSpaceMap", "DefaultRGB", icc); got != "ICCBasedColorSpace" {
		t.Errorf("arlingtonChildType(DefaultRGB, [/ICCBased ...]) = %q, want ICCBasedColorSpace", got)
	}

	// Unknown first element, an empty array, and a non-scalar discriminator all fail closed.
	if got := arlingtonChildType("ColorSpaceMap", "DefaultRGB", pdf.PDFArray{pdf.PDFName{Value: "Bogus"}}); got != "" {
		t.Errorf("unknown colour-space family resolved to %q, want \"\"", got)
	}
	if got := arlingtonChildType("ColorSpaceMap", "DefaultRGB", pdf.PDFArray{}); got != "" {
		t.Errorf("empty colour-space array resolved to %q, want \"\"", got)
	}
	if got := arlingtonChildType("ColorSpaceMap", "DefaultRGB", pdf.PDFArray{stream}); got != "" {
		t.Errorf("non-scalar discriminator resolved to %q, want \"\"", got)
	}
}

// hasDetail reports whether ctx holds a finding of c carrying exactly the
// Arlington schema location (typeName, key).
func hasDetail(ctx *ValidationContext, c pdf.Check, typeName, key string) bool {
	for _, e := range ctx.errs {
		if e.Check() != c {
			continue
		}
		if d, ok := e.ObjModelDetail(); ok && d.TypeName == typeName && d.Key == key {
			return true
		}
	}
	return false
}

// TestObjModelDetailAttached asserts every objmodel emission site attaches the Arlington
// schema location fixers target: named dict rows, wildcard rows, post-1.4 keys, and array
// rows (fixed and wildcard, keyed by decimal element index).
func TestObjModelDetailAttached(t *testing.T) {
	dict := func(kv map[string]pdf.PDFValue) pdf.PDFDict {
		d := pdf.NewPDFDict()
		for k, v := range kv {
			d.Entries[k] = v
		}
		return d
	}
	directPages := dict(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Pages"}})
	directStream := pdf.NewPDFDict()
	directStream.HasStream = true

	cases := []struct {
		name          string
		run           func(ctx *ValidationContext)
		check         pdf.Check
		typeName, key string
	}{
		{"dict missing required", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Catalog"}}), "Catalog", ctx)
		}, pdf.Checks.ObjectModel.MissingRequiredKey, "Catalog", "Pages"},
		{"dict wrong type", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"Type": pdf.PDFInteger(1)}), "Catalog", ctx)
		}, pdf.Checks.ObjectModel.WrongValueType, "Catalog", "Type"},
		{"dict disallowed enum", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"NonFullScreenPageMode": pdf.PDFName{Value: "Bogus"}}), "ViewerPreferences", ctx)
		}, pdf.Checks.ObjectModel.DisallowedValue, "ViewerPreferences", "NonFullScreenPageMode"},
		{"dict range", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"CA": pdf.PDFReal(1.5)}), "GraphicsStateParameter", ctx)
		}, pdf.Checks.ObjectModel.DisallowedValue, "GraphicsStateParameter", "CA"},
		{"dict pinned value", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{
				"Filter": pdf.PDFName{Value: "Standard"},
				"V":      pdf.PDFInteger(2), "R": pdf.PDFInteger(2),
				"O": pdf.PDFString{Value: "o"}, "U": pdf.PDFString{Value: "u"},
				"P": pdf.PDFInteger(-4),
			}), "EncryptionStandard", ctx)
		}, pdf.Checks.ObjectModel.DisallowedValue, "EncryptionStandard", "R"},
		{"dict special case", func(ctx *ValidationContext) {
			s := dict(map[string]pdf.PDFValue{
				"Filter":      pdf.PDFArray{pdf.PDFName{Value: "ASCIIHexDecode"}, pdf.PDFName{Value: "FlateDecode"}},
				"DecodeParms": pdf.PDFArray{pdf.NewPDFDict()},
			})
			s.HasStream = true
			validateAgainstSchema(s, "Stream", ctx)
		}, pdf.Checks.ObjectModel.ConstraintViolated, "Stream", "DecodeParms"},
		{"dict indirect required", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{
				"Type": pdf.PDFName{Value: "Catalog"}, "Pages": directPages,
			}), "Catalog", ctx)
		}, pdf.Checks.ObjectModel.IndirectRequired, "Catalog", "Pages"},
		{"dict post-1.4 key", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"PrintScaling": pdf.PDFName{Value: "AppDefault"}}), "ViewerPreferences", ctx)
		}, pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14, "ViewerPreferences", "PrintScaling"},
		{"wildcard wrong type", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"Im1": pdf.PDFName{Value: "NotAStream"}}), "XObjectMap", ctx)
		}, pdf.Checks.ObjectModel.WrongValueType, "XObjectMap", "Im1"},
		{"wildcard disallowed enum", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"CS0": pdf.PDFName{Value: "BogusColorSpace"}}), "ColorSpaceMap", ctx)
		}, pdf.Checks.ObjectModel.DisallowedValue, "ColorSpaceMap", "CS0"},
		{"wildcard indirect required", func(ctx *ValidationContext) {
			validateAgainstSchema(dict(map[string]pdf.PDFValue{"Im1": directStream}), "XObjectMap", ctx)
		}, pdf.Checks.ObjectModel.IndirectRequired, "XObjectMap", "Im1"},
		{"array missing required element", func(ctx *ValidationContext) {
			validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(612)}, "ArrayOf_4Numbers", pdf.NewPDFDict(), ctx)
		}, pdf.Checks.ObjectModel.MissingRequiredKey, "ArrayOf_4Numbers", "3"},
		{"array element wrong type", func(ctx *ValidationContext) {
			validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFString{Value: "x"}, pdf.PDFInteger(612), pdf.PDFInteger(792)}, "ArrayOf_4Numbers", pdf.NewPDFDict(), ctx)
		}, pdf.Checks.ObjectModel.WrongValueType, "ArrayOf_4Numbers", "1"},
		{"array element disallowed enum", func(ctx *ValidationContext) {
			validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFInteger(2), pdf.PDFName{Value: "XYZ"}, nil}, "Dest1Array", pdf.NewPDFDict(), ctx)
		}, pdf.Checks.ObjectModel.DisallowedValue, "Dest1Array", "1"},
		{"array element range", func(ctx *ValidationContext) {
			validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFReal(-1), pdf.PDFInteger(1), pdf.PDFReal(1.089)}, "WhitepointArray", pdf.NewPDFDict(), ctx)
		}, pdf.Checks.ObjectModel.DisallowedValue, "WhitepointArray", "0"},
		{"array element special case", func(ctx *ValidationContext) {
			validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFReal(0.5), pdf.PDFReal(0.5)}, "ArrayOf_4NumbersColorAnnotation", pdf.NewPDFDict(), ctx)
		}, pdf.Checks.ObjectModel.ConstraintViolated, "ArrayOf_4NumbersColorAnnotation", "1"},
		{"array wildcard element indirect", func(ctx *ValidationContext) {
			validateArrayAgainstSchema(pdf.PDFArray{directPages}, "ArrayOfPageTreeNodeKids", pdf.NewPDFDict(), ctx)
		}, pdf.Checks.ObjectModel.IndirectRequired, "ArrayOfPageTreeNodeKids", "0"},
		{"array wildcard element enum", func(ctx *ValidationContext) {
			validateArrayAgainstSchema(pdf.PDFArray{pdf.PDFName{Value: "BogusDecode"}}, "ArrayOfFilterNames", pdf.NewPDFDict(), ctx)
		}, pdf.Checks.ObjectModel.DisallowedValue, "ArrayOfFilterNames", "0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{}
			tc.run(ctx)
			if !hasDetail(ctx, tc.check, tc.typeName, tc.key) {
				t.Errorf("no %s finding carrying detail {%s, %s}; findings: %v", tc.check.Name(), tc.typeName, tc.key, ctx.errs)
			}
		})
	}
}
