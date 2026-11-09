package convert

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// xpacketSizeAttrRe matches the attributes an xpacket header may not have.
var xpacketSizeAttrRe = regexp.MustCompile(`\s+(?:bytes|encoding)\s*=\s*(?:"[^"]*"|'[^']*')`)

// xpacketHeaderRe matches an xpacket header.
var xpacketHeaderRe = regexp.MustCompile(`<\?xpacket\s+begin[^?]*\?>`)

// keepExistingXMP replaces the stream of meta, the catalog's metadata stream,
// with its own packet, unfiltered, under the level's identification. It does
// so only if the level's rules accept the result, and reports whether it did.
// Only Part 2 checks a packet property by property, which is what makes a
// packet safe to keep; under Part 1 the caller builds a new packet.
func keepExistingXMP(meta pdf.PDFDict, spec pdfa.Spec) bool {
	if spec.Part < 2 || !meta.IsStream() {
		return false
	}
	data, err := pdf.DecodeStream(meta)
	if err != nil {
		return false
	}
	patched, err := identifyXMP(pdf.DecodeXMPEncoding(data), spec)
	if err != nil || !verify.DocumentXMPConforms(patched, spec) {
		return false
	}
	setXMPPacket(meta, patched)
	return true
}

// identifyXMP returns packet with its PDF/A identification replaced by the
// level's, and with the xpacket header attributes that PDF/A forbids removed.
// It edits the packet's bytes rather than re-serializing a parsed form, so
// everything it does not change stays exactly as written.
func identifyXMP(packet []byte, spec pdfa.Spec) ([]byte, error) {
	var edits []splice
	prefixes := map[string]bool{}
	about, inserted := "", false

	dec := xml.NewDecoder(bytes.NewReader(packet))
	for {
		start := int(dec.InputOffset())
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		end := int(dec.InputOffset())
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == pdf.PDFAIDNamespace {
				if err := dec.Skip(); err != nil {
					return nil, err
				}
				edits = append(edits, splice{start, int(dec.InputOffset()), ""})
				continue
			}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" && a.Value == pdf.PDFAIDNamespace {
					prefixes[a.Name.Local] = true
				}
				if about == "" && a.Name.Space == pdf.RDFNamespace && a.Name.Local == "about" {
					about = a.Value
				}
			}
			if slices.ContainsFunc(t.Attr, func(a xml.Attr) bool { return a.Name.Space == pdf.PDFAIDNamespace }) {
				edits = append(edits, splice{start, end, stripIdentificationAttrs(string(packet[start:end]), prefixes)})
			}
		case xml.EndElement:
			if t.Name.Space == pdf.RDFNamespace && t.Name.Local == "RDF" && !inserted {
				raw := strings.TrimSuffix(strings.TrimPrefix(string(packet[start:end]), "</"), ">")
				prefix, _, hasPrefix := strings.Cut(strings.TrimSpace(raw), ":")
				if !hasPrefix {
					return nil, errors.New("rdf:RDF has no namespace prefix")
				}
				edits = append(edits, splice{start, start, identificationDescription(prefix, about, spec)})
				inserted = true
			}
		}
	}
	if !inserted {
		return nil, errors.New("XMP packet has no rdf:RDF element")
	}

	return xpacketHeaderRe.ReplaceAllFunc(applySplices(packet, edits), func(h []byte) []byte {
		return xpacketSizeAttrRe.ReplaceAll(h, nil)
	}), nil
}

// stripIdentificationAttrs returns tag, a start tag, with every attribute in
// the PDF/A identification namespace removed. prefixes holds the prefixes
// bound to that namespace.
func stripIdentificationAttrs(tag string, prefixes map[string]bool) string {
	for prefix := range prefixes {
		re := regexp.MustCompile(`\s+` + regexp.QuoteMeta(prefix) + `:[\w.-]+\s*=\s*(?:"[^"]*"|'[^']*')`)
		tag = re.ReplaceAllString(tag, "")
	}
	return tag
}

// identificationDescription returns an rdf:Description, with the given prefix
// for the RDF namespace, that holds the level's PDF/A identification.
func identificationDescription(rdf, about string, spec pdfa.Spec) string {
	return "<" + rdf + `:Description ` + rdf + `:about="` + xmlEscapeAttr(about) + `" xmlns:pdfaid="` + pdf.PDFAIDNamespace + `">` + "\n" +
		"<pdfaid:part>" + spec.XMPPart + "</pdfaid:part>\n" +
		"<pdfaid:conformance>" + spec.XMPConformance + "</pdfaid:conformance>\n" +
		"</" + rdf + ":Description>\n"
}

// setXMPPacket makes meta the metadata stream holding packet, unfiltered. It
// does not call MarkStreamDirty, which makes the writer Flate-encode the
// stream, and PDF/A-1 forbids a filter on the metadata stream (6.7.2).
func setXMPPacket(meta pdf.PDFDict, packet []byte) {
	dropFilters(&meta)
	meta.Entries.Del("DP")
	meta.Entries.Set("Type", pdf.PDFName{Value: "Metadata"})
	meta.Entries.Set("Subtype", pdf.PDFName{Value: "XML"})
	meta.SetStream(packet)
}
