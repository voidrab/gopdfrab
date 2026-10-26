package verify

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// specDict builds a dict from alternating keys and values.
func specDict(kv ...any) pdf.PDFDict {
	d := pdf.NewPDFDict()
	for i := 0; i+1 < len(kv); i += 2 {
		d.Entries.Set(kv[i].(string), kv[i+1].(pdf.PDFValue))
	}
	return d
}

func specName(n string) pdf.PDFName { return pdf.PDFName{Value: n} }

// withSpec runs fn on a throwaway context applying s and returns its issues.
func withSpec(s pdfa.Spec, fn func(ctx *ValidationContext)) []pdf.PDFError {
	ctx := NewContext(nil)
	ctx.Spec = &s
	fn(ctx)
	return ctx.errs
}

func reports(issues []pdf.PDFError, c pdf.Check) bool {
	for _, e := range issues {
		if e.Check() == c {
			return true
		}
	}
	return false
}

// iccHeader is a 128-byte ICC profile header of the given major version.
func iccHeader(major byte, class string) []byte {
	data := make([]byte, 128)
	data[8] = major
	copy(data[12:16], class)
	copy(data[16:20], "RGB ")
	copy(data[36:40], "acsp")
	return data
}

func iccStream(major byte, class string) pdf.PDFDict {
	s := specDict("N", pdf.PDFInteger(3))
	s.HasStream = true
	s.RawStream = iccHeader(major, class)
	return s
}

// outputIntentDoc is a reader whose catalogue has one output intent of
// subtype s, with an RGB destination profile.
func outputIntentDoc(t *testing.T, s string) *pdf.Reader {
	t.Helper()
	intent := specDict("Type", specName("OutputIntent"), "S", specName(s),
		"OutputConditionIdentifier", pdf.PDFString{Value: "sRGB"},
		"DestOutputProfile", iccStream(2, "mntr"))
	root := specDict("Type", specName("Catalog"), "OutputIntents", pdf.PDFArray{intent})
	return rawReaderWithTrailer(t, specDict("Root", root))
}

// TestChecksFollowTheLevelSpec: each rule a level decides is read from the
// spec, not hard-wired. Under PDF/A-1b the input is reported; under a spec
// changed in that one rule it is not (or, for a required value, the other
// way round).
func TestChecksFollowTheLevelSpec(t *testing.T) {
	big := strings.Repeat("n", 128)
	for _, tc := range []struct {
		name   string
		check  pdf.Check
		change func(s *pdfa.Spec)
		run    func(s pdfa.Spec) []pdf.PDFError
		// inverted: the input conforms to PDF/A-1b and the changed spec
		// rejects it.
		inverted bool
	}{
		{"ExtGState rendering intent", pdf.Checks.Transparency.ExtGStateRenderingIntent,
			func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateExtGState(specDict("Type", specName("ExtGState"), "RI", specName("Custom")), ctx)
				})
			}, false},
		{"blend mode", pdf.Checks.Transparency.BlendMode,
			func(s *pdfa.Spec) { s.BlendModes = pdfa.NewNames("Multiply") },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateExtGState(specDict("BM", specName("Multiply")), ctx)
				})
			}, false},
		{"ExtGState soft mask", pdf.Checks.Transparency.SoftMaskExtGState,
			func(s *pdfa.Spec) { s.Transparency = true },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateExtGState(specDict("SMask", specDict("S", specName("Luminosity"))), ctx)
				})
			}, false},
		{"stroking alpha", pdf.Checks.Transparency.StrokingAlpha,
			func(s *pdfa.Spec) { s.Transparency = true },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateExtGState(specDict("CA", pdf.PDFReal(0.5)), ctx)
				})
			}, false},
		{"non-stroking alpha", pdf.Checks.Transparency.NonStrokingAlpha,
			func(s *pdfa.Spec) { s.Transparency = true },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateExtGState(specDict("ca", pdf.PDFReal(0.5)), ctx)
				})
			}, false},
		{"transparency group", pdf.Checks.Transparency.TransparencyGroup,
			func(s *pdfa.Spec) { s.Transparency = true },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateTransparencyGroup(specDict("Group", specDict("S", specName("Transparency"))), ctx)
				})
			}, false},
		{"image soft mask", pdf.Checks.Transparency.ImageWithSoftMask,
			func(s *pdfa.Spec) { s.Transparency = true },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateXObjectDict(specDict("Subtype", specName("Image"), "SMask", specDict()), ctx)
				})
			}, false},
		{"image rendering intent", pdf.Checks.Image.ImageRenderingIntent,
			func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateXObjectDict(specDict("Subtype", specName("Image"), "Intent", specName("Custom")), ctx)
				})
			}, false},
		{"forbidden action", pdf.Checks.Action.ForbiddenActionType,
			func(s *pdfa.Spec) { s.ForbiddenActions = pdfa.Names{} },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateActions(specDict("S", specName("Launch")), ctx)
				})
			}, false},
		{"named action", pdf.Checks.Action.DisallowedNamedAction,
			func(s *pdfa.Spec) { s.NamedActions = pdfa.NewNames("Print") },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateActions(specDict("S", specName("Named"), "N", specName("Print")), ctx)
				})
			}, false},
		{"viewer preference", pdf.Checks.Structure.PostPDF14ViewerPref,
			func(s *pdfa.Spec) { s.ForbiddenViewerPrefKeys = pdfa.Names{} },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateViewerPreferences(specDict("Type", specName("Catalog"),
						"ViewerPreferences", specDict("PrintScaling", specName("None"))), ctx)
				})
			}, false},
		{"annotation subtype", pdf.Checks.Annotation.DisallowedSubtype,
			func(s *pdfa.Spec) { s.AnnotationSubtypes = pdfa.NewNames("FileAttachment") },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateAnnotation(specDict("Type", specName("Annot"), "Subtype", specName("FileAttachment")), ctx)
				})
			}, false},
		{"content rendering intent", pdf.Checks.Colour.RenderingIntent,
			func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					contentChecker(nil, pdf.PDFDict{}, ctx)("ri", []pdf.PDFValue{specName("Custom")})
				})
			}, false},
		{"inline image rendering intent", pdf.Checks.Colour.RenderingIntent,
			func(s *pdfa.Spec) { s.RenderingIntents = pdfa.NewNames("Custom") },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					checkInlineImageOther(nil, []pdf.PDFValue{specName("Intent"), specName("Custom")}, ctx)
				})
			}, false},
		{"q nesting", pdf.Checks.Structure.GraphicsStateNesting,
			func(s *pdfa.Spec) { s.Limits.MaxQNesting = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					scan := contentChecker(nil, pdf.PDFDict{}, ctx)
					for range 29 {
						scan("q", nil)
					}
				})
			}, false},
		{"content integer", pdf.Checks.Structure.IntegerOutOfRange,
			func(s *pdfa.Spec) { s.Limits.MaxInteger, s.Limits.MinInteger = 0, 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					checkOperandLimits(pdf.PDFInteger(1<<31), nil, ctx)
					checkOperandLimits(pdf.PDFInteger(-1<<31-1), nil, ctx)
				})
			}, false},
		{"content real", pdf.Checks.Structure.RealOutOfRange,
			func(s *pdfa.Spec) { s.Limits.MaxReal = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) { checkOperandLimits(pdf.PDFReal(40000), nil, ctx) })
			}, false},
		{"content string", pdf.Checks.Structure.StringTooLong,
			func(s *pdfa.Spec) { s.Limits.MaxStringBytes = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					checkOperandLimits(pdf.PDFString{Value: strings.Repeat("x", 65536)}, nil, ctx)
				})
			}, false},
		{"name", pdf.Checks.Structure.NameTooLong,
			func(s *pdfa.Spec) { s.Limits.MaxNameBytes = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) { validateArchitecturalLimits(specName(big), nil, ctx) })
			}, false},
		{"dictionary key", pdf.Checks.Structure.NameTooLong,
			func(s *pdfa.Spec) { s.Limits.MaxNameBytes = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) { verifyDocument(specDict(big, pdf.PDFInteger(1)), ctx) })
			}, false},
		{"integer", pdf.Checks.Structure.IntegerOutOfRange,
			func(s *pdfa.Spec) { s.Limits.MaxInteger, s.Limits.MinInteger = 0, 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateArchitecturalLimits(pdf.PDFInteger(1<<31), nil, ctx)
					validateArchitecturalLimits(pdf.PDFInteger(-1<<31-1), nil, ctx)
				})
			}, false},
		{"real", pdf.Checks.Structure.RealOutOfRange,
			func(s *pdfa.Spec) { s.Limits.MaxReal = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) { validateArchitecturalLimits(pdf.PDFReal(-40000), nil, ctx) })
			}, false},
		{"string", pdf.Checks.Structure.StringTooLong,
			func(s *pdfa.Spec) { s.Limits.MaxStringBytes = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateArchitecturalLimits(pdf.PDFString{Value: strings.Repeat("x", 65536)}, nil, ctx)
				})
			}, false},
		{"dictionary size", pdf.Checks.Structure.DictTooLarge,
			func(s *pdfa.Spec) { s.Limits.MaxDictEntries = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				d := pdf.NewPDFDict()
				for i := range 4096 {
					d.Entries.Set("k"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune(i)), pdf.PDFInteger(i))
				}
				return withSpec(s, func(ctx *ValidationContext) { validateArchitecturalLimits(d, nil, ctx) })
			}, false},
		{"array size", pdf.Checks.Structure.ArrayTooLarge,
			func(s *pdfa.Spec) { s.Limits.MaxArrayElements = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					verifyDocument(specDict("A", make(pdf.PDFArray, 8192)), ctx)
				})
			}, false},
		{"DeviceN colorants", pdf.Checks.Structure.DeviceNColorants,
			func(s *pdfa.Spec) { s.Limits.MaxDeviceNColorants = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateColourSpaceArray(pdf.PDFArray{specName("DeviceN"), make(pdf.PDFArray, 9),
						specName("DeviceCMYK"), specDict()}, ctx)
				})
			}, false},
		{"CMap CID", pdf.Checks.Structure.CMapCIDOutOfRange,
			func(s *pdfa.Spec) { s.Limits.MaxCID = 0 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					checkCMapCIDLimits(nil, []byte("1 begincidchar <00> 70000 endcidchar 1 begincidrange <00> <01> 70000 endcidrange"), ctx)
				})
			}, false},
		{"ICCBased profile version", pdf.Checks.Colour.ICCBasedProfileInvalid,
			func(s *pdfa.Spec) { s.ICCMaxMajorVersion = 4 },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					validateICCBasedColourSpace(pdf.PDFArray{specName("ICCBased"), iccStream(4, "mntr")}, ctx)
				})
			}, false},
		{"output intent profile version", pdf.Checks.Colour.OutputIntentICCVersion,
			func(s *pdfa.Spec) { s.ICCMaxMajorVersion = 4 },
			func(s pdfa.Spec) []pdf.PDFError {
				if err := ValidateICCProfileStream(iccStream(4, "mntr"), s); err != nil {
					return []pdf.PDFError{*err}
				}
				return nil
			}, false},
		{"font file subtype", pdf.Checks.Font.FontFileSubtype,
			func(s *pdfa.Spec) { s.FontFileSubtypes = pdfa.NewNames("OpenType") },
			func(s pdfa.Spec) []pdf.PDFError {
				ff := specDict("Subtype", specName("OpenType"))
				ff.HasStream = true
				return withSpec(s, func(ctx *ValidationContext) {
					ValidateFontProgram(nil, specDict("FontFile3", ff), "F", ctx)
				})
			}, false},
		{"embedded file spec", pdf.Checks.Structure.EmbeddedFileSpec,
			func(s *pdfa.Spec) { s.EmbeddedFiles = true },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) { validateObject(specDict("EF", specDict()), ctx) })
			}, false},
		{"embedded files", pdf.Checks.Structure.EmbeddedFiles,
			func(s *pdfa.Spec) { s.EmbeddedFiles = true },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) { validateObject(specDict("EmbeddedFiles", specDict()), ctx) })
			}, false},
		{"indirect objects", pdf.Checks.Structure.IndirectObjectsExceeded,
			func(s *pdfa.Spec) { s.Limits.MaxIndirectObjects = 0 },
			func(s pdfa.Spec) []pdf.PDFError { return indirectObjectLimit(8388608, s) }, false},
		{"optional content", pdf.Checks.Structure.OptionalContent,
			func(s *pdfa.Spec) { s.OptionalContent = true },
			func(s pdfa.Spec) []pdf.PDFError {
				root := specDict("Type", specName("Catalog"), "OCProperties", specDict())
				return verifyOptionalContent(rawReaderWithTrailer(t, specDict("Root", root)), s)
			}, false},
		{"output intent subtype", pdf.Checks.Colour.OutputIntentWrongS,
			func(s *pdfa.Spec) { s.OutputIntentSubtype = "GTS_PDFX" },
			func(s pdfa.Spec) []pdf.PDFError { return verifyOutputIntent(outputIntentDoc(t, "GTS_PDFA1"), s) }, true},
		{"output intent coverage", pdf.Checks.Colour.DeviceColourContentStream,
			func(s *pdfa.Spec) { s.OutputIntentSubtype = "GTS_PDFX" },
			func(s pdfa.Spec) []pdf.PDFError {
				return withSpec(s, func(ctx *ValidationContext) {
					computeColourCoverage(outputIntentDoc(t, "GTS_PDFA1"), ctx)
					contentChecker(nil, pdf.PDFDict{}, ctx)("rg", nil)
				})
			}, true},
		{"PDF/A part", pdf.Checks.Metadata.PDFAPartNumber,
			func(s *pdfa.Spec) { s.XMPPart = "2" },
			func(s pdfa.Spec) []pdf.PDFError {
				return checkPDFAIdentifier(`<x xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="1" pdfaid:conformance="B"/>`, s)
			}, true},
		{"PDF/A conformance", pdf.Checks.Metadata.PDFAConformanceLevel,
			func(s *pdfa.Spec) { s.XMPConformances = pdfa.NewNames("U") },
			func(s pdfa.Spec) []pdf.PDFError {
				return checkPDFAIdentifier(`<x xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="1" pdfaid:conformance="B"/>`, s)
			}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := a1b()
			tc.change(&changed)
			under1b, underChanged := reports(tc.run(a1b()), tc.check), reports(tc.run(changed), tc.check)
			if tc.inverted {
				under1b, underChanged = underChanged, under1b
			}
			if !under1b {
				t.Errorf("PDF/A-1b does not report %s", tc.check.Name())
			}
			if underChanged {
				t.Errorf("the changed spec still reports %s: the rule is hard-wired", tc.check.Name())
			}
		})
	}
}
