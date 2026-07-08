package arlington

import (
	"os"
	"os/exec"
	"path/filepath"
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

	// LW/LC carry fn:Eval range constraints: only the Values column is predicated, so their
	// type checks still run.
	for _, name := range []string{"LW", "LC"} {
		kd := findKey(gs, name)
		want := Predication{Values: true}
		if kd == nil || kd.Predicated != want || len(kd.PossibleValues) != 0 {
			t.Errorf("GraphicsStateParameter.%s: want Predicated %+v with empty PossibleValues, got %+v", name, want, kd)
		}
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

// TestClassificationFloor tracks the fraction of TSV rows the generator can classify as
// simple (no unresolved fn: predicate in Required/IndirectReference/PossibleValues). This is
// a visible regression guard per arlington.md's Limitations section, not a target to chase.
func TestClassificationFloor(t *testing.T) {
	const floor = 0.88 // observed ~89.3% after version-gate folding; headroom for TSV churn

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
