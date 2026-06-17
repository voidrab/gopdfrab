package pdfrab

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

const pdfaIDNamespace = "http://www.aiim.org/pdfa/ns/id/"

// PDF/A extension schema namespace URIs (6.7.8).
const (
	nsRDF  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	nsExt  = "http://www.aiim.org/pdfa/ns/extension/"
	nsScm  = "http://www.aiim.org/pdfa/ns/schema#"
	nsProp = "http://www.aiim.org/pdfa/ns/property#"
	nsType = "http://www.aiim.org/pdfa/ns/type#"
	nsFld  = "http://www.aiim.org/pdfa/ns/field#"
)

// knownXMPNamespaces lists the XMP namespace URIs predefined by PDF/A-1b
// that do not require extension schema descriptions.
var knownXMPNamespaces = map[string]bool{
	"http://www.w3.org/1999/02/22-rdf-syntax-ns#": true,
	"adobe:ns:meta/":                               true,
	"http://ns.adobe.com/xap/1.0/":                 true,
	"http://ns.adobe.com/xap/1.0/mm/":              true,
	"http://ns.adobe.com/xap/1.0/rights/":          true,
	"http://ns.adobe.com/xap/1.0/t/pg/":            true,
	"http://ns.adobe.com/pdf/1.3/":                 true,
	"http://purl.org/dc/elements/1.1/":             true,
	"http://www.aiim.org/pdfa/ns/id/":              true,
	"http://www.aiim.org/pdfa/ns/extension/":       true,
	"http://www.aiim.org/pdfa/ns/schema#":          true,
	"http://www.aiim.org/pdfa/ns/property#":        true,
	"http://www.aiim.org/pdfa/ns/type#":            true,
	"http://www.aiim.org/pdfa/ns/field#":           true,
	"http://ns.adobe.com/photoshop/1.0/":           true,
	"http://ns.adobe.com/tiff/1.0/":                true,
	"http://ns.adobe.com/exif/1.0/":                true,
	"http://ns.adobe.com/exif/1.0/aux/":            true,
	"http://ns.adobe.com/camera-raw-settings/1.0/": true,
}

// xmpBuiltinTypes lists the predefined XMP value types valid for
// pdfaProperty:valueType, pdfaField:valueType and pdfaType:type.
var xmpBuiltinTypes = map[string]bool{
	"Boolean": true, "Date": true, "Integer": true, "Real": true,
	"Text": true, "URI": true, "URL": true, "Bag": true, "Seq": true,
	"Alt": true, "LangAlt": true, "ProperName": true, "MIMEType": true,
	"ResourceRef": true, "ResourceEvent": true, "Rational": true,
	"RenditionClass": true, "Thumbnail": true, "XPath": true, "Locale": true,
}

var xmpNSBindRe = regexp.MustCompile(`xmlns:(\w+)\s*=\s*"([^"]*)"`)

// Intermediate structures for parsed extension schema content.

type extSchema struct {
	namespaceURI string
	prefix       string
	properties   []extProperty
	valueTypes   []extType
}

type extProperty struct {
	name, valueType, category, description string
	nameCount                              int // >1 means duplicate (t02-f)
}

type extType struct {
	typeName, namespaceURI, prefix, description string
	fields                                      []extField
}

type extField struct {
	name, valueType, description string
}

// checkExtensionSchemas validates PDF/A extension schema declarations (6.7.8).
func checkExtensionSchemas(xmp string) []PDFError {
	var errs []PDFError

	// Collect all xmlns:prefix="uri" bindings declared in the XMP.
	bindPrefixToURI := map[string]string{}
	bindURIToPrefixes := map[string][]string{}
	for _, m := range xmpNSBindRe.FindAllStringSubmatch(xmp, -1) {
		prefix, uri := m[1], m[2]
		if _, exists := bindPrefixToURI[prefix]; !exists {
			bindPrefixToURI[prefix] = uri
		}
		found := slices.Contains(bindURIToPrefixes[uri], prefix)
		if !found {
			bindURIToPrefixes[uri] = append(bindURIToPrefixes[uri], prefix)
		}
	}

	// Conventional prefix→URI map for extension schema vocabulary.
	convPrefixToURI := map[string]string{
		"pdfaExtension": nsExt,
		"pdfaSchema":    nsScm,
		"pdfaProperty":  nsProp,
		"pdfaType":      nsType,
		"pdfaField":     nsFld,
	}
	convURIToPrefix := map[string]string{}
	for p, u := range convPrefixToURI {
		convURIToPrefix[u] = p
	}

	// If a conventional prefix is declared but bound to the wrong URI → t02-b family.
	for prefix, expectedURI := range convPrefixToURI {
		if uri, ok := bindPrefixToURI[prefix]; ok && uri != expectedURI {
			errs = append(errs, xmpErr("6.7.8", 2,
				"extension-schema prefix "+prefix+" bound to wrong namespace URI"))
		}
	}

	// If an extension-schema namespace URI is bound to the wrong prefix → t02-a family.
	for uri, prefixes := range bindURIToPrefixes {
		conv, isExt := convURIToPrefix[uri]
		if !isExt {
			continue
		}
		for _, prefix := range prefixes {
			if prefix != conv {
				errs = append(errs, xmpErr("6.7.8", 1,
					"extension-schema namespace "+uri+" bound with non-standard prefix "+prefix))
			}
		}
	}

	// If errors already found (wrong prefix/URI), return early.
	if len(errs) > 0 {
		return errs
	}

	// Find custom namespace prefixes that are actually used as elements in the XMP.
	customNSUsed := false
	for prefix, uri := range bindPrefixToURI {
		if knownXMPNamespaces[uri] {
			continue
		}
		if strings.Contains(xmp, "<"+prefix+":") {
			customNSUsed = true
			break
		}
	}

	hasSchemas := strings.Contains(xmp, "pdfaExtension:schemas")

	// Custom namespace properties without any extension schema → t01.
	if customNSUsed && !hasSchemas {
		return append(errs, xmpErr("6.7.8", 1,
			"custom-namespace properties used without extension schema"))
	}

	if !hasSchemas {
		return errs
	}

	// Deep structural validation via XML parsing.
	errs = append(errs, validateExtSchemas([]byte(xmp), bindPrefixToURI)...)
	return errs
}

// validateExtSchemas parses and validates the pdfaExtension:schemas structure.
func validateExtSchemas(data []byte, bindPrefixToURI map[string]string) []PDFError {
	// Strip leading BOM / whitespace to make a valid XML fragment.
	if i := bytes.IndexByte(data, '<'); i > 0 {
		data = data[i:]
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false

	// Locate the pdfaExtension:schemas element.
	var schemas []extSchema
	schemasContainerType := ""
	found := false

	for !found {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Space == nsExt && se.Name.Local == "schemas" {
			found = true
			schemasContainerType, schemas = parseSchemasBag(dec)
		}
	}

	if !found {
		return nil
	}

	var errs []PDFError

	// The schemas container must be rdf:Bag, not rdf:Seq → t02-c.
	if schemasContainerType == "Seq" {
		errs = append(errs, xmpErr("6.7.8", 3, "pdfaExtension:schemas must use rdf:Bag, not rdf:Seq"))
	}

	// Build a map of all documented namespaceURIs for cross-reference.
	docNS := map[string]bool{}
	for _, s := range schemas {
		docNS[s.namespaceURI] = true
		errs = append(errs, validateExtSchema(s, bindPrefixToURI, string(data))...)
	}

	return errs
}

// parseSchemasBag parses the content of pdfaExtension:schemas, returning the
// container type ("Bag" or "Seq") and all schema entries found.
func parseSchemasBag(dec *xml.Decoder) (string, []extSchema) {
	var containerType string
	var schemas []extSchema
	inContainer := false

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsRDF && (t.Name.Local == "Bag" || t.Name.Local == "Seq") && !inContainer {
				containerType = t.Name.Local
				inContainer = true
			} else if t.Name.Space == nsRDF && t.Name.Local == "li" && inContainer {
				s := parseSchemaLi(dec)
				schemas = append(schemas, s)
			} else if inContainer {
				xmlSkipElem(dec)
			}
		case xml.EndElement:
			if t.Name.Space == nsExt && t.Name.Local == "schemas" {
				return containerType, schemas
			}
			if t.Name.Space == nsRDF && (t.Name.Local == "Bag" || t.Name.Local == "Seq") {
				inContainer = false
			}
		}
	}
	return containerType, schemas
}

// parseSchemaLi parses one rdf:li element inside pdfaExtension:schemas.
func parseSchemaLi(dec *xml.Decoder) extSchema {
	var s extSchema
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case t.Name.Space == nsScm && t.Name.Local == "namespaceURI":
				s.namespaceURI = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsScm && t.Name.Local == "prefix":
				s.prefix = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsScm && t.Name.Local == "schema":
				xmlSkipElem(dec)
				depth--
			case t.Name.Space == nsScm && t.Name.Local == "property":
				s.properties = parsePropertySeq(dec)
				depth--
			case t.Name.Space == nsScm && t.Name.Local == "valueType":
				s.valueTypes = parseTypeSeq(dec)
				depth--
			default:
				xmlSkipElem(dec)
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return s
}

// parsePropertySeq parses pdfaSchema:property content (rdf:Seq of rdf:li).
func parsePropertySeq(dec *xml.Decoder) []extProperty {
	var props []extProperty
	inSeq := false
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Space == nsRDF && t.Name.Local == "Seq" && !inSeq {
				inSeq = true
			} else if t.Name.Space == nsRDF && t.Name.Local == "li" && inSeq {
				p := parsePropertyLi(dec)
				props = append(props, p)
				depth--
			} else {
				xmlSkipElem(dec)
				depth--
			}
		case xml.EndElement:
			depth--
			if t.Name.Space == nsRDF && t.Name.Local == "Seq" {
				inSeq = false
			}
		}
	}
	return props
}

// parsePropertyLi parses one rdf:li inside pdfaSchema:property.
func parsePropertyLi(dec *xml.Decoder) extProperty {
	var p extProperty
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case t.Name.Space == nsProp && t.Name.Local == "name":
				p.nameCount++
				if p.name == "" {
					p.name = xmlTokenText(dec)
				} else {
					xmlSkipElem(dec)
				}
				depth--
			case t.Name.Space == nsProp && t.Name.Local == "valueType":
				p.valueType = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsProp && t.Name.Local == "category":
				p.category = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsProp && t.Name.Local == "description":
				p.description = xmlTokenText(dec)
				depth--
			default:
				xmlSkipElem(dec)
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return p
}

// parseTypeSeq parses pdfaSchema:valueType content (rdf:Seq of rdf:li).
func parseTypeSeq(dec *xml.Decoder) []extType {
	var types []extType
	inSeq := false
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Space == nsRDF && t.Name.Local == "Seq" && !inSeq {
				inSeq = true
			} else if t.Name.Space == nsRDF && t.Name.Local == "li" && inSeq {
				tp := parseTypeLi(dec)
				types = append(types, tp)
				depth--
			} else {
				xmlSkipElem(dec)
				depth--
			}
		case xml.EndElement:
			depth--
			if t.Name.Space == nsRDF && t.Name.Local == "Seq" {
				inSeq = false
			}
		}
	}
	return types
}

// parseTypeLi parses one rdf:li inside pdfaSchema:valueType.
func parseTypeLi(dec *xml.Decoder) extType {
	var tp extType
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case t.Name.Space == nsType && t.Name.Local == "type":
				tp.typeName = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsType && t.Name.Local == "namespaceURI":
				tp.namespaceURI = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsType && t.Name.Local == "prefix":
				tp.prefix = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsType && t.Name.Local == "description":
				tp.description = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsType && t.Name.Local == "field":
				tp.fields = parseFieldSeq(dec)
				depth--
			default:
				xmlSkipElem(dec)
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return tp
}

// parseFieldSeq parses pdfaType:field content (rdf:Seq of rdf:li).
func parseFieldSeq(dec *xml.Decoder) []extField {
	var fields []extField
	inSeq := false
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Space == nsRDF && t.Name.Local == "Seq" && !inSeq {
				inSeq = true
			} else if t.Name.Space == nsRDF && t.Name.Local == "li" && inSeq {
				f := parseFieldLi(dec)
				fields = append(fields, f)
				depth--
			} else {
				xmlSkipElem(dec)
				depth--
			}
		case xml.EndElement:
			depth--
			if t.Name.Space == nsRDF && t.Name.Local == "Seq" {
				inSeq = false
			}
		}
	}
	return fields
}

// parseFieldLi parses one rdf:li inside pdfaType:field.
func parseFieldLi(dec *xml.Decoder) extField {
	var f extField
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case t.Name.Space == nsFld && t.Name.Local == "name":
				f.name = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsFld && t.Name.Local == "valueType":
				f.valueType = xmlTokenText(dec)
				depth--
			case t.Name.Space == nsFld && t.Name.Local == "description":
				f.description = xmlTokenText(dec)
				depth--
			default:
				xmlSkipElem(dec)
				depth--
			}
		case xml.EndElement:
			depth--
		}
	}
	return f
}

// xmlTokenText reads a start element's text content up to its matching end tag.
func xmlTokenText(dec *xml.Decoder) string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 1 {
				b.Write(t)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// xmlSkipElem consumes tokens until the current start element is closed.
func xmlSkipElem(dec *xml.Decoder) {
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

// validateExtSchema validates one extension schema entry.
func validateExtSchema(s extSchema, bindPrefixToURI map[string]string, xmp string) []PDFError {
	var errs []PDFError

	// Build set of defined custom type names for cross-reference.
	definedTypes := map[string]bool{}
	for _, tp := range s.valueTypes {
		definedTypes[tp.typeName] = true
	}

	// Build set of documented property names.
	docProps := map[string]bool{}
	for _, p := range s.properties {
		docProps[p.name] = true
	}

	// Validate each property entry.
	for _, p := range s.properties {
		// t02-f: multiple pdfaProperty:name elements in same rdf:li
		if p.nameCount > 1 {
			errs = append(errs, xmpErr("6.7.8", 4,
				"multiple pdfaProperty:name elements in a single property entry"))
		}
		if p.name == "" {
			errs = append(errs, xmpErr("6.7.8", 5, "pdfaProperty entry missing name"))
		}
		// t02-e: missing pdfaProperty:valueType
		if p.valueType == "" {
			errs = append(errs, xmpErr("6.7.8", 5, "pdfaProperty "+p.name+" missing valueType"))
		}
		// t02-i: missing pdfaProperty:category
		if p.category == "" {
			errs = append(errs, xmpErr("6.7.8", 5, "pdfaProperty "+p.name+" missing category"))
		}
		if p.description == "" {
			errs = append(errs, xmpErr("6.7.8", 5, "pdfaProperty "+p.name+" missing description"))
		}
		// t02-k: if property's valueType is a non-primitive custom type, its actual
		// usage in the XMP data must use rdf:parseType="Resource"
		if p.valueType != "" && !xmpBuiltinTypes[p.valueType] {
			// Find the actual usage of this property under the schema's prefix
			propTag := "<" + s.prefix + ":" + p.name
			if strings.Contains(xmp, propTag) &&
				!strings.Contains(xmp, propTag+` rdf:parseType="Resource"`) &&
				!strings.Contains(xmp, propTag+" rdf:parseType='Resource'") {
				errs = append(errs, xmpErr("6.7.8", 6,
					"property "+p.name+" declared as complex type but used as simple value"))
			}
		}
	}

	// Validate each valueType entry.
	for _, tp := range s.valueTypes {
		// t02-g: pdfaType:type name must match a property valueType or be self-consistent
		if tp.typeName == "" {
			errs = append(errs, xmpErr("6.7.8", 7, "pdfaType entry missing type name"))
		}
		// t02-h: missing pdfaType:namespaceURI
		if tp.namespaceURI == "" {
			errs = append(errs, xmpErr("6.7.8", 7, "pdfaType "+tp.typeName+" missing namespaceURI"))
		}
		if tp.prefix == "" {
			errs = append(errs, xmpErr("6.7.8", 7, "pdfaType "+tp.typeName+" missing prefix"))
		}
		if tp.description == "" {
			errs = append(errs, xmpErr("6.7.8", 7, "pdfaType "+tp.typeName+" missing description"))
		}
		// Validate field entries.
		for _, f := range tp.fields {
			if f.name == "" {
				errs = append(errs, xmpErr("6.7.8", 8, "pdfaField entry missing name"))
			}
			// t02-j: pdfaField:valueType must be a predefined type or a defined custom type
			if f.valueType != "" && !xmpBuiltinTypes[f.valueType] && !definedTypes[f.valueType] {
				errs = append(errs, xmpErr("6.7.8", 8,
					"pdfaField "+f.name+" has invalid valueType "+f.valueType))
			}
			if f.valueType == "" {
				errs = append(errs, xmpErr("6.7.8", 8, "pdfaField "+f.name+" missing valueType"))
			}
			if f.description == "" {
				errs = append(errs, xmpErr("6.7.8", 8, "pdfaField "+f.name+" missing description"))
			}
		}
	}

	// t02-d: cross-check documented property names against actual used properties.
	// Find properties actually used under this schema's prefix.
	if s.prefix != "" {
		usedRe := regexp.MustCompile(`<` + regexp.QuoteMeta(s.prefix) + `:(\w[\w.-]*)`)
		for _, m := range usedRe.FindAllStringSubmatch(xmp, -1) {
			propName := m[1]
			// Ignore RDF/pdfaExt structural elements
			if propName == "Description" || propName == "RDF" {
				continue
			}
			if !docProps[propName] {
				errs = append(errs, xmpErr("6.7.8", 9,
					"property "+s.prefix+":"+propName+" used but not documented in extension schema"))
			}
		}
	}

	// t02-g: check that all referenced custom value types are defined.
	for _, p := range s.properties {
		if p.valueType != "" && !xmpBuiltinTypes[p.valueType] && !definedTypes[p.valueType] {
			errs = append(errs, xmpErr("6.7.8", 10,
				"property "+p.name+" references undefined value type "+p.valueType))
		}
	}

	return errs
}

var (
	xpacketRe  = regexp.MustCompile(`<\?xpacket[^>]*>`)
	pdfaNSRe   = regexp.MustCompile(`xmlns:pdfaid\s*=\s*"([^"]*)"`)
	pdfaPartRe = regexp.MustCompile(`pdfaid:part\s*=\s*"([^"]*)"|<pdfaid:part>\s*([^<\s]+)\s*</pdfaid:part>`)
	pdfaConfRe = regexp.MustCompile(`pdfaid:conformance\s*=\s*"([^"]*)"|<pdfaid:conformance>\s*([^<\s]+)\s*</pdfaid:conformance>`)
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
	errs = append(errs, checkXMPPropertyTypes(xmp)...)
	errs = append(errs, d.checkInfoXMPSync(xmp)...)
	errs = append(errs, checkExtensionSchemas(xmp)...)

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
var xmpModifyDateRe = regexp.MustCompile(`xmp:ModifyDate\s*=\s*"([^"]*)"|<xmp:ModifyDate>\s*([^<\s]+)\s*</xmp:ModifyDate>`)

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

// dcDescRe matches a dc:description element and captures its inner content.
var dcDescRe = regexp.MustCompile(`(?s)<dc:description[^>]*>(.*?)</dc:description>`)

// checkXMPPropertyTypes validates that standard XMP properties use their
// required data types and that only valid properties are used (6.7.2).
func checkXMPPropertyTypes(xmp string) []PDFError {
	var errs []PDFError

	// t02-fail-a: The rdf namespace is declared but the root element uses a
	// wrong/undeclared prefix (e.g. <RDF:RDF> instead of <rdf:RDF>).
	if strings.Contains(xmp, `xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"`) &&
		!strings.Contains(xmp, "<rdf:RDF") {
		errs = append(errs, xmpErr("6.7.2", 1,
			"rdf namespace declared but rdf:RDF root element is missing or uses wrong prefix"))
	}

	// t02-fail-b: xmp:Title is not a valid property in the xmp: namespace.
	if strings.Contains(xmp, "<xmp:Title") {
		errs = append(errs, xmpErr("6.7.2", 2,
			"xmp:Title is not a defined property in the xmp namespace; use dc:title instead"))
	}

	// t02-fail-c: dc:description must be of value type LangAlt (rdf:Alt with
	// xml:lang attributes), not plain text.
	if m := dcDescRe.FindStringSubmatch(xmp); m != nil {
		if !strings.Contains(m[1], "<rdf:Alt") {
			errs = append(errs, xmpErr("6.7.2", 3,
				"dc:description must be of type LangAlt, not plain text"))
		}
	}

	return errs
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
		n := min(len(infoDigits), len(xmpDigits), 14)
		if n < 8 || infoDigits[:n] != xmpDigits[:n] {
			errs = append(errs, xmpErr("6.7.3", 1,
				"document info CreationDate not synchronized with XMP xmp:CreateDate"))
		}
	}

	// ModDate must equal xmp:ModifyDate to second precision (14 digits).
	if md := strings.TrimSpace(info["ModDate"]); md != "" {
		xmpDate, _ := firstGroup(xmpModifyDateRe, xmp)
		if xmpDate != "" {
			infoDigits := digitsOf(md)
			xmpDigits := digitsOf(xmpDate)
			n := min(len(infoDigits), len(xmpDigits), 14)
			if n < 8 || infoDigits[:n] != xmpDigits[:n] {
				errs = append(errs, xmpErr("6.7.3", 1,
					"document info ModDate not synchronized with XMP xmp:ModifyDate"))
			}
		}
	}

	return errs
}
