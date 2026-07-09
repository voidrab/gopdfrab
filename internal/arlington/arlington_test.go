package arlington

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// findKey returns the KeyDef named name in t, or nil if absent.
func findKey(t ObjectType, name string) *KeyDef {
	for i := range t.Keys {
		if t.Keys[i].Name == name {
			return &t.Keys[i]
		}
	}
	return nil
}

func TestCatalog(t *testing.T) {
	cat, ok := Type("Catalog")
	if !ok {
		t.Fatal("Catalog type not found")
	}

	typeKey := findKey(cat, "Type")
	if typeKey == nil || !typeKey.Required {
		t.Errorf("Catalog.Type: want Required, got %+v", typeKey)
	}
	pages := findKey(cat, "Pages")
	if pages == nil || !pages.Required || pages.IndirectReference != IndirectRequired {
		t.Errorf("Catalog.Pages: want Required + IndirectRequired, got %+v", pages)
	}
	if pages == nil || !equalStrings(soleCandidates(pages), []string{"PageTreeNodeRoot"}) {
		t.Errorf("Catalog.Pages: want sole candidate [PageTreeNodeRoot], got %+v", pages)
	}

	version := findKey(cat, "Version")
	wantVersions := []string{"1.0", "1.1", "1.2", "1.3", "1.4", "1.5", "1.6", "1.7", "2.0"}
	if version == nil || !equalStrings(version.PossibleValues, wantVersions) {
		t.Errorf("Catalog.Version: want PossibleValues %v, got %+v", wantVersions, version)
	}
	if version == nil || version.Required {
		t.Errorf("Catalog.Version: want not Required, got %+v", version)
	}

	pageMode := findKey(cat, "PageMode")
	wantModes := []string{"UseNone", "UseOutlines", "UseThumbs", "FullScreen", "UseOC", "UseAttachments"}
	if pageMode == nil || !equalStrings(pageMode.PossibleValues, wantModes) {
		t.Errorf("Catalog.PageMode: want PossibleValues %v, got %+v", wantModes, pageMode)
	}

	outputIntents := findKey(cat, "OutputIntents")
	if outputIntents == nil || !equalStrings(soleCandidates(outputIntents), []string{"ArrayOfOutputIntents"}) {
		t.Errorf("Catalog.OutputIntents: want sole candidate [ArrayOfOutputIntents], got %+v", outputIntents)
	}
}

// soleCandidates returns kd's Candidates when it has exactly one LinkGroup, for asserting the
// common unambiguous case; nil otherwise.
func soleCandidates(kd *KeyDef) []string {
	if len(kd.LinkGroups) != 1 {
		return nil
	}
	return kd.LinkGroups[0].Candidates
}

func TestValueTypeString(t *testing.T) {
	// Exhaustive: one case per declared ValueType constant, matching the Arlington TSV token.
	cases := map[ValueType]string{
		Array: "array", Bitmask: "bitmask", Boolean: "boolean", Date: "date",
		Dictionary: "dictionary", Integer: "integer", Matrix: "matrix", Name: "name",
		NameTree: "name-tree", Null: "null", Number: "number", NumberTree: "number-tree",
		Rectangle: "rectangle", Stream: "stream", String: "string", StringASCII: "string-ascii",
		StringByte: "string-byte", StringText: "string-text",
	}
	for vt, want := range cases {
		if got := vt.String(); got != want {
			t.Errorf("ValueType(%d).String() = %q, want %q", vt, got, want)
		}
	}
	if got := ValueType(999).String(); got != "unknown" {
		t.Errorf("ValueType(999).String() = %q, want %q", got, "unknown")
	}
}

func TestPageObject(t *testing.T) {
	if _, ok := Type("PageObject"); !ok {
		t.Fatal("PageObject type not found")
	}
}

func TestGraphicsStateParameter(t *testing.T) {
	gs, ok := Type("GraphicsStateParameter")
	if !ok {
		t.Fatal("GraphicsStateParameter type not found")
	}

	ri := findKey(gs, "RI")
	wantRI := []string{"AbsoluteColorimetric", "RelativeColorimetric", "Saturation", "Perceptual"}
	if ri == nil || !equalStrings(ri.PossibleValues, wantRI) || ri.Predicated.Any() {
		t.Errorf("GraphicsStateParameter.RI: want PossibleValues %v, not Predicated, got %+v", wantRI, ri)
	}

	// LW/LC carry fn:Eval range constraints, compiled into ValueCond trees.
	for _, name := range []string{"LW", "LC"} {
		kd := findKey(gs, name)
		if kd == nil || kd.Predicated.Any() || kd.ValueCond == nil || len(kd.PossibleValues) != 0 {
			t.Errorf("GraphicsStateParameter.%s: want compiled ValueCond, no PossibleValues, got %+v", name, kd)
		}
	}
	lw := findKey(gs, "LW")
	wantLW := &Cond{Op: CondGe, Key: "LW", Value: "0"}
	if lw == nil || !reflect.DeepEqual(lw.ValueCond, wantLW) {
		t.Errorf("GraphicsStateParameter.LW: want ValueCond %+v, got %+v", wantLW, lw.ValueCond)
	}
}

// TestPost14Keys checks the tsv/latest-vs-tsv/1.4 diff against ViewerPreferences, whose
// post-1.4 keys are independently documented by the hand-written Post14ViewerPrefKeys
// (internal/verify/checks_dict.go) that this data-driven check generalizes.
func TestPost14Keys(t *testing.T) {
	vp, ok := Type("ViewerPreferences")
	if !ok {
		t.Fatal("ViewerPreferences type not found")
	}
	want := []string{"PrintScaling", "PickTrayByPDFSize", "PrintPageRange", "NumCopies"}
	for _, k := range want {
		found := false
		for _, p := range vp.Post14Keys {
			if p == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ViewerPreferences.Post14Keys = %v, want to contain %q", vp.Post14Keys, k)
		}
	}
}

func TestArrayOfOutputIntentsWildcard(t *testing.T) {
	a, ok := Type("ArrayOfOutputIntents")
	if !ok {
		t.Fatal("ArrayOfOutputIntents type not found")
	}
	if a.Wildcard == nil {
		t.Fatal("ArrayOfOutputIntents: want a Wildcard entry")
	}
	if !equalStrings(soleCandidates(a.Wildcard), []string{"OutputIntents"}) {
		t.Errorf("ArrayOfOutputIntents.Wildcard: want sole candidate [OutputIntents], got %+v", a.Wildcard)
	}
}

// TestAnnotationDiscriminator confirms the wildcard entry of ArrayOfAnnots -- 19 candidate
// Annot* types -- resolves via its Subtype key, and that a couple of representative subtypes
// map to the expected Arlington type.
func TestAnnotationDiscriminator(t *testing.T) {
	a, ok := Type("ArrayOfAnnots")
	if !ok {
		t.Fatal("ArrayOfAnnots type not found")
	}
	if a.Wildcard == nil || len(a.Wildcard.LinkGroups) != 1 {
		t.Fatalf("ArrayOfAnnots.Wildcard: want exactly one LinkGroup, got %+v", a.Wildcard)
	}
	g := a.Wildcard.LinkGroups[0]
	if g.Discriminator != "Subtype" {
		t.Errorf("ArrayOfAnnots.Wildcard: want Discriminator %q, got %q", "Subtype", g.Discriminator)
	}
	for value, want := range map[string]string{"Widget": "AnnotWidget", "Popup": "AnnotPopup", "Text": "AnnotText"} {
		if got := g.ByValue[value]; got != want {
			t.Errorf("ArrayOfAnnots.Wildcard.ByValue[%q] = %q, want %q", value, got, want)
		}
	}
}

// TestActionDiscriminator confirms an action-dispatch group resolves via S, specifically that
// /S == "JavaScript" maps to Arlington type ActionECMAScript -- not a naive "Action"+S
// concatenation, which would wrongly produce "ActionJavaScript".
func TestActionDiscriminator(t *testing.T) {
	a, ok := Type("ActionGoTo")
	if !ok {
		t.Fatal("ActionGoTo type not found")
	}
	next := findKey(a, "Next")
	if next == nil {
		t.Fatal("ActionGoTo.Next not found")
	}
	var dictGroup *LinkGroup
	for i := range next.LinkGroups {
		for _, vt := range next.LinkGroups[i].ValueTypes {
			if vt == Dictionary {
				dictGroup = &next.LinkGroups[i]
			}
		}
	}
	if dictGroup == nil {
		t.Fatal("ActionGoTo.Next: want a Dictionary-typed LinkGroup")
	}
	if dictGroup.Discriminator != "S" {
		t.Errorf("ActionGoTo.Next dict group: want Discriminator %q, got %q", "S", dictGroup.Discriminator)
	}
	if got := dictGroup.ByValue["JavaScript"]; got != "ActionECMAScript" {
		t.Errorf(`ActionGoTo.Next dict group ByValue["JavaScript"] = %q, want "ActionECMAScript"`, got)
	}
}

// TestXObjectDiscriminator confirms XObjectMap's wildcard resolves via Subtype, and that the
// two PostScript XObject variants -- which collide with XObjectFormType1's Subtype value
// "Form" -- are correctly absent from ByValue rather than guessed.
func TestXObjectDiscriminator(t *testing.T) {
	xm, ok := Type("XObjectMap")
	if !ok {
		t.Fatal("XObjectMap type not found")
	}
	if xm.Wildcard == nil || len(xm.Wildcard.LinkGroups) != 1 {
		t.Fatalf("XObjectMap.Wildcard: want exactly one LinkGroup, got %+v", xm.Wildcard)
	}
	g := xm.Wildcard.LinkGroups[0]
	if g.Discriminator != "Subtype" {
		t.Errorf("XObjectMap.Wildcard: want Discriminator %q, got %q", "Subtype", g.Discriminator)
	}
	if got := g.ByValue["Form"]; got != "XObjectFormType1" {
		t.Errorf(`XObjectMap.Wildcard ByValue["Form"] = %q, want "XObjectFormType1"`, got)
	}
	if got := g.ByValue["Image"]; got != "XObjectImage" {
		t.Errorf(`XObjectMap.Wildcard ByValue["Image"] = %q, want "XObjectImage"`, got)
	}
}

// TestTableIntegrity loads every vendored type and confirms every Link target names another
// entry in Types, catching generator parse drift.
func TestTableIntegrity(t *testing.T) {
	const wantTypes = 288
	if len(Types) != wantTypes {
		t.Errorf("Types: want %d entries, got %d", wantTypes, len(Types))
	}

	checkLinkGroups := func(owner string, groups []LinkGroup) {
		for _, g := range groups {
			for _, c := range g.Candidates {
				if _, ok := Types[c]; !ok {
					t.Errorf("%s: Candidate %q does not resolve to a known Arlington type", owner, c)
				}
			}
			if g.Discriminator == "" {
				if len(g.ByValue) != 0 {
					t.Errorf("%s: ByValue set without a Discriminator", owner)
				}
				continue
			}
			if len(g.Candidates) < 2 {
				t.Errorf("%s: Discriminator %q set on a group with < 2 Candidates", owner, g.Discriminator)
			}
			for value, candidate := range g.ByValue {
				if !stringSliceContains(g.Candidates, candidate) {
					t.Errorf("%s: ByValue[%q] = %q, not among Candidates %v", owner, value, candidate, g.Candidates)
				}
			}
		}
	}
	for name, ot := range Types {
		if ot.Name != name {
			t.Errorf("Types[%q].Name = %q, want %q", name, ot.Name, name)
		}
		for _, kd := range ot.Keys {
			checkLinkGroups(name+"."+kd.Name, kd.LinkGroups)
		}
		if ot.Wildcard != nil {
			checkLinkGroups(name+".*", ot.Wildcard.LinkGroups)
		}
	}
}

func stringSliceContains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// TestVersionGateFolding pins representative rows whose fn: version-gate predicates the
// generator folds against the model's PDF 1.4 baseline, plus rows that must stay predicated
// because their condition depends on runtime document state.
func TestVersionGateFolding(t *testing.T) {
	// fn:MustBeIndirect(fn:BeforeVersion(2.0)) -> IndirectRequired at 1.4.
	outlines := findKey(Types["Catalog"], "Outlines")
	if outlines == nil || outlines.Predicated.Any() || outlines.IndirectReference != IndirectRequired {
		t.Errorf("Catalog.Outlines: want folded IndirectRequired, got %+v", outlines)
	}

	// The Indirect column folds even when the row's Required column stays predicated
	// (it carries a runtime fn:IsPresent condition).
	info := findKey(Types["FileTrailer"], "Info")
	wantInfo := Predication{Required: true}
	if info == nil || info.Predicated != wantInfo || info.IndirectReference != IndirectRequired {
		t.Errorf("FileTrailer.Info: want folded IndirectRequired with Predicated %+v, got %+v", wantInfo, info)
	}

	// fn:IsRequired(fn:BeforeVersion(1.3)) -> not required at 1.4.
	matrix := findKey(Types["XObjectFormType1"], "Matrix")
	if matrix == nil || matrix.Predicated.Any() || matrix.Required {
		t.Errorf("XObjectFormType1.Matrix: want folded not-Required, got %+v", matrix)
	}

	// fn:IsRequired(fn:SinceVersion(2.0,...)) -> not required at 1.4, runtime payload moot.
	ap := findKey(Types["AnnotText"], "AP")
	if ap == nil || ap.Predicated.Any() || ap.Required {
		t.Errorf("AnnotText.AP: want folded not-Required, got %+v", ap)
	}

	// fn:MustBeIndirect(fn:SinceVersion(1.7)) -> unconstrained at 1.4, wildcard un-predicated.
	annots := Types["ArrayOfAnnots"].Wildcard
	if annots == nil || annots.Predicated.Any() || annots.IndirectReference != IndirectEither {
		t.Errorf("ArrayOfAnnots.*: want folded IndirectEither, got %+v", annots)
	}

	// Version-gated enum entries fold entry-wise: deprecated values stay legal, values
	// introduced after 1.4 drop out.
	v := findKey(Types["EncryptionStandard"], "V")
	want := []string{"0", "1", "2", "3"}
	if v == nil || v.Predicated.Any() || !equalStrings(v.PossibleValues, want) {
		t.Errorf("EncryptionStandard.V: want folded PossibleValues %v, got %+v", want, v)
	}

	// A literal "*" entry legalizes any value, so the list must not be emitted at all.
	n := findKey(Types["ActionNamed"], "N")
	if n == nil || len(n.PossibleValues) != 0 {
		t.Errorf("ActionNamed.N: want no PossibleValues (list contains *), got %+v", n)
	}

	// Runtime conditions stay predicated in exactly their own column.
	as := findKey(Types["AnnotText"], "AS")
	if as == nil || as.Predicated != (Predication{Required: true}) {
		t.Errorf("AnnotText.AS: want only Required predicated (runtime fn:IsPresent), got %+v", as)
	}
}

// TestCompiledConditions pins representative fn:IsRequired conditions the generator compiles
// into runtime Cond trees instead of leaving the row predicated.
func TestCompiledConditions(t *testing.T) {
	// fn:IsRequired(@Type!=Template) -> a plain comparison leaf.
	parent := findKey(Types["PageObject"], "Parent")
	wantParent := &Cond{Op: CondNe, Key: "Type", Value: "Template"}
	if parent == nil || parent.Predicated.Required || !reflect.DeepEqual(parent.RequiredWhen, wantParent) {
		t.Errorf("PageObject.Parent: want RequiredWhen %+v, got %+v", wantParent, parent)
	}

	// fn:IsRequired(fn:IsPresent(EF) || fn:SinceVersion(2.0,fn:IsPresent(EP)) || fn:IsPresent(RF)):
	// the 2.0-gated operand folds away, the sibling-presence operands survive.
	typ := findKey(Types["FileSpecification"], "Type")
	wantTyp := &Cond{Op: CondOr, Kids: []Cond{{Op: CondPresent, Key: "EF"}, {Op: CondPresent, Key: "RF"}}}
	if typ == nil || !reflect.DeepEqual(typ.RequiredWhen, wantTyp) {
		t.Errorf("FileSpecification.Type: want RequiredWhen %+v, got %+v", wantTyp, typ)
	}

	// fn:IsRequired((@R==5) || (@R==6)) -> comparison alternatives.
	oe := findKey(Types["EncryptionStandard"], "OE")
	wantOE := &Cond{Op: CondOr, Kids: []Cond{{Op: CondEq, Key: "R", Value: "5"}, {Op: CondEq, Key: "R", Value: "6"}}}
	if oe == nil || !reflect.DeepEqual(oe.RequiredWhen, wantOE) {
		t.Errorf("EncryptionStandard.OE: want RequiredWhen %+v, got %+v", wantOE, oe)
	}

	// Cross-object paths stay out of reach: AnnotText.AS (AP::N::*) has no compiled tree.
	as := findKey(Types["AnnotText"], "AS")
	if as == nil || as.RequiredWhen != nil {
		t.Errorf("AnnotText.AS: want no RequiredWhen (path condition), got %+v", as)
	}

	// Fixed array-index rows compile fn:Eval ranges with element-index operands:
	// WhitepointArray.0 is fn:Eval(@0>0).
	wp := findKey(Types["WhitepointArray"], "0")
	wantWP := &Cond{Op: CondGt, Key: "0", Value: "0"}
	if wp == nil || wp.Predicated.Values || !reflect.DeepEqual(wp.ValueCond, wantWP) {
		t.Errorf("WhitepointArray.0: want ValueCond %+v, got %+v", wantWP, wp)
	}

	// IndexedColorSpace.2 (hival) is fn:Eval((@2>=0) && (@2<=255)).
	hival := findKey(Types["IndexedColorSpace"], "2")
	wantHival := &Cond{Op: CondAnd, Kids: []Cond{
		{Op: CondGe, Key: "2", Value: "0"}, {Op: CondLe, Key: "2", Value: "255"},
	}}
	if hival == nil || !reflect.DeepEqual(hival.ValueCond, wantHival) {
		t.Errorf("IndexedColorSpace.2: want ValueCond %+v, got %+v", wantHival, hival)
	}

	// Multi-group columns and offset-wildcard rows stay predicated -- fail closed.
	for _, tc := range []struct{ typ, key string }{
		{"Dest0Array", "0"}, {"ArrayOfAttributeRevisions", "1*"},
	} {
		kd := findKey(Types[tc.typ], tc.key)
		if kd == nil || !kd.Predicated.Values || kd.ValueCond != nil {
			t.Errorf("%s.%s: want Values predicated with no ValueCond, got %+v", tc.typ, tc.key, kd)
		}
	}

	// fn:Eval((@Rotate mod 90)==0) -> a modulo equality leaf.
	rot := findKey(Types["PageObject"], "Rotate")
	wantRot := &Cond{Op: CondEq, Key: "Rotate", Value: "0", Mod: 90}
	if rot == nil || rot.Predicated.Values || !reflect.DeepEqual(rot.ValueCond, wantRot) {
		t.Errorf("PageObject.Rotate: want ValueCond %+v, got %+v", wantRot, rot)
	}

	// EncryptionStandard.Length: the fn:Extension(ADBE_Extn3,...) arm compiles to a
	// CondUnknown leaf inside the Or, so 40..128 and mod 8 are still enforced.
	length := findKey(Types["EncryptionStandard"], "Length")
	wantLen := &Cond{Op: CondAnd, Kids: []Cond{
		{Op: CondGe, Key: "Length", Value: "40"},
		{Op: CondOr, Kids: []Cond{{Op: CondLe, Key: "Length", Value: "128"}, {Op: CondUnknown}}},
		{Op: CondEq, Key: "Length", Value: "0", Mod: 8},
	}}
	if length == nil || length.Predicated.Values || !reflect.DeepEqual(length.ValueCond, wantLen) {
		t.Errorf("EncryptionStandard.Length: want ValueCond %+v, got %+v", wantLen, length)
	}

	// A tree of only CondUnknown leaves enforces nothing and must stay predicated; FileTrailer
	// .Prev (fn:FileSize operand) is representative of &&-siblings staying uncompilable too.
	prev := findKey(Types["FileTrailer"], "Prev")
	if prev == nil || !prev.Predicated.Values || prev.ValueCond != nil {
		t.Errorf("FileTrailer.Prev: want Values predicated (fn:FileSize operand), got %+v", prev)
	}

	// fn:IsRequired(fn:Not(fn:Contains(@Filter,JPXDecode) || (@ImageMask==true))).
	cs := findKey(Types["XObjectImage"], "ColorSpace")
	wantCS := &Cond{Op: CondNot, Kids: []Cond{{Op: CondOr, Kids: []Cond{
		{Op: CondContains, Key: "Filter", Value: "JPXDecode"},
		{Op: CondEq, Key: "ImageMask", Value: "true"},
	}}}}
	if cs == nil || cs.Predicated.Required || !reflect.DeepEqual(cs.RequiredWhen, wantCS) {
		t.Errorf("XObjectImage.ColorSpace: want RequiredWhen %+v, got %+v", wantCS, cs)
	}

	// fn:Eval((@TI>=0) && (@TI<fn:ArrayLength(Opt))) -- a derived right operand.
	ti := findKey(Types["FieldChoice"], "TI")
	wantTI := &Cond{Op: CondAnd, Kids: []Cond{
		{Op: CondGe, Key: "TI", Value: "0"},
		{Op: CondLt, Key: "TI", RHSKey: "Opt", RHSFn: FnArrayLength},
	}}
	if ti == nil || ti.Predicated.Values || !reflect.DeepEqual(ti.ValueCond, wantTI) {
		t.Errorf("FieldChoice.TI: want ValueCond %+v, got %+v", wantTI, ti)
	}

	// Element-vs-element comparisons now compile on fixed-index rows: LabRangeArray @0<=@1.
	lab := findKey(Types["LabRangeArray"], "0")
	wantLab := &Cond{Op: CondLe, Key: "0", RHSKey: "1"}
	if lab == nil || lab.Predicated.Values || !reflect.DeepEqual(lab.ValueCond, wantLab) {
		t.Errorf("LabRangeArray.0: want ValueCond %+v, got %+v", wantLab, lab)
	}
}

// TestClassificationFloor tracks the fraction of TSV rows the generator can classify as
// simple (no unresolved fn: predicate in Required/IndirectReference/PossibleValues). This is
// a visible regression guard per arlington.md's Limitations section, not a target to chase.
func TestClassificationFloor(t *testing.T) {
	const floor = 0.945 // observed ~95.2% after operand functions; headroom for TSV churn

	total, simple := 0, 0
	for _, ot := range Types {
		for _, kd := range ot.Keys {
			total++
			if !kd.Predicated.Any() {
				simple++
			}
		}
		if ot.Wildcard != nil {
			total++
			if !ot.Wildcard.Predicated.Any() {
				simple++
			}
		}
	}
	if total == 0 {
		t.Fatal("no rows loaded")
	}
	fraction := float64(simple) / float64(total)
	if fraction < floor {
		t.Errorf("simple-row fraction dropped to %.3f (%d/%d), floor is %.2f", fraction, simple, total, floor)
	}
	t.Logf("simple-row fraction: %.3f (%d/%d)", fraction, simple, total)
}

// TestGeneratorIdempotent regenerates model_gen.go into a scratch copy of the package and
// asserts it is byte-identical to the checked-in file, guarding against edits to gen.go or
// the vendored TSVs that were never regenerated.
func TestGeneratorIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes `go run`; skipped in -short mode")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile(filepath.Join(wd, "model_gen.go"))
	if err != nil {
		t.Fatal(err)
	}

	scratch := t.TempDir()
	for _, name := range []string{"gen.go", "arlington.go"} {
		data, err := os.ReadFile(filepath.Join(wd, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scratch, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(wd, "testdata"), filepath.Join(scratch, "testdata")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", "gen.go")
	cmd.Dir = scratch
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go run gen.go: %v\n%s", err, out)
	}

	regenerated, err := os.ReadFile(filepath.Join(scratch, "model_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(regenerated) != string(checkedIn) {
		t.Error("model_gen.go is stale: regenerating produces a different file; run `go generate ./internal/arlington/...`")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSelfIdentified pins representative entries of the generated self-identification table:
// unambiguous (Type, Subtype) pairs resolve, colliding or unknown ones stay "".
func TestSelfIdentified(t *testing.T) {
	cases := []struct {
		typ, sub, want string
	}{
		{"Page", "", "PageObject"},
		{"Template", "", "PageObject"},
		{"Catalog", "", "Catalog"},
		{"Annot", "Text", "AnnotText"},
		{"Font", "Type1", "FontType1"},
		{"Metadata", "XML", "Metadata"},
		{"StructElem", "", "StructElem"},
		// An unknown subtype falls back to the bare (Type, "") claim when one exists.
		{"Page", "Odd", "PageObject"},
		// Four FontDescriptor* types collide on the bare pair; XObject types collide on Form.
		{"FontDescriptor", "", ""},
		{"XObject", "Form", ""},
		// Annot alone (no Subtype) is claimed by every annotation type, so it stays ambiguous.
		{"Annot", "", ""},
		{"Metadata", "", ""},
		{"Nope", "", ""},
	}
	for _, c := range cases {
		if got := SelfIdentified(c.typ, c.sub); got != c.want {
			t.Errorf("SelfIdentified(%q, %q) = %q, want %q", c.typ, c.sub, got, c.want)
		}
	}
}

// TestRequiredOverrides pins the generator's relaxations of vendored Required rows no PDF/A
// validator enforces (see requiredOverrides in gen.go).
func TestRequiredOverrides(t *testing.T) {
	for _, c := range [][2]string{
		{"XObjectFormType1", "Resources"},
		{"PageObject", "LastModified"},
		{"StructElem", "P"},
	} {
		ot, ok := Type(c[0])
		if !ok {
			t.Fatalf("type %s missing", c[0])
		}
		kd := findKey(ot, c[1])
		if kd == nil {
			t.Fatalf("%s.%s missing", c[0], c[1])
		}
		if kd.Required || kd.RequiredWhen != nil || kd.Predicated.Required {
			t.Errorf("%s.%s: Required=%v RequiredWhen=%v Predicated.Required=%v, want fully optional",
				c[0], c[1], kd.Required, kd.RequiredWhen, kd.Predicated.Required)
		}
	}
}

// TestColourSpaceArrayDiscriminator pins the array-index discriminator on colour-space
// links: the first element's name picks the candidate.
func TestColourSpaceArrayDiscriminator(t *testing.T) {
	m, ok := Type("ColorSpaceMap")
	if !ok {
		t.Fatal("ColorSpaceMap missing")
	}
	rgb := findKey(m, "DefaultRGB")
	if rgb == nil || len(rgb.LinkGroups) != 1 {
		t.Fatalf("DefaultRGB LinkGroups = %+v, want one group", rgb)
	}
	g := rgb.LinkGroups[0]
	if g.Discriminator != "0" {
		t.Errorf("DefaultRGB discriminator = %q, want \"0\"", g.Discriminator)
	}
	if g.ByValue["ICCBased"] != "ICCBasedColorSpace" {
		t.Errorf("ByValue[ICCBased] = %q, want ICCBasedColorSpace", g.ByValue["ICCBased"])
	}
}
