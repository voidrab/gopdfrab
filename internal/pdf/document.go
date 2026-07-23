package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// fileSource is the byte source a Reader parses and verifies from: a file on
// disk (Open) or an in-memory buffer (OpenBytes), so a freshly-written PDF can
// be re-verified without a temp-file round-trip.
type fileSource interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

// Reader parses a PDF file's structure (header, xref, trailer) and resolves
// its indirect object graph, caching decoded objects and object streams.
// Validation lives above this package; Reader only records the structural
// parse diagnostics (see PDFError) it discovers as a side effect of
// reading -- e.g. malformed stream framing -- for that layer to interpret.
type Reader struct {
	file       fileSource
	size       int64
	header     []byte
	trailer    PDFDict
	xrefTable  map[int]int64
	xrefOffset int64
	// pdfStart is the byte offset of the "%PDF-" header. Non-zero when the
	// file begins with garbage bytes before the PDF header (6.1.2).
	pdfStart int64

	// firstPageTrailer holds the linearized first-page trailer (with /Root,
	// /Info, /ID) when the main trailer lacks /Root. See EffectiveTrailer().
	firstPageTrailer PDFDict

	// parseDiagnostics collects document-structure violations (e.g. 6.1.8
	// object framing) found lazily during resolution; framingChecked dedupes
	// per object. See PDFError.
	parseDiagnostics []PDFError
	framingChecked   map[int]bool
	streamChecked    map[string]bool

	objCache map[int]PDFValue

	// resolvingInProgress marks object numbers currently being parsed, to break
	// self-referential resolution cycles. See ResolveReference.
	resolvingInProgress map[int]bool

	// data is the full file content as a byte slice (mmap on unix, heap on
	// other platforms, or the caller-supplied slice for OpenBytes).
	// nil only for NewRawReader, which drives test-only paths.
	data  []byte
	unmap func() error // non-nil when data is mmap'd; called by Close

	// compressedXref maps an object number to its location inside a
	// compressed object stream (xref type 2, PDF 1.5+), for objects not
	// found in xrefTable. See xrefstream.go / objstm.go.
	compressedXref map[int]compressedXrefEntry
	// objStmCache memoizes the decoded contents of an object stream, keyed
	// by the object stream's own object number.
	objStmCache map[int][]objStmEntry

	resolvedPtrs  map[uintptr]bool
	resolvedGraph PDFValue
	graphResolved bool

	// danglingScanRan dedupes the one-shot fill-in scan parseReference runs
	// before resolving a reference with no xref entry to null.
	danglingScanRan bool

	// degradeUnresolvable makes resolution turn unparseable objects into
	// recorded nulls (after a recovery attempt) instead of hard errors. Off
	// during initializeStructure so Open's error classification (ErrDamaged,
	// ErrEncrypted, ...) is preserved.
	degradeUnresolvable bool

	// degradedObjs / recoveredObjs dedupe per-object resolution-failure
	// diagnostics; degradedDiags keeps the null-degraded subset so convert can
	// carry the content loss into its final result.
	degradedObjs  map[int]bool
	recoveredObjs map[int]bool
	degradedDiags []PDFError

	// headerScan memoizes one whole-file "N G obj" scan for offset recovery,
	// mapping object number to header offsets in file order.
	headerScan map[int][]int64

	// decodedCache memoizes DecodeStream's output keyed by StreamKeyOf, so
	// repeated verify passes over the same Reader -- e.g. convert's fixer
	// iterations, which reseed the same Reader via SeedResolvedGraph -- decode
	// each unchanged content stream at most once. decodedMu guards it only
	// for DecodeStreamCachedConcurrent callers.
	decodedCache map[StreamKey][]byte
	decodedMu    sync.Mutex

	// scanCache memoizes TokenizeContent's output keyed by StreamKeyOf, so an
	// unchanged content stream is lexed/parsed at most once across all of a
	// Reader's verify passes, even though each pass re-evaluates every check
	// against the tokens fresh (a check's verdict can depend on state that
	// changes between iterations, e.g. OutputIntent coverage, so only the
	// token list -- never a check's result -- is safe to cache here).
	scanCache map[StreamKey][]ScannedOp

	// password is the password supplied to open the file (nil == empty), used
	// once to build crypt; crypt is the authenticated decryptor, non-nil only
	// for an encrypted file whose password authenticated. See crypt.go.
	password []byte
	crypt    *stdSecurityHandler
}

// DecodeStreamCached decodes dict's stream, memoizing the result by content
// identity (StreamKeyOf) on the Reader so callers sharing one Reader across
// multiple verify passes never re-inflate an unchanged stream.
func (d *Reader) DecodeStreamCached(dict PDFDict) ([]byte, error) {
	key, ok := StreamKeyOf(dict)
	if !ok {
		return DecodeStream(dict)
	}
	if data, ok := d.decodedCache[key]; ok {
		return data, nil
	}
	data, err := DecodeStream(dict)
	if err != nil {
		return nil, err
	}
	if d.decodedCache == nil {
		d.decodedCache = map[StreamKey][]byte{}
	}
	d.decodedCache[key] = data
	return data, nil
}

// DecodeStreamCachedConcurrent is DecodeStreamCached for concurrent callers:
// map access is locked, inflation happens outside the lock (a racing miss may
// decode a stream twice; results are identical). Never mix with the unlocked
// DecodeStreamCached concurrently.
func (d *Reader) DecodeStreamCachedConcurrent(dict PDFDict) ([]byte, error) {
	key, ok := StreamKeyOf(dict)
	if !ok {
		return DecodeStream(dict)
	}
	d.decodedMu.Lock()
	data, hit := d.decodedCache[key]
	d.decodedMu.Unlock()
	if hit {
		return data, nil
	}
	data, err := DecodeStream(dict)
	if err != nil {
		return nil, err
	}
	d.decodedMu.Lock()
	if d.decodedCache == nil {
		d.decodedCache = map[StreamKey][]byte{}
	}
	d.decodedCache[key] = data
	d.decodedMu.Unlock()
	return data, nil
}

// AdoptStreamCaches shares src's decoded-stream and token caches with d.
// Sound because StreamKey identifies RawStream bytes by pointer identity and
// both Readers are seeded with the same in-memory graph.
func (d *Reader) AdoptStreamCaches(src *Reader) {
	if src == nil {
		return
	}
	if src.decodedCache != nil {
		d.decodedCache = src.decodedCache
	}
	if src.scanCache != nil {
		d.scanCache = src.scanCache
	}
}

// ScanStreamCached decodes and tokenizes dict's content stream, memoizing the
// token list by content identity (StreamKeyOf) alongside DecodeStreamCached's
// decoded-bytes cache, so callers sharing one Reader across multiple verify
// passes tokenize each unchanged stream at most once.
func (d *Reader) ScanStreamCached(dict PDFDict) ([]ScannedOp, error) {
	key, ok := StreamKeyOf(dict)
	if !ok {
		data, err := DecodeStream(dict)
		if err != nil {
			return nil, err
		}
		return TokenizeContent(data), nil
	}
	if ops, ok := d.scanCache[key]; ok {
		return ops, nil
	}
	data, err := d.DecodeStreamCached(dict)
	if err != nil {
		return nil, err
	}
	ops := TokenizeContent(data)
	if d.scanCache == nil {
		d.scanCache = map[StreamKey][]ScannedOp{}
	}
	d.scanCache[key] = ops
	return ops, nil
}

// StructErrors returns the structural parse diagnostics recorded so far.
func (d *Reader) StructErrors() []PDFError {
	return d.parseDiagnostics
}

// recordStreamFraming records a 6.1.7 stream-framing violation, deduplicated
// per object number and check name.
func (d *Reader) recordStreamFraming(objNum int, check Check, msg string) {
	if d.streamChecked == nil {
		d.streamChecked = map[string]bool{}
	}
	key := fmt.Sprintf("%d:%s", objNum, check.Name())
	if d.streamChecked[key] {
		return
	}
	d.streamChecked[key] = true
	d.parseDiagnostics = append(d.parseDiagnostics, NewError(check, []error{errors.New(msg)}, objNum, nil))
}

// recordFraming records 6.1.8 object-framing violations for an object, at most
// once per object number.
func (d *Reader) recordFraming(objNum int, errs []error) {
	if len(errs) == 0 {
		return
	}
	if d.framingChecked == nil {
		d.framingChecked = map[int]bool{}
	}
	if d.framingChecked[objNum] {
		return
	}
	d.framingChecked[objNum] = true
	d.parseDiagnostics = append(d.parseDiagnostics, NewError(Checks.Structure.ObjectFraming, errs, objNum, nil))
}

// recordDegraded records that objNum could not be parsed or recovered and was
// resolved to null, at most once per object. Degraded objects taint
// content-usage completeness (see HasDegradedObjects) so check suppressions
// never hide violations behind them.
func (d *Reader) recordDegraded(objNum int, cause error) {
	if d.degradedObjs[objNum] {
		return
	}
	if d.degradedObjs == nil {
		d.degradedObjs = map[int]bool{}
	}
	d.degradedObjs[objNum] = true
	e := NewError(Checks.Structure.GraphResolutionFailure,
		[]error{fmt.Errorf("object %d unresolvable, treated as null: %v", objNum, cause)},
		0, &PDFRef{ObjNum: objNum})
	d.degradedDiags = append(d.degradedDiags, e)
	d.parseDiagnostics = append(d.parseDiagnostics, e)
}

// recordRecovered records that objNum's xref offset was invalid but the object
// was recovered at its real header, at most once per object.
func (d *Reader) recordRecovered(objNum int, badOffset, newOffset int64, cause error) {
	if d.recoveredObjs[objNum] {
		return
	}
	if d.recoveredObjs == nil {
		d.recoveredObjs = map[int]bool{}
	}
	d.recoveredObjs[objNum] = true
	d.parseDiagnostics = append(d.parseDiagnostics, NewError(Checks.Structure.GraphResolutionFailure,
		[]error{fmt.Errorf("object %d: invalid xref offset %d (%v), recovered at offset %d",
			objNum, badOffset, cause, newOffset)},
		0, &PDFRef{ObjNum: objNum}))
}

// DegradedObjects returns the diagnostics for objects resolved to null because
// they could not be parsed or recovered.
func (d *Reader) DegradedObjects() []PDFError { return d.degradedDiags }

// HasDegradedObjects reports whether any object degraded to null during
// resolution, making state derived from the graph (e.g. content usage)
// potentially incomplete.
func (d *Reader) HasDegradedObjects() bool { return len(d.degradedObjs) > 0 }

// discardObjDiagnostics drops diagnostics recorded for objNum since mark,
// un-marking their dedup keys so a later successful parse can record its own.
func (d *Reader) discardObjDiagnostics(mark, objNum int) {
	kept := d.parseDiagnostics[:mark]
	for _, e := range d.parseDiagnostics[mark:] {
		if e.page != objNum {
			kept = append(kept, e)
			continue
		}
		if e.check == Checks.Structure.ObjectFraming {
			delete(d.framingChecked, objNum)
		} else {
			delete(d.streamChecked, fmt.Sprintf("%d:%s", objNum, e.check.Name()))
		}
	}
	d.parseDiagnostics = kept
}

// NewRawReader builds a Reader directly from already-known structural state,
// bypassing Open/newReader's normal parse pipeline. It exists for white-box
// tests in other internal packages (verify) that drive Reader-derived
// behavior against a specific -- often deliberately malformed -- structure
// without a real file round-trip; production code should use Open/OpenBytes.
func NewRawReader(file interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}, trailer PDFDict, size int64, xrefOffset int64) *Reader {
	return &Reader{file: file, trailer: trailer, size: size, xrefOffset: xrefOffset, degradeUnresolvable: true}
}

// Open initializes the PDF document at path, decrypting with the empty
// password when the file is encrypted.
func Open(path string) (*Reader, error) { return OpenWithPassword(path, nil) }

// OpenWithPassword is Open with an explicit password for an encrypted file.
// nil is the empty password, which covers the common permission-only case.
// (The root package reaches this path via gopdfrab.Options.Password on the
// *Context entry points.)
func OpenWithPassword(path string, password []byte) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	data, unmap, err := mmapFile(f, info.Size())
	if err != nil {
		f.Close()
		return nil, err
	}

	return newDocument(f, info.Size(), data, unmap, password)
}

// bytesFileSource adapts a *bytes.Reader (which has no Close) to fileSource.
type bytesFileSource struct{ *bytes.Reader }

func (bytesFileSource) Close() error { return nil }

// OpenBytes initializes a Reader from an in-memory PDF, parsing the same way
// Open does but without touching disk -- used to re-verify freshly-written
// bytes during conversion.
func OpenBytes(data []byte) (*Reader, error) { return OpenBytesWithPassword(data, nil) }

// OpenBytesWithPassword is OpenBytes with an explicit password.
func OpenBytesWithPassword(data, password []byte) (*Reader, error) {
	return newDocument(bytesFileSource{bytes.NewReader(data)}, int64(len(data)), data, nil, password)
}

// newDocument parses a Reader's structure from an already-opened byte source
// of the given size, shared by Open and OpenBytes.
func newDocument(src fileSource, size int64, data []byte, unmap func() error, password []byte) (*Reader, error) {
	header := make([]byte, 8)
	if _, err := src.ReadAt(header, 0); err != nil {
		src.Close()
		// Too short to even hold a header: not a PDF.
		return nil, fmt.Errorf("%w: could not read header: %v", ErrNotPDF, err)
	}

	doc := &Reader{
		file:     src,
		size:     size,
		header:   header,
		data:     data,
		unmap:    unmap,
		password: password,
	}

	if err := doc.initializeStructure(); err != nil {
		src.Close()
		return nil, fmt.Errorf("failed to parse structure: %w", err)
	}

	doc.degradeUnresolvable = true
	return doc, nil
}

// initializeStructure locates startxref, then parses the xref table and trailer.
func (d *Reader) initializeStructure() error {
	// Detect garbage bytes preceding the %PDF- marker (6.1.2).
	// xref offsets in such files are relative to the PDF content start.
	scanSize := min(d.size, 1024)
	scanBuf := make([]byte, scanSize)
	if _, err := d.file.ReadAt(scanBuf, 0); err == nil {
		idx := bytes.Index(scanBuf, []byte("%PDF-"))
		if idx < 0 {
			// No header anywhere in the leading bytes: not a PDF, not merely
			// damaged. (A garbage prefix before %PDF- is tolerated -- see below.)
			return fmt.Errorf("%w: no %%PDF- header", ErrNotPDF)
		}
		if idx > 0 {
			d.pdfStart = int64(idx)
		}
	}

	tailSize := min(d.size, int64(1500))

	tailOffset := d.size - tailSize
	tail := make([]byte, tailSize)
	if _, err := d.file.ReadAt(tail, tailOffset); err != nil {
		return err
	}

	// Locate startxref and its offset. A missing keyword, a missing offset
	// token, or a non-numeric offset all leave the cross-reference section
	// unlocatable -- recoverable by a full-file object scan (6.1.4) rather than
	// a hard failure, since a damaged xref is exactly what a PDF/A converter is
	// reached for.
	startXrefIdx := bytes.LastIndex(tail, []byte("startxref"))
	xrefOffset, haveOffset := int64(0), false
	if startXrefIdx != -1 {
		if tokens := strings.Fields(string(tail[startXrefIdx+9:])); len(tokens) > 0 {
			if off, perr := strconv.ParseInt(tokens[0], 10, 64); perr == nil {
				xrefOffset, haveOffset = off, true
			}
		}
	}

	var xrefErr error
	if haveOffset {
		d.xrefOffset = xrefOffset
		xrefErr = d.parseXRefTable(xrefOffset + d.pdfStart)
		if xrefErr != nil && d.pdfStart != 0 {
			// Some malformed (6.1.2) files record xref offsets relative to true
			// byte 0 instead of the "%PDF-" marker; retry unadjusted.
			xrefErr = d.parseXRefTable(xrefOffset)
		}
	} else {
		xrefErr = errors.New("startxref keyword or offset missing")
	}

	// usedXRefStream is set when the classic parser failed but the object at
	// xrefOffset turned out to be a genuine cross-reference stream (PDF
	// 1.5+); its own dictionary then doubles as the trailer, so the literal
	// "trailer" keyword search below is skipped entirely.
	var xrefStreamTrailer PDFDict
	usedXRefStream := false

	if xrefErr != nil {
		// 6.1.4: the classic xref table is unparseable or unlocatable. Recover
		// the object table as accurately as possible so an otherwise-valid
		// modern PDF can still be read, and record the appropriate violation.
		d.xrefTable = make(map[int]int64)
		if haveOffset {
			trailer, xsErr := d.tryParseXRefStream(xrefOffset+d.pdfStart, false)
			if xsErr != nil && d.pdfStart != 0 {
				trailer, xsErr = d.tryParseXRefStream(xrefOffset, false)
			}
			if xsErr == nil {
				// It's actually a cross-reference stream, which PDF/A-1b
				// prohibits (6.1.4-3); its dictionary doubles as the trailer.
				xrefStreamTrailer, usedXRefStream = trailer, true
				d.parseDiagnostics = append(d.parseDiagnostics,
					NewError(Checks.Structure.XRefStream,
						[]error{errors.New("cross-reference streams are not permitted")}, 1, nil))
			}
		}
		if !usedXRefStream {
			d.parseDiagnostics = append(d.parseDiagnostics, NewError(Checks.Structure.XRefKeyword,
				[]error{fmt.Errorf("cross-reference table could not be parsed, rebuilt by scanning for objects: %v", xrefErr)}, 1, nil))
			if err := d.recoverXRefByBruteForceScan(false); err != nil {
				return fmt.Errorf("%w: could not recover object table: %v", ErrDamaged, xrefErr)
			}
		}
	}

	if usedXRefStream {
		d.trailer = xrefStreamTrailer
	} else {
		// A missing startxref leaves no boundary before the trailer keyword;
		// search the whole tail in that case.
		searchEnd := len(tail)
		if startXrefIdx != -1 {
			searchEnd = startXrefIdx
		}
		searchBlock := tail[:searchEnd]
		trailerIdx := bytes.LastIndex(searchBlock, []byte("trailer"))

		var literalTrailer PDFDict
		haveLiteral := false
		if trailerIdx != -1 {
			l := NewLexer(bytes.NewReader(searchBlock[trailerIdx:]))
			if tok := l.NextToken(); tok.Value == "trailer" {
				if t, terr := parseDictionary(l); terr == nil {
					literalTrailer, haveLiteral = t, true
				} else if xrefErr == nil {
					l.Release()
					return fmt.Errorf("%w: could not parse trailer dictionary: %v", ErrDamaged, terr)
				}
			} else if xrefErr == nil {
				l.Release()
				return fmt.Errorf("%w: expected 'trailer' keyword", ErrDamaged)
			}
			l.Release()
		} else if xrefErr == nil {
			return fmt.Errorf("%w: trailer keyword not found", ErrDamaged)
		}

		if haveLiteral {
			d.trailer = literalTrailer
		} else {
			// No usable literal trailer, but the xref was already in recovery:
			// synthesize a trailer from a scanned cross-reference stream or the
			// document catalog.
			trailer, err := d.recoverTrailer()
			if err != nil {
				return fmt.Errorf("%w: trailer could not be recovered: %v", ErrDamaged, err)
			}
			d.trailer = trailer
		}
	}

	// Build a complete object table for incrementally-updated PDFs; newer
	// revisions already in d.xrefTable take precedence over older entries.
	d.followXRefPrevChain()

	// Linearized PDFs may have a main trailer lacking /Root; locate the
	// first-page trailer instead so /Root and /ID can be found.
	if d.trailer.Entries["Root"] == nil {
		d.findAndLoadFirstPageTrailer()
	}

	return d.setupDecryption()
}

// followXRefPrevChain walks the /Prev chain from d.trailer, filling in
// d.xrefTable from older xref sections without overwriting newer entries.
func (d *Reader) followXRefPrevChain() {
	d.mergeHybridXRefStream(d.trailer)
	visited := map[int64]bool{d.xrefOffset: true}
	prev := d.trailer.Entries["Prev"]
	for {
		prevInt, ok := prev.(PDFInteger)
		if !ok {
			return
		}
		offset := int64(prevInt) + d.pdfStart
		if visited[offset] {
			return
		}
		visited[offset] = true

		prevTrailer, err := d.ParseXRefSectionAt(offset, true /* fillIn */)
		if err != nil {
			// The previous revision may itself be a cross-reference stream
			// rather than a classic table (e.g. a chain of incremental
			// updates that all use xref streams).
			prevTrailer, err = d.tryParseXRefStream(offset, true /* fillIn */)
			if err != nil {
				// The older section is unparseable (6.1.4); recover its
				// objects by brute force so the graph stays resolvable. The
				// scan covers the whole file, so stop following the chain.
				d.recoverXRefByBruteForceScan(true)
				return
			}
		}
		d.mergeHybridXRefStream(prevTrailer)
		prev = prevTrailer.Entries["Prev"]
	}
}

// mergeHybridXRefStream merges the cross-reference stream a classic trailer
// points to via /XRefStm (hybrid-reference files, ISO 32000-1 7.5.8.4). Such a
// trailer's newest objects live only in that stream; existing entries win.
func (d *Reader) mergeHybridXRefStream(trailer PDFDict) {
	stm, ok := trailer.Entries["XRefStm"].(PDFInteger)
	if !ok {
		return
	}
	d.tryParseXRefStream(int64(stm)+d.pdfStart, true /* fillIn */)
}

// xrefLineRe matches "xref" at a line boundary, capturing it in group 1
// (excludes the "xref" suffix inside "startxref").
var xrefLineRe = regexp.MustCompile(`(?:^|[\r\n])(xref[\r\n])`)

// bruteForceObjRe matches an "N G obj" indirect-object header at a line
// boundary, used to rebuild the object table when xref parsing fails (6.1.4).
var bruteForceObjRe = regexp.MustCompile(`(?:^|[\r\n])(\d+)\s+\d+\s+obj\b`)

// recoverXRefByBruteForceScan rebuilds d.xrefTable by scanning the file for
// "N G obj" headers, used when an xref section cannot be parsed (6.1.4).
// Later occurrences win, matching how a real /Prev chain resolves duplicates.
// In fillIn mode existing entries are kept (newer revisions take precedence).
func (d *Reader) recoverXRefByBruteForceScan(fillIn bool) error {
	raw, err := d.fullBytes()
	if err != nil {
		return err
	}

	if !fillIn {
		d.xrefTable = make(map[int]int64)
	}
	existing := map[int]bool{}
	if fillIn {
		for objNum := range d.xrefTable {
			existing[objNum] = true
		}
	}
	found := false
	for _, loc := range bruteForceObjRe.FindAllSubmatchIndex(raw, -1) {
		objNum, err := strconv.Atoi(string(raw[loc[2]:loc[3]]))
		if err != nil || existing[objNum] {
			continue
		}
		d.xrefTable[objNum] = int64(loc[2])
		found = true
	}
	if !found {
		return errors.New("no indirect objects found")
	}
	return nil
}

// recoverTrailerFromXRefStream finds a brute-force-scanned object declaring
// "/Type /XRef" and returns its dict, recovering /Root, /Info, and /ID.
func (d *Reader) recoverTrailerFromXRefStream() (PDFDict, error) {
	best, bestOff, found := PDFDict{}, int64(-1), false
	for objNum, off := range d.xrefTable {
		v, err := d.ResolveReference(PDFRef{ObjNum: objNum})
		if err != nil {
			continue
		}
		dict, ok := v.(PDFDict)
		if !ok {
			continue
		}
		// Later occurrence (higher offset) wins, matching /Prev-chain order and
		// keeping recovery deterministic across map-iteration order.
		if dict.Entries["Type"] == (PDFName{Value: "XRef"}) && off > bestOff {
			best, bestOff, found = dict, off, true
		}
	}
	if !found {
		return PDFDict{}, errors.New("no cross-reference stream object found")
	}
	return best, nil
}

// recoverTrailer synthesizes a trailer for a file whose real trailer cannot be
// found, from objects already brute-force-scanned into d.xrefTable. It prefers
// a cross-reference stream dict (/Type /XRef, which carries /Root, /Info and
// /ID); failing that it locates the document catalog (/Type /Catalog) and
// returns "<< /Root <catalogRef> >>". The synthesized trailer has no /ID, so
// the recovered file is reported non-conformant (6.1.3) as it should be.
func (d *Reader) recoverTrailer() (PDFDict, error) {
	if t, err := d.recoverTrailerFromXRefStream(); err == nil {
		return t, nil
	}
	catalogNum, catalogOff, found := 0, int64(-1), false
	for objNum, off := range d.xrefTable {
		v, err := d.ResolveReference(PDFRef{ObjNum: objNum})
		if err != nil {
			continue
		}
		dict, ok := v.(PDFDict)
		if !ok {
			continue
		}
		if dict.Entries["Type"] == (PDFName{Value: "Catalog"}) && off > catalogOff {
			catalogNum, catalogOff, found = objNum, off, true
		}
	}
	if !found {
		return PDFDict{}, errors.New("no catalog or cross-reference stream object found")
	}
	t := NewPDFDict()
	t.Entries["Root"] = PDFRef{ObjNum: catalogNum}
	return t, nil
}

// findAndLoadFirstPageTrailer scans every xref section in a linearized PDF,
// filling d.xrefTable and setting d.firstPageTrailer to the first one with /Root.
func (d *Reader) findAndLoadFirstPageTrailer() {
	raw, err := d.fullBytes()
	if err != nil {
		return
	}

	for _, loc := range xrefLineRe.FindAllSubmatchIndex(raw, -1) {
		// loc[2] and loc[3] delimit the captured group 1 ("xref\r" or "xref\n").
		// The "xref" keyword itself starts at loc[2].
		offset := int64(loc[2])
		trailer, err := d.ParseXRefSectionAt(offset, true /* fillIn */)
		if err != nil {
			continue
		}
		if trailer.Entries["Root"] != nil && d.firstPageTrailer.Entries == nil {
			d.firstPageTrailer = trailer
		}
	}
}

// FullBytes returns the entire file as a byte slice. When the document is
// memory-mapped or was opened from bytes this is the backing slice itself,
// with no copy; callers must treat it as read-only.
func (d *Reader) FullBytes() ([]byte, error) {
	return d.fullBytes()
}

// fullBytes returns the full file as a byte slice, reusing d.data when
// available to avoid a redundant heap allocation.
func (d *Reader) fullBytes() ([]byte, error) {
	if d.data != nil {
		return d.data, nil
	}
	raw := make([]byte, d.size)
	_, err := d.file.ReadAt(raw, 0)
	return raw, err
}

// EffectiveTrailer returns d.trailer, or d.firstPageTrailer for linearized
// PDFs whose overflow trailer lacks /Root.
func (d *Reader) EffectiveTrailer() PDFDict {
	if d.firstPageTrailer.Entries != nil {
		return d.firstPageTrailer
	}
	return d.trailer
}

func (d *Reader) BuildPageIndex(graph PDFValue) (map[int]int, error) {
	index := make(map[int]int)

	graphDict, ok := graph.(PDFDict)
	if !ok {
		return nil, fmt.Errorf("document graph is not a dictionary")
	}
	root := graphDict.Entries["Root"]
	if root == nil {
		return nil, fmt.Errorf("dict Root is nil")
	}
	rootDict, ok := root.(PDFDict)
	if !ok {
		return nil, fmt.Errorf("Root is not a dictionary")
	}
	pages := rootDict.Entries["Pages"]
	if pages == nil {
		return nil, fmt.Errorf("dict Pages is nil")
	}

	pageNum := 0

	// A malformed page tree can be cyclic (a Kids entry referring back to an
	// ancestor) or pathologically deep. Guard both: seen dedupes indirect
	// nodes already visited (breaking cycles), and depth caps degenerate but
	// acyclic nesting, so neither can drive walk into a stack overflow.
	seen := make(map[int]bool)
	const maxPageTreeDepth = 1 << 16
	var walk func(node PDFValue, depth int) error
	walk = func(node PDFValue, depth int) error {
		if depth > maxPageTreeDepth {
			return fmt.Errorf("page tree exceeds maximum depth")
		}
		dict, ok := node.(PDFDict)
		if !ok {
			return nil
		}
		if ref, ok := dict.Entries["_ref"].(PDFRef); ok {
			if seen[ref.ObjNum] {
				return nil
			}
			seen[ref.ObjNum] = true
		}

		if (dict.Entries["Type"] == PDFName{Value: "Page"}) {
			pageNum++
			if ref, ok := dict.Entries["_ref"].(PDFRef); ok {
				index[ref.ObjNum] = pageNum
			}
			return nil
		}

		if kids, ok := dict.Entries["Kids"].(PDFArray); ok {
			for _, kid := range kids {
				if err := walk(kid, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	err := walk(pages, 0)
	return index, err
}

// Close releases all resources held by the Reader.
func (d *Reader) Close() error {
	if d.unmap != nil {
		if err := d.unmap(); err != nil {
			d.file.Close()
			return err
		}
		d.unmap = nil
		d.data = nil
	}
	return d.file.Close()
}

// ReadAt reads len(p) bytes from the underlying file source at offset off,
// for validation that inspects raw bytes directly (e.g. header/trailer framing).
func (d *Reader) ReadAt(p []byte, off int64) (int, error) {
	return d.file.ReadAt(p, off)
}

// Size returns the total size of the underlying file source in bytes.
func (d *Reader) Size() int64 {
	return d.size
}

// PDFStart returns the byte offset of the "%PDF-" header, non-zero when the
// file begins with garbage bytes before it (6.1.2).
func (d *Reader) PDFStart() int64 {
	return d.pdfStart
}

// XRefOffset returns the byte offset of the main cross-reference section, as
// recorded by the trailer's startxref.
func (d *Reader) XRefOffset() int64 {
	return d.xrefOffset
}

// XRefTable returns the object number -> byte offset map built while parsing
// the cross-reference table(s).
func (d *Reader) XRefTable() map[int]int64 {
	return d.xrefTable
}

// Trailer returns the document's main trailer dictionary, as parsed -- use
// EffectiveTrailer for the one that actually carries /Root on linearized PDFs.
func (d *Reader) Trailer() PDFDict {
	return d.trailer
}

// Version extracts the PDF version from the Reader header
func (d *Reader) Version() (string, error) {
	if !bytes.HasPrefix(d.header, []byte("%PDF-")) {
		return "", errors.New("invalid file format: missing PDF header")
	}

	rest := d.header[len("%PDF-"):]

	end := bytes.LastIndexFunc(rest, func(r rune) bool {
		return r == '\n' || r == '\r' || unicode.IsSpace(r)
	})

	var version string
	if end == -1 {
		version = string(rest)
	} else {
		version = string(rest[:end])
	}

	if version == "" {
		return "", errors.New("invalid PDF header: missing version")
	}

	return version, nil
}

// Metadata extracts info from the Info dictionary.
func (d *Reader) Metadata() (map[string]string, error) {
	value, err := d.ResolveGraphByPath([]string{"Info"})
	if err != nil {
		return nil, fmt.Errorf("no information dictionary found: %v", err)
	}

	dict, ok := value.(PDFDict)
	if !ok {
		return nil, errors.New("information object is not a dictionary")
	}

	metadata := make(map[string]string)
	for k, v := range dict.Entries {
		switch s := v.(type) {
		case PDFString:
			metadata[k] = DecodePDFTextString([]byte(s.Value))
		case PDFHexString:
			metadata[k] = DecodePDFTextString(DecodePDFHexStringBytes(s.Value))
		}
	}
	return metadata, nil
}

// XMPMetadata returns the document's raw XMP metadata packet (Root/Metadata),
// decoded and normalised to UTF-8. It returns an error if the document has no
// XMP metadata stream, regardless of whether the document otherwise validates
// as PDF/A.
func (d *Reader) XMPMetadata() ([]byte, error) {
	data, _, err := d.RawXMP()
	return data, err
}

// ClaimedConformance returns the PDF/A part and conformance level the
// document's XMP metadata claims (e.g. "1", "B"), read from the pdfaid
// namespace. This reflects what the file claims, not whether it actually
// validates — use Verify or IsPDFA to check actual compliance.
func (d *Reader) ClaimedConformance() (part, conformance string, err error) {
	data, _, err := d.RawXMP()
	if err != nil {
		return "", "", err
	}
	xmp := string(data)

	part, hasPart := FirstRegexpGroup(PDFAPartRe, xmp)
	if !hasPart {
		return "", "", errors.New("no PDF/A part identifier in XMP metadata")
	}
	conformance, hasConf := FirstRegexpGroup(PDFAConfRe, xmp)
	if !hasConf {
		return part, "", errors.New("no PDF/A conformance level in XMP metadata")
	}
	return part, conformance, nil
}

// PageCount retrieves the page count.
func (d *Reader) PageCount() (int, error) {
	value, err := d.ResolveGraphByPath([]string{"Root", "Pages", "Count"})
	if err != nil {
		return 0, err
	}
	count, ok := value.(PDFInteger)
	if !ok {
		return 0, nil
	}

	return int(count), nil
}

// ResolveGraphByPath resolves the PDF object graph by path,
// starting from the effective trailer (firstPageTrailer if set, else trailer).
func (d *Reader) ResolveGraphByPath(path []string) (PDFValue, error) {
	if len(path) == 0 {
		return nil, errors.New("path cannot be empty")
	}

	return d.resolvePath(d.EffectiveTrailer(), path)
}

// ResolveGraph resolves the PDF object graph,
// starting from the effective trailer (firstPageTrailer if set, else trailer).
func (d *Reader) ResolveGraph() (PDFValue, error) {
	if d.graphResolved {
		return d.resolvedGraph, nil
	}
	g, err := d.resolveInPlace(d.EffectiveTrailer())
	if err != nil {
		return nil, err
	}
	d.resolvedGraph, d.graphResolved = g, true
	return g, nil
}

// SeedResolvedGraph pre-populates the Reader with an already-resolved graph
// and object cache.
func (d *Reader) SeedResolvedGraph(graph PDFDict, objs map[int]PDFValue) {
	d.resolvedGraph = graph
	d.graphResolved = true
	d.objCache = objs
}

// resolvePath walks a PDF object following path elements, which may be
// dictionary keys or array indices. node must already be a resolved object.
func (d *Reader) resolvePath(node PDFValue, path []string) (PDFValue, error) {
	current, err := d.ResolveObject(node)
	if err != nil {
		return nil, err
	}

	for _, key := range path {

		current, err = d.resolveShallow(current)
		if err != nil {
			return nil, err
		}

		if arr, ok := current.(PDFArray); ok {
			idx, err := strconv.Atoi(key)
			if err != nil {
				return arr, nil
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("array index out of range: %d", idx)
			}
			current = arr[idx]
			continue
		}

		if dict, ok := current.(PDFDict); ok {
			val, found := dict.Entries[key]
			if !found {
				return nil, fmt.Errorf("key %q not found in dictionary", key)
			}
			current = val
			continue
		}

		return current, nil
	}

	return d.ResolveObject(current)
}

// resolveInPlace returns obj fully resolved.
func (d *Reader) resolveInPlace(obj PDFValue) (PDFValue, error) {
	return d.resolveInPlaceDepth(obj, 0)
}

// maxResolveDepth bounds resolveInPlace recursion so a deep acyclic reference
// chain cannot overflow the stack. Var, not const, only so tests can lower it.
var maxResolveDepth = 1 << 17

func (d *Reader) resolveInPlaceDepth(obj PDFValue, depth int) (PDFValue, error) {
	if depth > maxResolveDepth {
		return nil, fmt.Errorf("resolve depth limit exceeded")
	}
	switch v := obj.(type) {
	case PDFRef:
		target, err := d.ResolveReference(v)
		if err != nil {
			return nil, err
		}
		return d.resolveInPlaceDepth(target, depth+1)

	case PDFDict:
		ptr := ValuePointer(v.Entries)
		if d.resolvedPtrs[ptr] {
			return v, nil
		}
		if d.resolvedPtrs == nil {
			d.resolvedPtrs = map[uintptr]bool{}
		}
		d.resolvedPtrs[ptr] = true // mark before recursing, so cycles terminate
		for k, val := range v.Entries {
			if k == "_ref" {
				continue
			}
			r, err := d.resolveInPlaceDepth(val, depth+1)
			if err != nil {
				// Unmark: this dict did not actually finish resolving (some
				// entries past the failing key are still raw PDFRefs), so a
				// subsequent ResolveGraph call (e.g. a caller retrying after
				// this error) must redo it rather than short-circuit-return
				// the partially-resolved value as if it were complete.
				delete(d.resolvedPtrs, ptr)
				return nil, err
			}
			v.Entries[k] = r
		}
		return v, nil

	case PDFArray:
		ptr := ValuePointer(v)
		if d.resolvedPtrs[ptr] {
			return v, nil
		}
		if d.resolvedPtrs == nil {
			d.resolvedPtrs = map[uintptr]bool{}
		}
		d.resolvedPtrs[ptr] = true
		for i, elem := range v {
			r, err := d.resolveInPlaceDepth(elem, depth+1)
			if err != nil {
				delete(d.resolvedPtrs, ptr) // see the PDFDict case above
				return nil, err
			}
			v[i] = r
		}
		return v, nil

	default:
		return v, nil
	}
}
