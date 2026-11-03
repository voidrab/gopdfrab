package verify

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"slices"
	"strings"
)

// nsXML is the namespace the xml prefix is bound to.
const nsXML = "http://www.w3.org/XML/1998/namespace"

// nsDCDeprecated is the older Dublin Core namespace URI, which addChild maps
// to nsDC.
const nsDCDeprecated = "http://purl.org/dc/1.1/"

// errBadRDF reports XMP whose RDF is outside the subset XMP allows.
var errBadRDF = errors.New("XMP packet is not valid XMP RDF")

// xmpForm is the shape of an XMP property value.
type xmpForm uint8

const (
	xmpSimple xmpForm = iota
	xmpStructure
	xmpBag
	xmpSeq
	xmpAlt
)

// xmpNode is a property, structure field or array item of a parsed XMP
// packet, in the data model of the XMP specification.
type xmpNode struct {
	ns, prefix, name string
	form             xmpForm
	hasLang          bool
	value            string
	// children are the fields of a structure or the items of an array.
	children []*xmpNode
}

func (n *xmpNode) isArray() bool { return n.form >= xmpBag }

// isAltText reports whether n is an Alt array with an item that has an
// xml:lang qualifier.
func (n *xmpNode) isAltText() bool {
	return n.form == xmpAlt && slices.ContainsFunc(n.children, func(c *xmpNode) bool { return c.hasLang })
}

// isQualified reports whether n is a structure holding rdf:value, which
// addChild always puts first.
func (n *xmpNode) isQualified() bool {
	return n.form == xmpStructure && len(n.children) > 0 &&
		n.children[0].ns == nsRDF && n.children[0].name == "value"
}

// isOrdered reports whether n is a Seq or an Alt array.
func (n *xmpNode) isOrdered() bool { return n.form == xmpSeq || n.form == xmpAlt }

// child returns n's field named name in namespace ns, or nil.
func (n *xmpNode) child(ns, name string) *xmpNode { return findXMPNode(n.children, ns, name) }

// findXMPNode returns the node of nodes named name in namespace ns, or nil.
func findXMPNode(nodes []*xmpNode, ns, name string) *xmpNode {
	for _, c := range nodes {
		if c.ns == ns && c.name == name {
			return c
		}
	}
	return nil
}

// xmpPacket is a parsed XMP packet: its top-level properties and the PDF/A
// extension schema container held apart from them.
type xmpPacket struct {
	props   []*xmpNode
	schemas *xmpNode
}

// prop returns the top-level property name in namespace ns, or nil.
func (p *xmpPacket) prop(ns, name string) *xmpNode { return findXMPNode(p.props, ns, name) }

// xmlElem is an element of a parsed XML document, with namespaces resolved.
type xmlElem struct {
	uri, prefix, local string
	attrs              []xmlAttr
	kids               []xmlKid
}

// xmlKid is one child of an element: an element, or text when elem is nil.
type xmlKid struct {
	elem *xmlElem
	text string
}

type xmlAttr struct {
	uri, prefix, local, value string
}

func (a xmlAttr) is(uri, local string) bool { return a.uri == uri && a.local == local }

// isLang reports whether a is the xml:lang attribute.
func (a xmlAttr) isLang() bool { return a.is(nsXML, "lang") }

// parseXMPPacket parses an XMP packet the way XMP readers do, without
// normalising aliases or values. A packet with no rdf:RDF element has no
// properties. The error is non-nil when the packet is not well-formed XML or
// its RDF is not XMP.
func parseXMPPacket(data []byte) (*xmpPacket, error) {
	doc, err := parseXMLDocument(data)
	if err != nil {
		return nil, err
	}
	pkt := &xmpPacket{}
	rdf := findRDFRoot(doc)
	if rdf == nil {
		return pkt, nil
	}
	p := rdfParser{pkt: pkt}
	if err := p.rdf(rdf); err != nil {
		return nil, err
	}
	return pkt, nil
}

// parseXMLDocument parses data into a document node whose children are its
// top-level elements, resolving namespace prefixes.
func parseXMLDocument(data []byte) (*xmlElem, error) {
	if i := bytes.IndexByte(data, '<'); i > 0 {
		data = data[i:]
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	// data is already UTF-8, so an encoding declaration only names the
	// original encoding and is not applied.
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	doc := &xmlElem{}
	stack := []*xmlElem{doc}
	scopes := []map[string]string{{"xml": nsXML}}
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			var scope map[string]string
			for _, a := range t.Attr {
				if prefix, ok := xmlnsPrefix(a.Name); ok {
					if scope == nil {
						scope = map[string]string{}
					}
					scope[prefix] = a.Value
				}
			}
			scopes = append(scopes, scope)
			uri, err := resolveXMLPrefix(scopes, t.Name.Space)
			if err != nil {
				return nil, err
			}
			el := &xmlElem{uri: uri, prefix: t.Name.Space, local: t.Name.Local}
			for _, a := range t.Attr {
				if _, ok := xmlnsPrefix(a.Name); ok {
					continue
				}
				auri := ""
				if a.Name.Space != "" {
					if auri, err = resolveXMLPrefix(scopes, a.Name.Space); err != nil {
						return nil, err
					}
				}
				el.attrs = append(el.attrs, xmlAttr{auri, a.Name.Space, a.Name.Local, a.Value})
			}
			top.kids = append(top.kids, xmlKid{elem: el})
			stack = append(stack, el)
		case xml.EndElement:
			if len(stack) == 1 || top.prefix != t.Name.Space || top.local != t.Name.Local {
				return nil, errors.New("mismatched XML end element")
			}
			stack = stack[:len(stack)-1]
			scopes = scopes[:len(scopes)-1]
		case xml.CharData:
			if len(stack) > 1 {
				top.kids = append(top.kids, xmlKid{text: string(t)})
			}
		}
	}
	if len(stack) != 1 {
		return nil, errors.New("unclosed XML element")
	}
	return doc, nil
}

// xmlnsPrefix reports whether an attribute named name declares a namespace,
// and for which prefix; the default namespace has the empty prefix.
func xmlnsPrefix(name xml.Name) (string, bool) {
	switch {
	case name.Space == "xmlns":
		return name.Local, true
	case name.Space == "" && name.Local == "xmlns":
		return "", true
	}
	return "", false
}

// resolveXMLPrefix returns the namespace bound to prefix in the innermost of
// scopes that binds it. An unbound empty prefix means no namespace.
func resolveXMLPrefix(scopes []map[string]string, prefix string) (string, error) {
	for i := len(scopes) - 1; i >= 0; i-- {
		if uri, ok := scopes[i][prefix]; ok {
			return uri, nil
		}
	}
	if prefix == "" {
		return "", nil
	}
	return "", errors.New("unbound XML namespace prefix " + prefix)
}

// findRDFRoot returns the first rdf:RDF element, looking inside x:xmpmeta
// and any other element on the way.
func findRDFRoot(e *xmlElem) *xmlElem {
	for _, k := range e.kids {
		if k.elem == nil {
			continue
		}
		if k.elem.uri == nsRDF && k.elem.local == "RDF" {
			return k.elem
		}
		if r := findRDFRoot(k.elem); r != nil {
			return r
		}
	}
	return nil
}

// RDF syntax terms, as XMP readers classify rdf: names.
type rdfTerm uint8

const (
	rdfOther rdfTerm = iota
	rdfRDF
	rdfID
	rdfAbout
	rdfParseType
	rdfResource
	rdfNodeID
	rdfDatatype
	rdfDescription
	rdfLi
	rdfOld // aboutEach, aboutEachPrefix, bagID
)

func termOf(uri, local string) rdfTerm {
	if uri != nsRDF {
		return rdfOther
	}
	switch local {
	case "RDF":
		return rdfRDF
	case "ID":
		return rdfID
	case "about":
		return rdfAbout
	case "parseType":
		return rdfParseType
	case "resource":
		return rdfResource
	case "nodeID":
		return rdfNodeID
	case "datatype":
		return rdfDatatype
	case "Description":
		return rdfDescription
	case "li":
		return rdfLi
	case "aboutEach", "aboutEachPrefix", "bagID":
		return rdfOld
	}
	return rdfOther
}

// attrTerm classifies an attribute of owner; an unqualified about or ID on
// an rdf: element counts as the rdf: one.
func attrTerm(a xmlAttr, owner *xmlElem) rdfTerm {
	if a.uri == "" && (a.local == "about" || a.local == "ID") && owner.uri == nsRDF {
		return termOf(nsRDF, a.local)
	}
	return termOf(a.uri, a.local)
}

// childElems returns the element children of el, which may have white
// space between them but no other text.
func childElems(el *xmlElem) ([]*xmlElem, error) {
	var out []*xmlElem
	for _, k := range el.kids {
		switch {
		case k.elem != nil:
			out = append(out, k.elem)
		case strings.TrimSpace(k.text) != "":
			return nil, errBadRDF
		}
	}
	return out, nil
}

// qualifierAttrs checks that el has no attribute but xml:lang, rdf:ID and
// the rdf: attribute named allowed, and reports whether it has xml:lang.
func qualifierAttrs(el *xmlElem, allowed string) (hasLang bool, err error) {
	for _, a := range el.attrs {
		switch {
		case a.isLang():
			hasLang = true
		case a.is(nsRDF, "ID"), a.is(nsRDF, allowed):
		default:
			return false, errBadRDF
		}
	}
	return hasLang, nil
}

// rdfParser builds an xmpPacket from the rdf:RDF element of a packet.
type rdfParser struct {
	pkt   *xmpPacket
	about string
	// seen holds the namespace and name of each top-level property.
	seen map[[2]string]bool
}

func (p *rdfParser) rdf(rdf *xmlElem) error {
	nodes, err := childElems(rdf)
	if err != nil {
		return err
	}
	for _, el := range nodes {
		if err := p.nodeElement(nil, el); err != nil {
			return err
		}
	}
	if i := slices.IndexFunc(p.pkt.props, func(n *xmpNode) bool { return n.ns == nsExt && n.name == "schemas" }); i >= 0 {
		p.pkt.schemas = p.pkt.props[i]
		p.pkt.props = slices.Delete(p.pkt.props, i, i+1)
	}
	return nil
}

// nodeElement parses an rdf:Description or typed node into the fields of
// parent, or into top-level properties when parent is nil.
func (p *rdfParser) nodeElement(parent *xmpNode, el *xmlElem) error {
	// A typed node may only give the value of a property.
	if term := termOf(el.uri, el.local); term != rdfDescription && (term != rdfOther || parent == nil) {
		return errBadRDF
	}
	exclusive := 0
	for _, a := range el.attrs {
		switch term := attrTerm(a, el); term {
		case rdfID, rdfNodeID, rdfAbout:
			if exclusive++; exclusive > 1 {
				return errBadRDF
			}
			if parent == nil && term == rdfAbout {
				if p.about != "" && a.value != "" && a.value != p.about {
					return errBadRDF
				}
				if p.about == "" {
					p.about = a.value
				}
			}
		case rdfOther:
			n, err := p.addChild(parent, a.uri, a.prefix, a.local)
			if err != nil {
				return err
			}
			n.value = a.value
		default:
			return errBadRDF
		}
	}
	return p.propertyElementList(parent, el)
}

func (p *rdfParser) propertyElementList(parent *xmpNode, el *xmlElem) error {
	props, err := childElems(el)
	if err != nil {
		return err
	}
	for _, c := range props {
		if err := p.propertyElement(parent, c); err != nil {
			return err
		}
	}
	return nil
}

func (p *rdfParser) propertyElement(parent *xmpNode, el *xmlElem) error {
	switch termOf(el.uri, el.local) {
	case rdfOther, rdfLi:
	default:
		return errBadRDF
	}
	if len(el.attrs) > 3 {
		return p.emptyPropertyElement(parent, el)
	}
	for _, a := range el.attrs {
		if a.isLang() {
			continue
		}
		switch {
		case a.is(nsRDF, "datatype"):
			return p.literalPropertyElement(parent, el)
		case !a.is(nsRDF, "parseType"):
			return p.emptyPropertyElement(parent, el)
		case a.value == "Resource":
			return p.parseTypeResource(parent, el)
		default:
			// parseType Literal, Collection and any other are not XMP.
			return errBadRDF
		}
	}
	if len(el.kids) == 0 {
		return p.emptyPropertyElement(parent, el)
	}
	for _, k := range el.kids {
		if k.elem != nil {
			return p.resourcePropertyElement(parent, el)
		}
	}
	return p.literalPropertyElement(parent, el)
}

// resourcePropertyElement parses a property whose value is an array or a
// structure given as a nested node element; el has an element child and no
// attribute but xml:lang.
func (p *rdfParser) resourcePropertyElement(parent *xmpNode, el *xmlElem) error {
	if parent == nil && el.prefix == "iX" && el.local == "changes" {
		return nil
	}
	n, err := p.addChild(parent, el.uri, el.prefix, el.local)
	if err != nil {
		return err
	}
	// el has no attribute but xml:lang, so any attribute is an xml:lang one.
	n.hasLang = len(el.attrs) > 0
	kids, err := childElems(el)
	if err != nil || len(kids) != 1 {
		return errBadRDF
	}
	c := kids[0]
	switch {
	case c.uri == nsRDF && c.local == "Bag":
		n.form = xmpBag
	case c.uri == nsRDF && c.local == "Seq":
		n.form = xmpSeq
	case c.uri == nsRDF && c.local == "Alt":
		n.form = xmpAlt
	default:
		n.form = xmpStructure
		if c.uri == "" && c.local != "Description" {
			return errBadRDF
		}
	}
	if err := p.nodeElement(n, c); err != nil {
		return err
	}
	fixupQualifiedNode(n)
	return nil
}

func (p *rdfParser) literalPropertyElement(parent *xmpNode, el *xmlElem) error {
	n, err := p.addChild(parent, el.uri, el.prefix, el.local)
	if err != nil {
		return err
	}
	if n.hasLang, err = qualifierAttrs(el, "datatype"); err != nil {
		return err
	}
	var text strings.Builder
	for _, k := range el.kids {
		if k.elem != nil {
			return errBadRDF
		}
		text.WriteString(k.text)
	}
	n.value = text.String()
	return nil
}

func (p *rdfParser) parseTypeResource(parent *xmpNode, el *xmlElem) error {
	n, err := p.addChild(parent, el.uri, el.prefix, el.local)
	if err != nil {
		return err
	}
	n.form = xmpStructure
	if n.hasLang, err = qualifierAttrs(el, "parseType"); err != nil {
		return err
	}
	if err := p.propertyElementList(n, el); err != nil {
		return err
	}
	fixupQualifiedNode(n)
	return nil
}

// emptyPropertyElement parses a property element without content: a simple
// value given by rdf:value or rdf:resource, or a structure whose fields are
// the element's attributes.
func (p *rdfParser) emptyPropertyElement(parent *xmpNode, el *xmlElem) error {
	if len(el.kids) > 0 {
		return errBadRDF
	}
	var hasPropertyAttrs, hasResource, hasNodeID, hasValue bool
	valueAttr := -1
	for i, a := range el.attrs {
		switch attrTerm(a, el) {
		case rdfID:
		case rdfResource:
			if hasNodeID || hasValue {
				return errBadRDF
			}
			hasResource, valueAttr = true, i
		case rdfNodeID:
			if hasResource {
				return errBadRDF
			}
			hasNodeID = true
		case rdfOther:
			switch {
			case a.is(nsRDF, "value"):
				if hasResource {
					return errBadRDF
				}
				hasValue, valueAttr = true, i
			case !a.isLang():
				hasPropertyAttrs = true
			}
		default:
			return errBadRDF
		}
	}
	n, err := p.addChild(parent, el.uri, el.prefix, el.local)
	if err != nil {
		return err
	}
	switch {
	case hasValue || hasResource:
		n.value = el.attrs[valueAttr].value
	case hasPropertyAttrs:
		n.form = xmpStructure
	}
	for i, a := range el.attrs {
		if i == valueAttr {
			continue
		}
		if attrTerm(a, el) != rdfOther {
			continue
		}
		switch {
		case a.isLang():
			n.hasLang = true
		case n.form == xmpStructure:
			f, err := p.addChild(n, a.uri, a.prefix, a.local)
			if err != nil {
				return err
			}
			f.value = a.value
		}
	}
	return nil
}

// addChild adds a node for an element or attribute to parent, or a
// top-level property when parent is nil.
func (p *rdfParser) addChild(parent *xmpNode, uri, prefix, local string) (*xmpNode, error) {
	if uri == "" {
		return nil, errBadRDF
	}
	if uri == nsDCDeprecated {
		uri = nsDC
	}
	n := &xmpNode{ns: uri, prefix: prefix, name: local}
	isItem := uri == nsRDF && local == "li"
	isValue := uri == nsRDF && local == "value"
	if parent == nil {
		key := [2]string{uri, local}
		if isItem || isValue || p.seen[key] {
			return nil, errBadRDF
		}
		if p.seen == nil {
			p.seen = map[[2]string]bool{}
		}
		p.seen[key] = true
		p.pkt.props = append(p.pkt.props, n)
		return n, nil
	}
	switch {
	case isItem:
		if !parent.isArray() {
			return nil, errBadRDF
		}
		n.name = "[]"
	case parent.child(uri, local) != nil:
		return nil, errBadRDF
	case isValue && parent.form != xmpStructure:
		return nil, errBadRDF
	}
	if isValue {
		parent.children = append([]*xmpNode{n}, parent.children...)
	} else {
		parent.children = append(parent.children, n)
	}
	return n, nil
}

// fixupQualifiedNode turns a structure holding rdf:value into the value it
// qualifies; the other fields are qualifiers, which the PDF/A rules ignore.
// It leaves any other node unchanged.
func fixupQualifiedNode(n *xmpNode) {
	if !n.isQualified() {
		return
	}
	v := n.children[0]
	n.form, n.value, n.children = v.form, v.value, v.children
	n.hasLang = n.hasLang || v.hasLang
}
