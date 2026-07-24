package pdf

// Test-only seams for the hardening crasher tests.

func SetMaxInflateOutput(n int64) (restore func()) {
	old := decodedStreamCap.Load()
	decodedStreamCap.Store(n)
	return func() { decodedStreamCap.Store(old) }
}

func SetMaxResolveDepth(n int) (restore func()) {
	old := maxResolveDepth
	maxResolveDepth = n
	return func() { maxResolveDepth = old }
}

func XRefFieldWidths(dict PDFDict) ([3]int, error) { return xrefFieldWidths(dict) }

func DecodeObjStmForTest(d *Reader, streamObjNum int) error {
	_, err := d.decodeObjStm(streamObjNum)
	return err
}
