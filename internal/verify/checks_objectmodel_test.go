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
	if got := resolveLinkGroups(nextKey.LinkGroups, pdf.PDFName{Value: "X"}); got != "" {
		t.Errorf("resolveLinkGroups(Next, a name) = %q, want \"\" (no matching group)", got)
	}
	if got := resolveLinkGroups(nextKey.LinkGroups, pdf.PDFArray{}); got != "ArrayOfActions" {
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
	if got := resolveLinkGroups(groups, pdf.PDFName{Value: "X"}); got != "" {
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
