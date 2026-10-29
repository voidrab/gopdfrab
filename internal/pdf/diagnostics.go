package pdf

// DiagKind names something the reader observed about a file's structure while
// parsing it. A kind states a fact about the bytes, not a rule: whether it
// violates a conformance level, and which check it then raises, is the level's
// call (see pdfa.Spec.Issue).
type DiagKind uint8

const (
	// DiagObjectFraming: an object's "N G obj … endobj" framing is malformed.
	DiagObjectFraming DiagKind = iota + 1
	// DiagStreamKeywordEOL: "stream" is not followed by a single CRLF or LF.
	DiagStreamKeywordEOL
	// DiagStreamLengthIncludesEOL: /Length counts the EOL before "endstream".
	DiagStreamLengthIncludesEOL
	// DiagStreamLengthMismatch: /Length is not the stream's data length.
	DiagStreamLengthMismatch
	// DiagEndstreamEOL: "endstream" is not preceded by an EOL marker.
	DiagEndstreamEOL
	// DiagUsedXRefStream: the cross-reference section is a stream.
	DiagUsedXRefStream
	// DiagXRefRebuilt: the cross-reference table could not be parsed and was
	// rebuilt by scanning for objects.
	DiagXRefRebuilt
	// DiagObjectDegraded: an object could not be parsed or recovered and was
	// resolved to null.
	DiagObjectDegraded
	// DiagObjectRecovered: an object's xref offset was wrong, but the object
	// was found at its real header.
	DiagObjectRecovered
	// DiagStringUndecryptable: a string did not decrypt and was kept as stored.
	DiagStringUndecryptable
)

// Diagnostic is one structural observation the reader recorded.
type Diagnostic struct {
	Kind DiagKind
	// Ref is the object the diagnostic is about; nil for one about the file.
	Ref  *PDFRef
	Errs []error
}

// framing reports whether k is about how an object or stream is framed, which
// a parse at a wrong offset can observe and a correct parse then withdraws.
func (k DiagKind) framing() bool {
	return k >= DiagObjectFraming && k <= DiagEndstreamEOL
}
