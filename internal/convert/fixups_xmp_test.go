package convert

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestStripEmbeddedMetadataRemovesNonCatalog confirms that stripEmbeddedMetadata
// deletes /Metadata from image/XObject dicts while keeping the catalog's.
func TestStripEmbeddedMetadataRemovesNonCatalog(t *testing.T) {
	// Catalog metadata (must be kept).
	catalogMeta := pdf.NewPDFDict()
	catalogMeta.HasStream = true
	catalogMeta.Entries["Type"] = pdf.PDFName{Value: "Metadata"}

	root := pdf.NewPDFDict()
	root.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	root.Entries["Metadata"] = catalogMeta

	// Image XObject with its own embedded metadata (must be stripped).
	imageMeta := pdf.NewPDFDict()
	imageMeta.HasStream = true
	imageMeta.Entries["Type"] = pdf.PDFName{Value: "Metadata"}

	image := pdf.NewPDFDict()
	image.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	image.Entries["Metadata"] = imageMeta

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root
	trailer.Entries["Image"] = image // reachable from trailer for the walk

	if err := stripEmbeddedMetadata(&trailer); err != nil {
		t.Fatalf("stripEmbeddedMetadata: %v", err)
	}

	rootAfter := trailer.Entries["Root"].(pdf.PDFDict)
	if rootAfter.Entries["Metadata"] == nil {
		t.Errorf("catalog /Metadata was removed but should be kept")
	}
	imageAfter := trailer.Entries["Image"].(pdf.PDFDict)
	if imageAfter.Entries["Metadata"] != nil {
		t.Errorf("image /Metadata still present after stripEmbeddedMetadata")
	}
}

// TestInfoStringKeepsDecodedText confirms infoString passes through the
// decoded string bytes PDFString holds (parens need no special handling).
func TestInfoStringKeepsDecodedText(t *testing.T) {
	info := pdf.NewPDFDict()
	info.Entries["Title"] = pdf.PDFString{Value: "Title (Edition)"}

	got := infoString(info, "Title")
	if got != "Title (Edition)" {
		t.Errorf("infoString = %q, want %q", got, "Title (Edition)")
	}
}

// TestInfoStringDecodesPDFDocEncoding confirms that bytes in the 0xA0-0xFF
// range are decoded as PDFDocEncoding (= Latin-1), producing valid Unicode.
func TestInfoStringDecodesPDFDocEncoding(t *testing.T) {
	info := pdf.NewPDFDict()
	// 0xE4 = ä in PDFDocEncoding/Latin-1.
	info.Entries["Author"] = pdf.PDFString{Value: string([]byte{0xE4})}

	got := infoString(info, "Author")
	if !strings.Contains(got, "ä") {
		t.Errorf("infoString = %q, want string containing ä", got)
	}
}

// TestBuildXMPPacketKeepsParens confirms buildXMPPacket carries the decoded
// Info value into the XML output unchanged.
func TestBuildXMPPacketKeepsParens(t *testing.T) {
	info := pdf.NewPDFDict()
	info.Entries["Title"] = pdf.PDFString{Value: "Doc (v2)"}

	xmp := buildXMPPacket(info)
	if !strings.Contains(xmp, "(v2)") {
		t.Errorf("buildXMPPacket XMP does not contain (v2): %s", xmp)
	}
}
