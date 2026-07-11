package pdf

// This file exposes a few internal hardening seams to the external pdf_test
// package so the crasher regression tests can exercise each guard directly and
// deterministically, without having to hand-build a full malformed document or
// allocate at the production cap. Test-only.

// SetMaxInflateOutput temporarily lowers the InflateZlib output cap and returns
// a function that restores the previous value.
func SetMaxInflateOutput(n int64) (restore func()) {
	old := maxInflateOutput
	maxInflateOutput = n
	return func() { maxInflateOutput = old }
}

// SetMaxResolveDepth temporarily lowers the resolveInPlace depth cap and returns
// a function that restores the previous value.
func SetMaxResolveDepth(n int) (restore func()) {
	old := maxResolveDepth
	maxResolveDepth = n
	return func() { maxResolveDepth = old }
}

// XRefFieldWidths exposes xrefFieldWidths for the /W bounds test.
func XRefFieldWidths(dict PDFDict) ([3]int, error) { return xrefFieldWidths(dict) }

// DecodeObjStmForTest exposes decodeObjStm's error path for the /N bounds test.
func DecodeObjStmForTest(d *Reader, streamObjNum int) error {
	_, err := d.decodeObjStm(streamObjNum)
	return err
}
