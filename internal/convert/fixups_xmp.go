package convert

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func init() {
	registerPreemptiveFixup(regenerateXMP)
	// Joins the shared pre-emptive walk. Its prepare captures the catalog's
	// metadata identity after regenerateXMP has installed the fresh packet,
	// since plain fixups run before the walk.
	registerPreemptiveVisitor(stripEmbeddedMetadataVisitor)
}

// regenerateXMP replaces the document's XMP metadata (Root/Metadata) with a
// freshly-built, minimal packet that satisfies clause 6.7: a correct PDF/A-1b
// identifier (pdfaid:part=1, pdfaid:conformance=B), no xpacket
// bytes/encoding attributes, an unfiltered stream, and -- for every Info
// dictionary entry that has a PDF/A-recognized XMP counterpart -- a
// synchronized dc:/xmp:/pdf: property in its required container shape (see
// checks_xmp.go's xmpNSSchemas/xmpLangAltProps). This is applied
// unconditionally and pre-emptively (see convert.go): it is far simpler and
// more reliable to regenerate a known-good packet from scratch than to patch
// an arbitrary existing one into compliance, and doing so resolves the large
// majority of clause 6.7's many sub-checks (and the Info/XMP sync checks,
// 6.7.3/6.1.5, since the packet is generated directly from Info) in one pass.
func regenerateXMP(trailer *pdf.PDFDict, _ *pdf.Reader) error {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return fmt.Errorf("regenerateXMP: Root is not a dictionary")
	}

	normalizeInfoDict(trailer)
	info, _ := trailer.Entries["Info"].(pdf.PDFDict)
	xmp := buildXMPPacket(info)

	meta, _ := root.Entries["Metadata"].(pdf.PDFDict)
	delete(meta.Entries, "Filter")
	delete(meta.Entries, "DecodeParms")
	delete(meta.Entries, "DP")
	if meta.Entries == nil {
		meta = pdf.NewPDFDict()
		meta.Entries["Type"] = pdf.PDFName{Value: "Metadata"}
		meta.Entries["Subtype"] = pdf.PDFName{Value: "XML"}
	}
	meta.HasStream = true
	meta.RawStream = []byte(xmp)
	// Deliberately not MarkStreamDirty: the XMP packet must stay unfiltered
	// (6.7.2 forbids a Filter on the Metadata stream), whereas MarkStreamDirty
	// tells the writer to Flate-encode and add /Filter /FlateDecode.

	root.Entries["Metadata"] = meta
	trailer.Entries["Root"] = root
	return nil
}

// stripEmbeddedMetadata removes /Metadata from every non-catalog object.
// PDF/A-1b requires exactly one metadata stream (Root/Metadata); non-catalog
// /Type /Metadata streams violate 6.7.5 when they lack an xpacket wrapper.
func stripEmbeddedMetadata(trailer *pdf.PDFDict, doc *pdf.Reader) error {
	if visit := stripEmbeddedMetadataVisitor(trailer, doc); visit != nil {
		walkDicts(*trailer, map[uintptr]bool{}, visit)
	}
	return nil
}

// stripEmbeddedMetadataVisitor is stripEmbeddedMetadata's per-dict visitor
// for the shared pre-emptive walk; nil when there is no catalog to protect.
func stripEmbeddedMetadataVisitor(trailer *pdf.PDFDict, _ *pdf.Reader) func(pdf.PDFDict) {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return nil
	}
	var catalogMetaPtr uintptr
	if meta, ok := root.Entries["Metadata"].(pdf.PDFDict); ok {
		catalogMetaPtr = pdf.ValuePointer(meta.Entries)
	}
	return func(d pdf.PDFDict) {
		meta, ok := d.Entries["Metadata"].(pdf.PDFDict)
		if !ok {
			return
		}
		// Keep the catalog's metadata; strip all others.
		if pdf.ValuePointer(meta.Entries) != catalogMetaPtr {
			delete(d.Entries, "Metadata")
		}
	}
}

// normalizeInfoDict coerces the Info dictionary's standard entries (Table
// 10.2, PDF Reference 4th ed.) to the types and formats clause 6.1.5
// requires -- text string for every field except Trapped (a name), and a
// "D:"-prefixed PDF date for CreationDate/ModDate -- deleting any entry that
// can't be coerced. regenerateXMP calls this first, before building the XMP
// packet from Info, so the packet never re-embeds the same non-conformance
// it would otherwise inherit (this is what closes 6.1.5/6.7.3 for a
// non-"D:" date, since checkInfoXMPSync flags that independently of XMP
// content).
func normalizeInfoDict(trailer *pdf.PDFDict) {
	info, ok := trailer.Entries["Info"].(pdf.PDFDict)
	if !ok {
		return
	}
	for _, key := range []string{"Title", "Author", "Subject", "Keywords", "Creator", "Producer"} {
		switch info.Entries[key].(type) {
		case nil:
		case pdf.PDFString, pdf.PDFHexString:
			if infoString(info, key) == "" {
				delete(info.Entries, key)
			}
		default:
			delete(info.Entries, key)
		}
	}
	// checkInfoXMPSync compares Author against the XMP dc:creator value with
	// the latter trimmed but not the former (checks_xmp.go); trimming here,
	// the single source both Metadata and the regenerated XMP read from,
	// keeps the two sides in sync instead of requiring matching surgery there.
	if s, ok := info.Entries["Author"].(pdf.PDFString); ok {
		info.Entries["Author"] = pdf.PDFString{Value: strings.TrimSpace(s.Value)}
	}
	if v, ok := info.Entries["Trapped"]; ok {
		if _, isName := v.(pdf.PDFName); !isName {
			delete(info.Entries, "Trapped")
		}
	}
	for _, key := range []string{"CreationDate", "ModDate"} {
		v, present := info.Entries[key]
		if !present {
			continue
		}
		s, ok := v.(pdf.PDFString)
		if !ok {
			delete(info.Entries, key)
			continue
		}
		if normalized, ok := normalizePDFDate(s.Value); ok {
			info.Entries[key] = pdf.PDFString{Value: normalized}
		} else {
			delete(info.Entries, key)
		}
	}
	// The object model (DocInfo's wildcard row) types every custom Info key as
	// a text string; a non-string custom value has no faithful coercion, so it
	// is dropped (real-world producers park integers/names here, e.g. /SPDF).
	for key, v := range info.Entries {
		switch key {
		case "_ref", "Title", "Author", "Subject", "Keywords", "Creator", "Producer",
			"Trapped", "CreationDate", "ModDate":
			continue
		}
		switch v.(type) {
		case nil, pdf.PDFString, pdf.PDFHexString:
		default:
			delete(info.Entries, key)
		}
	}
}

// isoDateRe loosely matches an ISO-8601-ish date/time, every component but
// the year optional, as a fallback for a CreationDate/ModDate that's valid
// data but missing the PDF "D:" prefix (e.g. "2008-05-13T09:00:00+02:00").
var isoDateRe = regexp.MustCompile(`^(\d{4})-?(\d{2})?-?(\d{2})?[T ]?(\d{2})?:?(\d{2})?:?(\d{2})?(Z|[+-]\d{2}:?\d{2})?$`)

// normalizePDFDate coerces s to a "D:YYYY[MMDDHHmmSSOHH'mm']" PDF date
// string (ISO 32000-1 7.9.4). ok is false if s isn't already "D:"-prefixed
// and doesn't match isoDateRe, in which case the caller should drop the
// Info entry rather than keep an unfixable non-conformant date.
func normalizePDFDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "D:") {
		return s, true
	}
	m := isoDateRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	out := "D:" + m[1]
	for _, g := range m[2:7] {
		if g == "" {
			return out, true
		}
		out += g
	}
	tz := m[7]
	switch {
	case tz == "":
	case tz == "Z":
		out += "Z"
	default:
		rest := strings.ReplaceAll(tz[1:], ":", "")
		if len(rest) >= 4 {
			out += string(tz[0]) + rest[:2] + "'" + rest[2:4] + "'"
		}
	}
	return out, true
}

// infoString reads and decodes a text value from the Info dictionary, using
// DecodeInfoTextString (literal escapes + PDFDocEncoding / UTF-16BE), so the
// result matches what Metadata returns and checkInfoXMPSync compares against.
func infoString(info pdf.PDFDict, key string) string {
	s := pdf.DecodeInfoTextString(info.Entries[key])
	if trimmed := strings.TrimSpace(s); trimmed == "" || trimmed == "null" {
		return ""
	}
	return s
}

// buildXMPPacket builds a minimal, schema-correct XMP packet synchronized
// with info's Title/Subject/Author/Creator/Producer/Keywords/CreationDate/
// ModDate (whichever are present), plus the mandatory PDF/A-1b identifier.
func buildXMPPacket(info pdf.PDFDict) string {
	title := infoString(info, "Title")
	subject := infoString(info, "Subject")
	author := infoString(info, "Author")
	creatorTool := infoString(info, "Creator")
	producer := infoString(info, "Producer")
	keywords := infoString(info, "Keywords")
	createDate, _ := pdfDateToXMP(infoString(info, "CreationDate"))
	modifyDate, _ := pdfDateToXMP(infoString(info, "ModDate"))

	var b strings.Builder
	b.WriteString("<?xpacket begin=\"\xEF\xBB\xBF\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n")
	b.WriteString(`<x:xmpmeta xmlns:x="adobe:ns:meta/">` + "\n")
	b.WriteString(`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` + "\n")

	b.WriteString(`<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">` + "\n")
	b.WriteString("<pdfaid:part>1</pdfaid:part>\n")
	b.WriteString("<pdfaid:conformance>B</pdfaid:conformance>\n")
	b.WriteString("</rdf:Description>\n")

	// dc:title/dc:description/dc:creator must be Alt/Seq containers, which
	// have no attribute form, so checkInfoXMPSync's matching comparisons
	// (Title/Subject: both sides trimmed by the checker; Author: only the
	// XMP side is trimmed) are the best fidelity available here.
	if title != "" || subject != "" || author != "" {
		b.WriteString(`<rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/">` + "\n")
		writeLangAltProp(&b, "dc:title", title)
		writeLangAltProp(&b, "dc:description", subject)
		if author != "" {
			fmt.Fprintf(&b, "<dc:creator><rdf:Seq><rdf:li>%s</rdf:li></rdf:Seq></dc:creator>\n", xmlEscapeText(author))
		}
		b.WriteString("</rdf:Description>\n")
	}

	// CreatorTool/CreateDate/ModifyDate/Producer/Keywords are written as
	// rdf:Description attributes rather than child elements: an
	// attribute's value is preserved exactly (xmpScalarValue's attribute
	// branch returns it unmodified), whereas the element-form branch trims
	// it -- and checkInfoXMPSync compares most of these against the Info
	// dictionary's raw, untrimmed value (see infoString).
	if creatorTool != "" || createDate != "" || modifyDate != "" {
		b.WriteString(`<rdf:Description rdf:about="" xmlns:xmp="http://ns.adobe.com/xap/1.0/"`)
		writeScalarAttr(&b, "xmp:CreatorTool", creatorTool)
		writeScalarAttr(&b, "xmp:CreateDate", createDate)
		writeScalarAttr(&b, "xmp:ModifyDate", modifyDate)
		b.WriteString("/>\n")
	}

	if producer != "" || keywords != "" {
		b.WriteString(`<rdf:Description rdf:about="" xmlns:pdf="http://ns.adobe.com/pdf/1.3/"`)
		writeScalarAttr(&b, "pdf:Producer", producer)
		writeScalarAttr(&b, "pdf:Keywords", keywords)
		b.WriteString("/>\n")
	}

	b.WriteString("</rdf:RDF>\n")
	b.WriteString("</x:xmpmeta>\n")
	b.WriteString(`<?xpacket end="w"?>`)
	return b.String()
}

// writeLangAltProp writes a LangAlt-container property (dc:title,
// dc:description), required by xmpLangAltProps to carry xml:lang on every
// rdf:li, or nothing at all if value is empty.
func writeLangAltProp(b *strings.Builder, prop, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, `<%s><rdf:Alt><rdf:li xml:lang="x-default">%s</rdf:li></rdf:Alt></%s>`+"\n",
		prop, xmlEscapeText(value), prop)
}

// writeScalarAttr appends ` prop="value"` to an open (not yet closed) start
// tag, or nothing if value is empty.
func writeScalarAttr(b *strings.Builder, prop, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, ` %s="%s"`, prop, xmlEscapeAttr(value))
}

// xmlEscapeText escapes s for use as XML element text content. Only & < >
// are escaped (structurally required); ' and " are left unescaped in element
// text since escaping them is unnecessary and checkInfoXMPSync decodes only
// the five standard XML entities when comparing XMP values with the Info
// dictionary. Does not decode s as UTF-8 first (PDF strings have no declared
// charset; encoding/xml.EscapeText would corrupt non-UTF-8 bytes with U+FFFD).
func xmlEscapeText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c < 0x20 && c != '\t' && c != '\n' && c != '\r':
			// XML 1.0 forbids these control characters outright -- no entity
			// escaping makes them legal -- so drop them rather than emit a
			// byte xmpWellFormed's XML decoder will always reject (6.7.9).
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// xmlEscapeAttr is xmlEscapeText plus '"', the one additional character
// that must be escaped inside a double-quote-delimited attribute value (see
// writeScalarAttr) to avoid prematurely terminating it.
func xmlEscapeAttr(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c == '"':
			b.WriteString("&quot;")
		case c < 0x20 && c != '\t' && c != '\n' && c != '\r':
			// See xmlEscapeText: an illegal XML control character, dropped.
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// pdfDateToXMP converts a PDF date string ("D:YYYYMMDDHHmmSSOHH'mm'", ISO
// 32000-1 7.9.4) to an XMP/ISO-8601 date string, preserving however much
// precision the source provides (down to just a year), defaulting an
// unspecified timezone to UTC. ok is false if cd doesn't start with "D:" or
// has fewer than four digits of year.
func pdfDateToXMP(cd string) (out string, ok bool) {
	cd = strings.TrimPrefix(cd, "D:")
	if len(cd) < 4 || !isAllDigits(cd[:4]) {
		return "", false
	}
	out, rest := cd[:4], cd[4:]

	month, rest, ok := takeDigits2(rest)
	if !ok {
		return out, true
	}
	out += "-" + month

	day, rest, ok := takeDigits2(rest)
	if !ok {
		return out, true
	}
	out += "-" + day

	hour, rest, ok := takeDigits2(rest)
	if !ok {
		return out, true
	}
	minute, rest, ok := takeDigits2(rest)
	if !ok {
		minute = "00"
	}
	second, rest, ok := takeDigits2(rest)
	if !ok {
		second = "00"
	}
	out += "T" + hour + ":" + minute + ":" + second

	if len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
		sign := string(rest[0])
		tzh, rest, ok := takeDigits2(rest[1:])
		if !ok {
			return out + "Z", true
		}
		rest = strings.TrimPrefix(rest, "'")
		tzm, _, ok := takeDigits2(rest)
		if !ok {
			tzm = "00"
		}
		out += sign + tzh + ":" + tzm
	} else {
		out += "Z"
	}
	return out, true
}

// takeDigits2 consumes a 2-digit prefix of s, reporting ok=false (and
// returning s unmodified) if s doesn't start with two digits.
func takeDigits2(s string) (digits, rest string, ok bool) {
	if len(s) < 2 || !isAllDigits(s[:2]) {
		return "", s, false
	}
	return s[:2], s[2:], true
}

// isAllDigits reports whether every byte of s is an ASCII digit.
func isAllDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
