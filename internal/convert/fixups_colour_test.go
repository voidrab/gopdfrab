package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// buildICCProfileHeader returns a minimal 128-byte ICC v2 profile header
// declaring the given colour space signature.
func buildICCProfileHeader(colorSpace string) []byte {
	return buildICCProfileHeaderWith(2, "mntr", colorSpace)
}

// buildICCProfileHeaderWith is buildICCProfileHeader with the profile's version
// and device class chosen as well.
func buildICCProfileHeaderWith(version byte, deviceClass, colorSpace string) []byte {
	data := make([]byte, 128)
	data[8] = version
	copy(data[12:16], deviceClass)
	copy(data[16:20], colorSpace)
	copy(data[36:40], "acsp")
	return data
}

// iccBasedTrailer wraps an [/ICCBased stream] colour space in a trailer, under
// a resource dictionary the way a real document holds one.
func iccBasedTrailer(colorSpace string, n int) (trailer pdf.PDFDict, cs pdf.PDFArray) {
	return iccBasedTrailerWith(buildICCProfileHeader(colorSpace), n)
}

// iccBasedTrailerWith is iccBasedTrailer over a profile the caller built.
func iccBasedTrailerWith(profile []byte, n int) (trailer pdf.PDFDict, cs pdf.PDFArray) {
	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.RawStream = profile
	if n >= 0 {
		stream.Entries.Set("N", pdf.PDFInteger(n))
	}
	cs = pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream}

	spaces := pdf.NewPDFDict()
	spaces.Entries.Set("CS0", cs)
	res := pdf.NewPDFDict()
	res.Entries.Set("ColorSpace", spaces)
	trailer = pdf.NewPDFDict()
	trailer.Entries.Set("Resources", res)
	return trailer, cs
}

// currentSpace returns the colour space now sitting in the trailer built by
// iccBasedTrailer, which the fixer may have replaced outright.
func currentSpace(t *testing.T, trailer pdf.PDFDict) pdf.PDFValue {
	t.Helper()
	res := trailer.Entries.Get("Resources").(pdf.PDFDict)
	return res.Entries.Get("ColorSpace").(pdf.PDFDict).Entries.Get("CS0")
}

// TestICCBasedProfileFixerAppliesToBothHalvesOfTheClause covers the fixer's
// reach: both 6.2.3.2 defects, and nothing else.
func TestICCBasedProfileFixerAppliesToBothHalvesOfTheClause(t *testing.T) {
	fixer := iccBasedProfileFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Colour.ICCBasedComponentsMismatch ||
			c == pdf.Checks.Colour.ICCBasedProfileInvalid
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s/%d) = %v, want %v", c.Clause(), c.Subclause(), got, want)
		}
	}
}

// TestICCBasedProfileFixerReplacesProfile covers the main repair: /N stays as
// it is -- content streams pass that many operands to sc and scn -- and the
// profile is swapped for a bundled one that really has that many components.
func TestICCBasedProfileFixerReplacesProfile(t *testing.T) {
	for _, tc := range []struct {
		name       string
		colorSpace string
		n          int
		wantCS     string
	}{
		{"three components", "CMYK", 3, "RGB "},
		{"four components", "RGB ", 4, "CMYK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trailer, _ := iccBasedTrailer(tc.colorSpace, tc.n)
			runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

			cs, ok := currentSpace(t, trailer).(pdf.PDFArray)
			if !ok {
				t.Fatalf("colour space = %v, want an ICCBased array", currentSpace(t, trailer))
			}
			stream := cs[1].(pdf.PDFDict)
			if stream.Entries.Get("N") != pdf.PDFInteger(tc.n) {
				t.Errorf("N = %v, want %d unchanged", stream.Entries.Get("N"), tc.n)
			}
			data, err := pdf.DecodeStream(stream)
			if err != nil {
				t.Fatalf("DecodeStream: %v", err)
			}
			if got := string(data[16:20]); got != tc.wantCS {
				t.Errorf("replacement profile colour space = %q, want %q", got, tc.wantCS)
			}
			if msg := verify.ICCComponentsMismatch(stream, data); msg != "" {
				t.Errorf("still mismatched after the fix: %s", msg)
			}
		})
	}
}

// TestICCBasedProfileFixerReplacesGrayProfile covers the one-component case,
// which has a bundled profile like the other two: the space stays ICCBased
// rather than being dropped for a device colour space.
func TestICCBasedProfileFixerReplacesGrayProfile(t *testing.T) {
	trailer, _ := iccBasedTrailer("RGB ", 1)
	runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

	cs, ok := currentSpace(t, trailer).(pdf.PDFArray)
	if !ok {
		t.Fatalf("colour space = %v, want an ICCBased array", currentSpace(t, trailer))
	}
	stream := cs[1].(pdf.PDFDict)
	if stream.Entries.Get("N") != pdf.PDFInteger(1) {
		t.Errorf("N = %v, want 1 unchanged", stream.Entries.Get("N"))
	}
	data, err := pdf.DecodeStream(stream)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if got := string(data[16:20]); got != "GRAY" {
		t.Errorf("replacement profile colour space = %q, want GRAY", got)
	}
	if msg := verify.ICCComponentsMismatch(stream, data); msg != "" {
		t.Errorf("still mismatched after the fix: %s", msg)
	}
	if msg := verify.ICCInputProfileDefect(data); msg != "" {
		t.Errorf("replacement profile is not one PDF/A allows: %s", msg)
	}
}

// TestICCBasedProfileFixerReplacesUnusableProfileKind covers the other half of
// the clause: a profile PDF/A does not allow is swapped out even though the
// component count it declares is right.
func TestICCBasedProfileFixerReplacesUnusableProfileKind(t *testing.T) {
	for _, tc := range []struct {
		name        string
		version     byte
		deviceClass string
		colorSpace  string
		n           int
		wantCS      string
	}{
		{"version 4 rgb", 4, "mntr", "RGB ", 3, "RGB "},
		{"version 4 scanner rgb", 4, "scnr", "RGB ", 3, "RGB "},
		{"version 4 cmyk", 4, "prtr", "CMYK", 4, "CMYK"},
		{"version 4 gray", 4, "mntr", "GRAY", 1, "GRAY"},
		{"device link class", 2, "link", "RGB ", 3, "RGB "},
		{"connection space as the colour space", 2, "mntr", "XYZ ", 3, "RGB "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := buildICCProfileHeaderWith(tc.version, tc.deviceClass, tc.colorSpace)
			trailer, _ := iccBasedTrailerWith(profile, tc.n)
			runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

			cs, ok := currentSpace(t, trailer).(pdf.PDFArray)
			if !ok {
				t.Fatalf("colour space = %v, want an ICCBased array", currentSpace(t, trailer))
			}
			stream := cs[1].(pdf.PDFDict)
			if stream.Entries.Get("N") != pdf.PDFInteger(tc.n) {
				t.Errorf("N = %v, want %d unchanged", stream.Entries.Get("N"), tc.n)
			}
			data, err := pdf.DecodeStream(stream)
			if err != nil {
				t.Fatalf("DecodeStream: %v", err)
			}
			if got := string(data[16:20]); got != tc.wantCS {
				t.Errorf("replacement profile colour space = %q, want %q", got, tc.wantCS)
			}
			if msg := verify.ICCInputProfileDefect(data); msg != "" {
				t.Errorf("replacement profile is not one PDF/A allows: %s", msg)
			}
			if msg := verify.ICCComponentsMismatch(stream, data); msg != "" {
				t.Errorf("replacement profile does not match /N: %s", msg)
			}
		})
	}
}

// TestICCBasedProfileFixerDerivesMissingN covers the other direction: when /N
// is absent or out of range, nothing in the file depends on it yet, so it is
// set from the profile rather than the profile being replaced.
func TestICCBasedProfileFixerDerivesMissingN(t *testing.T) {
	for _, tc := range []struct {
		name       string
		colorSpace string
		n          int
		wantN      int
	}{
		{"no N, gray profile", "GRAY", -1, 1},
		{"no N, rgb profile", "RGB ", -1, 3},
		{"N out of range, cmyk profile", "CMYK", 7, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trailer, _ := iccBasedTrailer(tc.colorSpace, tc.n)
			runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

			cs := currentSpace(t, trailer).(pdf.PDFArray)
			stream := cs[1].(pdf.PDFDict)
			if stream.Entries.Get("N") != pdf.PDFInteger(tc.wantN) {
				t.Errorf("N = %v, want %d", stream.Entries.Get("N"), tc.wantN)
			}
			data, err := pdf.DecodeStream(stream)
			if err != nil {
				t.Fatalf("DecodeStream: %v", err)
			}
			if got := string(data[16:20]); got != tc.colorSpace {
				t.Errorf("profile colour space = %q, want %q unchanged", got, tc.colorSpace)
			}
		})
	}
}

// TestICCBasedProfileFixerUnusableProfile covers a profile that says nothing
// useful: with no /N to preserve and no readable colour space, the space falls
// back to sRGB with three components.
func TestICCBasedProfileFixerUnusableProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile []byte
	}{
		{"unknown colour space", buildICCProfileHeader("5CLR")},
		{"too short to hold a header", make([]byte, 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := pdf.NewPDFDict()
			stream.HasStream = true
			stream.RawStream = tc.profile
			cs := pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream}
			trailer := pdf.NewPDFDict()
			trailer.Entries.Set("CS", cs)

			runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

			got := trailer.Entries.Get("CS").(pdf.PDFArray)[1].(pdf.PDFDict)
			if got.Entries.Get("N") != pdf.PDFInteger(3) {
				t.Errorf("N = %v, want 3", got.Entries.Get("N"))
			}
			data, err := pdf.DecodeStream(got)
			if err != nil {
				t.Fatalf("DecodeStream: %v", err)
			}
			if s := string(data[16:20]); s != "RGB " {
				t.Errorf("profile colour space = %q, want RGB ", s)
			}
		})
	}
}

// TestICCBasedProfileFixerSkipsConformingSpaces covers the no-op cases: a
// matching space, and anything that is not an ICCBased space at all.
func TestICCBasedProfileFixerSkipsConformingSpaces(t *testing.T) {
	trailer, _ := iccBasedTrailer("RGB ", 3)
	changed, err := iccBasedProfileFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Error("changed = true for a colour space that already matches its profile")
	}

	other := pdf.NewPDFDict()
	other.Entries.Set("Short", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}})
	other.Entries.Set("NotAName", pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(2)})
	other.Entries.Set("NotICC", pdf.PDFArray{pdf.PDFName{Value: "CalRGB"}, pdf.NewPDFDict()})
	other.Entries.Set("NoStream", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, pdf.NewPDFDict()})
	if changed, _ := (iccBasedProfileFixer{}).Fix(&other, nil); changed {
		t.Error("changed = true for arrays that are not ICCBased colour spaces")
	}
}

// TestICCBasedProfileFixerFindsNestedSpaces covers the traversal: an ICCBased
// space reached only through another array, as /Indexed bases are.
func TestICCBasedProfileFixerFindsNestedSpaces(t *testing.T) {
	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.RawStream = buildICCProfileHeader("CMYK")
	stream.Entries.Set("N", pdf.PDFInteger(3))

	indexed := pdf.PDFArray{
		pdf.PDFName{Value: "Indexed"},
		pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream},
		pdf.PDFInteger(255),
		pdf.PDFString{Value: "lookup"},
	}
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("CS", indexed)

	runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

	base := trailer.Entries.Get("CS").(pdf.PDFArray)[1].(pdf.PDFArray)
	data, err := pdf.DecodeStream(base[1].(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if got := string(data[16:20]); got != "RGB " {
		t.Errorf("nested profile colour space = %q, want RGB ", got)
	}
}

// TestConvertClearsICCBasedMismatch is the end-to-end proof: a document whose
// page draws in an ICCBased space that lies about its component count converts
// to conformance without the page being rasterized.
func TestConvertClearsICCBasedMismatch(t *testing.T) {
	profile := buildICCProfileHeader("CMYK")

	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "+
		"/Resources << /ColorSpace << /CS0 [/ICCBased 4 0 R] >> >> /Contents 5 0 R >>")
	b.StreamObj(4, "<< /N 3", profile)
	content := []byte("/CS0 cs 1 0 0 sc 10 10 50 50 re f")
	b.StreamObj(5, "<<", content)
	data := b.FinishClassic("<< /Size 6 /Root 1 0 R >>")

	res, err := verify.VerifyBytes(data, pdf.PDFA1B, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	found := false
	for _, iss := range res.Issues {
		if iss.Check() == pdf.Checks.Colour.ICCBasedComponentsMismatch {
			found = true
		}
	}
	if !found {
		t.Fatal("sanity: ICCBasedComponentsMismatch not reported for /N 3 over a CMYK profile")
	}

	cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer cr.Close()
	for _, iss := range cr.Result.Issues {
		if iss.Check() == pdf.Checks.Colour.ICCBasedComponentsMismatch {
			t.Errorf("ICCBasedComponentsMismatch survived conversion: %v", iss)
		}
	}
	if len(cr.RasterizedPages) != 0 {
		t.Errorf("page %v was rasterized; the profile should have been replaced instead", cr.RasterizedPages)
	}
}

// TestConvertClearsICCBasedProfileInvalid is the end-to-end proof for the other
// half of the clause: a page drawing in an ICCBased space whose profile is a
// version PDF/A does not allow converts to conformance, profile replaced rather
// than page rasterized.
func TestConvertClearsICCBasedProfileInvalid(t *testing.T) {
	profile := buildICCProfileHeaderWith(4, "mntr", "RGB ")

	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "+
		"/Resources << /ColorSpace << /CS0 [/ICCBased 4 0 R] >> >> /Contents 5 0 R >>")
	b.StreamObj(4, "<< /N 3", profile)
	content := []byte("/CS0 cs 1 0 0 sc 10 10 50 50 re f")
	b.StreamObj(5, "<<", content)
	data := b.FinishClassic("<< /Size 6 /Root 1 0 R >>")

	res, err := verify.VerifyBytes(data, pdf.PDFA1B, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	found := false
	for _, iss := range res.Issues {
		if iss.Check() == pdf.Checks.Colour.ICCBasedProfileInvalid {
			found = true
		}
		if iss.Check() == pdf.Checks.Colour.ICCBasedComponentsMismatch {
			t.Error("sanity: the component count agrees, only the profile kind is wrong")
		}
	}
	if !found {
		t.Fatal("sanity: ICCBasedProfileInvalid not reported for a version 4 profile")
	}

	cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer cr.Close()
	for _, iss := range cr.Result.Issues {
		if iss.Check() == pdf.Checks.Colour.ICCBasedProfileInvalid {
			t.Errorf("ICCBasedProfileInvalid survived conversion: %v", iss)
		}
	}
	if len(cr.RasterizedPages) != 0 {
		t.Errorf("page %v was rasterized; the profile should have been replaced instead", cr.RasterizedPages)
	}
}

// TestICCBasedProfileFixerRepairsNestedGraySpace covers a one-component space
// held by another array, as /Indexed bases are: it is repaired where it sits.
func TestICCBasedProfileFixerRepairsNestedGraySpace(t *testing.T) {
	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.RawStream = buildICCProfileHeader("RGB ")
	stream.Entries.Set("N", pdf.PDFInteger(1))

	indexed := pdf.PDFArray{
		pdf.PDFName{Value: "Indexed"},
		pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream},
		pdf.PDFInteger(255),
		pdf.PDFString{Value: "lookup"},
	}
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("CS", indexed)

	runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

	base, ok := trailer.Entries.Get("CS").(pdf.PDFArray)[1].(pdf.PDFArray)
	if !ok {
		t.Fatalf("base colour space = %v, want an ICCBased array", trailer.Entries.Get("CS").(pdf.PDFArray)[1])
	}
	data, err := pdf.DecodeStream(base[1].(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if got := string(data[16:20]); got != "GRAY" {
		t.Errorf("nested profile colour space = %q, want GRAY", got)
	}
}

// TestICCBasedProfileFixerCollectsEverySlot covers the traversal: an array
// reached twice is walked once, and a colour space sitting in two slots is the
// same array in both, so repairing it once shows up in both.
func TestICCBasedProfileFixerCollectsEverySlot(t *testing.T) {
	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.RawStream = buildICCProfileHeader("RGB ")
	stream.Entries.Set("N", pdf.PDFInteger(1))
	cs := pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream}

	shared := pdf.PDFArray{cs, cs}
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("A", shared)
	trailer.Entries.Set("B", shared)

	if got := len(collectICCBasedSpaces(trailer)); got != 2 {
		t.Errorf("collectICCBasedSpaces found %d spaces, want 2 (one per slot)", got)
	}

	if _, err := (iccBasedProfileFixer{}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	for i, got := range trailer.Entries.Get("A").(pdf.PDFArray) {
		arr, ok := got.(pdf.PDFArray)
		if !ok {
			t.Fatalf("slot %d = %v, want an ICCBased array", i, got)
		}
		data, err := pdf.DecodeStream(arr[1].(pdf.PDFDict))
		if err != nil {
			t.Fatalf("slot %d: DecodeStream: %v", i, err)
		}
		if s := string(data[16:20]); s != "GRAY" {
			t.Errorf("slot %d profile colour space = %q, want GRAY", i, s)
		}
	}
	if changed, _ := (iccBasedProfileFixer{}).Fix(&trailer, nil); changed {
		t.Error("changed = true on second pass, want false (fixer must be idempotent)")
	}
}
