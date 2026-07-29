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
	data := make([]byte, 128)
	data[8] = 2 // major version 2
	copy(data[12:16], "mntr")
	copy(data[16:20], colorSpace)
	copy(data[36:40], "acsp")
	return data
}

// iccBasedTrailer wraps an [/ICCBased stream] colour space in a trailer, under
// a resource dictionary the way a real document holds one.
func iccBasedTrailer(colorSpace string, n int) (trailer pdf.PDFDict, cs pdf.PDFArray) {
	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.RawStream = buildICCProfileHeader(colorSpace)
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

func TestICCBasedProfileFixerAppliesOnlyToComponentsMismatch(t *testing.T) {
	fixer := iccBasedProfileFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Colour.ICCBasedComponentsMismatch
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

// TestICCBasedProfileFixerFallsBackToDeviceGray covers the one component count
// with no bundled replacement profile: the space becomes DeviceGray, which any
// PDF/A output intent covers and which still takes one operand per colour.
func TestICCBasedProfileFixerFallsBackToDeviceGray(t *testing.T) {
	trailer, _ := iccBasedTrailer("RGB ", 1)
	runFixerAndCheckIdempotent(t, iccBasedProfileFixer{}, &trailer)

	if got := currentSpace(t, trailer); (got != pdf.PDFName{Value: "DeviceGray"}) {
		t.Errorf("colour space = %v, want /DeviceGray", got)
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
