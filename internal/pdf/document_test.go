package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func createValidPDF(filename string) error {
	header := "%PDF-1.7\n"
	comment := "%äüöß\n"
	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /OCProperties (Test) >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Count 5 >>\nendobj\n"
	obj3 := "3 0 obj\n<< /Title (Test PDF) /Producer (GoLib) >>\nendobj\n"

	offset1 := len(header) + len(comment)
	offset2 := offset1 + len(obj1)
	offset3 := offset2 + len(obj2)
	xrefOffset := offset3 + len(obj3)

	xref := fmt.Sprintf("xref\n0 4\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n",
		offset1, offset2, offset3)

	trailer := "trailer\n<< /Size 4 /Root 1 0 R /Info 3 0 R >>\n"
	startxref := fmt.Sprintf("startxref\n%d\n%%EOF", xrefOffset)

	content := header + comment + obj1 + obj2 + obj3 + xref + trailer + startxref
	return os.WriteFile(filename, []byte(content), 0644)
}

func TestDocument_OpenAndRead(t *testing.T) {
	filename := "test.pdf"
	if err := createValidPDF(filename); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(filename)

	doc, err := Open(filename)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer doc.Close()

	if doc.size == 0 {
		t.Error("Reader size reported as 0")
	}

	meta, err := doc.GetMetadata()
	if err != nil {
		t.Errorf("GetMetadata error: %v", err)
	}
	if meta["Title"] != "Test PDF" {
		t.Errorf("Expected Title 'Test PDF', got %v", meta["Title"])
	}

	count, err := doc.GetPageCount()
	if err != nil {
		t.Errorf("GetPageCount error: %v", err)
	}
	if count != 5 {
		t.Errorf("Expected 5 pages, got %d", count)
	}

	version, err := doc.GetVersion()
	if err != nil {
		t.Errorf("GetVersion error: %v", err)
	}
	if version != "1.7" {
		t.Errorf("Expected PDF version 1.7, got %v", version)
	}
}

func TestDocument_GetPageCount(t *testing.T) {
	filename := "test_getpagecount.pdf"
	if err := createValidPDF(filename); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(filename)

	doc, err := Open(filename)
	if err != nil {
		t.Fatalf("Failed to open valid PDF: %v", err)
	}
	defer doc.Close()

	count, err := doc.GetPageCount()
	if err != nil {
		t.Errorf("GetPageCount error: %v", err)
	}
	if count != 5 {
		t.Errorf("Expected 5 pages, got %d", count)
	}
}

func TestDocument_GetVersion(t *testing.T) {
	filename := "test_getversion.pdf"
	if err := createValidPDF(filename); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(filename)

	doc, err := Open(filename)
	if err != nil {
		t.Fatalf("Failed to open valid PDF: %v", err)
	}
	defer doc.Close()

	version, err := doc.GetVersion()
	if err != nil {
		t.Errorf("GetVersion error: %v", err)
	}
	if version != "1.7" {
		t.Errorf("Expected PDF version 1.7, got %v", version)
	}
}

func TestDocument_OpenInvalid(t *testing.T) {
	filename := "test_invalid.pdf"
	os.WriteFile(filename, []byte("Not a PDF file"), 0644)
	defer os.Remove(filename)

	_, err := Open(filename)
	if err == nil {
		t.Error("Expected error when opening invalid PDF, got nil")
	}
}

// TestGetVersionBranches covers every branch of GetVersion via a bare Reader.
func TestGetVersionBranches(t *testing.T) {
	cases := []struct {
		header  string
		want    string
		wantErr bool
	}{
		{"%PDF-1.4\n", "1.4", false},
		{"%PDF-1.7", "1.7", false}, // no trailing newline (end == -1)
		{"garbage", "", true},      // missing header
		{"%PDF-\n", "", true},      // missing version
	}
	for _, c := range cases {
		got, err := (&Reader{header: []byte(c.header)}).GetVersion()
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("GetVersion(%q) = %q, %v; want %q, err=%v", c.header, got, err, c.want, c.wantErr)
		}
	}
}

// writeMinimalPDF writes a PDF with the given object bodies (1-indexed) and a
// trailer dict body, computing a correct classic xref, and returns its path.
func writeMinimalPDF(t *testing.T, objs []string, trailerBody string) string {
	t.Helper()
	header := "%PDF-1.7\n"
	body := header
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = len(body)
		body += fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xrefOffset := len(body)
	xref := fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for i := 1; i <= len(objs); i++ {
		xref += fmt.Sprintf("%010d 00000 n \n", offsets[i])
	}
	body += xref + "trailer\n" + trailerBody + "\n"
	body += fmt.Sprintf("startxref\n%d\n%%%%EOF", xrefOffset)

	path := filepath.Join(t.TempDir(), "minimal.pdf")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

// TestAccessorsMissingMetadata covers the error paths of GetMetadata,
// XMPMetadata, and ClaimedConformance on a document that has neither an Info
// dictionary nor an XMP metadata stream, and confirms GetPageCount still works.
func TestAccessorsMissingMetadata(t *testing.T) {
	path := writeMinimalPDF(t,
		[]string{
			"<< /Type /Catalog /Pages 2 0 R >>",
			"<< /Type /Pages /Count 3 /Kids [] >>",
		},
		"<< /Size 3 /Root 1 0 R >>",
	)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	if _, err := doc.GetMetadata(); err == nil {
		t.Error("GetMetadata should error when there is no Info dict")
	}
	if _, err := doc.XMPMetadata(); err == nil {
		t.Error("XMPMetadata should error when there is no metadata stream")
	}
	if _, _, err := doc.ClaimedConformance(); err == nil {
		t.Error("ClaimedConformance should error when there is no XMP")
	}
	if n, err := doc.GetPageCount(); err != nil || n != 3 {
		t.Errorf("GetPageCount = %d, %v; want 3", n, err)
	}
}

// TestBruteForceXRefRecovery opens a PDF whose startxref points past EOF,
// forcing the brute-force object scan recovery path.
func TestBruteForceXRefRecovery(t *testing.T) {
	header := "%PDF-1.7\n"
	body := header
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	body += "2 0 obj\n<< /Type /Pages /Count 7 /Kids [] >>\nendobj\n"
	body += "trailer\n<< /Size 3 /Root 1 0 R >>\n"
	body += "startxref\n999999\n%%EOF" // deliberately invalid offset

	path := filepath.Join(t.TempDir(), "broken_xref.pdf")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open (brute-force recovery) failed: %v", err)
	}
	defer doc.Close()

	if n, err := doc.GetPageCount(); err != nil || n != 7 {
		t.Errorf("recovered GetPageCount = %d, %v; want 7", n, err)
	}
}

// TestDecodeStreamCachedConcurrent covers the no-key fallback, cache miss +
// store, cache hit, and decode-failure paths.
func TestDecodeStreamCachedConcurrent(t *testing.T) {
	d := &Reader{}

	// No RawStream bytes: not cacheable, falls back to plain DecodeStream,
	// which errors because the dict isn't a stream at all.
	if _, err := d.DecodeStreamCachedConcurrent(PDFDict{}); err == nil {
		t.Error("expected error decoding a non-stream dict")
	}

	streamDict := PDFDict{HasStream: true, RawStream: []byte("cached")}
	got1, err := d.DecodeStreamCachedConcurrent(streamDict)
	if err != nil || string(got1) != "cached" {
		t.Fatalf("first decode = %q, %v", got1, err)
	}
	if len(d.decodedCache) != 1 {
		t.Errorf("expected one cache entry after first decode, got %d", len(d.decodedCache))
	}

	got2, err := d.DecodeStreamCachedConcurrent(streamDict)
	if err != nil || string(got2) != "cached" {
		t.Fatalf("second (cache-hit) decode = %q, %v", got2, err)
	}

	badDict := PDFDict{
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "JPXDecode"}},
		HasStream: true, RawStream: []byte("x"),
	}
	if _, err := d.DecodeStreamCachedConcurrent(badDict); err == nil {
		t.Error("expected error for an unsupported filter")
	}
}

// TestAdoptStreamCaches covers the nil-src no-op and copying populated caches,
// leaving a destination's own caches alone when src has none.
func TestAdoptStreamCaches(t *testing.T) {
	d := &Reader{}
	d.AdoptStreamCaches(nil) // must not panic

	src := &Reader{
		decodedCache: map[StreamKey][]byte{{ptr: 1, len: 2}: []byte("x")},
		scanCache:    map[StreamKey][]ScannedOp{{ptr: 3, len: 4}: {{Op: "Tj"}}},
	}
	d.AdoptStreamCaches(src)
	if len(d.decodedCache) != 1 || len(d.scanCache) != 1 {
		t.Errorf("caches not adopted: decoded=%d scan=%d", len(d.decodedCache), len(d.scanCache))
	}

	own := &Reader{decodedCache: map[StreamKey][]byte{{ptr: 5, len: 6}: []byte("y")}}
	own.AdoptStreamCaches(&Reader{})
	if len(own.decodedCache) != 1 {
		t.Error("own cache should be untouched when src has none")
	}
}

// TestRecordFramingDedup covers recordFraming's empty-errs no-op and its
// once-per-object-number dedup.
func TestRecordFramingDedup(t *testing.T) {
	d := &Reader{}
	d.recordFraming(1, nil)
	if len(d.parseDiagnostics) != 0 {
		t.Fatal("empty errs should not record a diagnostic")
	}
	d.recordFraming(1, []error{errors.New("bad framing")})
	d.recordFraming(1, []error{errors.New("bad framing again")})
	if len(d.parseDiagnostics) != 1 {
		t.Errorf("expected exactly one recorded diagnostic, got %d", len(d.parseDiagnostics))
	}
}

// TestRecordStreamFramingDedup covers recordStreamFraming's once-per-object-per-check dedup.
func TestRecordStreamFramingDedup(t *testing.T) {
	d := &Reader{}
	d.recordStreamFraming(1, Checks.Structure.StreamKeywordEOL, "msg")
	d.recordStreamFraming(1, Checks.Structure.StreamKeywordEOL, "msg again")
	if len(d.parseDiagnostics) != 1 {
		t.Errorf("expected dedup, got %d diagnostics", len(d.parseDiagnostics))
	}
}

// TestNewRawReaderAndSeedResolvedGraph covers the two white-box test seams
// that build/populate a Reader without a real parse pipeline.
func TestNewRawReaderAndSeedResolvedGraph(t *testing.T) {
	trailer := PDFDict{Entries: map[string]PDFValue{"Root": PDFRef{ObjNum: 1}}}
	d := NewRawReader(nil, trailer, 100, 50)
	if d.size != 100 || d.xrefOffset != 50 {
		t.Errorf("NewRawReader: size=%d xrefOffset=%d, want 100, 50", d.size, d.xrefOffset)
	}
	if !EqualPDFValue(d.trailer.Entries["Root"], trailer.Entries["Root"]) {
		t.Error("NewRawReader did not set trailer")
	}

	graph := PDFDict{Entries: map[string]PDFValue{"Type": PDFName{Value: "Catalog"}}}
	d.SeedResolvedGraph(graph, map[int]PDFValue{1: PDFInteger(7)})
	if !d.graphResolved {
		t.Error("SeedResolvedGraph should set graphResolved")
	}
	got, err := d.ResolveGraph()
	if err != nil || !EqualPDFValue(got, graph) {
		t.Errorf("ResolveGraph after seeding = %v, %v; want %v", got, err, graph)
	}
}

// buildMetadataStreamObj returns a Metadata stream object body with a correct
// /Length for xmp, for use with writeMinimalPDF.
func buildMetadataStreamObj(xmp string) string {
	return fmt.Sprintf("<< /Type /Metadata /Subtype /XML /Length %d >>\nstream\n%s\nendstream", len(xmp), xmp)
}

// TestClaimedConformanceAndRawXMP covers RawXMP's non-stream-Metadata error and
// ClaimedConformance's part-only and part+conformance success paths.
func TestClaimedConformanceAndRawXMP(t *testing.T) {
	t.Run("non-stream metadata", func(t *testing.T) {
		path := writeMinimalPDF(t,
			[]string{
				"<< /Type /Catalog /Pages 2 0 R /Metadata 3 0 R >>",
				"<< /Type /Pages /Count 1 /Kids [] >>",
				"<< /Type /Metadata /Subtype /XML >>", // no stream: not a metadata stream
			},
			"<< /Size 4 /Root 1 0 R >>",
		)
		doc, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer doc.Close()

		if _, _, err := doc.RawXMP(); !errors.Is(err, ErrXMPMetadataNotStream) {
			t.Errorf("RawXMP error = %v, want ErrXMPMetadataNotStream", err)
		}
	})

	t.Run("part only", func(t *testing.T) {
		xmp := `<x><pdfaid:part>1</pdfaid:part></x>`
		path := writeMinimalPDF(t,
			[]string{
				"<< /Type /Catalog /Pages 2 0 R /Metadata 3 0 R >>",
				"<< /Type /Pages /Count 1 /Kids [] >>",
				buildMetadataStreamObj(xmp),
			},
			"<< /Size 4 /Root 1 0 R >>",
		)
		doc, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer doc.Close()

		part, conf, err := doc.ClaimedConformance()
		if part != "1" || conf != "" || err == nil {
			t.Errorf("ClaimedConformance = %q, %q, %v; want (1, \"\", err)", part, conf, err)
		}
	})

	t.Run("part and conformance", func(t *testing.T) {
		xmp := `<x><pdfaid:part>1</pdfaid:part><pdfaid:conformance>B</pdfaid:conformance></x>`
		path := writeMinimalPDF(t,
			[]string{
				"<< /Type /Catalog /Pages 2 0 R /Metadata 3 0 R >>",
				"<< /Type /Pages /Count 1 /Kids [] >>",
				buildMetadataStreamObj(xmp),
			},
			"<< /Size 4 /Root 1 0 R >>",
		)
		doc, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer doc.Close()

		part, conf, err := doc.ClaimedConformance()
		if part != "1" || conf != "B" || err != nil {
			t.Errorf("ClaimedConformance = %q, %q, %v; want (1, B, nil)", part, conf, err)
		}
	})
}

// TestRecoverTrailerFromXRefStream covers the resolve-error continue branch,
// the non-dict continue branch, the matching /Type /XRef success path, and
// the exhausted-loop error when no such object exists.
func TestRecoverTrailerFromXRefStream(t *testing.T) {
	r := &Reader{
		data:      []byte{}, // any offset lookup fails to parse -> resolve error, continue
		xrefTable: map[int]int64{1: 0, 2: 0, 3: 0},
		objCache: map[int]PDFValue{
			2: PDFInteger(7), // resolves fine but isn't a dict -> continue
			3: PDFDict{Entries: map[string]PDFValue{
				"Type": PDFName{Value: "XRef"},
				"Root": PDFRef{ObjNum: 9},
			}},
		},
	}
	dict, err := r.recoverTrailerFromXRefStream()
	if err != nil {
		t.Fatalf("recoverTrailerFromXRefStream: %v", err)
	}
	if !EqualPDFValue(dict.Entries["Root"], PDFRef{ObjNum: 9}) {
		t.Errorf("Root = %v, want 9 0 R", dict.Entries["Root"])
	}

	r2 := &Reader{xrefTable: map[int]int64{1: 0}, objCache: map[int]PDFValue{1: PDFDict{}}}
	if _, err := r2.recoverTrailerFromXRefStream(); err == nil {
		t.Error("expected error when no /Type /XRef object exists")
	}
}

// TestFollowXRefPrevChainMergesOlderRevision builds two classic xref sections
// back to back, simulating an incrementally-updated PDF: the current
// (already-parsed) revision only lists object 1, and its trailer's /Prev
// points at an older revision that also defines object 2. Following the chain
// must add object 2 without disturbing object 1's newer offset.
func TestFollowXRefPrevChainMergesOlderRevision(t *testing.T) {
	older := "xref\n0 3\n0000000000 65535 f \n0000000111 00000 n \n0000000133 00000 n \ntrailer\n<< /Size 3 /Root 1 0 R >>\n"
	olderOffset := 0
	newer := "xref\n0 2\n0000000000 65535 f \n0000000222 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R /Prev %d >>\n"
	newerOffset := len(older)
	data := []byte(older + fmt.Sprintf(newer, olderOffset))

	r := &Reader{
		data:       data,
		file:       bytesFileSource{bytes.NewReader(data)},
		xrefTable:  map[int]int64{1: 222},
		xrefOffset: int64(newerOffset),
		trailer: PDFDict{Entries: map[string]PDFValue{
			"Root": PDFRef{ObjNum: 1}, "Prev": PDFInteger(olderOffset),
		}},
	}
	r.followXRefPrevChain()

	if r.xrefTable[1] != 222 {
		t.Errorf("xrefTable[1] = %d, want 222 (newer revision must win)", r.xrefTable[1])
	}
	if r.xrefTable[2] != 133 {
		t.Errorf("xrefTable[2] = %d, want 133 (merged from older revision)", r.xrefTable[2])
	}
}

// TestFollowXRefPrevChainCyclicPrevStops covers the visited-offset guard: a
// /Prev pointing back at the already-processed current offset must not loop.
func TestFollowXRefPrevChainCyclicPrevStops(t *testing.T) {
	data := []byte("xref\n0 2\n0000000000 65535 f \n0000000010 00000 n \ntrailer\n<< /Size 2 >>\n")
	r := &Reader{
		data:       data,
		file:       bytesFileSource{bytes.NewReader(data)},
		xrefTable:  map[int]int64{1: 10},
		xrefOffset: 0,
		trailer: PDFDict{Entries: map[string]PDFValue{
			"Prev": PDFInteger(0), // points at d.xrefOffset itself
		}},
	}
	done := make(chan struct{})
	go func() {
		r.followXRefPrevChain()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("followXRefPrevChain did not return: cyclic /Prev not caught")
	}
}

// TestFindAndLoadFirstPageTrailer scans a file with three xref sections and
// confirms firstPageTrailer is set to the first one bearing /Root, ignoring
// an earlier Root-less section and a later one.
func TestFindAndLoadFirstPageTrailer(t *testing.T) {
	noRoot := "xref\n0 2\n0000000000 65535 f \n0000000010 00000 n \ntrailer\n<< /Size 2 >>\n"
	firstRoot := "xref\n0 2\n0000000000 65535 f \n0000000020 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\n"
	laterRoot := "xref\n0 2\n0000000000 65535 f \n0000000030 00000 n \ntrailer\n<< /Size 2 /Root 2 0 R >>\n"
	data := []byte(noRoot + firstRoot + laterRoot)

	r := &Reader{data: data, file: bytesFileSource{bytes.NewReader(data)}, xrefTable: map[int]int64{}}
	r.findAndLoadFirstPageTrailer()

	if !EqualPDFValue(r.firstPageTrailer.Entries["Root"], PDFRef{ObjNum: 1}) {
		t.Errorf("firstPageTrailer.Root = %v, want 1 0 R", r.firstPageTrailer.Entries["Root"])
	}
}

// TestEffectiveTrailer covers both branches: the linearized firstPageTrailer
// override, and falling back to the main trailer when none is set.
func TestEffectiveTrailer(t *testing.T) {
	main := PDFDict{Entries: map[string]PDFValue{"Root": PDFRef{ObjNum: 1}}}
	r := &Reader{trailer: main}
	if got := r.EffectiveTrailer(); !EqualPDFValue(got, main) {
		t.Errorf("EffectiveTrailer() = %v, want main trailer %v", got, main)
	}

	first := PDFDict{Entries: map[string]PDFValue{"Root": PDFRef{ObjNum: 2}}}
	r.firstPageTrailer = first
	if got := r.EffectiveTrailer(); !EqualPDFValue(got, first) {
		t.Errorf("EffectiveTrailer() = %v, want firstPageTrailer %v", got, first)
	}
}

// TestBuildPageIndex covers the nil-Root error, nil-Pages error, and a
// successful walk through nested Kids assigning 1-based page numbers by
// object reference.
func TestBuildPageIndex(t *testing.T) {
	r := &Reader{}

	if _, err := r.BuildPageIndex(PDFDict{Entries: map[string]PDFValue{}}); err == nil {
		t.Error("expected error for nil Root")
	}

	rootNoPages := PDFDict{Entries: map[string]PDFValue{"Root": PDFDict{Entries: map[string]PDFValue{}}}}
	if _, err := r.BuildPageIndex(rootNoPages); err == nil {
		t.Error("expected error for nil Pages")
	}

	page1 := PDFDict{Entries: map[string]PDFValue{"Type": PDFName{Value: "Page"}, "_ref": PDFRef{ObjNum: 10}}}
	page2 := PDFDict{Entries: map[string]PDFValue{"Type": PDFName{Value: "Page"}, "_ref": PDFRef{ObjNum: 20}}}
	kids := PDFDict{Entries: map[string]PDFValue{"Kids": PDFArray{page1, page2}}}
	graph := PDFDict{Entries: map[string]PDFValue{
		"Root": PDFDict{Entries: map[string]PDFValue{"Pages": kids}},
	}}
	index, err := r.BuildPageIndex(graph)
	if err != nil {
		t.Fatalf("BuildPageIndex: %v", err)
	}
	if index[10] != 1 || index[20] != 2 {
		t.Errorf("index = %v, want {10:1, 20:2}", index)
	}
}

// TestDecodeStreamCached covers the no-key fallback, cache miss + store, and
// cache hit paths of the (unlocked) sync variant.
func TestDecodeStreamCached(t *testing.T) {
	d := &Reader{}
	if _, err := d.DecodeStreamCached(PDFDict{}); err == nil {
		t.Error("expected error decoding a non-stream dict")
	}

	streamDict := PDFDict{HasStream: true, RawStream: []byte("sync-cached")}
	got1, err := d.DecodeStreamCached(streamDict)
	if err != nil || string(got1) != "sync-cached" {
		t.Fatalf("first decode = %q, %v", got1, err)
	}
	if len(d.decodedCache) != 1 {
		t.Errorf("expected one cache entry, got %d", len(d.decodedCache))
	}
	got2, err := d.DecodeStreamCached(streamDict)
	if err != nil || string(got2) != "sync-cached" {
		t.Fatalf("second (cache-hit) decode = %q, %v", got2, err)
	}
}

// TestScanStreamCachedNoKeyAndHit covers ScanStreamCached's no-cache-key
// decode-and-tokenize path and its cache-hit path.
func TestScanStreamCachedNoKeyAndHit(t *testing.T) {
	d := &Reader{}
	ops, err := d.ScanStreamCached(PDFDict{HasStream: true, RawStream: nil})
	if err != nil {
		t.Fatalf("ScanStreamCached (no key): %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("ops = %v, want none for empty stream", ops)
	}

	streamDict := PDFDict{HasStream: true, RawStream: []byte("1 2 Tj")}
	ops1, err := d.ScanStreamCached(streamDict)
	if err != nil || len(ops1) != 1 || ops1[0].Op != "Tj" {
		t.Fatalf("ScanStreamCached (first) = %+v, %v", ops1, err)
	}
	if len(d.scanCache) != 1 {
		t.Errorf("expected one scanCache entry, got %d", len(d.scanCache))
	}
	ops2, err := d.ScanStreamCached(streamDict)
	if err != nil || len(ops2) != 1 || ops2[0].Op != "Tj" {
		t.Fatalf("ScanStreamCached (cache-hit) = %+v, %v", ops2, err)
	}
}
