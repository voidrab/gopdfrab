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

	if err := stripEmbeddedMetadata(&trailer, nil); err != nil {
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

// TestNormalizePDFDate covers the already-"D:"-prefixed pass-through, the
// ISO-8601 fallback at several precisions (including a "Z" and a "+HH:MM"
// offset), and the no-match rejection.
func TestNormalizePDFDate(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"D:20080513090000+02'00'", "D:20080513090000+02'00'", true}, // already prefixed
		{"2008-05-13T09:00:00+02:00", "D:20080513090000+02'00'", true},
		{"2008-05-13T09:00:00Z", "D:20080513090000Z", true},
		{"2008", "D:2008", true},               // year only
		{"not a date at all", "", false},       // no match
		{"  D:20080513  ", "D:20080513", true}, // whitespace trimmed around a D: value
	}
	for _, c := range cases {
		got, ok := normalizePDFDate(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("normalizePDFDate(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestNormalizeInfoDict covers each per-key branch: absent Info is a no-op,
// a nil/non-string text entry is dropped, an empty decoded string is
// dropped, Author is trimmed, an invalid /Trapped is dropped, and
// CreationDate/ModDate are normalized or dropped if unparseable.
func TestNormalizeInfoDict(t *testing.T) {
	// Absent Info: must not panic and must not add one.
	noInfo := pdf.NewPDFDict()
	normalizeInfoDict(&noInfo)
	if noInfo.Entries["Info"] != nil {
		t.Error("normalizeInfoDict added an Info dict where none existed")
	}

	info := pdf.NewPDFDict()
	info.Entries["Title"] = pdf.PDFInteger(1) // wrong type: dropped
	info.Entries["Subject"] = pdf.PDFString{Value: ""}
	info.Entries["Author"] = pdf.PDFString{Value: "  Jane Doe  "}
	info.Entries["Trapped"] = pdf.PDFInteger(1) // not a name: dropped
	info.Entries["CreationDate"] = pdf.PDFString{Value: "2008-05-13T09:00:00Z"}
	info.Entries["ModDate"] = pdf.PDFInteger(1) // not a string: dropped
	trailer := pdf.NewPDFDict()
	trailer.Entries["Info"] = info

	normalizeInfoDict(&trailer)
	got := trailer.Entries["Info"].(pdf.PDFDict)

	if got.Entries["Title"] != nil {
		t.Error("wrong-typed Title not dropped")
	}
	if got.Entries["Subject"] != nil {
		t.Error("empty-string Subject not dropped")
	}
	if s, ok := got.Entries["Author"].(pdf.PDFString); !ok || s.Value != "Jane Doe" {
		t.Errorf("Author = %v, want trimmed \"Jane Doe\"", got.Entries["Author"])
	}
	if got.Entries["Trapped"] != nil {
		t.Error("non-name Trapped not dropped")
	}
	if s, ok := got.Entries["CreationDate"].(pdf.PDFString); !ok || s.Value != "D:20080513090000Z" {
		t.Errorf("CreationDate = %v, want normalized D:20080513090000Z", got.Entries["CreationDate"])
	}
	if got.Entries["ModDate"] != nil {
		t.Error("non-string ModDate not dropped")
	}
}

func TestXMLEscapeAttr(t *testing.T) {
	got := xmlEscapeAttr("a&b<c>d\"e\x01f\tg")
	want := "a&amp;b&lt;c&gt;d&quot;ef\tg" // control char x01 dropped, tab kept
	if got != want {
		t.Errorf("xmlEscapeAttr = %q, want %q", got, want)
	}
}

func TestXMLEscapeText(t *testing.T) {
	got := xmlEscapeText("a&b<c>d\x01e\tf")
	want := "a&amp;b&lt;c&gt;de\tf" // '"' left as-is, control x01 dropped, tab kept
	if got != want {
		t.Errorf("xmlEscapeText = %q, want %q", got, want)
	}
}
