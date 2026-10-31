package gopdfrab_test

import "testing"

// Inputs that the fuzz targets have failed on, pinned as ordinary tests:
// testdata/fuzz/ is gitignored, so the reproducers the fuzzer writes cannot be
// committed as they are.

// TestFuzzRegressionSelfListingKids verifies that convert's page walk stops on
// a Pages node that lists itself in /Kids. Unbounded recursion there is a fatal
// stack overflow, which recover does not catch.
func TestFuzzRegressionSelfListingKids(t *testing.T) {
	checkConvertConverges(t, []byte("%PDF-1\n1 0 obj<< /Type /Catalog /Pages 2 0 R >>\n2 0 obj\n<< /Type /Pages /Kids [3 0 R 2 0 R /MediaBox 0  20 200]/Resources << >> >>\nendobj\nxref\n0 4\n0000000000 65535 f \n000000015 00000 n \n00000064 00000 n \n0000000121 00000 n \ntraile\n< /Size 4 /Root 1 0 R >\nstartxref\n20\n%EF\n3 0 obj\n<< /Type /Page /Par\xffnt 2 0R /MediaBox [0 0 400 400] /Reources << >> >>\nendobj\nxref\n3 1\n0000000352 00000 n \ntrailer<</Size 4 /Root 1 0 R /Prev 209 >>\nstartxrefi"))
}

// TestFuzzRegressionAbbreviatedStreamFilter verifies that Convert's verdict is
// honest for a content stream whose /Filter is the inline-image abbreviation
// /Fl: when Convert reports its output valid, an independent verification of
// that output must agree.
func TestFuzzRegressionAbbreviatedStreamFilter(t *testing.T) {
	for name, data := range map[string]string{
		"length 50": "%PDF-\n1 0 obj<</Pages 2 0R>>\n2 0 obj<</Kids[3 0R]/Count 0>>\n3 0 obj<</Type Page/Parent 0 0R/<>/Contents 4 0R>>\n4 0 obj<</Filter Fl/Length 50>>stream\nx\x9c2000000000000008000000000000000000000000000000000000endstreamtrailer/Size 1/Root 1 0R>>",
		"length 07": "%PDF-\n1 0 obj<</Type//Pages 2 0R>>\n2 0 obj<</Type//Kids[3 0R]/Count 0>>\n3 0 obj<</Type Page/Parent<<>>/Contents 4 0R>>\n4 0 obj<</Filter Fl/Length 07>>stream\nx\x9c2000000000000000000000000000000000000000000000000000endstreamtrailer/Size 1/Root 1 0R>>",
	} {
		t.Run(name, func(t *testing.T) { checkConvertHonest(t, []byte(data)) })
	}
}
