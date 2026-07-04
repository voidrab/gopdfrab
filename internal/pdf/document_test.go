package pdf

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
