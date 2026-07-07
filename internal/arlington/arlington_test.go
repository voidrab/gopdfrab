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
	if pages == nil || len(pages.Link) != 1 || pages.Link[0] != "PageTreeNodeRoot" {
		t.Errorf("Catalog.Pages: want Link [PageTreeNodeRoot], got %+v", pages)
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
	if outputIntents == nil || len(outputIntents.Link) != 1 || outputIntents.Link[0] != "ArrayOfOutputIntents" {
		t.Errorf("Catalog.OutputIntents: want Link [ArrayOfOutputIntents], got %+v", outputIntents)
	}
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
	if ri == nil || !equalStrings(ri.PossibleValues, wantRI) || ri.Predicated {
		t.Errorf("GraphicsStateParameter.RI: want PossibleValues %v, not Predicated, got %+v", wantRI, ri)
	}

	for _, name := range []string{"LW", "LC"} {
		kd := findKey(gs, name)
		if kd == nil || !kd.Predicated || len(kd.PossibleValues) != 0 {
			t.Errorf("GraphicsStateParameter.%s: want Predicated with empty PossibleValues, got %+v", name, kd)
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
	if len(a.Wildcard.Link) != 1 || a.Wildcard.Link[0] != "OutputIntents" {
		t.Errorf("ArrayOfOutputIntents.Wildcard: want Link [OutputIntents], got %+v", a.Wildcard)
	}
}

// TestTableIntegrity loads every vendored type and confirms every Link target names another
// entry in Types, catching generator parse drift.
func TestTableIntegrity(t *testing.T) {
	const wantTypes = 288
	if len(Types) != wantTypes {
		t.Errorf("Types: want %d entries, got %d", wantTypes, len(Types))
	}

	checkLinks := func(owner string, links []string) {
		for _, link := range links {
			if _, ok := Types[link]; !ok {
				t.Errorf("%s: Link %q does not resolve to a known Arlington type", owner, link)
			}
		}
	}
	for name, ot := range Types {
		if ot.Name != name {
			t.Errorf("Types[%q].Name = %q, want %q", name, ot.Name, name)
		}
		for _, kd := range ot.Keys {
			checkLinks(name+"."+kd.Name, kd.Link)
		}
		if ot.Wildcard != nil {
			checkLinks(name+".*", ot.Wildcard.Link)
		}
	}
}

// TestClassificationFloor tracks the fraction of TSV rows the generator can classify as
// simple (no unresolved fn: predicate in Required/IndirectReference/PossibleValues). This is
// a visible regression guard per arlington.md's Limitations section, not a target to chase.
func TestClassificationFloor(t *testing.T) {
	const floor = 0.85 // observed ~87.3%; leaves headroom for upstream TSV churn

	total, simple := 0, 0
	for _, ot := range Types {
		for _, kd := range ot.Keys {
			total++
			if !kd.Predicated {
				simple++
			}
		}
		if ot.Wildcard != nil {
			total++
			if !ot.Wildcard.Predicated {
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
