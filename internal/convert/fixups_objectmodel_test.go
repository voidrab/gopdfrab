package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

func TestIndirectRequiredFixerPromotesDirectDicts(t *testing.T) {
	trailer := pdf.NewPDFDict()
	info := pdf.NewPDFDict()
	info.Entries["Title"] = pdf.PDFString{Value: "T"}
	trailer.Entries["Info"] = info
	custom := pdf.NewPDFDict()
	trailer.Entries["Custom"] = custom

	changed, err := indirectRequiredFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix: changed=%v err=%v, want changed", changed, err)
	}
	if _, ok := info.Entries["_ref"].(pdf.PDFRef); !ok {
		t.Error("direct trailer Info was not promoted to an indirect object")
	}
	if _, ok := custom.Entries["_ref"]; ok {
		t.Error("a key outside the indirect-required set must stay direct")
	}

	changed, err = indirectRequiredFixer{}.Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("second Fix: changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

func TestDescentSignFixerNegatesPositiveDescent(t *testing.T) {
	trailer := pdf.NewPDFDict()
	fd := pdf.NewPDFDict()
	fd.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	fd.Entries["Descent"] = pdf.PDFInteger(205)
	trailer.Entries["FD"] = fd
	fdReal := pdf.NewPDFDict()
	fdReal.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	fdReal.Entries["Descent"] = pdf.PDFReal(12.5)
	trailer.Entries["FDReal"] = fdReal
	other := pdf.NewPDFDict()
	other.Entries["Descent"] = pdf.PDFInteger(300) // not a FontDescriptor
	trailer.Entries["Other"] = other

	changed, err := descentSignFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix: changed=%v err=%v, want changed", changed, err)
	}
	if got := fd.Entries["Descent"]; got != pdf.PDFInteger(-205) {
		t.Errorf("integer Descent = %v, want -205", got)
	}
	if got := fdReal.Entries["Descent"]; got != pdf.PDFReal(-12.5) {
		t.Errorf("real Descent = %v, want -12.5", got)
	}
	if got := other.Entries["Descent"]; got != pdf.PDFInteger(300) {
		t.Errorf("non-descriptor Descent = %v, want untouched 300", got)
	}

	changed, err = descentSignFixer{}.Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("second Fix: changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

// objModelIssue builds a MissingRequiredKey finding against ref carrying the
// given Arlington detail.
func objModelIssue(c pdf.Check, ref *pdf.PDFRef, typeName, key string) pdf.PDFError {
	e := pdf.NewError(c, nil, 0, ref)
	return e.WithObjModelDetail(pdf.ObjModelDetail{TypeName: typeName, Key: key})
}

func TestMissingRequiredKeyFixerInjectsSingleEnum(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog
	ref := pdf.PDFRef{ObjNum: 1}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{1: catalog}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &ref, "Catalog", "Type")}

	changed, handled, err := missingRequiredKeyFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if got := catalog.Entries["Type"]; !pdf.EqualPDFValue(got, pdf.PDFName{Value: "Catalog"}) {
		t.Errorf("Type = %v, want /Catalog", got)
	}

	changed, handled, err = missingRequiredKeyFixer{}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Errorf("second fixTargeted = (%v, %v, %v), want idempotent (false, true, nil)", changed, handled, err)
	}
}

func TestMissingRequiredKeyFixerInjectsPinnedValue(t *testing.T) {
	// EncryptionStandard.R is pinned to 2 when V < 2.
	enc := pdf.NewPDFDict()
	enc.Entries["V"] = pdf.PDFInteger(1)
	enc.Entries["R"] = nil // an explicit null is equivalent to absent and must be overwritten
	enc.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Encrypt"] = enc
	ref := pdf.PDFRef{ObjNum: 3}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{3: enc}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &ref, "EncryptionStandard", "R")}

	changed, _, err := missingRequiredKeyFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed {
		t.Fatalf("fixTargeted = (%v, _, %v), want changed", changed, err)
	}
	if got := enc.Entries["R"]; got != pdf.PDFInteger(2) {
		t.Errorf("R = %v, want 2 (pinned for V=1)", got)
	}
}

func TestMissingRequiredKeyFixerSkipsUnsynthesizable(t *testing.T) {
	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 2}
	trailer := pdf.NewPDFDict()
	trailer.Entries["P"] = page
	pageRef := pdf.PDFRef{ObjNum: 2}
	staleRef := pdf.PDFRef{ObjNum: 9}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{2: page}}

	plain := pdf.NewError(pdf.Checks.ObjectModel.MissingRequiredKey, nil, 0, &pageRef)
	issues := []pdf.PDFError{
		// Parent has no single legal value: required, but never synthesizable.
		objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &pageRef, "PageObject", "Parent"),
		// A missing array element is never synthesized.
		objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &pageRef, "ArrayOf_4Numbers", "3"),
		// Unknown type and unknown key fail closed.
		objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &pageRef, "NoSuchType", "Type"),
		objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &pageRef, "Catalog", "NoSuchKey"),
		// Already-present key (repaired earlier): stale finding.
		objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &pageRef, "PageObject", "Type"),
		// No detail, and an unresolvable ref.
		plain,
		objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &staleRef, "Catalog", "Type"),
		objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, nil, "Catalog", "Type"),
	}

	changed, handled, err := missingRequiredKeyFixer{}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (false, true, nil)", changed, handled, err)
	}
	if _, ok := page.Entries["Parent"]; ok {
		t.Error("Parent must not be synthesized")
	}
	if _, ok := page.Entries["3"]; ok {
		t.Error("an array-element key must never be injected into the owner dict")
	}

	if got, err := (missingRequiredKeyFixer{}).Fix(&trailer, nil); got || err != nil {
		t.Errorf("Fix = (%v, %v), want targeted-only no-op", got, err)
	}
}

func TestScalarFromEnum(t *testing.T) {
	cases := []struct {
		s     string
		types []arlington.ValueType
		want  pdf.PDFValue
		ok    bool
	}{
		{"Catalog", []arlington.ValueType{arlington.Name}, pdf.PDFName{Value: "Catalog"}, true},
		{"3", []arlington.ValueType{arlington.Integer}, pdf.PDFInteger(3), true},
		{"8", []arlington.ValueType{arlington.Bitmask}, pdf.PDFInteger(8), true},
		{"true", []arlington.ValueType{arlington.Boolean}, pdf.PDFBoolean(true), true},
		{"false", []arlington.ValueType{arlington.Boolean}, pdf.PDFBoolean(false), true},
		{"x", []arlington.ValueType{arlington.Boolean}, nil, false},
		{"x", []arlington.ValueType{arlington.Integer, arlington.Name}, pdf.PDFName{Value: "x"}, true},
		{"1.5", []arlington.ValueType{arlington.Number}, nil, false},
		{"x", nil, nil, false},
	}
	for _, tc := range cases {
		got, ok := scalarFromEnum(tc.s, tc.types)
		if ok != tc.ok || (ok && !pdf.EqualPDFValue(got, tc.want)) {
			t.Errorf("scalarFromEnum(%q, %v) = (%v, %v), want (%v, %v)", tc.s, tc.types, got, ok, tc.want, tc.ok)
		}
	}
}

// TestConvertObjectModelInjectsMissingType proves the fixer end to end: a
// catalog without /Type converts to a fully valid object-model rewrite.
func TestConvertObjectModelInjectsMissingType(t *testing.T) {
	data := buildOnePageDoc(t, func(_, catalog, _ pdf.PDFDict) {
		delete(catalog.Entries, "Type")
	})

	res, err := verify.VerifyBytes(data, pdf.PDF)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Fatalf("fixture must fail with MissingRequiredKey, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF)
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}
}
