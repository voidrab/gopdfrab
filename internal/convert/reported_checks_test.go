package convert

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// issuesOf is one issue under each of checks, the shape a verify that
// reported exactly those checks hands a fixer.
func issuesOf(checks ...pdf.Check) []pdf.PDFError {
	issues := make([]pdf.PDFError, 0, len(checks))
	for _, c := range checks {
		issues = append(issues, pdf.NewError(c, nil, 1, nil))
	}
	return issues
}

// everyCheck is one issue for each check f remediates: what a verify that
// found every violation f knows how to repair would hand it.
func everyCheck(f Fixer) []pdf.PDFError {
	var checks []pdf.Check
	for _, c := range pdf.AllChecks() {
		if f.Applies(c) {
			checks = append(checks, c)
		}
	}
	return issuesOf(checks...)
}

// TestExtGStateFixerRepairsOnlyReportedKeys verifies that one ExtGState breaks
// all seven rules and that only one is reported. The fixer repairs that key and
// leaves the other six as they were, so a repair never runs on a rule the level
// does not have.
func TestExtGStateFixerRepairsOnlyReportedKeys(t *testing.T) {
	tr := pdf.Checks.Transparency
	for _, tc := range []struct {
		check    pdf.Check
		key      string
		repaired func(pdf.PDFValue) bool
	}{
		{tr.TransferFunction, "TR", func(v pdf.PDFValue) bool { return v == nil }},
		{tr.DefaultTransferFunction, "TR2", func(v pdf.PDFValue) bool { return v == specName("Default") }},
		{tr.ExtGStateRenderingIntent, "RI", func(v pdf.PDFValue) bool { return v == nil }},
		{tr.SoftMaskExtGState, "SMask", func(v pdf.PDFValue) bool { return v == specName("None") }},
		{tr.BlendMode, "BM", func(v pdf.PDFValue) bool { return v == specName("Normal") }},
		{tr.StrokingAlpha, "CA", func(v pdf.PDFValue) bool { return v == pdf.PDFReal(1) }},
		{tr.NonStrokingAlpha, "ca", func(v pdf.PDFValue) bool { return v == pdf.PDFReal(1) }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			gs := specDict("Type", specName("ExtGState"), "TR", specName("Identity"),
				"TR2", specName("Identity"), "RI", specName("Custom"), "SMask", specDict(),
				"BM", specName("Multiply"), "CA", pdf.PDFReal(0.5), "ca", pdf.PDFReal(0.5))
			before := map[string]pdf.PDFValue{}
			for k, v := range gs.Entries.All() {
				before[k] = v
			}
			trailer := trailerWith("GS", gs)

			changed, err := extGStateFixer{spec: a1bSpec}.Fix(&trailer, issuesOf(tc.check))
			if err != nil || !changed {
				t.Fatalf("Fix = %v, %v; want changed", changed, err)
			}
			if got := gs.Entries.Get(tc.key); !tc.repaired(got) {
				t.Errorf("/%s = %v after its own check was reported; want it repaired", tc.key, got)
			}
			for k, v := range before {
				if k == tc.key || k == "Type" {
					continue
				}
				got := gs.Entries.Get(k)
				if _, isDict := v.(pdf.PDFDict); isDict && isSameDict(got, v) || !isDict && got == v {
					continue
				}
				t.Errorf("/%s = %v, want %v left alone: only %s was reported", k, got, v, tc.check.Name())
			}
		})
	}
}

// isSameDict reports whether a and b are the same dictionary, which == does
// not answer for a pdf.PDFDict.
func isSameDict(a, b pdf.PDFValue) bool {
	da, okA := a.(pdf.PDFDict)
	db, okB := b.(pdf.PDFDict)
	return okA && okB && pdf.ValuePointer(da.Entries) == pdf.ValuePointer(db.Entries)
}

// TestExtGStateFixerKeepsZeroOpacityContentUnlessAlphaReported verifies that
// taking out what is drawn at zero opacity belongs to the opacity repair, so it
// does not run when only another ExtGState rule was reported.
func TestExtGStateFixerKeepsZeroOpacityContentUnlessAlphaReported(t *testing.T) {
	const content = "/GS0 gs 0 0 10 10 re f\n"
	for _, tc := range []struct {
		name    string
		issues  []pdf.PDFError
		dropped bool
	}{
		{"blend mode only", issuesOf(pdf.Checks.Transparency.BlendMode), false},
		{"non-stroking alpha", issuesOf(pdf.Checks.Transparency.NonStrokingAlpha), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trailer, page, resources := pageWithContent(content)
			resources.Entries.Set("ExtGState", specDict("GS0",
				specDict("Type", specName("ExtGState"), "ca", pdf.PDFReal(0), "BM", specName("Multiply"))))

			if _, err := (extGStateFixer{spec: a1bSpec}).Fix(&trailer, tc.issues); err != nil {
				t.Fatal(err)
			}
			// The path stays, ended with n instead of painted with f.
			if got := !bytes.Contains(pageContent(t, page), []byte(" f")); got != tc.dropped {
				t.Errorf("invisible fill dropped = %v, want %v; content now %q", got, tc.dropped, pageContent(t, page))
			}
		})
	}
}

// TestContentLimitsFixerRepairsOnlyReportedChecks verifies that the graph and
// the content stream break every rule the fixer covers, and that only one is
// reported. That one is repaired and the rest are left as they were.
func TestContentLimitsFixerRepairsOnlyReportedChecks(t *testing.T) {
	st, co := pdf.Checks.Structure, pdf.Checks.Colour
	long := strings.Repeat("x", a1bSpec.Limits.MaxStringBytes+1)
	type state struct{ integer, real, str, hex, intent, operator bool }
	for _, tc := range []struct {
		check pdf.Check
		want  state // which violations are repaired
	}{
		{st.IntegerOutOfRange, state{integer: true}},
		{st.RealOutOfRange, state{real: true}},
		{st.StringTooLong, state{str: true}},
		// The two hex rules are one repair: dropping a stray character can
		// leave an odd count, and padding needs the stray ones gone first.
		{st.HexStringOddLength, state{hex: true}},
		{st.HexStringInvalidChar, state{hex: true}},
		{co.RenderingIntent, state{intent: true}},
		{co.UndefinedOperator, state{operator: true}},
	} {
		t.Run(tc.check.Name(), func(t *testing.T) {
			trailer, page, _ := pageWithContent("/Custom ri\nfoo\nBI /W 1 /H 1 /BPC 8 /CS /G /Intent /Custom ID \x00 EI\n")
			info := specDict("I", pdf.PDFInteger(1<<40), "R", pdf.PDFReal(1e30),
				"S", pdf.PDFString{Value: long}, "H", pdf.PDFHexString{Value: "4G1"})
			trailer.Entries.Set("Info", info)

			if _, err := (contentLimitsFixer{spec: a1bSpec}).Fix(&trailer, issuesOf(tc.check)); err != nil {
				t.Fatal(err)
			}
			content := pageContent(t, page)
			got := state{
				integer:  info.Entries.Get("I") != pdf.PDFInteger(1<<40),
				real:     info.Entries.Get("R") != pdf.PDFReal(1e30),
				str:      info.Entries.Get("S") != pdf.PDFString{Value: long},
				hex:      info.Entries.Get("H") != pdf.PDFHexString{Value: "4G1"},
				intent:   !bytes.Contains(content, []byte("Custom")),
				operator: !bytes.Contains(content, []byte("foo")),
			}
			if got != tc.want {
				t.Errorf("repaired %+v, want %+v", got, tc.want)
			}
			if !bytes.Contains(content, []byte("BI")) {
				t.Errorf("inline image dropped; content now %q", content)
			}
		})
	}
}

// pairFixer covers two checks and records every call, the shape of a fixer
// like extGStateFixer that one registration hands several checks.
type pairFixer struct {
	a, b  pdf.Check
	calls *[][]pdf.PDFError
}

func (f pairFixer) Applies(c pdf.Check) bool { return c == f.a || c == f.b }
func (f pairFixer) Fix(_ *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	*f.calls = append(*f.calls, issues)
	return false, nil
}

// TestDispatchHandsAFixerAllItsReportedChecks verifies that a fixer covering
// two reported checks runs once in the pass and sees the issues of both, which
// is what lets it repair each reported rule and only those.
func TestDispatchHandsAFixerAllItsReportedChecks(t *testing.T) {
	a, b := pdf.Checks.Transparency.TransferFunction, pdf.Checks.Transparency.BlendMode
	var calls [][]pdf.PDFError
	f := pairFixer{a: a, b: b, calls: &calls}
	result := pdf.Result{Issues: issuesOf(b, a, b)}

	trailer := pdf.NewPDFDict()
	if _, err := dispatchFixers(&fixPass{trailer: &trailer}, result, map[pdf.Check]Fixer{a: f, b: f}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("fixer ran %d times in one pass, want once", len(calls))
	}
	var got []pdf.Check
	for _, iss := range calls[0] {
		got = append(got, iss.Check())
	}
	if want := []pdf.Check{a, b, b}; !slices.Equal(got, want) {
		t.Errorf("fixer saw checks %v, want %v (clause order, every issue)", got, want)
	}
}

// allContentLimits is contentLimitsFixer under PDF/A-1b with every check it
// covers reported, for the tests of its content rewrite on its own.
var allContentLimits = contentLimitsFixer{spec: a1bSpec}.reporting(everyCheck(contentLimitsFixer{}))

// TestDispatchFlattensBeforeTheOpacityRepair verifies that a group is
// flattened before the opacity repair runs, so that overlapping translucent
// fills accumulate: two red fills at opacity 0.3 must yield 0.51, not 0.3.
func TestDispatchFlattensBeforeTheOpacityRepair(t *testing.T) {
	gs := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0.3)})}
	form := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Form"},
		"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
		"Group":   pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}})},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ExtGState": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"A": gs})},
		})},
	}), []byte("/A gs 1 0 0 rg 0 0 20 20 re f 0 0 20 20 re f"))
	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm": form})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"MediaBox":  pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"XObject": xobjects})},
		"Contents":  pdf.NewStreamDict(nil, []byte("/Fm Do")),
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"}, "Kids": pdf.PDFArray{page}, "Count": pdf.PDFInteger(1),
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Catalog"}, "Pages": pages})},
	})}

	tr := pdf.Checks.Transparency
	spec := a1b()
	local := standardRepairs.fixersFor(&fixerRun{spec: &spec, dpi: 72})
	result := pdf.Result{Issues: issuesOf(tr.NonStrokingAlpha, tr.TransparencyGroup)}
	if _, err := dispatchFixers(&fixPass{trailer: &trailer}, result, local); err != nil {
		t.Fatal(err)
	}

	flat := xobjects.Entries.Get("Fm").(pdf.PDFDict)
	res, _ := flat.Entries.Get("Resources").(pdf.PDFDict)
	inner, _ := res.Entries.Get("XObject").(pdf.PDFDict)
	img, ok := inner.Entries.Get("Im0").(pdf.PDFDict)
	if !ok {
		t.Fatal("the group form was not flattened")
	}
	samples, err := pdf.DecodeStream(img)
	if err != nil {
		t.Fatalf("decoding the flattened image: %v", err)
	}
	// Red at 0.51 over white: G and B at 255 * 0.49, about 125.
	if g := int(samples[1]); g < 120 || g > 130 {
		t.Errorf("flattened pixel = %v, want G about 125 (two fills at 0.3 built up)", samples[:3])
	}
}

// TestAnnotationFlagsFixerRepairsOnlyReportedChecks verifies that an
// annotation breaking all five flag and opacity rules has only the reported
// rule repaired.
func TestAnnotationFlagsFixerRepairsOnlyReportedChecks(t *testing.T) {
	an := pdf.Checks.Annotation
	const bad = verify.AnnotFlagHidden | verify.AnnotFlagInvisible | verify.AnnotFlagNoView
	for _, tc := range []struct {
		check pdf.Check
		flags int
		ca    pdf.PDFValue
	}{
		{an.PrintFlagNotSet, bad | verify.AnnotFlagPrint, pdf.PDFReal(0.5)},
		{an.HiddenFlagSet, bad &^ verify.AnnotFlagHidden, pdf.PDFReal(0.5)},
		{an.InvisibleFlagSet, bad &^ verify.AnnotFlagInvisible, pdf.PDFReal(0.5)},
		{an.NoViewFlagSet, bad &^ verify.AnnotFlagNoView, pdf.PDFReal(0.5)},
		{an.OpacityNotOne, bad, pdf.PDFReal(1)},
	} {
		t.Run(tc.check.Name(), func(t *testing.T) {
			annot := specDict("Type", specName("Annot"), "F", pdf.PDFInteger(bad), "CA", pdf.PDFReal(0.5))
			trailer := trailerWith("Annot0", annot)

			changed, err := annotationFlagsFixer{}.Fix(&trailer, issuesOf(tc.check))
			if err != nil || !changed {
				t.Fatalf("Fix = %v, %v; want changed", changed, err)
			}
			if got := annot.Entries.Get("F"); got != pdf.PDFInteger(tc.flags) {
				t.Errorf("/F = %v, want %d", got, tc.flags)
			}
			if got := annot.Entries.Get("CA"); got != tc.ca {
				t.Errorf("/CA = %v, want %v", got, tc.ca)
			}
		})
	}
}

// repairProbe reports whether one repair of a fixer was made on a fixture.
type repairProbe struct {
	check    pdf.Check
	what     string
	repaired func() bool
}

// assertRepairsOnlyReported runs fix once per probe on a fresh fixture that
// breaks every rule, with only that probe's check reported, and verifies that
// exactly the probes of that check were repaired.
func assertRepairsOnlyReported(t *testing.T, fixture func() (pdf.PDFDict, graphFixer, []repairProbe)) {
	t.Helper()
	_, _, probes := fixture()
	seen := map[pdf.Check]bool{}
	for _, p := range probes {
		if seen[p.check] {
			continue
		}
		seen[p.check] = true
		t.Run(p.check.Name(), func(t *testing.T) {
			trailer, f, probes := fixture()
			changed, err := f.Fix(&trailer, issuesOf(p.check))
			if err != nil || !changed {
				t.Fatalf("Fix = %v, %v; want changed", changed, err)
			}
			for _, q := range probes {
				if got, want := q.repaired(), q.check == p.check; got != want {
					t.Errorf("%s repaired = %v, want %v with only %s reported", q.what, got, want, p.check.Name())
				}
			}
		})
	}
}

// TestActionFixerRepairsOnlyReportedChecks verifies that the forbidden action,
// named action and additional-actions repairs each run only when their own
// check was reported.
func TestActionFixerRepairsOnlyReportedChecks(t *testing.T) {
	ac := pdf.Checks.Action
	assertRepairsOnlyReported(t, func() (pdf.PDFDict, graphFixer, []repairProbe) {
		launch := specDict("S", specName("Launch"))
		named := specDict("S", specName("Named"), "N", specName("Quit"))
		link := specDict("Type", specName("Annot"), "Subtype", specName("Link"), "A", named)
		trailer := trailerWith("OpenAction", launch)
		root := trailer.Entries.Get("Root").(pdf.PDFDict)
		root.Entries.Set("AA", specDict("WC", specDict("S", specName("GoTo"))))
		root.Entries.Set("Names", specDict("JavaScript",
			specDict("Names", pdf.PDFArray{pdf.PDFString{Value: "a"}, specDict("S", specName("JavaScript"))})))
		root.Entries.Set("Link", link)
		names := root.Entries.Get("Names").(pdf.PDFDict)
		return trailer, actionFixer{spec: a1bSpec}, []repairProbe{
			{ac.ForbiddenActionType, "/OpenAction", func() bool { return root.Entries.Get("OpenAction") == nil }},
			{ac.ForbiddenActionType, "JavaScript name tree", func() bool { return names.Entries.Get("JavaScript") == nil }},
			{ac.DisallowedNamedAction, "named action", func() bool { return named.Entries.Get("S") == nil }},
			{ac.AdditionalActions, "/AA", func() bool { return root.Entries.Get("AA") == nil }},
		}
	})
}

// TestFormFixerRepairsOnlyReportedChecks verifies that each AcroForm and form
// field repair runs only when its own check was reported.
func TestFormFixerRepairsOnlyReportedChecks(t *testing.T) {
	fm := pdf.Checks.Form
	assertRepairsOnlyReported(t, func() (pdf.PDFDict, graphFixer, []repairProbe) {
		form := specDict("NeedAppearances", pdf.PDFBoolean(true), "XFA", pdf.PDFArray{})
		field := specDict("FT", specName("Tx"), "A", specDict("S", specName("GoTo")),
			"AA", specDict("K", specDict("S", specName("GoTo"))))
		trailer := trailerWith("AcroForm", form)
		form.Entries.Set("Fields", pdf.PDFArray{field})
		return trailer, formFixer{}, []repairProbe{
			{fm.NeedAppearances, "/NeedAppearances", func() bool { return form.Entries.Get("NeedAppearances") == pdf.PDFBoolean(false) }},
			{fm.XFA, "/XFA", func() bool { return form.Entries.Get("XFA") == nil }},
			{fm.FieldAction, "field /A", func() bool { return field.Entries.Get("A") == nil }},
			{fm.FieldAdditionalActions, "field /AA", func() bool { return field.Entries.Get("AA") == nil }},
		}
	})
}

// TestFileSpecFixerRepairsOnlyReportedChecks verifies that each embedded file
// and external stream file repair runs only when its own check was reported.
func TestFileSpecFixerRepairsOnlyReportedChecks(t *testing.T) {
	st := pdf.Checks.Structure
	assertRepairsOnlyReported(t, func() (pdf.PDFDict, graphFixer, []repairProbe) {
		spec := specDict("Type", specName("Filespec"), "EF", specDict("F", specStream([]byte("x"))))
		annot := specDict("Type", specName("Annot"), "Subtype", specName("FileAttachment"), "FS", spec)
		names := specDict("EmbeddedFiles", specDict("Names", pdf.PDFArray{}))
		stream := specStream([]byte("x"), "F", pdf.PDFString{Value: "a"},
			"FFilter", specName("FlateDecode"), "FDecodeParms", specDict())
		trailer := trailerWith("Names", names)
		root := trailer.Entries.Get("Root").(pdf.PDFDict)
		root.Entries.Set("Annot0", annot)
		root.Entries.Set("S", stream)
		gone := func(d pdf.PDFDict, key string) func() bool {
			return func() bool { return d.Entries.Get(key) == nil }
		}
		return trailer, fileSpecFixer{}, []repairProbe{
			{st.EmbeddedFileSpec, "/EF", gone(spec, "EF")},
			{st.EmbeddedFiles, "/EmbeddedFiles", gone(names, "EmbeddedFiles")},
			{st.StreamFileSpec, "stream /F", gone(stream, "F")},
			{st.StreamFileFilter, "stream /FFilter", gone(stream, "FFilter")},
			{st.StreamFileDecodeParams, "stream /FDecodeParms", gone(stream, "FDecodeParms")},
		}
	})
}
