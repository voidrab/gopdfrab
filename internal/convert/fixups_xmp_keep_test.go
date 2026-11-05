package convert

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// xmpPacketWith wraps body, the content of rdf:RDF, in a packet.
func xmpPacketWith(header, body string) string {
	return header + `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		body + `</rdf:RDF></x:xmpmeta><?xpacket end="r"?>`
}

const plainXPacketHeader = "<?xpacket begin=\"\xEF\xBB\xBF\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>"

const dcTitleDescription = `<rdf:Description rdf:about="uuid:1" xmlns:dc="http://purl.org/dc/elements/1.1/">` +
	`<dc:title><rdf:Alt><rdf:li xml:lang="x-default">Kept title</rdf:li></rdf:Alt></dc:title></rdf:Description>`

func metadataStream(packet, filter string) pdf.PDFDict {
	m := pdf.NewStreamDict(pdf.NewDict(), []byte(packet))
	if filter != "" {
		m.Entries.Set("Filter", pdf.PDFName{Value: filter})
	}
	return m
}

func TestIdentifyXMP(t *testing.T) {
	spec := *a2bSpec
	cases := []struct {
		name          string
		packet        string
		want, notWant []string
	}{
		{
			name: "element identification replaced",
			packet: xmpPacketWith(plainXPacketHeader, dcTitleDescription+
				`<rdf:Description rdf:about="uuid:1" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"><pdfaid:part>1</pdfaid:part><pdfaid:conformance>A</pdfaid:conformance></rdf:Description>`),
			want:    []string{"Kept title", "<pdfaid:part>2</pdfaid:part>", "<pdfaid:conformance>B</pdfaid:conformance>", `rdf:about="uuid:1"`},
			notWant: []string{"<pdfaid:part>1<", "<pdfaid:conformance>A<"},
		},
		{
			name: "attribute identification replaced",
			packet: xmpPacketWith(plainXPacketHeader,
				`<rdf:Description rdf:about="" xmlns:id="http://www.aiim.org/pdfa/ns/id/" id:part="1" id:conformance='B'/>`),
			want:    []string{"<pdfaid:part>2</pdfaid:part>"},
			notWant: []string{`id:part="1"`, "id:conformance="},
		},
		{
			name:    "forbidden header attributes removed",
			packet:  xmpPacketWith("<?xpacket begin=\"\xEF\xBB\xBF\" id=\"W5M0MpCehiHzreSzNTczkc9d\" bytes=\"12\" encoding='UTF-8'?>", dcTitleDescription),
			want:    []string{`id="W5M0MpCehiHzreSzNTczkc9d"?>`, "Kept title"},
			notWant: []string{"bytes=", "encoding="},
		},
		{
			name:   "about value escaped",
			packet: xmpPacketWith(plainXPacketHeader, `<rdf:Description rdf:about="a&amp;b"/>`),
			want:   []string{`:about="a&amp;b" xmlns:pdfaid`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := identifyXMP([]byte(tc.packet), spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(string(got), w) {
					t.Errorf("result lacks %q:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(string(got), w) {
					t.Errorf("result still has %q:\n%s", w, got)
				}
			}
		})
	}
}

func TestIdentifyXMPRejectsWhatItCannotEdit(t *testing.T) {
	spec := *a2bSpec
	for name, packet := range map[string]string{
		"malformed":             `<a><b></a>`,
		"no RDF":                `<x:xmpmeta xmlns:x="adobe:ns:meta/"/>`,
		"unprefixed RDF":        `<RDF xmlns="http://www.w3.org/1999/02/22-rdf-syntax-ns#"></RDF>`,
		"unterminated property": `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns:p="http://www.aiim.org/pdfa/ns/id/"><p:part>`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := identifyXMP([]byte(packet), spec); err == nil {
				t.Errorf("identifyXMP = %q, want an error", got)
			}
		})
	}
}

func TestKeepExistingXMP(t *testing.T) {
	spec := *a2bSpec
	good := xmpPacketWith(plainXPacketHeader, dcTitleDescription)
	undeclared := xmpPacketWith(plainXPacketHeader,
		`<rdf:Description rdf:about="" xmlns:acme="http://acme.example/ns/"><acme:x>1</acme:x></rdf:Description>`)
	a1b, _ := pdfa.For(pdf.A1B)

	cases := []struct {
		name string
		meta pdf.PDFDict
		spec pdfa.Spec
		want bool
	}{
		{"valid packet", metadataStream(good, ""), spec, true},
		{"Part 1 builds its own", metadataStream(good, ""), a1b, false},
		{"not a stream", pdf.NewPDFDict(), spec, false},
		{"undecodable", metadataStream("not hex", "ASCIIHexDecode"), spec, false},
		{"not XML", metadataStream("not xml", ""), spec, false},
		{"undeclared property", metadataStream(undeclared, ""), spec, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepExistingXMP(tc.meta, tc.spec); got != tc.want {
				t.Fatalf("keepExistingXMP = %v, want %v", got, tc.want)
			}
			if !tc.want {
				return
			}
			if !strings.Contains(string(tc.meta.Stream()), "Kept title") {
				t.Errorf("packet lost its properties:\n%s", tc.meta.Stream())
			}
			if tc.meta.Entries.Get("Type") != (pdf.PDFName{Value: "Metadata"}) || tc.meta.Entries.Get("Subtype") != (pdf.PDFName{Value: "XML"}) {
				t.Error("Type and Subtype not set")
			}
		})
	}
}

// TestRegenerateXMPKeepsConformingPacket checks the wiring: a filtered valid
// packet stays, unfiltered, and an unusable one is rebuilt.
func TestRegenerateXMPKeepsConformingPacket(t *testing.T) {
	spec := *a2bSpec
	run := func(meta pdf.PDFDict) string {
		root := pdf.NewPDFDict()
		root.Entries.Set("Metadata", meta)
		trailer := pdf.NewPDFDict()
		trailer.Entries.Set("Root", root)
		if err := regenerateXMP(&trailer, nil, spec); err != nil {
			t.Fatal(err)
		}
		got := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Metadata").(pdf.PDFDict)
		if got.Entries.Get("Filter") != nil {
			t.Error("the packet is still filtered")
		}
		return string(got.Stream())
	}
	hex := strings.Builder{}
	const digits = "0123456789abcdef"
	for _, b := range []byte(xmpPacketWith(plainXPacketHeader, dcTitleDescription)) {
		hex.WriteByte(digits[b>>4])
		hex.WriteByte(digits[b&15])
	}
	if got := run(metadataStream(hex.String()+">", "ASCIIHexDecode")); !strings.Contains(got, "Kept title") {
		t.Errorf("conforming packet not kept:\n%s", got)
	}
	if got := run(metadataStream("not xml", "")); !strings.Contains(got, "<pdfaid:part>2</pdfaid:part>") {
		t.Errorf("unusable packet not rebuilt:\n%s", got)
	}
}

// TestStripEmbeddedMetadataKeepsConformingUnderPart2 pins each branch of the
// object metadata decision.
func TestStripEmbeddedMetadataKeepsConformingUnderPart2(t *testing.T) {
	good := metadataStream(xmpPacketWith(plainXPacketHeader, dcTitleDescription), "")
	bad := metadataStream(xmpPacketWith(plainXPacketHeader,
		`<rdf:Description rdf:about="" xmlns:acme="http://acme.example/ns/"><acme:x>1</acme:x></rdf:Description>`), "")
	cases := []struct {
		name string
		meta pdf.PDFDict
		spec pdf.LevelType
		keep bool
	}{
		{"conforming, Part 2", good, pdf.A2B, true},
		{"conforming, Part 1", good, pdf.A1B, false},
		{"undeclared property", bad, pdf.A2B, false},
		{"not a stream", pdf.NewPDFDict(), pdf.A2B, false},
		{"undecodable", metadataStream("not hex", "ASCIIHexDecode"), pdf.A2B, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, _ := pdfa.For(tc.spec)
			catalogMeta := metadataStream(xmpPacketWith(plainXPacketHeader, ""), "")
			root := pdf.NewPDFDict()
			root.Entries.Set("Metadata", catalogMeta)
			trailer := pdf.NewPDFDict()
			trailer.Entries.Set("Root", root)
			image := pdf.NewPDFDict()
			image.Entries.Set("Metadata", tc.meta)

			visit := stripEmbeddedMetadataVisitor(&trailer, nil, spec)
			visit(image)
			visit(root)
			if got := image.Entries.Get("Metadata") != nil; got != tc.keep {
				t.Errorf("metadata kept = %v, want %v", got, tc.keep)
			}
			if root.Entries.Get("Metadata") == nil {
				t.Error("the catalog's metadata was removed")
			}
		})
	}
}
