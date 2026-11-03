package verify

import (
	"strings"
	"testing"
)

// rdfDoc wraps RDF content in an x:xmpmeta packet that binds the prefixes
// the tests use.
func rdfDoc(body string) string {
	return `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>` +
		`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="` + nsRDF + `"` +
		` xmlns:dc="` + nsDC + `" xmlns:xmp="` + nsXMP + `" xmlns:t="urn:t/" xmlns:u="urn:t/">` +
		body + `</rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
}

// desc wraps properties in a top-level rdf:Description.
func desc(props string) string {
	return rdfDoc(`<rdf:Description rdf:about="">` + props + `</rdf:Description>`)
}

func mustParseXMP(t *testing.T, xmp string) *xmpPacket {
	t.Helper()
	pkt, err := parseXMPPacket([]byte(xmp))
	if err != nil {
		t.Fatalf("parseXMPPacket: %v", err)
	}
	return pkt
}

func TestParseXMPPacketShapes(t *testing.T) {
	pkt := mustParseXMP(t, desc(
		`<dc:title><rdf:Alt><rdf:li xml:lang="x-default">T</rdf:li></rdf:Alt></dc:title>`+
			`<dc:subject><rdf:Bag><rdf:li>a</rdf:li><rdf:li>b</rdf:li></rdf:Bag></dc:subject>`+
			`<dc:creator><rdf:Seq><rdf:li>c</rdf:li></rdf:Seq></dc:creator>`+
			`<t:alt><rdf:Alt><rdf:li>plain</rdf:li></rdf:Alt></t:alt>`+
			`<t:res rdf:parseType="Resource"><t:f>1</t:f></t:res>`+
			`<t:desc><rdf:Description t:g="2"><t:h>3</t:h></rdf:Description></t:desc>`+
			`<t:typed><t:Kind t:k="v"/></t:typed>`+
			`<t:empty t:a="1" t:b="2" xml:lang="en"/>`+
			`<t:ref rdf:resource="urn:r" xml:lang="en"/>`+
			`<t:val rdf:value="v"/>`+
			`<t:lit xml:lang="en" rdf:datatype="d">  text  </t:lit>`+
			`<t:blank/>`))
	want := map[string]struct {
		form  xmpForm
		value string
		kids  int
	}{
		"title": {xmpAlt, "", 1}, "subject": {xmpBag, "", 2}, "creator": {xmpSeq, "", 1},
		"alt": {xmpAlt, "", 1}, "res": {xmpStructure, "", 1}, "desc": {xmpStructure, "", 2},
		"typed": {xmpStructure, "", 1}, "empty": {xmpStructure, "", 2},
		"ref": {xmpSimple, "urn:r", 0}, "val": {xmpSimple, "v", 0},
		"lit": {xmpSimple, "  text  ", 0}, "blank": {xmpSimple, "", 0},
	}
	if len(pkt.props) != len(want) {
		t.Fatalf("got %d properties, want %d", len(pkt.props), len(want))
	}
	for _, n := range pkt.props {
		w := want[n.name]
		if n.form != w.form || n.value != w.value || len(n.children) != w.kids {
			t.Errorf("%s = form %d value %q, %d children; want %+v", n.name, n.form, n.value, len(n.children), w)
		}
	}
	if !pkt.prop(nsDC, "title").altText || pkt.prop("urn:t/", "alt").altText {
		t.Error("altText: want it on the lang-qualified Alt only")
	}
	if !pkt.prop("urn:t/", "lit").hasLang || !pkt.prop("urn:t/", "ref").hasLang {
		t.Error("hasLang not recorded")
	}
	if item := pkt.prop(nsDC, "subject").children[0]; item.name != "[]" || item.value != "a" {
		t.Errorf("array item = %+v", item)
	}
	if pkt.prop("urn:t/", "nope") != nil || pkt.prop(nsDC, "subject").child("urn:t/", "x") != nil {
		t.Error("lookup of an absent node returned a node")
	}
}

func TestParseXMPPacketQualifiedValue(t *testing.T) {
	pkt := mustParseXMP(t, desc(
		`<t:q rdf:parseType="Resource"><rdf:value>v</rdf:value><t:qual>x</t:qual></t:q>`+
			`<t:r xml:lang="en"><rdf:Description><rdf:value><rdf:Alt><rdf:li xml:lang="en">a</rdf:li></rdf:Alt></rdf:value></rdf:Description></t:r>`+
			`<t:s><rdf:Description><t:x>1</t:x><rdf:value>w</rdf:value></rdf:Description></t:s>`))
	q, r, s := pkt.prop("urn:t/", "q"), pkt.prop("urn:t/", "r"), pkt.prop("urn:t/", "s")
	if q.form != xmpSimple || q.value != "v" || len(q.children) != 0 {
		t.Errorf("q = %+v, want simple v", q)
	}
	if r.form != xmpAlt || !r.altText || !r.hasLang {
		t.Errorf("r = %+v, want the lang Alt it qualifies", r)
	}
	if s.value != "w" {
		t.Errorf("s = %+v, want w (rdf:value goes first)", s)
	}
}

func TestParseXMPPacketSchemasAndAbout(t *testing.T) {
	pkt := mustParseXMP(t, rdfDoc(
		`<rdf:Description rdf:about="" xmlns:pdfaExtension="`+nsExt+`">`+
			`<pdfaExtension:schemas><rdf:Bag/></pdfaExtension:schemas></rdf:Description>`+
			`<rdf:Description about="uuid:1" dc:format="a/b"/>`+
			`<rdf:Description rdf:about="uuid:1"/>`+
			`<rdf:Description rdf:ID="i"/>`))
	if pkt.schemas == nil || pkt.schemas.form != xmpBag || len(pkt.props) != 1 {
		t.Fatalf("schemas = %+v, props = %d", pkt.schemas, len(pkt.props))
	}
	if n := pkt.props[0]; n.ns != nsDC || n.value != "a/b" {
		t.Errorf("attribute property = %+v", n)
	}
}

func TestParseXMPPacketDeprecatedDCAndIXChanges(t *testing.T) {
	pkt := mustParseXMP(t, rdfDoc(`<rdf:Description xmlns:old="`+nsDCDeprecated+`" xmlns:iX="urn:ix/">`+
		`<old:format>a/b</old:format><iX:changes><rdf:Bag/></iX:changes></rdf:Description>`))
	if len(pkt.props) != 1 || pkt.props[0].ns != nsDC {
		t.Errorf("props = %+v, want one dc property", pkt.props)
	}
}

func TestParseXMPPacketWithoutRDF(t *testing.T) {
	for _, xmp := range []string{
		`<x:xmpmeta xmlns:x="adobe:ns:meta/">text<y/></x:xmpmeta>`,
		``,
	} {
		if pkt := mustParseXMP(t, xmp); len(pkt.props) != 0 || pkt.schemas != nil {
			t.Errorf("%q: got %+v, want an empty packet", xmp, pkt)
		}
	}
	pkt := mustParseXMP(t, `junk<w><rdf:RDF xmlns:rdf="`+nsRDF+`"><rdf:Description xmlns:t="urn:t/" t:a="1"/></rdf:RDF></w>`)
	if len(pkt.props) != 1 {
		t.Errorf("RDF below another element: got %d properties, want 1", len(pkt.props))
	}
}

func TestParseXMPPacketRejects(t *testing.T) {
	for name, xmp := range map[string]string{
		"not XML":                    `<a b=>`,
		"unclosed":                   `<a>`,
		"stray end":                  `</a>`,
		"mismatched end":             `<a></b>`,
		"unbound element prefix":     `<q:a/>`,
		"unbound attribute prefix":   `<a q:b="1"/>`,
		"text in rdf:RDF":            rdfDoc(`text`),
		"rdf:li node element":        rdfDoc(`<rdf:li/>`),
		"top-level typed node":       rdfDoc(`<t:Thing/>`),
		"two node IDs":               rdfDoc(`<rdf:Description rdf:about="" rdf:ID="a"/>`),
		"mismatched about":           rdfDoc(`<rdf:Description rdf:about="a"/><rdf:Description rdf:about="b"/>`),
		"node parseType":             rdfDoc(`<rdf:Description rdf:parseType="Resource"/>`),
		"duplicate property":         rdfDoc(`<rdf:Description t:a="1"/><rdf:Description t:a="2"/>`),
		"text among properties":      desc(`text`),
		"rdf:Description property":   desc(`<rdf:Description/>`),
		"parseType Literal":          desc(`<t:a rdf:parseType="Literal"/>`),
		"parseType Collection":       desc(`<t:a rdf:parseType="Collection"/>`),
		"resource with attribute":    desc(`<t:a t:x="1"><rdf:Bag/></t:a>`),
		"resource with text":         desc(`<t:a>x<rdf:Bag/></t:a>`),
		"resource with two nodes":    desc(`<t:a><rdf:Bag/><rdf:Bag/></t:a>`),
		"typed node without NS":      `<rdf:RDF xmlns:rdf="` + nsRDF + `"><rdf:Description xmlns:t="urn:t/"><t:a><Thing/></t:a></rdf:Description></rdf:RDF>`,
		"bad item inside array":      desc(`<t:a><rdf:Bag><rdf:Description/></rdf:Bag></t:a>`),
		"literal attribute":          desc(`<t:a rdf:datatype="d" t:x="1">v</t:a>`),
		"literal with element":       desc(`<t:a rdf:datatype="d"><t:b/></t:a>`),
		"property without NS":        `<rdf:RDF xmlns:rdf="` + nsRDF + `"><rdf:Description><a>1</a></rdf:Description></rdf:RDF>`,
		"resource without NS":        `<rdf:RDF xmlns:rdf="` + nsRDF + `"><rdf:Description><a><rdf:Bag/></a></rdf:Description></rdf:RDF>`,
		"parseType without NS":       `<rdf:RDF xmlns:rdf="` + nsRDF + `"><rdf:Description><a rdf:parseType="Resource"/></rdf:Description></rdf:RDF>`,
		"empty without NS":           `<rdf:RDF xmlns:rdf="` + nsRDF + `"><rdf:Description><a/></rdf:Description></rdf:RDF>`,
		"parseType attribute":        desc(`<t:a rdf:parseType="Resource" t:x="1"/>`),
		"parseType bad field":        desc(`<t:a rdf:parseType="Resource">text</t:a>`),
		"empty with content":         desc(`<t:a t:b="1" t:c="2" t:d="3" t:e="4">x</t:a>`),
		"resource and nodeID":        desc(`<t:a rdf:nodeID="n" rdf:resource="r"/>`),
		"value and resource":         desc(`<t:a rdf:value="v" rdf:resource="r"/>`),
		"resource and value":         desc(`<t:a rdf:resource="r" rdf:value="v"/>`),
		"nodeID and resource":        desc(`<t:a rdf:resource="r" rdf:nodeID="n"/>`),
		"empty with core attribute":  desc(`<t:a rdf:about="x"/>`),
		"duplicate field":            desc(`<t:a t:x="1" u:x="2"/>`),
		"top-level rdf:value":        desc(`<rdf:value>1</rdf:value>`),
		"top-level rdf:li":           desc(`<rdf:li>1</rdf:li>`),
		"rdf:li outside an array":    desc(`<t:a rdf:parseType="Resource"><rdf:li>1</rdf:li></t:a>`),
		"rdf:value outside a struct": desc(`<t:a><rdf:Bag><rdf:value>1</rdf:value></rdf:Bag></t:a>`),
		"duplicate struct field":     desc(`<t:a rdf:parseType="Resource"><t:x>1</t:x><t:x>2</t:x></t:a>`),
		"duplicate rdf:value":        desc(`<t:a rdf:parseType="Resource"><rdf:value>1</rdf:value><rdf:value>2</rdf:value></t:a>`),
	} {
		if _, err := parseXMPPacket([]byte(xmp)); err == nil {
			t.Errorf("%s: parseXMPPacket accepted %s", name, xmp)
		}
	}
}

// TestParseXMPPacketWhitespaceAndQualifiers verifies that white space
// between elements, xml:lang, an rdf:ID after rdf:parseType and a default
// namespace are accepted.
func TestParseXMPPacketWhitespaceAndQualifiers(t *testing.T) {
	pkt := mustParseXMP(t, rdfDoc("\n <rdf:Description xmlns:d=\"urn:d/\">\n"+
		`<t:a xml:lang="en"> <rdf:Bag> <rdf:li>1</rdf:li> </rdf:Bag> </t:a>`+
		`<t:b xml:lang="en" rdf:parseType="Resource" rdf:ID="j"> <t:c>2</t:c> </t:b>`+
		`<e xmlns="urn:e/">3</e>`+
		"\n</rdf:Description>\n"))
	a, b, e := pkt.prop("urn:t/", "a"), pkt.prop("urn:t/", "b"), pkt.prop("urn:e/", "e")
	if a == nil || a.form != xmpBag || !a.hasLang || len(a.children) != 1 {
		t.Errorf("a = %+v", a)
	}
	if b == nil || b.form != xmpStructure || !b.hasLang || len(b.children) != 1 {
		t.Errorf("b = %+v", b)
	}
	if e == nil || e.value != "3" {
		t.Errorf("default-namespace property = %+v", e)
	}
}

func TestParseXMPPacketEmptyElementVariants(t *testing.T) {
	pkt := mustParseXMP(t, desc(
		`<t:a rdf:ID="i" rdf:nodeID="n"/>`+
			`<t:b t:x="1" t:y="2" t:z="3" t:w="4"/>`))
	if a := pkt.prop("urn:t/", "a"); a.form != xmpSimple || a.value != "" {
		t.Errorf("a = %+v, want an empty simple value", a)
	}
	if b := pkt.prop("urn:t/", "b"); b.form != xmpStructure || len(b.children) != 4 {
		t.Errorf("b = %+v, want a four-field structure", b)
	}
}

func TestAttrTermUnqualifiedOnRDFElement(t *testing.T) {
	rdfEl := &xmlElem{uri: nsRDF, local: "Description"}
	if got := attrTerm(xmlAttr{local: "ID"}, rdfEl); got != rdfID {
		t.Errorf("unqualified ID on an rdf: element = %d, want rdfID", got)
	}
	if got := attrTerm(xmlAttr{local: "ID"}, &xmlElem{uri: "urn:t/"}); got != rdfOther {
		t.Errorf("unqualified ID elsewhere = %d, want rdfOther", got)
	}
	for local, want := range map[string]rdfTerm{
		"RDF": rdfRDF, "parseType": rdfParseType, "resource": rdfResource, "datatype": rdfDatatype,
		"li": rdfLi, "aboutEach": rdfOld, "bagID": rdfOld, "value": rdfOther,
	} {
		if got := termOf(nsRDF, local); got != want {
			t.Errorf("termOf(rdf:%s) = %d, want %d", local, got, want)
		}
	}
}

func TestParseXMPPacketOldTermProperty(t *testing.T) {
	if _, err := parseXMPPacket([]byte(desc(`<rdf:bagID>1</rdf:bagID>`))); err == nil {
		t.Error("an rdf:bagID property element was accepted")
	}
	if !strings.Contains(errBadRDF.Error(), "RDF") {
		t.Error("errBadRDF should name RDF")
	}
}

// TestParseXMPPacketEncodingDeclaration verifies that a declaration naming
// the encoding a packet had before it was decoded to UTF-8 is accepted.
func TestParseXMPPacketEncodingDeclaration(t *testing.T) {
	pkt := mustParseXMP(t, `<?xml version="1.0" encoding="UTF-16"?>`+desc(`<dc:format>a/b</dc:format>`))
	if len(pkt.props) != 1 {
		t.Errorf("got %d properties, want 1", len(pkt.props))
	}
}
