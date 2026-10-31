package convert

import (
	"bytes"
	"reflect"
	"strconv"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

func specName(n string) pdf.PDFName { return pdf.PDFName{Value: n} }

// specDict builds a dict from alternating keys and values.
func specDict(kv ...any) pdf.PDFDict {
	d := pdf.NewPDFDict()
	for i := 0; i+1 < len(kv); i += 2 {
		d.Entries.Set(kv[i].(string), kv[i+1].(pdf.PDFValue))
	}
	return d
}

// specStream is a stream dict holding data unfiltered.
func specStream(data []byte, kv ...any) pdf.PDFDict {
	d := specDict(kv...)
	d.SetStream(data)
	return d
}

// iccHeader is a 128-byte RGB display-class ICC profile header of the given
// major version.
func iccHeader(major byte) []byte {
	data := make([]byte, 128)
	data[8] = major
	copy(data[12:16], "mntr")
	copy(data[16:20], "RGB ")
	copy(data[36:40], "acsp")
	return data
}

// pageContent decodes the page's content stream.
func pageContent(t *testing.T, page pdf.PDFDict) []byte {
	t.Helper()
	data, err := pdf.DecodeStream(page.Entries.Get("Contents").(pdf.PDFDict))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestFixersFollowTheLevelSpec verifies that a fixer decides what to repair,
// and with what, by the run's spec rather than by hard-wired PDF/A-1b rules.
// Under PDF/A-1b each input is repaired; under a spec changed in that one rule
// it is left alone (or, inverted, the other way round).
func TestFixersFollowTheLevelSpec(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(s *pdfa.Spec)
		// fix applies the fixer bound to s to a fresh input and reports
		// whether it repaired it.
		fix      func(t *testing.T, s *pdfa.Spec) bool
		inverted bool
	}{
		{"annotation subtype", func(s *pdfa.Spec) { s.AnnotationSubtypes = pdfa.NewNames("Movie") },
			func(t *testing.T, s *pdfa.Spec) bool {
				annot := specDict("Type", specName("Annot"), "Subtype", specName("Movie"))
				trailer := trailerWith("A", annot)
				_, _ = disallowedAnnotFixer{spec: s}.Fix(&trailer, nil)
				return annot.Entries.Get("Subtype") == nil
			}, false},
		{"appearance of an allowed annotation", func(s *pdfa.Spec) { s.AnnotationSubtypes = pdfa.NewNames("Movie") },
			func(t *testing.T, s *pdfa.Spec) bool {
				annot := specDict("Type", specName("Annot"), "Subtype", specName("Movie"),
					"Rect", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
					"F", pdf.PDFInteger(4))
				trailer := trailerWith("A", annot)
				_, _ = appearanceFixer{spec: s}.Fix(&trailer, nil)
				return annot.Entries.Get("AP") != nil
			}, true},
		{"forbidden action", func(s *pdfa.Spec) { s.ForbiddenActions = pdfa.Names{} },
			func(t *testing.T, s *pdfa.Spec) bool {
				action := specDict("S", specName("Launch"), "F", pdf.PDFString{Value: "x"})
				trailer := trailerWith("X", action)
				_, _ = actionFixer{spec: s}.Fix(&trailer, everyCheck(actionFixer{}))
				return action.Entries.Get("S") == nil
			}, false},
		{"forbidden action in a JavaScript tree", func(s *pdfa.Spec) { s.ForbiddenActions = pdfa.Names{} },
			func(t *testing.T, s *pdfa.Spec) bool {
				action := specDict("S", specName("JavaScript"))
				tree := specDict("Names", pdf.PDFArray{pdf.PDFString{Value: "a"}, action})
				trailer := trailerWith("Names", specDict("JavaScript", tree))
				_, _ = actionFixer{spec: s}.Fix(&trailer, everyCheck(actionFixer{}))
				return action.Entries.Get("S") == nil
			}, false},
		{"dead action owner", func(s *pdfa.Spec) { s.ForbiddenActions = pdfa.Names{} },
			func(t *testing.T, s *pdfa.Spec) bool {
				annot := specDict("Type", specName("Annot"), "Subtype", specName("Link"),
					"A", specDict("S", specName("Launch")))
				trailer := trailerWith("L", annot)
				_, _ = actionFixer{spec: s}.Fix(&trailer, everyCheck(actionFixer{}))
				return annot.Entries.Get("A") == nil
			}, false},
		{"named action", func(s *pdfa.Spec) { s.NamedActions = pdfa.NewNames("Print") },
			func(t *testing.T, s *pdfa.Spec) bool {
				action := specDict("S", specName("Named"), "N", specName("Print"))
				trailer := trailerWith("OpenAction", action)
				_, _ = actionFixer{spec: s}.Fix(&trailer, everyCheck(actionFixer{}))
				return action.Entries.Get("S") == nil
			}, false},
		{"ExtGState rendering intent", func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(t *testing.T, s *pdfa.Spec) bool {
				gs := specDict("Type", specName("ExtGState"), "RI", specName("Custom"))
				trailer := trailerWith("GS", gs)
				_, _ = extGStateFixer{spec: s}.Fix(&trailer, everyCheck(extGStateFixer{}))
				return gs.Entries.Get("RI") == nil
			}, false},
		{"blend mode", func(s *pdfa.Spec) { s.BlendModes = pdfa.NewNames("Multiply") },
			func(t *testing.T, s *pdfa.Spec) bool {
				gs := specDict("Type", specName("ExtGState"), "BM", specName("Multiply"))
				trailer := trailerWith("GS", gs)
				_, _ = extGStateFixer{spec: s}.Fix(&trailer, everyCheck(extGStateFixer{}))
				return gs.Entries.Get("BM") == specName("Normal")
			}, false},
		{"ExtGState soft mask and alpha", func(s *pdfa.Spec) { s.Transparency = true },
			func(t *testing.T, s *pdfa.Spec) bool {
				gs := specDict("Type", specName("ExtGState"), "SMask", specDict(),
					"CA", pdf.PDFReal(0.5), "ca", pdf.PDFReal(0.5))
				trailer := trailerWith("GS", gs)
				_, _ = extGStateFixer{spec: s}.Fix(&trailer, everyCheck(extGStateFixer{}))
				smask := gs.Entries.Get("SMask") == specName("None")
				ca := gs.Entries.Get("CA") == pdf.PDFReal(1)
				nonStroking := gs.Entries.Get("ca") == pdf.PDFReal(1)
				if smask != ca || ca != nonStroking {
					t.Errorf("SMask, CA and ca disagree: repaired %v, %v, %v", smask, ca, nonStroking)
				}
				return smask
			}, false},
		{"image rendering intent", func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(t *testing.T, s *pdfa.Spec) bool {
				img := specDict("Subtype", specName("Image"), "Intent", specName("Custom"))
				trailer := trailerWith("I", img)
				_, _ = imageMetadataFixer{spec: s}.Fix(&trailer, nil)
				return img.Entries.Get("Intent") == nil
			}, false},
		{"viewer preference", func(s *pdfa.Spec) { s.ForbiddenViewerPrefKeys = pdfa.Names{} },
			func(t *testing.T, s *pdfa.Spec) bool {
				vp := specDict("PrintScaling", specName("None"))
				trailer := trailerWith("ViewerPreferences", vp)
				_, _ = viewerPrefFixer{spec: s}.Fix(&trailer, nil)
				return vp.Entries.Get("PrintScaling") == nil
			}, false},
		{"content rendering intent", func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(t *testing.T, s *pdfa.Spec) bool {
				trailer, page, _ := pageWithContent("/Custom ri\n")
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return !bytes.Contains(pageContent(t, page), []byte("Custom"))
			}, false},
		{"inline image rendering intent", func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(t *testing.T, s *pdfa.Spec) bool {
				trailer, page, _ := pageWithContent("BI /W 1 /H 1 /BPC 8 /CS /G /Intent /Custom ID \x00 EI\n")
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return !bytes.Contains(pageContent(t, page), []byte("Custom"))
			}, false},
		{"ICCBased profile version", func(s *pdfa.Spec) { s.ICCMaxMajorVersion = 4 },
			func(t *testing.T, s *pdfa.Spec) bool {
				icc := specStream(iccHeader(4), "N", pdf.PDFInteger(3))
				space := pdf.PDFArray{specName("ICCBased"), icc}
				trailer := trailerWith("CS", space)
				_, _ = iccBasedProfileFixer{spec: s}.Fix(&trailer, nil)
				return space[1].(pdf.PDFDict).Stream()[8] != 4
			}, false},
		{"output intent profile version", func(s *pdfa.Spec) { s.ICCMaxMajorVersion = 4 },
			func(t *testing.T, s *pdfa.Spec) bool {
				intent := specDict("Type", specName("OutputIntent"), "S", specName("GTS_PDFA1"),
					"OutputConditionIdentifier", pdf.PDFString{Value: "sRGB"},
					"DestOutputProfile", specStream(iccHeader(4), "N", pdf.PDFInteger(3)))
				trailer := trailerWith("OutputIntents", pdf.PDFArray{intent})
				if err := injectOutputIntent(&trailer, nil, *s); err != nil {
					t.Fatal(err)
				}
				root := trailer.Entries.Get("Root").(pdf.PDFDict)
				return !pdf.EqualPDFValue(root.Entries.Get("OutputIntents"), pdf.PDFArray{intent})
			}, false},
		{"font file subtype", func(s *pdfa.Spec) { s.FontFileSubtypes = pdfa.NewNames("OpenType") },
			func(t *testing.T, s *pdfa.Spec) bool {
				ff := specStream([]byte{0}, "Subtype", specName("OpenType"))
				font := specDict("Type", specName("Font"), "Subtype", specName("Type1"),
					"FontDescriptor", specDict("FontFile", ff))
				trailer := trailerWith("F", font)
				_, _ = fontFileSubtypeFixer{spec: s}.Fix(&trailer, nil)
				return ff.Entries.Get("Subtype") == nil
			}, false},
		{"output intent coverage of annotation colour", func(s *pdfa.Spec) { s.OutputIntentSubtype = "GTS_PDFX" },
			func(t *testing.T, s *pdfa.Spec) bool {
				annot := specDict("Type", specName("Annot"),
					"C", pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)})
				intent := specDict("S", specName("GTS_PDFA1"), "DestOutputProfile", specDict("N", pdf.PDFInteger(3)))
				trailer := trailerWith("A", annot)
				trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Set("OutputIntents", pdf.PDFArray{intent})
				_, _ = annotColourFixer{spec: s}.Fix(&trailer, nil)
				return annot.Entries.Get("C") == nil
			}, true},
		{"content real operand", func(s *pdfa.Spec) { s.Limits.MaxReal = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				trailer, page, _ := pageWithContent("40000.5 Tc\n")
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return !bytes.Contains(pageContent(t, page), []byte("40000"))
			}, false},
		{"content integer operand", func(s *pdfa.Spec) { s.Limits.MaxInteger, s.Limits.MinInteger = 0, 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				trailer, page, _ := pageWithContent("3000000000 Tz -3000000000 Tz\n")
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return !bytes.Contains(pageContent(t, page), []byte("3000000000"))
			}, false},
		{"content string operand", func(s *pdfa.Spec) { s.Limits.MaxStringBytes = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				long := string(bytes.Repeat([]byte("x"), 65536))
				trailer, page, _ := pageWithContent("BT (" + long + ") Tj ET\n")
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return !bytes.Contains(pageContent(t, page), []byte(long))
			}, false},
		{"graph real", func(s *pdfa.Spec) { s.Limits.MaxReal = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				d := specDict("V", pdf.PDFReal(-40000))
				trailer := trailerWith("D", d)
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return d.Entries.Get("V") != pdf.PDFReal(-40000)
			}, false},
		{"path coordinate", func(s *pdfa.Spec) { s.Limits.MaxReal = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				trailer, page, _ := pageWithContent("0 0 m 40000.5 0 l S\n")
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return !bytes.Contains(pageContent(t, page), []byte("40000"))
			}, false},
		{"oversized matrix", func(s *pdfa.Spec) { s.Limits.MaxReal = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				trailer, page, _ := pageWithContent("40000.5 0 0 40000.5 0 0 cm\n")
				_, _ = contentLimitsFixer{spec: s}.Fix(&trailer, everyCheck(contentLimitsFixer{}))
				return !bytes.Contains(pageContent(t, page), []byte("40000"))
			}, false},
		{"page tree Kids", func(s *pdfa.Spec) { s.Limits.MaxArrayElements = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				kids := make(pdf.PDFArray, 8192)
				for i := range kids {
					kids[i] = specDict("Type", specName("Page"))
				}
				pages := specDict("Type", specName("Pages"), "Kids", kids, "Count", pdf.PDFInteger(len(kids)))
				trailer := trailerWith("Pages", pages)
				_, _ = pagesTreeArrayFixer{spec: s}.Fix(&trailer, nil)
				return len(pages.Entries.Get("Kids").(pdf.PDFArray)) < len(kids)
			}, false},
		{"unused resources", func(s *pdfa.Spec) { s.Limits.MaxDictEntries = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				fonts := pdf.NewPDFDict()
				for i := range 4096 {
					fonts.Entries.Set("F"+strconv.Itoa(i), specDict())
				}
				page := specDict("Type", specName("Page"), "Resources", specDict("Font", fonts))
				trailer := trailerWith("P", page)
				_, _ = resourceDictPruneFixer{spec: s}.Fix(&trailer, nil)
				return fonts.Entries.Len() < 4096
			}, false},
		{"name value", func(s *pdfa.Spec) { s.Limits.MaxNameBytes = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				long := specName(string(bytes.Repeat([]byte("n"), 128)))
				d := specDict("N", long)
				trailer := trailerWith("D", d)
				_, _ = nameTooLongFixer{spec: s}.Fix(&trailer, nil)
				return d.Entries.Get("N") != long
			}, false},
		{"dictionary key", func(s *pdfa.Spec) { s.Limits.MaxNameBytes = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				long := string(bytes.Repeat([]byte("k"), 128))
				d := specDict(long, pdf.PDFInteger(1))
				trailer := trailerWith("D", d)
				_, _ = nameTooLongFixer{spec: s}.Fix(&trailer, nil)
				return d.Entries.Get(long) == nil
			}, false},
		{"CMap CID", func(s *pdfa.Spec) { s.Limits.MaxCID = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				cmap := specStream([]byte("1 begincidchar <00> 70000 endcidchar"), "Type", specName("CMap"))
				holder := specDict("C", cmap)
				trailer := trailerWith("H", holder)
				_, _ = cmapCIDClampFixer{spec: s}.Fix(&trailer, nil)
				data, err := pdf.DecodeStream(holder.Entries.Get("C").(pdf.PDFDict))
				if err != nil {
					t.Fatal(err)
				}
				return !bytes.Contains(data, []byte("70000"))
			}, false},
		{"q nesting", func(s *pdfa.Spec) { s.Limits.MaxQNesting = 0 },
			func(t *testing.T, s *pdfa.Spec) bool {
				trailer, page, _ := pageWithContent(nestedQ(29, "0 0 10 10 re f\n"))
				before := pageContent(t, page)
				_, _ = qNestingFixer{spec: s}.Fix(&trailer, nil)
				return !bytes.Equal(pageContent(t, page), before)
			}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			under1b, changed := a1b(), a1b()
			tc.change(&changed)
			repaired1b, repairedChanged := tc.fix(t, &under1b), tc.fix(t, &changed)
			if tc.inverted {
				repaired1b, repairedChanged = repairedChanged, repaired1b
			}
			if !repaired1b {
				t.Error("not repaired under PDF/A-1b")
			}
			if repairedChanged {
				t.Error("repaired under the changed spec: the rule is hard-wired")
			}
		})
	}
}

// TestRunBindsItsSpecIntoEveryFixer verifies that every fixer a run uses
// carries the run's spec. A fixer reads its rules from the spec it holds, and
// a nil spec panics.
func TestRunBindsItsSpecIntoEveryFixer(t *testing.T) {
	spec := a1b()
	local := standardRepairs.fixersFor(&fixerRun{spec: &spec, dpi: 72})
	specType := reflect.TypeOf(&spec)
	bound := 0
	for c, f := range local {
		v := reflect.ValueOf(f)
		field := v.FieldByName("spec")
		if !field.IsValid() {
			continue
		}
		if field.Type() != specType {
			t.Fatalf("%T.spec is a %s, want %s", f, field.Type(), specType)
		}
		if field.Pointer() != reflect.ValueOf(&spec).Pointer() {
			t.Errorf("the fixer for %s (%T) does not carry the run's spec", c.Name(), f)
		}
		bound++
	}
	if bound == 0 {
		t.Fatal("no fixer holds a spec")
	}
}

// TestInjectedOutputIntentNamesTheLevel verifies that the OutputIntent convert
// adds carries the subtype the level requires.
func TestInjectedOutputIntentNamesTheLevel(t *testing.T) {
	for _, subtype := range []string{"GTS_PDFA1", "GTS_PDFX"} {
		s := a1b()
		s.OutputIntentSubtype = subtype
		trailer := trailerWith("Pages", specDict("Type", specName("Pages")))
		if err := injectOutputIntent(&trailer, nil, s); err != nil {
			t.Fatal(err)
		}
		intents := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("OutputIntents").(pdf.PDFArray)
		if got := intents[0].(pdf.PDFDict).Entries.Get("S"); got != specName(subtype) {
			t.Errorf("injected /S = %v, want /%s", got, subtype)
		}
		// An intent of the level's own subtype is one the next run keeps.
		if _, ok := validPDFAOutputIntentN(trailer.Entries.Get("Root").(pdf.PDFDict), s); !ok {
			t.Errorf("the injected /%s intent is not a valid output intent of its own level", subtype)
		}
	}
}

// TestRegeneratedXMPDeclaresTheLevel verifies that the pdfaid identity convert
// writes is the level's, not a hard-wired 1/B.
func TestRegeneratedXMPDeclaresTheLevel(t *testing.T) {
	s := a1b()
	s.XMPPart, s.XMPConformance = "2", "U"
	xmp := buildXMPPacket(pdf.NewPDFDict(), s)
	for _, want := range []string{"<pdfaid:part>2</pdfaid:part>", "<pdfaid:conformance>U</pdfaid:conformance>"} {
		if !bytes.Contains([]byte(xmp), []byte(want)) {
			t.Errorf("packet lacks %s", want)
		}
	}
}
