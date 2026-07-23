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

func TestIndirectRequiredFixerPromotesArrayElements(t *testing.T) {
	trailer := pdf.NewPDFDict()
	owner := pdf.NewPDFDict()
	direct := pdf.NewPDFDict()
	direct.Entries["Type"] = pdf.PDFName{Value: "Page"}
	already := pdf.NewPDFDict()
	already.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	owner.Entries["Kids"] = pdf.PDFArray{direct, already, pdf.PDFInteger(1), pdf.PDFDict{}}
	unlisted := pdf.NewPDFDict()
	owner.Entries["ZZ"] = pdf.PDFArray{unlisted}
	owner.Entries["Count"] = pdf.PDFInteger(2) // non-array under a non-listed key
	trailer.Entries["P"] = owner

	changed, err := indirectRequiredFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix: changed=%v err=%v, want changed", changed, err)
	}
	if _, ok := direct.Entries["_ref"].(pdf.PDFRef); !ok {
		t.Error("a direct dict inside an indirect-required array was not promoted")
	}
	if got := already.Entries["_ref"]; got != (pdf.PDFRef{ObjNum: 3}) {
		t.Errorf("an already-indirect element's ref = %v, want untouched 3", got)
	}
	if _, ok := unlisted.Entries["_ref"]; ok {
		t.Error("an array under a key outside the element-indirect set must stay direct")
	}

	changed, err = indirectRequiredFixer{}.Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("second Fix: changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

func TestIndirectRequiredFixerTargetedWildcardKey(t *testing.T) {
	// A resource XObject map's entries are wildcard rows -- arbitrary key
	// names the whole-graph tables can never cover.
	xmap := pdf.NewPDFDict()
	direct := pdf.NewPDFDict()
	xmap.Entries["Fx0"] = direct
	xmap.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["XMap"] = xmap
	ref := pdf.PDFRef{ObjNum: 5}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: xmap}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.IndirectRequired, &ref, "XObjectMap", "Fx0")}

	changed, handled, err := indirectRequiredFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if _, ok := direct.Entries["_ref"].(pdf.PDFRef); !ok {
		t.Error("the wildcard-keyed direct dict was not promoted")
	}

	changed, _, err = indirectRequiredFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

func TestIndirectRequiredFixerTargetedSkips(t *testing.T) {
	xmap := pdf.NewPDFDict()
	xmap.Entries["Scalar"] = pdf.PDFInteger(1)
	xmap.Entries["NilDict"] = pdf.PDFDict{}
	already := pdf.NewPDFDict()
	already.Entries["_ref"] = pdf.PDFRef{ObjNum: 7}
	xmap.Entries["Already"] = already
	xmap.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["XMap"] = xmap
	ref := pdf.PDFRef{ObjNum: 5}
	dead := pdf.PDFRef{ObjNum: 9}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: xmap}}

	c := pdf.Checks.ObjectModel.IndirectRequired
	issues := []pdf.PDFError{
		objModelIssue(c, &ref, "ArrayOfPageTreeNodeKids", "0"), // array element: whole-graph territory
		pdf.NewError(c, nil, 0, &ref),                          // no detail
		objModelIssue(c, nil, "XObjectMap", "Fx0"),             // no ref
		objModelIssue(c, &dead, "XObjectMap", "Fx0"),           // dead ref
		objModelIssue(c, &ref, "NoSuchType", "Fx0"),            // unknown type
		objModelIssue(c, &ref, "DocInfo", "Custom"),            // wildcard row not indirect-required
		objModelIssue(c, &ref, "XObjectMap", "Scalar"),         // non-dict child
		objModelIssue(c, &ref, "XObjectMap", "NilDict"),        // nil-Entries child
		objModelIssue(c, &ref, "XObjectMap", "Already"),        // already indirect
	}
	changed, handled, err := indirectRequiredFixer{}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (false, true, nil)", changed, handled, err)
	}
	if got := already.Entries["_ref"]; got != (pdf.PDFRef{ObjNum: 7}) {
		t.Errorf("Already ref = %v, want untouched 7", got)
	}
}

// TestConvertObjectModelPromotesDirectKid proves the array-element promotion
// end to end: a page stored directly inside its Pages /Kids array converts to
// a fully valid rewrite.
func TestConvertObjectModelPromotesDirectKid(t *testing.T) {
	data := buildOnePageDoc(t, func(_, _, page pdf.PDFDict) {
		delete(page.Entries, "_ref")
	})

	res, err := verify.VerifyBytes(data, pdf.PDF, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.IndirectRequired) {
		t.Fatalf("fixture must fail with IndirectRequired, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}
}

func TestArrayElemTargetFailsClosed(t *testing.T) {
	owner := pdf.NewPDFDict()
	owner.Entries["Arr"] = pdf.PDFArray{pdf.PDFInteger(1)}
	owner.Entries["NotArr"] = pdf.PDFInteger(1)
	owner.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["O"] = owner
	ref := pdf.PDFRef{ObjNum: 5}
	dead := pdf.PDFRef{ObjNum: 9}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: owner}}

	c := pdf.Checks.ObjectModel.WrongValueType
	for name, iss := range map[string]pdf.PDFError{
		"no detail":         pdf.NewError(c, nil, 0, &ref),
		"empty entry":       objModelIssue(c, &ref, "ArrayOf_4Numbers", "0"),
		"non-index key":     objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "Arr", "NotAnIndex"),
		"no ref":            objModelElemIssue(c, nil, "ArrayOf_4Numbers", "Arr", "0"),
		"dead ref":          objModelElemIssue(c, &dead, "ArrayOf_4Numbers", "Arr", "0"),
		"entry not array":   objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "NotArr", "0"),
		"entry absent":      objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "Absent", "0"),
		"index out of rang": objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "Arr", "7"),
		"unknown type":      objModelElemIssue(c, &ref, "NoSuchType", "Arr", "0"),
		"no governing row":  objModelElemIssue(c, &ref, "Catalog", "Arr", "0"),
	} {
		if _, _, _, ok := arrayElemTarget(pass, iss); ok {
			t.Errorf("%s: arrayElemTarget must fail closed", name)
		}
	}
}

func TestWrongValueTypeFixerCoercesArrayElement(t *testing.T) {
	page := pdf.NewPDFDict()
	page.Entries["MediaBox"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFString{Value: "0"}, pdf.PDFInteger(612), pdf.NewPDFDict()}
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	trailer := pdf.NewPDFDict()
	trailer.Entries["P"] = page
	ref := pdf.PDFRef{ObjNum: 3}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{3: page}}
	c := pdf.Checks.ObjectModel.WrongValueType
	issues := []pdf.PDFError{
		objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "MediaBox", "1"),
		objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "MediaBox", "0"), // stale: already a number
		objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "MediaBox", "3"), // uncoercible dict: residual
	}

	changed, handled, err := wrongValueTypeFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	arr := page.Entries["MediaBox"].(pdf.PDFArray)
	if !pdf.EqualPDFValue(arr[1], pdf.PDFReal(0)) {
		t.Errorf("element 1 = %v, want coerced 0", arr[1])
	}
	if arr[0] != pdf.PDFInteger(0) {
		t.Errorf("element 0 = %v, want untouched", arr[0])
	}
	if _, isDict := arr[3].(pdf.PDFDict); !isDict {
		t.Error("an uncoercible element must never be deleted or nulled")
	}

	changed, _, err = wrongValueTypeFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

func TestDisallowedValueFixerRepairsArrayElements(t *testing.T) {
	owner := pdf.NewPDFDict()
	owner.Entries["CS"] = pdf.PDFArray{pdf.PDFName{Value: "Bogus"}, pdf.PDFName{Value: "DeviceRGB"}, pdf.PDFInteger(300), pdf.PDFString{Value: "x"}}
	owner.Entries["Gamma"] = pdf.PDFArray{pdf.PDFReal(-1), pdf.PDFReal(1), pdf.PDFReal(1)}
	owner.Entries["WP"] = pdf.PDFArray{pdf.PDFReal(-1), pdf.PDFInteger(2), pdf.PDFReal(1.089), nil}
	owner.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["O"] = owner
	ref := pdf.PDFRef{ObjNum: 5}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: owner}}
	c := pdf.Checks.ObjectModel.DisallowedValue
	issues := []pdf.PDFError{
		objModelElemIssue(c, &ref, "IndexedColorSpace", "CS", "0"), // single enum -> /Indexed
		objModelElemIssue(c, &ref, "IndexedColorSpace", "CS", "2"), // hival 300 -> clamp 255
		objModelElemIssue(c, &ref, "GammaArray", "Gamma", "0"),     // -1 -> clamp 0
		objModelElemIssue(c, &ref, "WhitepointArray", "WP", "0"),   // strict > 0: unclampable residual
		objModelElemIssue(c, &ref, "WhitepointArray", "WP", "1"),   // pinned numeric enum: uncoercible residual
		objModelElemIssue(c, &ref, "WhitepointArray", "WP", "3"),   // null element: never repaired
		objModelElemIssue(c, &ref, "GammaArray", "Gamma", "1"),     // stale: already legal
	}

	changed, handled, err := disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	cs := owner.Entries["CS"].(pdf.PDFArray)
	if !pdf.EqualPDFValue(cs[0], pdf.PDFName{Value: "Indexed"}) {
		t.Errorf("CS[0] = %v, want /Indexed", cs[0])
	}
	if cs[2] != pdf.PDFInteger(255) {
		t.Errorf("CS[2] = %v, want clamped 255", cs[2])
	}
	gamma := owner.Entries["Gamma"].(pdf.PDFArray)
	if gamma[0] != pdf.PDFReal(0) {
		t.Errorf("Gamma[0] = %v, want clamped 0", gamma[0])
	}
	wp := owner.Entries["WP"].(pdf.PDFArray)
	if wp[0] != pdf.PDFReal(-1) || wp[1] != pdf.PDFInteger(2) || wp[3] != nil {
		t.Errorf("WP = %v, want untouched residuals", wp)
	}

	changed, _, err = disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

func TestIndirectRequiredFixerPromotesTargetedArrayElement(t *testing.T) {
	// An indirect-required wildcard array under an arbitrary dict key: the
	// whole-graph name table cannot see it, only the targeted element path.
	owner := pdf.NewPDFDict()
	direct := pdf.NewPDFDict()
	owner.Entries["MyArr"] = pdf.PDFArray{direct, pdf.PDFInteger(1)}
	owner.Entries["Nums"] = pdf.PDFArray{pdf.PDFInteger(1)}
	owner.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["O"] = owner
	ref := pdf.PDFRef{ObjNum: 5}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: owner}}
	c := pdf.Checks.ObjectModel.IndirectRequired
	issues := []pdf.PDFError{
		objModelElemIssue(c, &ref, "ArrayOfPageTreeNodeKids", "MyArr", "0"),
		objModelElemIssue(c, &ref, "ArrayOfPageTreeNodeKids", "MyArr", "1"), // non-dict element
		objModelElemIssue(c, &ref, "GammaArray", "Nums", "0"),               // row not indirect-required
		objModelElemIssue(c, &ref, "ArrayOf_4Numbers", "Absent", "0"),       // unresolvable target
	}

	changed, handled, err := indirectRequiredFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if _, ok := direct.Entries["_ref"].(pdf.PDFRef); !ok {
		t.Error("the direct array element was not promoted")
	}

	changed, _, err = indirectRequiredFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

// TestConvertObjectModelClampsIndexedHival proves the element repair end to
// end: an Indexed colour space with hival 300 (legal range 0..255) converts
// to a fully valid rewrite with the page content untouched. The chain under
// test: array-first-element discrimination types the array, the finding
// carries the owner entry, and the fixer clamps the element in place.
func TestConvertObjectModelClampsIndexedHival(t *testing.T) {
	data := buildOnePageDoc(t, func(_, _, page pdf.PDFDict) {
		csMap := pdf.NewPDFDict()
		csMap.Entries["CS0"] = pdf.PDFArray{
			pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: "DeviceRGB"},
			pdf.PDFInteger(300), pdf.PDFString{Value: "lookup"},
		}
		csMap.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
		resources := pdf.NewPDFDict()
		resources.Entries["ColorSpace"] = csMap
		page.Entries["Resources"] = resources
	})

	res, err := verify.VerifyBytes(data, pdf.PDF, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.DisallowedValue) {
		t.Fatalf("fixture must fail with DisallowedValue, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}

	out, err := pdf.OpenBytes(cr.Output)
	if err != nil {
		t.Fatalf("OpenBytes(output): %v", err)
	}
	defer out.Close()
	graph, err := out.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph(output): %v", err)
	}
	page := assertOnePageGraph(t, graph)
	cs := page.Entries["Resources"].(pdf.PDFDict).Entries["ColorSpace"].(pdf.PDFDict).Entries["CS0"].(pdf.PDFArray)
	if cs[2] != pdf.PDFInteger(255) {
		t.Errorf("hival = %v, want clamped 255", cs[2])
	}
	assertContentStream(t, page, onePageContent)
}

func TestPost14KeyFixerAppliesExclusively(t *testing.T) {
	fixer := post14KeyFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want)
		}
	}
}

func TestPost14KeyFixerDeletesReportedKeys(t *testing.T) {
	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["UserUnit"] = pdf.PDFReal(2)
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	trailer := pdf.NewPDFDict()
	trailer.Entries["P"] = page
	ref := pdf.PDFRef{ObjNum: 3}
	dead := pdf.PDFRef{ObjNum: 9}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{3: page}}

	c := pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14
	issues := []pdf.PDFError{
		objModelIssue(c, &ref, "PageObject", "UserUnit"),
		// Skip cases: _ref and array-index detail keys, no detail, no ref,
		// dead ref, already-absent key.
		objModelIssue(c, &ref, "PageObject", "_ref"),
		objModelIssue(c, &ref, "ArrayOf_4Numbers", "2"),
		pdf.NewError(c, nil, 0, &ref),
		objModelIssue(c, nil, "PageObject", "UserUnit"),
		objModelIssue(c, &dead, "PageObject", "UserUnit"),
		objModelIssue(c, &ref, "PageObject", "Absent"),
	}
	changed, handled, err := post14KeyFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if _, ok := page.Entries["UserUnit"]; ok {
		t.Error("the reported post-1.4 key must be deleted")
	}
	if _, ok := page.Entries["_ref"].(pdf.PDFRef); !ok {
		t.Error("_ref must never be deleted")
	}

	changed, _, err = post14KeyFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op", changed, err)
	}
	if got, err := (post14KeyFixer{}).Fix(&trailer, nil); got || err != nil {
		t.Errorf("Fix = (%v, %v), want targeted-only no-op", got, err)
	}
}

// TestConvertObjectModelDeletesPost14Key proves the fixer end to end: a page
// carrying a post-1.4 key (/UserUnit, PDF 1.6) converts to a fully valid
// rewrite with the key removed and the page content untouched.
func TestConvertObjectModelDeletesPost14Key(t *testing.T) {
	data := buildOnePageDoc(t, func(_, _, page pdf.PDFDict) {
		page.Entries["UserUnit"] = pdf.PDFReal(2)
	})

	res, err := verify.VerifyBytes(data, pdf.PDF, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.KeyIntroducedAfterPDF14) {
		t.Fatalf("fixture must fail with KeyIntroducedAfterPDF14, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}

	out, err := pdf.OpenBytes(cr.Output)
	if err != nil {
		t.Fatalf("OpenBytes(output): %v", err)
	}
	defer out.Close()
	graph, err := out.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph(output): %v", err)
	}
	page := assertOnePageGraph(t, graph)
	if _, still := page.Entries["UserUnit"]; still {
		t.Error("UserUnit must be deleted from the output page")
	}
	assertContentStream(t, page, onePageContent)
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

	changed, err := disallowedValueFixer{}.Fix(&trailer, nil)
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

	changed, err = disallowedValueFixer{}.Fix(&trailer, nil)
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

// objModelElemIssue builds an array-element finding: entry is the owner dict's key holding
// the array, key the decimal element index.
func objModelElemIssue(c pdf.Check, ref *pdf.PDFRef, typeName, entry, key string) pdf.PDFError {
	e := pdf.NewError(c, nil, 0, ref)
	return e.WithObjModelDetail(pdf.ObjModelDetail{TypeName: typeName, Key: key, Entry: entry})
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

	res, err := verify.VerifyBytes(data, pdf.PDF, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.MissingRequiredKey) {
		t.Fatalf("fixture must fail with MissingRequiredKey, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}
}

func TestCoerceScalar(t *testing.T) {
	name := []arlington.ValueType{arlington.Name}
	str := []arlington.ValueType{arlington.StringText}
	integer := []arlington.ValueType{arlington.Integer}
	number := []arlington.ValueType{arlington.Number}
	boolean := []arlington.ValueType{arlington.Boolean}

	cases := []struct {
		val   pdf.PDFValue
		types []arlington.ValueType
		want  pdf.PDFValue
		ok    bool
	}{
		{pdf.PDFReal(90), integer, pdf.PDFInteger(90), true},
		{pdf.PDFReal(90.5), integer, nil, false},
		{pdf.PDFString{Value: " 7 "}, integer, pdf.PDFInteger(7), true},
		{pdf.PDFString{Value: "1.5"}, number, pdf.PDFReal(1.5), true},
		{pdf.PDFString{Value: "x"}, number, nil, false},
		{pdf.PDFString{Value: "True"}, name, pdf.PDFName{Value: "True"}, true},
		{pdf.PDFHexString{Value: "True"}, name, pdf.PDFName{Value: "True"}, true},
		{pdf.PDFName{Value: "T"}, str, pdf.PDFString{Value: "T"}, true},
		{pdf.PDFName{Value: "true"}, boolean, pdf.PDFBoolean(true), true},
		{pdf.PDFString{Value: "false"}, boolean, pdf.PDFBoolean(false), true},
		{pdf.PDFInteger(1), boolean, nil, false},
		{pdf.PDFArray{}, name, nil, false},
		{pdf.PDFName{Value: "D"}, []arlington.ValueType{arlington.Date}, nil, false},
	}
	for _, tc := range cases {
		got, ok := coerceScalar(tc.val, tc.types)
		if ok != tc.ok || (ok && !pdf.EqualPDFValue(got, tc.want)) {
			t.Errorf("coerceScalar(%v, %v) = (%v, %v), want (%v, %v)", tc.val, tc.types, got, ok, tc.want, tc.ok)
		}
	}
}

func TestWrongValueTypeFixerCoercesAndDeletes(t *testing.T) {
	info := pdf.NewPDFDict()
	info.Entries["Trapped"] = pdf.PDFString{Value: "True"} // named row: name
	info.Entries["CustomName"] = pdf.PDFName{Value: "V"}   // wildcard row: text string
	info.Entries["CustomDict"] = pdf.NewPDFDict()          // uncoercible optional: delete
	info.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Info"] = info
	ref := pdf.PDFRef{ObjNum: 5}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: info}}
	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "DocInfo", "Trapped"),
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "DocInfo", "CustomName"),
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "DocInfo", "CustomDict"),
	}

	changed, handled, err := wrongValueTypeFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if got := info.Entries["Trapped"]; !pdf.EqualPDFValue(got, pdf.PDFName{Value: "True"}) {
		t.Errorf("Trapped = %v, want /True", got)
	}
	if got := info.Entries["CustomName"]; !pdf.EqualPDFValue(got, pdf.PDFString{Value: "V"}) {
		t.Errorf("CustomName = %v, want (V)", got)
	}
	if _, ok := info.Entries["CustomDict"]; ok {
		t.Error("uncoercible optional key must be deleted")
	}

	changed, _, err = wrongValueTypeFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op (values now conform)", changed, err)
	}
}

func TestWrongValueTypeFixerNeverDeletesRequiredOrConditional(t *testing.T) {
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["Pages"] = pdf.PDFInteger(3) // required, uncoercible
	catalog.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}
	tr := pdf.NewPDFDict()
	tr.Entries["Root"] = catalog
	tr.Entries["ID"] = pdf.PDFInteger(4) // FileTrailer.ID: required-when-Encrypt, uncoercible
	tr.Entries["_ref"] = pdf.PDFRef{ObjNum: 8}
	cref := pdf.PDFRef{ObjNum: 1}
	tref := pdf.PDFRef{ObjNum: 8}
	pass := &fixPass{trailer: &tr, objs: map[int]pdf.PDFValue{1: catalog, 8: tr}}
	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &cref, "Catalog", "Pages"),
	}
	tissues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &tref, "FileTrailer", "ID"),
	}

	changed, handled, err := wrongValueTypeFixer{}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (false, true, nil)", changed, handled, err)
	}
	if _, ok := catalog.Entries["Pages"]; !ok {
		t.Error("a required key must never be deleted")
	}
	if changed, _, _ := (wrongValueTypeFixer{}).fixTargeted(pass, tissues); changed {
		t.Error("a conditionally-required key must never be deleted")
	}
	if _, ok := tr.Entries["ID"]; !ok {
		t.Error("FileTrailer.ID must survive")
	}
}

func TestWrongValueTypeFixerSkipsStaleAndUntargetable(t *testing.T) {
	info := pdf.NewPDFDict()
	info.Entries["Trapped"] = pdf.PDFName{Value: "True"} // already conformant
	info.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Info"] = info
	ref := pdf.PDFRef{ObjNum: 5}
	stale := pdf.PDFRef{ObjNum: 9}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: info}}

	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "DocInfo", "Trapped"),    // conforms
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "DocInfo", "Absent"),     // absent key
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "ArrayOf_4Numbers", "1"), // array element
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "NoSuchType", "X"),       // unknown type
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &ref, "Catalog", "CustomKey"),  // no wildcard row
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, &stale, "DocInfo", "Trapped"),  // dead ref
		objModelIssue(pdf.Checks.ObjectModel.WrongValueType, nil, "DocInfo", "Trapped"),     // no ref
		pdf.NewError(pdf.Checks.ObjectModel.WrongValueType, nil, 0, &ref),                   // no detail
	}
	changed, handled, err := wrongValueTypeFixer{}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (false, true, nil)", changed, handled, err)
	}
	if got, err := (wrongValueTypeFixer{}).Fix(&trailer, nil); got || err != nil {
		t.Errorf("Fix = (%v, %v), want targeted-only no-op", got, err)
	}
}

// TestConvertObjectModelCoercesRotate proves the fixer end to end: a page
// whose /Rotate is stored as a string converts to a fully valid rewrite.
func TestConvertObjectModelCoercesRotate(t *testing.T) {
	data := buildOnePageDoc(t, func(_, _, page pdf.PDFDict) {
		page.Entries["Rotate"] = pdf.PDFString{Value: "90"}
	})

	res, err := verify.VerifyBytes(data, pdf.PDF, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.WrongValueType) {
		t.Fatalf("fixture must fail with WrongValueType, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}
}

func TestDisallowedValueFixerReplacesSingleEnum(t *testing.T) {
	meta := pdf.NewPDFDict()
	meta.HasStream = true
	meta.Entries["Type"] = pdf.PDFName{Value: "Bogus"} // Metadata.Type has the single legal value /Metadata
	meta.Entries["Subtype"] = pdf.PDFName{Value: "XML"}
	meta.Entries["_ref"] = pdf.PDFRef{ObjNum: 6}
	trailer := pdf.NewPDFDict()
	trailer.Entries["M"] = meta
	ref := pdf.PDFRef{ObjNum: 6}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{6: meta}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "Metadata", "Type")}

	changed, handled, err := disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if got := meta.Entries["Type"]; !pdf.EqualPDFValue(got, pdf.PDFName{Value: "Metadata"}) {
		t.Errorf("Type = %v, want /Metadata", got)
	}

	changed, _, err = disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

func TestDisallowedValueFixerEnforcesPinnedValue(t *testing.T) {
	// EncryptionStandard.R must be 3 when V is 2.
	enc := pdf.NewPDFDict()
	enc.Entries["V"] = pdf.PDFInteger(2)
	enc.Entries["R"] = pdf.PDFInteger(2)
	enc.Entries["_ref"] = pdf.PDFRef{ObjNum: 7}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Encrypt"] = enc
	ref := pdf.PDFRef{ObjNum: 7}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{7: enc}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "EncryptionStandard", "R")}

	changed, _, err := disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed {
		t.Fatalf("fixTargeted = (%v, _, %v), want changed", changed, err)
	}
	if got := enc.Entries["R"]; got != pdf.PDFInteger(3) {
		t.Errorf("R = %v, want pinned 3", got)
	}

	// R=9 with V absent: pins undecidable, the multi-value enum has no single
	// replacement, and R is required -- must stay a residual.
	enc2 := pdf.NewPDFDict()
	enc2.Entries["R"] = pdf.PDFInteger(9)
	enc2.Entries["_ref"] = pdf.PDFRef{ObjNum: 8}
	pass = &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{8: enc2}}
	ref2 := pdf.PDFRef{ObjNum: 8}
	issues = []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref2, "EncryptionStandard", "R")}
	changed, _, err = disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("fixTargeted changed=%v err=%v, want required key left as residual", changed, err)
	}
	if got := enc2.Entries["R"]; got != pdf.PDFInteger(9) {
		t.Errorf("R = %v, want untouched 9", got)
	}
}

func TestDisallowedValueFixerClampsRanges(t *testing.T) {
	gs := pdf.NewPDFDict()
	gs.Entries["CA"] = pdf.PDFReal(1.5)
	gs.Entries["ca"] = pdf.PDFReal(-0.25)
	gs.Entries["_ref"] = pdf.PDFRef{ObjNum: 9}
	trailer := pdf.NewPDFDict()
	trailer.Entries["G"] = gs
	ref := pdf.PDFRef{ObjNum: 9}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{9: gs}}
	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "GraphicsStateParameter", "CA"),
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "GraphicsStateParameter", "ca"),
	}

	changed, _, err := disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed {
		t.Fatalf("fixTargeted = (%v, _, %v), want changed", changed, err)
	}
	if got := gs.Entries["CA"]; got != pdf.PDFReal(1) {
		t.Errorf("CA = %v, want clamped 1", got)
	}
	if got := gs.Entries["ca"]; got != pdf.PDFReal(0) {
		t.Errorf("ca = %v, want clamped 0", got)
	}
}

func TestDisallowedValueFixerNegatesUntypedDescent(t *testing.T) {
	// A descriptor without /Type is invisible to the whole-graph pass; the
	// targeted path must still prefer negation over clamping to zero.
	fd := pdf.NewPDFDict()
	fd.Entries["Descent"] = pdf.PDFInteger(205)
	fd.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}
	trailer := pdf.NewPDFDict()
	trailer.Entries["FD"] = fd
	ref := pdf.PDFRef{ObjNum: 4}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{4: fd}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "FontDescriptorType1", "Descent")}

	changed, _, err := disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed {
		t.Fatalf("fixTargeted = (%v, _, %v), want changed", changed, err)
	}
	if got := fd.Entries["Descent"]; got != pdf.PDFInteger(-205) {
		t.Errorf("Descent = %v, want -205 (negated, not clamped)", got)
	}
}

func TestDisallowedValueFixerDeletesOptionalEnum(t *testing.T) {
	info := pdf.NewPDFDict()
	info.Entries["Trapped"] = pdf.PDFName{Value: "Maybe"}
	info.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Info"] = info
	ref := pdf.PDFRef{ObjNum: 5}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: info}}
	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "DocInfo", "Trapped"),
		// Skip cases sharing the pass: stale null, array element, unknown type, no ref/detail.
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "DocInfo", "Absent"),
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "WhitepointArray", "0"),
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "NoSuchType", "X"),
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, nil, "DocInfo", "Trapped"),
		pdf.NewError(pdf.Checks.ObjectModel.DisallowedValue, nil, 0, &ref),
	}

	changed, handled, err := disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if _, ok := info.Entries["Trapped"]; ok {
		t.Error("an optional multi-enum violation must be deleted")
	}
}

func TestCondBoundsAndClamp(t *testing.T) {
	ge := arlington.Cond{Op: arlington.CondGe, Key: "K", Value: "0"}
	le := arlington.Cond{Op: arlington.CondLe, Key: "K", Value: "10"}
	and := arlington.Cond{Op: arlington.CondAnd, Kids: []arlington.Cond{ge, le}}

	if nv, ok := clampToBounds(pdf.PDFInteger(50), &and, "K"); !ok || nv != pdf.PDFInteger(10) {
		t.Errorf("clamp(50) = (%v, %v), want (10, true)", nv, ok)
	}
	if nv, ok := clampToBounds(pdf.PDFReal(-3), &and, "K"); !ok || nv != pdf.PDFReal(0) {
		t.Errorf("clamp(-3) = (%v, %v), want (0, true)", nv, ok)
	}
	if _, ok := clampToBounds(pdf.PDFInteger(5), &and, "K"); ok {
		t.Error("an in-bounds value must not clamp")
	}
	if _, ok := clampToBounds(pdf.PDFName{Value: "x"}, &and, "K"); ok {
		t.Error("a non-numeric value must not clamp")
	}

	// Strict, derived, mismatched, and modulo leaves contribute no bounds.
	for _, c := range []arlington.Cond{
		{Op: arlington.CondGt, Key: "K", Value: "0"},
		{Op: arlington.CondGe, Key: "Other", Value: "0"},
		{Op: arlington.CondGe, Key: "K", Fn: arlington.FnArrayLength, Value: "0"},
		{Op: arlington.CondGe, Key: "K", RHSKey: "L"},
		{Op: arlington.CondEq, Key: "K", Mod: 8, Value: "0"},
		{Op: arlington.CondGe, Key: "K", Value: "notanumber"},
	} {
		if _, ok := clampToBounds(pdf.PDFInteger(-1), &c, "K"); ok {
			t.Errorf("cond %+v must contribute no bounds", c)
		}
	}
}

// TestRepairDisallowedValueFallbacks drives the deletion fallbacks no real
// model row reaches: an uncoercible pin value and an unclampable range.
func TestRepairDisallowedValueFallbacks(t *testing.T) {
	d := pdf.NewPDFDict()
	d.Entries["X"] = pdf.PDFInteger(1)
	d.Entries["K"] = pdf.PDFInteger(5)
	kd := &arlington.KeyDef{
		Name:  "K",
		Types: []arlington.ValueType{arlington.Integer},
		PinnedValues: []arlington.PinnedValue{{
			When:  &arlington.Cond{Op: arlington.CondPresent, Key: "X"},
			Value: "notaninteger",
		}},
	}
	if !repairDisallowedValue(d, "K", kd) {
		t.Fatal("an uncoercible pin on an optional key must fall back to deletion")
	}
	if _, ok := d.Entries["K"]; ok {
		t.Error("K must be deleted")
	}

	d.Entries["K"] = pdf.PDFInteger(-1)
	kd = &arlington.KeyDef{
		Name:      "K",
		Types:     []arlington.ValueType{arlington.Integer},
		ValueCond: &arlington.Cond{Op: arlington.CondGt, Key: "K", Value: "0"},
	}
	if !repairDisallowedValue(d, "K", kd) {
		t.Fatal("a strict, unclampable range on an optional key must fall back to deletion")
	}
	if _, ok := d.Entries["K"]; ok {
		t.Error("K must be deleted after the range fallback")
	}
}

// TestDisallowedValueFixerSkipsNullValue covers the stale-null guard in the
// targeted loop.
func TestDisallowedValueFixerSkipsNullValue(t *testing.T) {
	info := pdf.NewPDFDict()
	info.Entries["Trapped"] = nil
	info.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Info"] = info
	ref := pdf.PDFRef{ObjNum: 5}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{5: info}}
	dead := pdf.PDFRef{ObjNum: 9}
	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &ref, "DocInfo", "Trapped"),
		objModelIssue(pdf.Checks.ObjectModel.DisallowedValue, &dead, "DocInfo", "Trapped"),
	}

	changed, handled, err := disallowedValueFixer{}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Errorf("fixTargeted = (%v, %v, %v), want (false, true, nil) for a null value", changed, handled, err)
	}
}

func TestConstraintFixerClearsTargetedFlagBits(t *testing.T) {
	// No /Type: invisible to the whole-graph flag pass, only targeting finds it.
	fd := pdf.NewPDFDict()
	fd.Entries["Flags"] = pdf.PDFInteger(32 + 1<<14) // Nonsymbolic + reserved bit 15
	fd.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}
	trailer := pdf.NewPDFDict()
	trailer.Entries["FD"] = fd
	ref := pdf.PDFRef{ObjNum: 4}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{4: fd}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, "FontDescriptorType1", "Flags")}

	changed, handled, err := constraintFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (true, true, nil)", changed, handled, err)
	}
	if got := fd.Entries["Flags"]; got != pdf.PDFInteger(32) {
		t.Errorf("Flags = %v, want 32 (reserved bit cleared)", got)
	}

	changed, _, err = constraintFixer{}.fixTargeted(pass, issues)
	if err != nil || changed {
		t.Errorf("second fixTargeted changed=%v err=%v, want idempotent no-op", changed, err)
	}
}

func TestConstraintFixerResizesDecodeParms(t *testing.T) {
	pad := pdf.NewPDFDict()
	pad.HasStream = true
	pad.Entries["Filter"] = pdf.PDFArray{pdf.PDFName{Value: "ASCIIHexDecode"}, pdf.PDFName{Value: "FlateDecode"}}
	pad.Entries["DecodeParms"] = pdf.PDFArray{pdf.NewPDFDict()}
	pad.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}
	trim := pdf.NewPDFDict()
	trim.HasStream = true
	trim.Entries["Filter"] = pdf.PDFArray{pdf.PDFName{Value: "FlateDecode"}}
	trim.Entries["DecodeParms"] = pdf.PDFArray{nil, nil, nil}
	trim.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}
	trailer := pdf.NewPDFDict()
	trailer.Entries["A"] = pad
	trailer.Entries["B"] = trim
	refPad := pdf.PDFRef{ObjNum: 4}
	refTrim := pdf.PDFRef{ObjNum: 5}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{4: pad, 5: trim}}
	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &refPad, "Stream", "DecodeParms"),
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &refTrim, "Stream", "DecodeParms"),
	}

	changed, _, err := constraintFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed {
		t.Fatalf("fixTargeted = (%v, _, %v), want changed", changed, err)
	}
	padded, _ := pad.Entries["DecodeParms"].(pdf.PDFArray)
	if len(padded) != 2 || padded[1] != nil {
		t.Errorf("DecodeParms = %v, want padded to [parms null]", padded)
	}
	trimmed, _ := trim.Entries["DecodeParms"].(pdf.PDFArray)
	if len(trimmed) != 1 {
		t.Errorf("DecodeParms = %v, want trimmed to 1 element", trimmed)
	}
}

func TestConstraintFixerPrunesFontFiles(t *testing.T) {
	fd := pdf.NewPDFDict()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff2 := pdf.NewPDFDict()
	ff2.HasStream = true
	fd.Entries["FontFile"] = ff
	fd.Entries["FontFile2"] = ff2
	fd.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}
	trailer := pdf.NewPDFDict()
	trailer.Entries["FD"] = fd
	ref := pdf.PDFRef{ObjNum: 4}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{4: fd}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, "FontDescriptorTrueType", "FontFile2")}

	changed, _, err := constraintFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed {
		t.Fatalf("fixTargeted = (%v, _, %v), want changed", changed, err)
	}
	if _, ok := fd.Entries["FontFile2"]; !ok {
		t.Error("a TrueType descriptor must keep FontFile2")
	}
	if _, ok := fd.Entries["FontFile"]; ok {
		t.Error("the surplus FontFile must be deleted")
	}

	// An unknown descriptor flavor fails closed.
	if pruneFontFiles(fd, "NoSuchDescriptor") {
		t.Error("pruneFontFiles must fail closed on unknown types")
	}
}

func TestConstraintFixerClampsLength1(t *testing.T) {
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	ff.Entries["Length1"] = pdf.PDFInteger(-5)
	ff.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}
	trailer := pdf.NewPDFDict()
	trailer.Entries["FF"] = ff
	ref := pdf.PDFRef{ObjNum: 4}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{4: ff}}
	issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, "FontFile", "Length1")}

	changed, _, err := constraintFixer{}.fixTargeted(pass, issues)
	if err != nil || !changed {
		t.Fatalf("fixTargeted = (%v, _, %v), want changed", changed, err)
	}
	if got := ff.Entries["Length1"]; got != pdf.PDFInteger(0) {
		t.Errorf("Length1 = %v, want clamped 0", got)
	}
}

func TestConstraintFixerLeavesMustBeSetResidual(t *testing.T) {
	// A pushbutton's Ff must have bit 17 set; setting a semantic bit is never
	// neutral, so the violation stays.
	btn := pdf.NewPDFDict()
	btn.Entries["FT"] = pdf.PDFName{Value: "Btn"}
	btn.Entries["Ff"] = pdf.PDFInteger(0)
	btn.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}
	trailer := pdf.NewPDFDict()
	trailer.Entries["F"] = btn
	ref := pdf.PDFRef{ObjNum: 4}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{4: btn}}
	issues := []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, "FieldBtnPush", "Ff"),
		// Skip cases: array element, no SpecialCase row, null value, dead ref, no detail.
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, "ArrayOf_4NumbersColorAnnotation", "1"),
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, "FieldBtnPush", "FT"),
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, "FieldBtnPush", "Absent"),
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, nil, "FieldBtnPush", "Ff"),
		pdf.NewError(pdf.Checks.ObjectModel.ConstraintViolated, nil, 0, &ref),
	}

	changed, handled, err := constraintFixer{}.fixTargeted(pass, issues)
	if err != nil || changed || !handled {
		t.Fatalf("fixTargeted = (%v, %v, %v), want (false, true, nil)", changed, handled, err)
	}
	if got := btn.Entries["Ff"]; got != pdf.PDFInteger(0) {
		t.Errorf("Ff = %v, want untouched 0", got)
	}
}

// TestConvertObjectModelResizesDecodeParms proves the fixer end to end: a
// content stream whose DecodeParms array is shorter than its Filter array
// converts to a fully valid rewrite.
func TestConvertObjectModelResizesDecodeParms(t *testing.T) {
	data := buildOnePageDoc(t, func(_, _, page pdf.PDFDict) {
		contents := page.Entries["Contents"].(pdf.PDFDict)
		hexed := []byte("30203020313030203130302072652066>") // "0 0 100 100 re f" hex-encoded
		contents.RawStream = hexed
		contents.Entries["Filter"] = pdf.PDFArray{pdf.PDFName{Value: "ASCIIHexDecode"}}
		contents.Entries["DecodeParms"] = pdf.PDFArray{nil, nil}
	})

	res, err := verify.VerifyBytes(data, pdf.PDF, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || !hasIssueForCheck(res.Issues, pdf.Checks.ObjectModel.ConstraintViolated) {
		t.Fatalf("fixture must fail with ConstraintViolated, got %v", res.Issues)
	}

	cr, err := ConvertBytes(data, pdf.PDF, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("Valid=%v, residual %v", cr.Result.Valid, issueClauses(cr.Residual()))
	}
}

// TestConstraintHelperEdges covers the helper branches the fixer-level tests
// don't reach: the whole-graph flag walk, non-array and equal-length resizes,
// nested couplings, and single-variant descriptors.
func TestConstraintHelperEdges(t *testing.T) {
	fd := pdf.NewPDFDict()
	fd.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	fd.Entries["Flags"] = pdf.PDFInteger(32 + 1<<14)
	trailer := pdf.NewPDFDict()
	trailer.Entries["FD"] = fd
	changed, err := constraintFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = (%v, %v), want the typed descriptor's reserved bit cleared", changed, err)
	}
	if got := fd.Entries["Flags"]; got != pdf.PDFInteger(32) {
		t.Errorf("Flags = %v, want 32", got)
	}

	eq := arlington.Cond{Op: arlington.CondEq, Key: "DecodeParms", Fn: arlington.FnArrayLength, RHSKey: "Filter", RHSFn: arlington.FnArrayLength}
	and := arlington.Cond{Op: arlington.CondAnd, Kids: []arlington.Cond{eq}}
	if sib, ok := lengthCoupledSibling(&and, "DecodeParms"); !ok || sib != "Filter" {
		t.Errorf("nested coupling = (%q, %v), want (Filter, true)", sib, ok)
	}
	if _, ok := lengthCoupledSibling(&eq, "Other"); ok {
		t.Error("a coupling keyed elsewhere must not match")
	}
	if _, ok := lengthCoupledSibling(&arlington.Cond{Op: arlington.CondPresent, Key: "X"}, "X"); ok {
		t.Error("a non-comparison leaf must not match")
	}
	// Affine couplings must not match: resizing Functions to len(Bounds) would be off by one.
	for _, affine := range []arlington.Cond{
		{Op: arlington.CondEq, Key: "Functions", Fn: arlington.FnArrayLength, RHSKey: "Bounds", RHSFn: arlington.FnArrayLength, RHSAdd: 1},
		{Op: arlington.CondEq, Key: "Range", Fn: arlington.FnArrayLength, RHSKey: "N", RHSFn: arlington.FnArrayLength, RHSMul: 2},
		{Op: arlington.CondEq, Key: "Widths", Fn: arlington.FnArrayLength, RHSKey: "LastChar", RHSFn: arlington.FnArrayLength, RHSKey2: "FirstChar"},
	} {
		if _, ok := lengthCoupledSibling(&affine, affine.Key); ok {
			t.Errorf("an affine coupling %+v must not resize", affine)
		}
	}

	d := pdf.NewPDFDict()
	d.Entries["DecodeParms"] = pdf.PDFName{Value: "NotAnArray"}
	d.Entries["Filter"] = pdf.PDFArray{pdf.PDFName{Value: "FlateDecode"}}
	if resizeToSiblingLength(d, "DecodeParms", "Filter") {
		t.Error("a non-array key must not resize")
	}
	d.Entries["DecodeParms"] = pdf.PDFArray{nil}
	if resizeToSiblingLength(d, "DecodeParms", "Filter") {
		t.Error("equal lengths must not resize")
	}
	d.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
	if resizeToSiblingLength(d, "DecodeParms", "Filter") {
		t.Error("a non-array sibling must not resize")
	}

	single := pdf.NewPDFDict()
	ff := pdf.NewPDFDict()
	ff.HasStream = true
	single.Entries["FontFile"] = ff
	if pruneFontFiles(single, "FontDescriptorType1") {
		t.Error("a single present variant must not prune")
	}

	// A dead ref in the targeted loop is skipped.
	dead := pdf.PDFRef{ObjNum: 9}
	pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{}}
	changed, handled, err := constraintFixer{}.fixTargeted(pass, []pdf.PDFError{
		objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &dead, "Stream", "DecodeParms"),
	})
	if err != nil || changed || !handled {
		t.Errorf("fixTargeted = (%v, %v, %v), want (false, true, nil)", changed, handled, err)
	}
}
