package verify

import (
	"fmt"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestDeviceColourModel(t *testing.T) {
	cases := []struct {
		cs   pdf.PDFValue
		want string
	}{
		{pdf.PDFName{Value: "DeviceRGB"}, "rgb"},
		{pdf.PDFName{Value: "RGB"}, "rgb"},
		{pdf.PDFName{Value: "DeviceGray"}, "gray"},
		{pdf.PDFName{Value: "G"}, "gray"},
		{pdf.PDFName{Value: "DeviceCMYK"}, "cmyk"},
		{pdf.PDFName{Value: "CMYK"}, "cmyk"},
		{pdf.PDFName{Value: "ICCBased"}, ""},
		{pdf.PDFArray{}, ""},
		{pdf.PDFArray{pdf.PDFInteger(1)}, ""}, // head not a name
		{pdf.PDFArray{pdf.PDFName{Value: "DeviceRGB"}}, "rgb"},
		{pdf.PDFArray{pdf.PDFName{Value: "DeviceGray"}}, "gray"},
		{pdf.PDFArray{pdf.PDFName{Value: "DeviceCMYK"}}, "cmyk"},
		{pdf.PDFArray{pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: "DeviceCMYK"}, pdf.PDFInteger(0)}, "cmyk"},
		{pdf.PDFArray{pdf.PDFName{Value: "I"}}, ""}, // Indexed with no base entry
		{pdf.PDFInteger(5), ""}, // unsupported type
	}
	for _, c := range cases {
		if got := DeviceColourModel(c.cs); got != c.want {
			t.Errorf("DeviceColourModel(%v) = %q, want %q", c.cs, got, c.want)
		}
	}
}

func TestDefaultColorSpaceDefined(t *testing.T) {
	res := pdf.NewPDFDict()
	if DefaultColorSpaceDefined("rgb", res) {
		t.Error("should be false with no ColorSpace dict")
	}
	cs := pdf.NewPDFDict()
	cs.Entries.Set("DefaultRGB", pdf.NewPDFDict())
	res.Entries.Set("ColorSpace", cs)
	if !DefaultColorSpaceDefined("rgb", res) {
		t.Error("should be true when DefaultRGB is present")
	}
	if DefaultColorSpaceDefined("cmyk", res) {
		t.Error("should be false when DefaultCMYK is absent")
	}
	if DefaultColorSpaceDefined("unknown-model", res) {
		t.Error("should be false for an unrecognized model")
	}
}

func TestCheckDeviceColour(t *testing.T) {
	// A model not covered by any OutputIntent and no Default* override -> reported.
	ctx := &ValidationContext{}
	checkDeviceColour(pdf.PDFDict{}, pdf.PDFName{Value: "DeviceRGB"}, ctx, "image")
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourSpaceUsage) {
		t.Error("expected DeviceColourSpaceUsage for an uncovered device colour space")
	}

	// Covered by an OutputIntent -> no report.
	ctx2 := &ValidationContext{rgbCovered: true}
	checkDeviceColour(pdf.PDFDict{}, pdf.PDFName{Value: "DeviceRGB"}, ctx2, "image")
	if hasCheck(ctx2, pdf.Checks.Colour.DeviceColourSpaceUsage) {
		t.Error("unexpected report when the colour model is covered")
	}

	// Overridden by a page-level Default* colour space -> no report.
	res := pdf.NewPDFDict()
	csDict := pdf.NewPDFDict()
	csDict.Entries.Set("DefaultRGB", pdf.NewPDFDict())
	res.Entries.Set("ColorSpace", csDict)
	ctx3 := &ValidationContext{resourceScope: res}
	checkDeviceColour(pdf.PDFDict{}, pdf.PDFName{Value: "DeviceRGB"}, ctx3, "image")
	if hasCheck(ctx3, pdf.Checks.Colour.DeviceColourSpaceUsage) {
		t.Error("unexpected report when a Default* colour space overrides it")
	}

	// Non-device colour space -> no-op.
	ctx4 := &ValidationContext{}
	checkDeviceColour(pdf.PDFDict{}, pdf.PDFName{Value: "ICCBased"}, ctx4, "image")
	if len(ctx4.errs) != 0 {
		t.Error("unexpected report for a non-device colour space")
	}
}

func TestValidateColourSpaceUsage(t *testing.T) {
	img := pdf.NewPDFDict()
	img.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	img.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceCMYK"})
	ctx := &ValidationContext{}
	validateColourSpaceUsage(img, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourSpaceUsage) {
		t.Error("expected DeviceColourSpaceUsage for an image with an uncovered device colour space")
	}

	shading := pdf.NewPDFDict()
	shading.Entries.Set("ShadingType", pdf.PDFInteger(2))
	shading.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	ctx2 := &ValidationContext{}
	validateColourSpaceUsage(shading, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Colour.DeviceColourSpaceUsage) {
		t.Error("expected DeviceColourSpaceUsage for a shading with an uncovered device colour space")
	}

	// Neither an image nor a shading dict -> no-op.
	ctx3 := &ValidationContext{}
	validateColourSpaceUsage(pdf.NewPDFDict(), ctx3)
	if len(ctx3.errs) != 0 {
		t.Error("unexpected report for an unrelated dictionary")
	}
}

func TestValidateColourSpaceArray(t *testing.T) {
	// Too few elements -> no-op.
	ctx := &ValidationContext{}
	validateColourSpaceArray(pdf.PDFArray{pdf.PDFName{Value: "Separation"}}, ctx)
	if len(ctx.errs) != 0 {
		t.Error("unexpected report for a too-short array")
	}

	// DeviceN with more than 8 colorants.
	names := make(pdf.PDFArray, 9)
	for i := range names {
		names[i] = pdf.PDFName{Value: "Spot"}
	}
	deviceN := pdf.PDFArray{pdf.PDFName{Value: "DeviceN"}, names, pdf.PDFName{Value: "DeviceGray"}, pdf.PDFDict{}}
	ctx2 := &ValidationContext{}
	validateColourSpaceArray(deviceN, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Structure.DeviceNColorants) {
		t.Error("expected DeviceNColorants for more than 8 colorants")
	}

	// Separation with an uncovered alternate device space.
	sep := pdf.PDFArray{pdf.PDFName{Value: "Separation"}, pdf.PDFName{Value: "Spot"}, pdf.PDFName{Value: "DeviceCMYK"}, pdf.PDFDict{}}
	ctx3 := &ValidationContext{}
	validateColourSpaceArray(sep, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Colour.SeparationAlternateColour) {
		t.Error("expected SeparationAlternateColour for an uncovered alternate space")
	}

	// Covered alternate space -> no report.
	ctx4 := &ValidationContext{cmykCovered: true}
	validateColourSpaceArray(sep, ctx4)
	if hasCheck(ctx4, pdf.Checks.Colour.SeparationAlternateColour) {
		t.Error("unexpected report when the alternate space is covered")
	}

	// Not a Separation/DeviceN array -> no-op.
	ctx5 := &ValidationContext{}
	validateColourSpaceArray(pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, pdf.PDFDict{}, pdf.PDFInteger(3)}, ctx5)
	if len(ctx5.errs) != 0 {
		t.Error("unexpected report for a non-Separation/DeviceN array")
	}
}

func TestComputeColourCoverage(t *testing.T) {
	if _, err := os.Stat(sampleVeraPassFile); err != nil {
		t.Skip("corpus not available")
	}
	doc, err := pdf.Open(sampleVeraPassFile)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()

	ctx := &ValidationContext{}
	computeColourCoverage(doc, ctx)
	if !ctx.hasOutputIntent {
		t.Error("expected hasOutputIntent = true for a file with a GTS_PDFA1 OutputIntent")
	}
	if !ctx.rgbCovered && !ctx.cmykCovered && !ctx.grayCovered {
		t.Error("expected at least one device colour model to be covered")
	}
}

func TestComputeColourCoverageEdgeCases(t *testing.T) {
	// Wrong S value: not a GTS_PDFA1 output intent.
	f := t.TempDir() + "/oi-wrong-s.pdf"
	writeMinimalPDFWithOutputIntent(t, f, "<< /S /GTS_PDFX /DestOutputProfile 5 0 R >>")
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()
	ctx := &ValidationContext{}
	computeColourCoverage(doc, ctx)
	if ctx.hasOutputIntent {
		t.Error("a non-GTS_PDFA1 OutputIntent should not set hasOutputIntent")
	}
}

// writeMinimalPDFWithOutputIntent writes a minimal PDF whose catalog has a
// single-entry /OutputIntents array pointing at intentBody (object 4).
func writeMinimalPDFWithOutputIntent(t *testing.T, filename, intentBody string) {
	t.Helper()
	header := "%PDF-1.7\n"
	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /OutputIntents [4 0 R] >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n"
	obj3 := "3 0 obj\n<< /Title (x) >>\nendobj\n"
	obj4 := "4 0 obj\n" + intentBody + "\nendobj\n"

	off1 := len(header)
	off2 := off1 + len(obj1)
	off3 := off2 + len(obj2)
	off4 := off3 + len(obj3)
	xrefOffset := off4 + len(obj4)

	xref := fmt.Sprintf("xref\n0 5\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n",
		off1, off2, off3, off4)
	trailer := "trailer\n<< /Size 5 /Root 1 0 R /Info 3 0 R >>\n"
	startxref := fmt.Sprintf("startxref\n%d\n%%EOF", xrefOffset)

	content := header + obj1 + obj2 + obj3 + obj4 + xref + trailer + startxref
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestComputeColourCoverageNoOutputIntent(t *testing.T) {
	f := t.TempDir() + "/no-oi.pdf"
	if err := createValidPDF(f); err != nil {
		t.Fatalf("createValidPDF: %v", err)
	}
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()

	ctx := &ValidationContext{}
	computeColourCoverage(doc, ctx)
	if ctx.hasOutputIntent {
		t.Error("expected hasOutputIntent = false with no OutputIntents entry")
	}
}

// iccBasedSpace builds an [/ICCBased stream] colour space whose profile
// declares the given colour space signature and whose dict declares n
// components; n < 0 omits /N entirely.
func iccBasedSpace(colorSpace string, n int) pdf.PDFArray {
	return iccBasedSpaceWithHeader(2, "mntr", colorSpace, n)
}

// iccBasedSpaceWithHeader is iccBasedSpace with the profile's version and
// device class chosen as well.
func iccBasedSpaceWithHeader(version byte, deviceClass, colorSpace string, n int) pdf.PDFArray {
	profile := buildValidICCProfile()
	profile[8] = version
	copy(profile[12:16], deviceClass)
	copy(profile[16:20], colorSpace)

	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.RawStream = profile
	if n >= 0 {
		stream.Entries.Set("N", pdf.PDFInteger(n))
	}
	return pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream}
}

// TestValidateICCBasedColourSpace covers 6.2.3.2: an ICCBased colour space
// must declare the number of components its embedded profile actually has.
func TestValidateICCBasedColourSpace(t *testing.T) {
	for _, tc := range []struct {
		name        string
		colorSpace  string
		n           int
		want        bool
		wantProfile bool
	}{
		{"gray with N 1", "GRAY", 1, false, false},
		{"rgb with N 3", "RGB ", 3, false, false},
		{"lab with N 3", "Lab ", 3, false, false},
		{"cmyk with N 4", "CMYK", 4, false, false},
		{"rgb with N 4", "RGB ", 4, true, false},
		{"cmyk with N 1", "CMYK", 1, true, false},
		{"N out of range", "RGB ", 5, true, false},
		{"no N at all", "RGB ", -1, true, false},
		// A colour space PDF/A does not permit is two defects at once: the
		// profile itself is unusable, and no /N can match it.
		{"colour space PDF/A does not permit", "5CLR", 5, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{}
			validateColourSpaceArray(iccBasedSpace(tc.colorSpace, tc.n), ctx)
			got := hasCheck(ctx, pdf.Checks.Colour.ICCBasedComponentsMismatch)
			if got != tc.want {
				t.Errorf("ICCBasedComponentsMismatch reported = %v, want %v", got, tc.want)
			}
			if got := hasCheck(ctx, pdf.Checks.Colour.ICCBasedProfileInvalid); got != tc.wantProfile {
				t.Errorf("ICCBasedProfileInvalid reported = %v, want %v", got, tc.wantProfile)
			}
		})
	}
}

// TestValidateICCBasedProfile covers the other half of 6.2.3.2: the profile an
// ICCBased colour space embeds must be one PDF/A-1 allows.
func TestValidateICCBasedProfile(t *testing.T) {
	for _, tc := range []struct {
		name        string
		version     byte
		deviceClass string
		colorSpace  string
		n           int
		want        bool
	}{
		{"version 2 display profile", 2, "mntr", "RGB ", 3, false},
		{"version 2 printer profile", 2, "prtr", "CMYK", 4, false},
		{"version 2 scanner profile", 2, "scnr", "RGB ", 3, false},
		{"version 2 colour space profile", 2, "spac", "Lab ", 3, false},
		{"version 4", 4, "mntr", "RGB ", 3, true},
		{"version 5", 5, "mntr", "RGB ", 3, true},
		{"device link class", 2, "link", "RGB ", 3, true},
		{"abstract class", 2, "abst", "RGB ", 3, true},
		{"named colour class", 2, "nmcl", "RGB ", 3, true},
		{"unknown class", 2, "xxxx", "RGB ", 3, true},
		{"connection space as the colour space", 2, "mntr", "XYZ ", 3, true},
		{"six-colorant space", 2, "mntr", "6CLR", 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{}
			arr := iccBasedSpaceWithHeader(tc.version, tc.deviceClass, tc.colorSpace, tc.n)
			validateColourSpaceArray(arr, ctx)
			if got := hasCheck(ctx, pdf.Checks.Colour.ICCBasedProfileInvalid); got != tc.want {
				t.Errorf("ICCBasedProfileInvalid reported = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateICCBasedColourSpaceSkipsUnreadable covers the cases the check
// stays silent on: a profile too damaged or too short to read a header from is
// a stream defect (6.1.7), and a non-integer /N is an object-model finding.
func TestValidateICCBasedColourSpaceSkipsUnreadable(t *testing.T) {
	short := pdf.NewPDFDict()
	short.HasStream = true
	short.RawStream = make([]byte, 10)
	short.Entries.Set("N", pdf.PDFInteger(3))

	undecodable := pdf.NewPDFDict()
	undecodable.HasStream = true
	undecodable.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	undecodable.RawStream = []byte("not a zlib stream")

	notAStream := pdf.NewPDFDict()

	for _, tc := range []struct {
		name string
		arr  pdf.PDFArray
	}{
		{"profile too short", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, short}},
		{"profile will not decode", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, undecodable}},
		{"not a stream", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, notAStream}},
		{"no profile at all", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{}
			validateColourSpaceArray(tc.arr, ctx)
			if hasCheck(ctx, pdf.Checks.Colour.ICCBasedComponentsMismatch) {
				t.Error("unexpected ICCBasedComponentsMismatch")
			}
			if hasCheck(ctx, pdf.Checks.Colour.ICCBasedProfileInvalid) {
				t.Error("unexpected ICCBasedProfileInvalid")
			}
		})
	}

	nonInteger := pdf.NewPDFDict()
	nonInteger.HasStream = true
	nonInteger.RawStream = buildValidICCProfile()
	nonInteger.Entries.Set("N", pdf.PDFReal(3))
	ctx := &ValidationContext{}
	validateColourSpaceArray(pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, nonInteger}, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.ICCBasedComponentsMismatch) {
		t.Error("expected ICCBasedComponentsMismatch for a non-integer /N")
	}
}

// TestICCColourSpaceComponents covers the signature-to-component-count table
// the output-intent and ICCBased paths share.
func TestICCColourSpaceComponents(t *testing.T) {
	for sig, want := range map[string]int{"GRAY": 1, "RGB ": 3, "Lab ": 3, "CMYK": 4} {
		if got, ok := ICCColourSpaceComponents(sig); !ok || got != want {
			t.Errorf("ICCColourSpaceComponents(%q) = %d, %v, want %d, true", sig, got, ok, want)
		}
	}
	if _, ok := ICCColourSpaceComponents("5CLR"); ok {
		t.Error("ICCColourSpaceComponents(5CLR) = ok, want false")
	}
}
