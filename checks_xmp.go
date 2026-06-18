package pdfrab

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
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
	"adobe:ns:meta/":                         true,
	"http://ns.adobe.com/xap/1.0/":           true,
	"http://ns.adobe.com/xap/1.0/mm/":        true,
	"http://ns.adobe.com/xap/1.0/rights/":    true,
	"http://ns.adobe.com/xap/1.0/t/pg/":      true,
	"http://ns.adobe.com/pdf/1.3/":           true,
	"http://purl.org/dc/elements/1.1/":       true,
	"http://www.aiim.org/pdfa/ns/id/":        true,
	"http://www.aiim.org/pdfa/ns/extension/": true,
	"http://www.aiim.org/pdfa/ns/schema#":    true,
	"http://www.aiim.org/pdfa/ns/property#":  true,
	"http://www.aiim.org/pdfa/ns/type#":      true,
	"http://www.aiim.org/pdfa/ns/field#":     true,
	"http://ns.adobe.com/photoshop/1.0/":     true,
	"http://ns.adobe.com/tiff/1.0/":          true,
	"http://ns.adobe.com/exif/1.0/":          true,
	// aux: and xmpDM: are not standard XMP 2004 schemas; omit so undeclared use is flagged.
	// Camera Raw is not a standard XMP 2004 schema; omit so undeclared use is flagged.
	// XMP structured-type namespaces (st* prefixes) — these are standard Adobe XMP
	// namespaces used for structured properties (e.g., xmpMM:DerivedFrom uses stRef).
	// None of them require extension schema declarations.
	"http://ns.adobe.com/xap/1.0/sType/ResourceRef#":   true,
	"http://ns.adobe.com/xap/1.0/sType/ResourceEvent#": true,
	"http://ns.adobe.com/xap/1.0/sType/Version#":       true,
	"http://ns.adobe.com/xap/1.0/sType/Font#":          true,
	"http://ns.adobe.com/xap/1.0/sType/Dimensions#":    true,
	"http://ns.adobe.com/xap/1.0/sType/Thumbnail#":     true,
	"http://ns.adobe.com/xap/1.0/sType/Job#":           true,
	"http://ns.adobe.com/xap/1.0/sType/ManifestItem#":  true,
	"http://ns.adobe.com/xap/1.0/g/img/":               true,
	"http://ns.adobe.com/xap/1.0/bj/":                  true,
	"http://ns.adobe.com/xmp/Identifier/qual/1.0/":     true,
	// xmpDM: is not a standard XMP 2004 schema; omit.
	"http://ns.adobe.com/InDesign/1.0/":    true,
	"http://ns.adobe.com/illustrator/1.0/": true,
	"http://ns.adobe.com/swf/1.0/":         true,
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

	// Find custom namespace prefixes that are actually used (element or attribute style).
	customNSUsed := false
	for prefix, uri := range bindPrefixToURI {
		if knownXMPNamespaces[uri] {
			continue
		}
		if strings.Contains(xmp, "<"+prefix+":") ||
			strings.Contains(xmp, " "+prefix+":") ||
			strings.Contains(xmp, "\t"+prefix+":") {
			customNSUsed = true
			break
		}
	}

	hasSchemas := strings.Contains(xmp, "pdfaExtension:schemas")

	// Custom namespace properties without any extension schema → t01.
	if customNSUsed && !hasSchemas {
		msg := "custom-namespace properties used without extension schema"
		return append(errs,
			xmpErr("6.7.8", 1, msg),
			xmpErr("6.7.2", 4, msg),
		)
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
	// Normalise UTF-16 LE/BE XMP streams to UTF-8 before any further processing.
	data = decodeXMPEncoding(data)
	xmp := string(data)

	errs = append(errs, checkXMPHeader(xmp)...)
	errs = append(errs, checkPDFAIdentifier(xmp)...)
	if !xmpWellFormed(data) {
		errs = append(errs, xmpErr("6.7.9", 2, "XMP metadata is not well-formed XML"))
	}
	errs = append(errs, checkXMPPropertyTypes(xmp)...)
	errs = append(errs, checkXMPPropertySchemas(data)...)
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

// decodeXMPEncoding converts an XMP stream to UTF-8 if it is UTF-16 or
// UTF-32 encoded. Encoding is detected by BOM or the leading '<' pattern:
//
//   - UTF-32 LE BOM  : FF FE 00 00
//   - UTF-32 BE BOM  : 00 00 FE FF
//   - UTF-32 LE      : 3C 00 00 00
//   - UTF-32 BE      : 00 00 00 3C
//   - UTF-16 LE BOM  : FF FE (not followed by 00 00)
//   - UTF-16 BE BOM  : FE FF
//   - UTF-16 LE      : 3C 00
//   - UTF-16 BE      : 00 3C
//   - UTF-8          : anything else
func decodeXMPEncoding(data []byte) []byte {
	if len(data) < 4 {
		if len(data) >= 2 {
			return decodeXMPEncoding16(data, 0)
		}
		return data
	}

	// UTF-32 BOM detection (must come before UTF-16 BOM check because
	// UTF-32 LE BOM starts with the same two bytes as UTF-16 LE BOM).
	if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
		return decodeUTF32(data[4:], true) // UTF-32 LE BOM
	}
	if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
		return decodeUTF32(data[4:], false) // UTF-32 BE BOM
	}
	// UTF-32 without BOM: '<' followed by three NUL bytes.
	if data[0] == 0x3C && data[1] == 0x00 && data[2] == 0x00 && data[3] == 0x00 {
		return decodeUTF32(data, true) // UTF-32 LE
	}
	if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x00 && data[3] == 0x3C {
		return decodeUTF32(data, false) // UTF-32 BE
	}
	// UTF-16 BOM or bare '<' + 0x00 pattern.
	return decodeXMPEncoding16(data, 0)
}

// decodeUTF32 converts a UTF-32 stream (offset bytes already stripped) to UTF-8.
func decodeUTF32(raw []byte, le bool) []byte {
	n := len(raw) / 4
	buf := make([]byte, 0, n*3)
	var tmp [4]byte
	for i := range n {
		b := raw[i*4 : i*4+4]
		var cp uint32
		if le {
			cp = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		} else {
			cp = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		}
		r := rune(cp)
		if r > 0x10FFFF {
			r = 0xFFFD
		}
		sz := utf8.EncodeRune(tmp[:], r)
		buf = append(buf, tmp[:sz]...)
	}
	return buf
}

// decodeXMPEncoding16 handles UTF-16 detection and conversion.
func decodeXMPEncoding16(data []byte, _ int) []byte {
	if len(data) < 2 {
		return data
	}
	var le bool
	offset := 0
	if data[0] == 0xFF && data[1] == 0xFE {
		le = true
		offset = 2
	} else if data[0] == 0xFE && data[1] == 0xFF {
		le = false
		offset = 2
	} else if data[0] == 0x3C && data[1] == 0x00 {
		le = true
	} else if data[0] == 0x00 && data[1] == 0x3C {
		le = false
	} else {
		return data // Already UTF-8
	}
	raw := data[offset:]
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		b0, b1 := raw[i*2], raw[i*2+1]
		if le {
			u16[i] = uint16(b0) | uint16(b1)<<8
		} else {
			u16[i] = uint16(b0)<<8 | uint16(b1)
		}
	}
	runes := utf16.Decode(u16)
	buf := make([]byte, 0, len(runes)*3)
	var tmp [4]byte
	for _, r := range runes {
		n := utf8.EncodeRune(tmp[:], r)
		buf = append(buf, tmp[:n]...)
	}
	return buf
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

// xmpScalarValueRe builds a regex that matches a named XMP property as either
// an attribute (prop="value") or a simple element (<prop>value</prop>).
func xmpScalarValueRe(prop string) *regexp.Regexp {
	q := regexp.QuoteMeta(prop)
	return regexp.MustCompile(`(?s)` + q + `\s*=\s*"([^"]*)"` +
		`|<` + q + `[^>]*>\s*([^<]*?)\s*</` + q + `>`)
}

// xmpScalarValue extracts an XMP scalar property value, checking both
// attribute style (prop="value") and element style (<prop>value</prop>).
func xmpScalarValue(xmp, prop string) (string, bool) {
	m := xmpScalarValueRe(prop).FindStringSubmatch(xmp)
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		return m[1], true
	}
	return strings.TrimSpace(m[2]), true
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
	// The PDF null keyword is resolved to the string "null" by the parser;
	// treat that as absent so it doesn't trigger a false sync mismatch.
	for key, prop := range map[string]string{
		"Title":   "dc:title",
		"Subject": "dc:description",
	} {
		val := strings.TrimSpace(info[key])
		if val == "" || val == "null" {
			continue
		}
		got, ok := xmpPropValue(xmp, prop)
		if !ok || got != val {
			msg := fmt.Sprintf("document info %s not synchronized with XMP %s", key, prop)
			errs = append(errs, xmpErr("6.7.3", 1, msg))
			errs = append(errs, xmpErr("6.1.5", 4, msg))
		}
	}

	// Author vs dc:creator: dc:creator is a Seq; Author must match the single item.
	if author := info["Author"]; author != "" && author != "null" {
		if m := regexp.MustCompile(`(?s)<dc:creator[^>]*>(.*?)</dc:creator>`).FindStringSubmatch(xmp); m != nil {
			items := rdfLiRe.FindAllStringSubmatch(m[1], -1)
			var msg string
			if len(items) > 1 {
				msg = "document info Author not synchronized with XMP dc:creator (multiple entries)"
			} else if len(items) == 1 && strings.TrimSpace(items[0][1]) != author {
				msg = "document info Author not synchronized with XMP dc:creator"
			}
			if msg != "" {
				errs = append(errs, xmpErr("6.7.3", 1, msg))
				errs = append(errs, xmpErr("6.1.5", 4, msg))
			}
		}
	}

	// Creator vs xmp:CreatorTool (scalar attribute).
	if creator := info["Creator"]; creator != "" && creator != "null" {
		if xmpCreator, ok := xmpScalarValue(xmp, "xmp:CreatorTool"); ok && xmpCreator != creator {
			msg := "document info Creator not synchronized with XMP xmp:CreatorTool"
			errs = append(errs, xmpErr("6.7.3", 1, msg))
			errs = append(errs, xmpErr("6.1.5", 4, msg))
		}
	}

	// Producer vs pdf:Producer (scalar attribute).
	if producer := info["Producer"]; producer != "" && producer != "null" {
		if xmpProducer, ok := xmpScalarValue(xmp, "pdf:Producer"); ok && xmpProducer != producer {
			msg := "document info Producer not synchronized with XMP pdf:Producer"
			errs = append(errs, xmpErr("6.7.3", 1, msg))
			errs = append(errs, xmpErr("6.1.5", 4, msg))
		}
	}

	// Keywords vs pdf:Keywords (scalar attribute).
	if kw := info["Keywords"]; kw != "" && kw != "null" {
		if xmpKW, ok := xmpScalarValue(xmp, "pdf:Keywords"); ok && xmpKW != kw {
			msg := "document info Keywords not synchronized with XMP pdf:Keywords"
			errs = append(errs, xmpErr("6.7.3", 1, msg))
			errs = append(errs, xmpErr("6.1.5", 4, msg))
		}
	}

	// Dates use different representations (PDF "D:YYYYMMDD..." vs ISO 8601);
	// compare their numeric components at the same precision.
	infoDateMismatch := func(infoKey, label string, xmpDateRe *regexp.Regexp) {
		cd := strings.TrimSpace(info[infoKey])
		if cd == "" {
			return
		}
		// PDF dates must start with "D:" — an ISO 8601 format is invalid.
		if !strings.HasPrefix(cd, "D:") {
			errs = append(errs, xmpErr("6.1.5", 4,
				fmt.Sprintf("document info %s is not in PDF date format", infoKey)))
			return
		}
		xmpDate, _ := firstGroup(xmpDateRe, xmp)
		infoDigits := digitsOf(cd)
		xmpDigits := digitsOf(xmpDate)
		n := min(len(infoDigits), len(xmpDigits), 14)
		if n < 8 || infoDigits[:n] != xmpDigits[:n] || len(infoDigits) < len(xmpDigits) {
			errs = append(errs, xmpErr("6.7.3", 1,
				fmt.Sprintf("document info %s not synchronized with XMP %s", infoKey, label)))
			errs = append(errs, xmpErr("6.1.5", 4,
				fmt.Sprintf("document info %s not synchronized with XMP %s", infoKey, label)))
		}
	}
	infoDateMismatch("CreationDate", "xmp:CreateDate", xmpCreateDateRe)
	infoDateMismatch("ModDate", "xmp:ModifyDate", xmpModifyDateRe)

	return errs
}

// xmpContainerKind describes how an XMP property must be structured.
type xmpContainerKind uint8

const (
	xmpKindScalar  xmpContainerKind = iota // plain text / attribute
	xmpKindInteger                         // integer scalar value
	xmpKindBoolean                         // boolean scalar ("True" or "False")
	xmpKindBag                             // rdf:Bag required
	xmpKindSeq                             // rdf:Seq required
	xmpKindAlt                             // rdf:Alt required (including LangAlt)
	xmpKindStruct                          // rdf:parseType="Resource" or nested rdf:Description
)

// xmpNSSchemas maps namespace URI to a map of (propertyName → expected container kind).
// A namespace present in this map is a "known 2004 schema"; any property from that
// namespace that is not listed is reported as not permitted (6.7.2).
var xmpNSSchemas = map[string]map[string]xmpContainerKind{
	// Dublin Core (dc:)
	"http://purl.org/dc/elements/1.1/": {
		"contributor": xmpKindBag, "coverage": xmpKindScalar,
		"creator": xmpKindSeq, "date": xmpKindSeq,
		"description": xmpKindAlt, "format": xmpKindScalar,
		"identifier": xmpKindScalar, "language": xmpKindBag,
		"publisher": xmpKindBag, "relation": xmpKindBag,
		"rights": xmpKindAlt, "source": xmpKindScalar,
		"subject": xmpKindBag, "title": xmpKindAlt,
		"type": xmpKindBag,
	},
	// XMP Basic (xmp:) — XMP 2004 valid subset only
	"http://ns.adobe.com/xap/1.0/": {
		"Advisory": xmpKindBag, "BaseURL": xmpKindScalar,
		"CreateDate": xmpKindScalar, "CreatorTool": xmpKindScalar,
		"Format": xmpKindScalar, "Identifier": xmpKindBag,
		"MetadataDate": xmpKindScalar, "ModifyDate": xmpKindScalar,
		"Nickname": xmpKindScalar, "Thumbnails": xmpKindAlt,
		// Author, Description, Label, Rating, Title are not valid in XMP 2004
	},
	// PDF schema (pdf:) — XMP 2004 valid subset only
	"http://ns.adobe.com/pdf/1.3/": {
		"Keywords": xmpKindScalar, "PDFVersion": xmpKindScalar, "Producer": xmpKindScalar,
		// Author, BaseURL, CreationDate, Creator, ModDate, Subject, Title, Trapped not valid in XMP 2004
	},
	// XMP Rights Management (xmpRights:)
	"http://ns.adobe.com/xap/1.0/rights/": {
		"Certificate": xmpKindScalar, "Marked": xmpKindBoolean,
		"Owner": xmpKindBag, "UsageTerms": xmpKindAlt, "WebStatement": xmpKindScalar,
		// Copyright is not valid in XMP 2004
	},
	// XMP Media Management (xmpMM:)
	"http://ns.adobe.com/xap/1.0/mm/": {
		"DerivedFrom": xmpKindStruct, "DocumentID": xmpKindScalar,
		"History": xmpKindSeq, "Ingredients": xmpKindBag,
		"InstanceID": xmpKindScalar, "LastURL": xmpKindScalar,
		"ManagedFrom": xmpKindStruct, "Manager": xmpKindScalar,
		"ManageTo": xmpKindScalar, "ManageUI": xmpKindScalar,
		"ManagerVariant": xmpKindScalar, "RenditionClass": xmpKindScalar,
		"RenditionOf": xmpKindStruct, "RenditionParams": xmpKindScalar,
		"SaveID": xmpKindInteger, "VersionID": xmpKindScalar, "Versions": xmpKindSeq,
		// Manifest, Pantry, placedXResolution not valid in XMP 2004
	},
	// XMP Basic Job Ticket (xmpBJ:)
	"http://ns.adobe.com/xap/1.0/bj/": {
		"JobRef": xmpKindBag,
	},
	// XMP Paged-Text (xmpTPg:) — XMP 2004 valid subset only
	"http://ns.adobe.com/xap/1.0/t/pg/": {
		"MaxPageSize": xmpKindStruct, "NPages": xmpKindInteger,
		// Fonts, Colorants, PlateNames not valid in XMP 2004
	},
	// Photoshop (photoshop:) — XMP 2004 valid subset only
	"http://ns.adobe.com/photoshop/1.0/": {
		"AuthorsPosition": xmpKindScalar, "CaptionWriter": xmpKindScalar,
		"Category": xmpKindScalar, "City": xmpKindScalar,
		"Country": xmpKindScalar, "Credit": xmpKindScalar,
		"DateCreated": xmpKindScalar, "Headline": xmpKindScalar,
		"ICCProfile": xmpKindScalar, "Instructions": xmpKindScalar,
		"Source": xmpKindScalar, "State": xmpKindScalar,
		"SupplementalCategories": xmpKindScalar,
		"TransmissionReference":  xmpKindScalar, "Urgency": xmpKindInteger,
		// Author, Copyright, History, Title not valid in XMP 2004
	},
	// TIFF (tiff:)
	"http://ns.adobe.com/tiff/1.0/": {
		"Artist": xmpKindScalar, "BitsPerSample": xmpKindSeq,
		"Compression": xmpKindInteger, "Copyright": xmpKindAlt,
		"DateTime": xmpKindScalar, "ImageDescription": xmpKindAlt,
		"ImageLength": xmpKindInteger, "ImageWidth": xmpKindInteger,
		"Make": xmpKindScalar, "Model": xmpKindScalar,
		"Orientation": xmpKindInteger, "PhotometricInterpretation": xmpKindInteger,
		"PlanarConfiguration": xmpKindInteger, "PrimaryChromaticities": xmpKindSeq,
		"ReferenceBlackWhite": xmpKindSeq, "ResolutionUnit": xmpKindInteger,
		"SamplesPerPixel": xmpKindInteger, "Software": xmpKindScalar,
		"TransferFunction": xmpKindSeq, "WhitePoint": xmpKindSeq,
		"XResolution": xmpKindScalar, "YCbCrCoefficients": xmpKindSeq,
		"YCbCrPositioning": xmpKindInteger, "YCbCrSubSampling": xmpKindSeq,
		"YResolution": xmpKindScalar,
	},
	// EXIF (exif:) — includes struct field sub-properties
	"http://ns.adobe.com/exif/1.0/": {
		"ApertureValue": xmpKindScalar, "BrightnessValue": xmpKindScalar,
		"CFAPattern": xmpKindStruct, "ColorSpace": xmpKindInteger,
		"ComponentsConfiguration": xmpKindSeq, "CompressedBitsPerPixel": xmpKindScalar,
		"Contrast": xmpKindInteger, "CustomRendered": xmpKindInteger,
		"DateTimeDigitized": xmpKindScalar, "DateTimeOriginal": xmpKindScalar,
		"DeviceSettingDescription": xmpKindStruct, "DigitalZoomRatio": xmpKindScalar,
		"ExifVersion": xmpKindScalar, "ExposureBiasValue": xmpKindScalar,
		"ExposureIndex": xmpKindScalar, "ExposureMode": xmpKindInteger,
		"ExposureProgram": xmpKindInteger, "ExposureTime": xmpKindScalar,
		"FNumber": xmpKindScalar, "FileSource": xmpKindInteger,
		"Flash": xmpKindStruct, "FlashEnergy": xmpKindScalar,
		"FlashpixVersion": xmpKindScalar, "FocalLength": xmpKindScalar,
		"FocalLengthIn35mmFilm":    xmpKindInteger,
		"FocalPlaneResolutionUnit": xmpKindInteger,
		"FocalPlaneXResolution":    xmpKindScalar, "FocalPlaneYResolution": xmpKindScalar,
		"GainControl": xmpKindInteger,
		"GPSAltitude": xmpKindScalar, "GPSAltitudeRef": xmpKindInteger,
		"GPSAreaInformation": xmpKindScalar,
		"GPSDOP":             xmpKindScalar, "GPSDestBearing": xmpKindScalar,
		"GPSDestBearingRef": xmpKindScalar, "GPSDestDistance": xmpKindScalar,
		"GPSDestDistanceRef": xmpKindScalar, "GPSDestLatitude": xmpKindScalar,
		"GPSDestLongitude": xmpKindScalar, "GPSDifferential": xmpKindInteger,
		"GPSImgDirection": xmpKindScalar, "GPSImgDirectionRef": xmpKindScalar,
		"GPSLatitude": xmpKindScalar, "GPSLongitude": xmpKindScalar,
		"GPSMapDatum": xmpKindScalar, "GPSMeasureMode": xmpKindScalar,
		"GPSProcessingMethod": xmpKindScalar, "GPSSatellites": xmpKindScalar,
		"GPSSpeed": xmpKindScalar, "GPSSpeedRef": xmpKindScalar,
		"GPSStatus": xmpKindScalar, "GPSTimeStamp": xmpKindScalar,
		"GPSTrack": xmpKindScalar, "GPSTrackRef": xmpKindScalar,
		"GPSVersionID":    xmpKindScalar,
		"ISOSpeedRatings": xmpKindSeq, "ImageUniqueID": xmpKindScalar,
		"LightSource": xmpKindInteger, "MakerNote": xmpKindScalar,
		"MaxApertureValue": xmpKindScalar, "MeteringMode": xmpKindInteger,
		"OECF": xmpKindStruct, "PixelXDimension": xmpKindInteger,
		"PixelYDimension": xmpKindInteger, "RelatedSoundFile": xmpKindScalar,
		"Saturation": xmpKindInteger, "SceneCaptureType": xmpKindInteger,
		"SceneType": xmpKindInteger, "SensingMethod": xmpKindInteger,
		"Sharpness": xmpKindInteger, "ShutterSpeedValue": xmpKindScalar,
		"SpatialFrequencyResponse": xmpKindStruct, "SpectralSensitivity": xmpKindScalar,
		"SubjectArea": xmpKindSeq, "SubjectDistance": xmpKindScalar,
		"SubjectDistanceRange": xmpKindInteger, "SubjectLocation": xmpKindSeq,
		"UserComment": xmpKindAlt, "WhiteBalance": xmpKindInteger,
		// Struct sub-fields (Flash, OECF/SFR, CFAPattern, DeviceSettingDescription):
		"Fired": xmpKindScalar, "Return": xmpKindInteger,
		"Mode": xmpKindInteger, "Function": xmpKindScalar, "RedEyeMode": xmpKindScalar,
		"Columns": xmpKindInteger, "Rows": xmpKindInteger,
		"Names": xmpKindSeq, "Values": xmpKindSeq, "Settings": xmpKindSeq,
	},
	// PDF/A Identification Schema (pdfaid:) — only part, conformance, and amd are defined.
	pdfaIDNamespace: {
		"part":        xmpKindInteger,
		"conformance": xmpKindScalar,
		"amd":         xmpKindScalar,
	},
}

// xmpValueKind constrains the plain-text value of an XMP property beyond its
// container shape (6.7.2): e.g. a Date-typed property must hold an ISO 8601
// date, not arbitrary text.
type xmpValueKind uint8

const (
	xmpVTDate       xmpValueKind = iota // ISO 8601 date/time
	xmpVTInteger                        // integer (reuses xmpIsInteger)
	xmpVTClosedText                     // text restricted to a fixed set of choices
)

// xmpValueRule describes a value-type constraint for a property; choices is
// only populated for xmpVTClosedText.
type xmpValueRule struct {
	kind    xmpValueKind
	choices []string
}

// xmpValueRules maps namespace URI → property name → value-type constraint,
// for the subset of XMP 2004 properties whose container shape alone (handled
// by xmpNSSchemas) does not capture their full value-type requirement.
var xmpValueRules = map[string]map[string]xmpValueRule{
	"http://purl.org/dc/elements/1.1/": {
		"date": {kind: xmpVTDate},
	},
	"http://ns.adobe.com/xap/1.0/": {
		"CreateDate":   {kind: xmpVTDate},
		"ModifyDate":   {kind: xmpVTDate},
		"MetadataDate": {kind: xmpVTDate},
	},
	"http://ns.adobe.com/photoshop/1.0/": {
		"DateCreated": {kind: xmpVTDate},
	},
	"http://ns.adobe.com/tiff/1.0/": {
		"DateTime":      {kind: xmpVTDate},
		"BitsPerSample": {kind: xmpVTInteger},
	},
	"http://ns.adobe.com/exif/1.0/": {
		"DateTimeOriginal": {kind: xmpVTDate},
		"GPSTimeStamp":     {kind: xmpVTDate},
		"ISOSpeedRatings":  {kind: xmpVTInteger},
		"GPSMeasureMode":   {kind: xmpVTClosedText, choices: []string{"2", "3"}},
	},
}

// xmpLangAltProps marks the XMP 2004 properties whose Alt container is
// specifically a "Language Alternative" (LangAlt): every rdf:li item must
// carry an xml:lang qualifier. Plain Alt properties (e.g. xmp:Thumbnails,
// an alternative set of images rather than language variants) are not
// listed here and are exempt from this requirement.
var xmpLangAltProps = map[string]map[string]bool{
	"http://purl.org/dc/elements/1.1/": {
		"description": true, "rights": true, "title": true,
	},
	"http://ns.adobe.com/xap/1.0/rights/": {
		"UsageTerms": true,
	},
	"http://ns.adobe.com/tiff/1.0/": {
		"Copyright": true, "ImageDescription": true,
	},
	"http://ns.adobe.com/exif/1.0/": {
		"UserComment": true,
	},
}

// xmpDateRe matches a valid XMP/ISO 8601 date value: a year, optionally
// extended with month, day, and time-of-day, where the time-of-day's
// timezone designator is either "Z" or a single "±hh:mm" offset (not both).
var xmpDateRe = regexp.MustCompile(
	`^\d{4}(-\d{2}(-\d{2}(T\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:\d{2})?)?)?)?$`)

// xmpIsDate reports whether s is a validly formatted XMP date/time value.
func xmpIsDate(s string) bool {
	return xmpDateRe.MatchString(s)
}

// xmpCheckValueRule validates value against rule, returning a human-readable
// description of the violation, or "" if value satisfies the rule.
func xmpCheckValueRule(rule xmpValueRule, value string) string {
	switch rule.kind {
	case xmpVTDate:
		if !xmpIsDate(value) {
			return fmt.Sprintf("must be a valid ISO 8601 date, got %q", value)
		}
	case xmpVTInteger:
		if !xmpIsInteger(value) {
			return fmt.Sprintf("must be an integer, got %q", value)
		}
	case xmpVTClosedText:
		if !slices.Contains(rule.choices, value) {
			return fmt.Sprintf("must be one of %v, got %q", rule.choices, value)
		}
	}
	return ""
}

// checkXMPPropertySchemas validates that XMP properties are used in accordance
// with their definitions in XMP 2004 (PDF/A-1b clause 6.7.2).
func checkXMPPropertySchemas(data []byte) []PDFError {
	if i := bytes.IndexByte(data, '<'); i > 0 {
		data = data[i:]
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false

	var errs []PDFError
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Space == nsRDF && se.Name.Local == "Description" {
			// Check attribute-style properties on rdf:Description.
			for _, a := range se.Attr {
				if a.Name.Space == "" || a.Name.Space == nsRDF ||
					a.Name.Space == "http://www.w3.org/XML/1998/namespace" {
					continue
				}
				errs = append(errs, xmpValidateProp(a.Name.Space, a.Name.Local, xmpKindScalar, a.Value, nil)...)
			}
		} else if _, inSchema := xmpNSSchemas[se.Name.Space]; inSchema {
			// Element-style property from a known schema.
			container, textVal, items := xmpConsumeProperty(dec, se)
			errs = append(errs, xmpValidateProp(se.Name.Space, se.Name.Local, container, textVal, items)...)
		}
	}
	return errs
}

// xmpPropItem is one rdf:li item of a Bag/Seq/Alt container property: its
// plain-text value, and whether it carried an xml:lang qualifier (relevant
// for LangAlt properties, 6.7.2).
type xmpPropItem struct {
	text    string
	hasLang bool
}

// xmpConsumeProperty reads the content of a property element and returns the
// container kind, plain-text value (if scalar), and the list of rdf:li items
// (if a Bag/Seq/Alt container). It consumes all child tokens.
func xmpConsumeProperty(dec *xml.Decoder, elem xml.StartElement) (kind xmpContainerKind, scalarText string, items []xmpPropItem) {
	for _, a := range elem.Attr {
		if a.Name.Space == nsRDF {
			if a.Name.Local == "parseType" && a.Value == "Resource" {
				xmpSkipElem(dec)
				return xmpKindStruct, "", nil
			}
			if a.Name.Local == "resource" {
				xmpSkipElem(dec)
				return xmpKindScalar, a.Value, nil
			}
		}
	}

	var text strings.Builder
	kind = xmpKindScalar
	depth := 1
	var curItem *xmpPropItem
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 && kind == xmpKindScalar {
				switch {
				case t.Name.Space == nsRDF && t.Name.Local == "Bag":
					kind = xmpKindBag
				case t.Name.Space == nsRDF && t.Name.Local == "Seq":
					kind = xmpKindSeq
				case t.Name.Space == nsRDF && t.Name.Local == "Alt":
					kind = xmpKindAlt
				case t.Name.Space == nsRDF && t.Name.Local == "Description":
					kind = xmpKindStruct
				default:
					kind = xmpKindStruct // non-rdf child → inline struct
				}
			} else if depth == 3 && t.Name.Space == nsRDF && t.Name.Local == "li" &&
				(kind == xmpKindBag || kind == xmpKindSeq || kind == xmpKindAlt) {
				item := xmpPropItem{}
				for _, a := range t.Attr {
					if a.Name.Space == "http://www.w3.org/XML/1998/namespace" && a.Name.Local == "lang" {
						item.hasLang = true
					}
				}
				items = append(items, item)
				curItem = &items[len(items)-1]
			}
		case xml.EndElement:
			depth--
			if depth == 2 {
				curItem = nil
			}
		case xml.CharData:
			if depth == 1 {
				text.Write([]byte(t))
			} else if depth == 3 && curItem != nil {
				curItem.text += string(t)
			}
		}
	}
	return kind, strings.TrimSpace(text.String()), items
}

// xmpSkipElem consumes tokens until the current element is closed.
func xmpSkipElem(dec *xml.Decoder) {
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

// xmpValidateProp validates a single XMP property usage against the schema.
// items is the list of rdf:li entries when actual is a Bag/Seq/Alt container
// (nil for a scalar attribute or element).
func xmpValidateProp(nsURI, propName string, actual xmpContainerKind, value string, items []xmpPropItem) []PDFError {
	schema := xmpNSSchemas[nsURI]
	if schema == nil {
		return nil
	}
	// The pdfaid namespace is part of the PDF/A identification requirements
	// (6.7.11), not the general XMP 2004 schema check (6.7.2): a malformed
	// pdfaid property is reported under the more specific clause.
	clause, sub := "6.7.2", 2
	if nsURI == pdfaIDNamespace {
		clause, sub = "6.7.11", 5
	}
	expected, defined := schema[propName]
	if !defined {
		return []PDFError{xmpErr(clause, sub,
			fmt.Sprintf("property %q is not defined in XMP 2004 schema %s", propName, nsURI))}
	}
	if !xmpContainerOK(expected, actual) {
		return []PDFError{xmpErr(clause, sub,
			fmt.Sprintf("property %q used with wrong container type", propName))}
	}
	if expected == xmpKindInteger && actual == xmpKindScalar && value != "" && !xmpIsInteger(value) {
		return []PDFError{xmpErr(clause, sub,
			fmt.Sprintf("property %q must be an integer, got %q", propName, value))}
	}
	if expected == xmpKindBoolean && actual == xmpKindScalar && value != "" && value != "True" && value != "False" {
		return []PDFError{xmpErr(clause, sub,
			fmt.Sprintf("property %q must be a boolean (True/False), got %q", propName, value))}
	}

	// Value-type constraints beyond container shape (6.7.2): dates, integer
	// items inside a Bag/Seq, and closed-choice text values.
	if rule, ok := xmpValueRules[nsURI][propName]; ok {
		switch actual {
		case xmpKindScalar:
			if value != "" {
				if msg := xmpCheckValueRule(rule, value); msg != "" {
					return []PDFError{xmpErr(clause, sub, fmt.Sprintf("property %q %s", propName, msg))}
				}
			}
		case xmpKindBag, xmpKindSeq:
			for _, item := range items {
				if item.text == "" {
					continue
				}
				if msg := xmpCheckValueRule(rule, item.text); msg != "" {
					return []PDFError{xmpErr(clause, sub, fmt.Sprintf("property %q item %s", propName, msg))}
				}
			}
		}
	}

	// LangAlt properties (6.7.2): every rdf:li in an Alt container must carry
	// an xml:lang qualifier.
	if actual == xmpKindAlt && xmpLangAltProps[nsURI][propName] {
		for _, item := range items {
			if !item.hasLang {
				return []PDFError{xmpErr(clause, sub,
					fmt.Sprintf("property %q is LangAlt but an rdf:li item lacks xml:lang", propName))}
			}
		}
	}

	return nil
}

func xmpContainerOK(expected, actual xmpContainerKind) bool {
	switch expected {
	case xmpKindScalar, xmpKindInteger, xmpKindBoolean:
		return actual == xmpKindScalar
	case xmpKindBag:
		return actual == xmpKindBag
	case xmpKindSeq:
		return actual == xmpKindSeq
	case xmpKindAlt:
		return actual == xmpKindAlt
	case xmpKindStruct:
		return actual == xmpKindStruct
	}
	return false
}

// xmpIsInteger reports whether s is a valid integer value (optional sign, then digits only).
func xmpIsInteger(s string) bool {
	if len(s) == 0 {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i = 1
	}
	if i == len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
