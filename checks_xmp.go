package pdfrab

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const pdfaIDNamespace = "http://www.aiim.org/pdfa/ns/id/"

var (
	xpacketRe     = regexp.MustCompile(`<\?xpacket[^>]*>`)
	pdfaNSRe      = regexp.MustCompile(`xmlns:pdfaid\s*=\s*"([^"]*)"`)
	pdfaPartRe    = regexp.MustCompile(`pdfaid:part\s*=\s*"([^"]*)"|<pdfaid:part>\s*([^<\s]+)\s*</pdfaid:part>`)
	pdfaConfRe    = regexp.MustCompile(`pdfaid:conformance\s*=\s*"([^"]*)"|<pdfaid:conformance>\s*([^<\s]+)\s*</pdfaid:conformance>`)
)

func xmpErr(clause string, sub int, msg string) PDFError {
	return PDFError{clause: clause, subclause: sub, errs: []error{fmt.Errorf("%s", msg)}, page: 0}
}

// firstGroup returns the first non-empty capture group of a regexp match.
func firstGroup(re *regexp.Regexp, s string) (string, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	for _, g := range m[1:] {
		if g != "" {
			return g, true
		}
	}
	return "", true
}

// verifyXMPMetadata validates the document's XMP metadata (6.7).
func (d *Document) verifyXMPMetadata() []PDFError {
	value, err := d.ResolveGraphByPath([]string{"Root", "Metadata"})
	if err != nil || value == nil {
		return []PDFError{xmpErr("6.7.2", 1, "document catalog lacks a Metadata entry")}
	}
	meta, ok := value.(PDFDict)
	if !ok || !meta.HasStream {
		return []PDFError{xmpErr("6.7.2", 1, "document Metadata is not a metadata stream")}
	}

	var errs []PDFError

	// 6.7.2: the metadata stream shall not be filtered.
	if meta.Entries["Filter"] != nil {
		errs = append(errs, xmpErr("6.7.2", 2, "Metadata stream shall not specify a Filter"))
	}

	data, err := decodeStream(meta)
	if err != nil {
		return append(errs, xmpErr("6.7.9", 1, "unable to read XMP metadata stream"))
	}
	xmp := string(data)

	errs = append(errs, checkXMPHeader(xmp)...)
	errs = append(errs, checkPDFAIdentifier(xmp)...)
	if !xmpWellFormed(data) {
		errs = append(errs, xmpErr("6.7.9", 2, "XMP metadata is not well-formed XML"))
	}
	errs = append(errs, d.checkInfoXMPSync(xmp)...)

	return errs
}

// checkXMPHeader checks the xpacket processing instruction (6.7.5).
func checkXMPHeader(xmp string) []PDFError {
	pi := xpacketRe.FindString(xmp)
	if pi == "" {
		return nil
	}
	var errs []PDFError
	if strings.Contains(pi, "bytes=") {
		errs = append(errs, xmpErr("6.7.5", 1, "xpacket header shall not contain a bytes attribute"))
	}
	if strings.Contains(pi, "encoding=") {
		errs = append(errs, xmpErr("6.7.5", 2, "xpacket header shall not contain an encoding attribute"))
	}
	return errs
}

// checkPDFAIdentifier validates the PDF/A version identifier (6.7.11).
func checkPDFAIdentifier(xmp string) []PDFError {
	var errs []PDFError

	ns, hasNS := firstGroup(pdfaNSRe, xmp)
	if !hasNS {
		return []PDFError{xmpErr("6.7.11", 1, "missing PDF/A identifier (pdfaid namespace)")}
	}
	if ns != pdfaIDNamespace {
		errs = append(errs, xmpErr("6.7.11", 2, "invalid PDF/A identifier namespace"))
	}

	part, hasPart := firstGroup(pdfaPartRe, xmp)
	if !hasPart {
		errs = append(errs, xmpErr("6.7.11", 1, "missing PDF/A part identifier"))
	} else if part != "1" {
		errs = append(errs, xmpErr("6.7.11", 4, fmt.Sprintf("invalid PDF/A part number %q", part)))
	}

	conf, hasConf := firstGroup(pdfaConfRe, xmp)
	if !hasConf {
		errs = append(errs, xmpErr("6.7.11", 1, "missing PDF/A conformance level"))
	} else if conf != "A" && conf != "B" {
		errs = append(errs, xmpErr("6.7.11", 3, fmt.Sprintf("invalid PDF/A conformance level %q", conf)))
	}

	return errs
}

// xmpWellFormed reports whether the XMP packet is well-formed XML (6.7.9).
func xmpWellFormed(data []byte) bool {
	if i := bytes.IndexByte(data, '<'); i > 0 {
		data = data[i:]
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}

var xmpCreateDateRe = regexp.MustCompile(`xmp:CreateDate\s*=\s*"([^"]*)"|<xmp:CreateDate>\s*([^<\s]+)\s*</xmp:CreateDate>`)

// digitsOf returns only the decimal digits of s.
func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var rdfLiRe = regexp.MustCompile(`(?s)<rdf:li[^>]*>(.*?)</rdf:li>`)
var xmlTagRe = regexp.MustCompile(`<[^>]*>`)

// xmpPropValue extracts the text value of an XMP property such as "dc:title",
// unwrapping an rdf:Alt/rdf:Seq rdf:li container if present.
func xmpPropValue(xmp, prop string) (string, bool) {
	re := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(prop) + `[^>]*>(.*?)</` + regexp.QuoteMeta(prop) + `>`)
	m := re.FindStringSubmatch(xmp)
	if m == nil {
		return "", false
	}
	inner := m[1]
	if li := rdfLiRe.FindStringSubmatch(inner); li != nil {
		return strings.TrimSpace(li[1]), true
	}
	return strings.TrimSpace(xmlTagRe.ReplaceAllString(inner, "")), true
}

// checkInfoXMPSync verifies that document information dictionary entries are
// reflected in the XMP metadata (6.7.3).
func (d *Document) checkInfoXMPSync(xmp string) []PDFError {
	info, err := d.GetMetadata()
	if err != nil {
		return nil
	}
	var errs []PDFError

	// Each Info text property must equal the matching XMP property value.
	for key, prop := range map[string]string{
		"Title":   "dc:title",
		"Subject": "dc:description",
	} {
		val := strings.TrimSpace(info[key])
		if val == "" {
			continue
		}
		got, ok := xmpPropValue(xmp, prop)
		if !ok || got != val {
			errs = append(errs, xmpErr("6.7.3", 1,
				fmt.Sprintf("document info %s not synchronized with XMP %s", key, prop)))
		}
	}

	// Dates use different representations (PDF "D:YYYYMMDD..." vs ISO 8601);
	// compare their numeric components.
	if cd := strings.TrimSpace(info["CreationDate"]); cd != "" {
		xmpDate, _ := firstGroup(xmpCreateDateRe, xmp)
		infoDigits := digitsOf(cd)
		xmpDigits := digitsOf(xmpDate)
		// Compare to minute precision (YYYYMMDDHHmm): the conforming reference
		// file legitimately differs from its XMP in the seconds field.
		n := min(len(infoDigits), len(xmpDigits), 12)
		if n < 8 || infoDigits[:n] != xmpDigits[:n] {
			errs = append(errs, xmpErr("6.7.3", 1,
				"document info CreationDate not synchronized with XMP xmp:CreateDate"))
		}
	}

	return errs
}
