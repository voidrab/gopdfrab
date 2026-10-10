package gopdfrab_test

import "testing"

// Inputs the nightly fuzz job failed on, pinned as ordinary tests:
// testdata/fuzz/ is gitignored, so the reproducers the fuzzer writes cannot be
// committed as they are.

// TestFuzzRegressionSelfListingKids: a Pages node listing itself in /Kids sent
// convert's page walk into unbounded recursion -- a fatal stack overflow that
// no recover catches, reported by the fuzzer as a worker that "hung or
// terminated unexpectedly". (FuzzConvertConverges, nightly 2026-10-09.)
func TestFuzzRegressionSelfListingKids(t *testing.T) {
	checkConvertConverges(t, []byte("%PDF-1\n1 0 obj<< /Type /Catalog /Pages 2 0 R >>\n2 0 obj\n<< /Type /Pages /Kids [3 0 R 2 0 R /MediaBox 0  20 200]/Resources << >> >>\nendobj\nxref\n0 4\n0000000000 65535 f \n000000015 00000 n \n00000064 00000 n \n0000000121 00000 n \ntraile\n< /Size 4 /Root 1 0 R >\nstartxref\n20\n%EF\n3 0 obj\n<< /Type /Page /Par\xffnt 2 0R /MediaBox [0 0 400 400] /Reources << >> >>\nendobj\nxref\n3 1\n0000000352 00000 n \ntrailer<</Size 4 /Root 1 0 R /Prev 209 >>\nstartxrefi"))
}

// TestFuzzRegressionAbbreviatedStreamFilter: a content stream declaring the
// inline-image abbreviation /Fl as its /Filter. The object-model fixer deleted
// the illegal /Filter, leaving compressed bytes to be read as operators, and
// the decode cache -- keyed on the raw bytes alone -- still answered with the
// old decoded content, so Convert reported Valid on an invalid file.
// (FuzzConvertHonest, nightly 2026-10-06 and 2026-10-09.)
func TestFuzzRegressionAbbreviatedStreamFilter(t *testing.T) {
	for name, data := range map[string]string{
		"length 50": "%PDF-\n1 0 obj<</Pages 2 0R>>\n2 0 obj<</Kids[3 0R]/Count 0>>\n3 0 obj<</Type Page/Parent 0 0R/<>/Contents 4 0R>>\n4 0 obj<</Filter Fl/Length 50>>stream\nx\x9c2000000000000008000000000000000000000000000000000000endstreamtrailer/Size 1/Root 1 0R>>",
		"length 07": "%PDF-\n1 0 obj<</Type//Pages 2 0R>>\n2 0 obj<</Type//Kids[3 0R]/Count 0>>\n3 0 obj<</Type Page/Parent<<>>/Contents 4 0R>>\n4 0 obj<</Filter Fl/Length 07>>stream\nx\x9c2000000000000000000000000000000000000000000000000000endstreamtrailer/Size 1/Root 1 0R>>",
	} {
		t.Run(name, func(t *testing.T) { checkConvertHonest(t, []byte(data)) })
	}
}
