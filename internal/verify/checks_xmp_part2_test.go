package verify

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// extNS binds the extension schema prefixes.
const extNS = ` xmlns:pdfaExtension="` + nsExt + `" xmlns:pdfaSchema="` + nsScm +
	`" xmlns:pdfaProperty="` + nsProp + `" xmlns:pdfaType="` + nsType + `" xmlns:pdfaField="` + nsFld + `"`

// extDesc returns a packet whose extension schemas are schemas (rdf:li
// items) and whose other properties are props.
func extDesc(schemas, props string) string {
	return rdfDoc(`<rdf:Description rdf:about=""` + extNS + `><pdfaExtension:schemas><rdf:Bag>` +
		schemas + `</rdf:Bag></pdfaExtension:schemas>` + props + `</rdf:Description>`)
}

// goodSchema is an extension schema for urn:t/ with a property p of the
// structured type T, a property q of type Text, and a property r whose type
// is unknown.
const goodSchema = `<rdf:li rdf:parseType="Resource">
<pdfaSchema:schema>S</pdfaSchema:schema><pdfaSchema:namespaceURI>urn:t/</pdfaSchema:namespaceURI><pdfaSchema:prefix>t</pdfaSchema:prefix>
<pdfaSchema:property><rdf:Seq>
 <rdf:li rdf:parseType="Resource"><pdfaProperty:name>p</pdfaProperty:name><pdfaProperty:valueType>T</pdfaProperty:valueType><pdfaProperty:category>external</pdfaProperty:category><pdfaProperty:description>d</pdfaProperty:description></rdf:li>
 <rdf:li rdf:parseType="Resource"><pdfaProperty:name>q</pdfaProperty:name><pdfaProperty:valueType>Plain</pdfaProperty:valueType><pdfaProperty:category>internal</pdfaProperty:category><pdfaProperty:description>d</pdfaProperty:description></rdf:li>
 <rdf:li rdf:parseType="Resource"><pdfaProperty:name>q</pdfaProperty:name><pdfaProperty:valueType>Integer</pdfaProperty:valueType><pdfaProperty:category>internal</pdfaProperty:category><pdfaProperty:description>dup</pdfaProperty:description></rdf:li>
</rdf:Seq></pdfaSchema:property>
<pdfaSchema:valueType><rdf:Seq>
 <rdf:li rdf:parseType="Resource"><pdfaType:type>T</pdfaType:type><pdfaType:namespaceURI>urn:f/</pdfaType:namespaceURI><pdfaType:prefix>f</pdfaType:prefix><pdfaType:description>d</pdfaType:description>
  <pdfaType:field><rdf:Seq><rdf:li rdf:parseType="Resource"><pdfaField:name>n</pdfaField:name><pdfaField:valueType>Integer</pdfaField:valueType><pdfaField:description>d</pdfaField:description></rdf:li></rdf:Seq></pdfaType:field></rdf:li>
 <rdf:li rdf:parseType="Resource"><pdfaType:type>Plain</pdfaType:type><pdfaType:namespaceURI>urn:p/</pdfaType:namespaceURI><pdfaType:prefix>p</pdfaType:prefix><pdfaType:description>d</pdfaType:description></rdf:li>
</rdf:Seq></pdfaSchema:valueType>
</rdf:li>`

// oddSchemas are extension schemas that define nothing or only part of what
// they declare: value types without a namespace or a name, a schema without
// a property array, one without a namespace URI, and properties without a
// known value type.
const oddSchemas = `<rdf:li rdf:parseType="Resource"><pdfaSchema:namespaceURI>urn:odd/</pdfaSchema:namespaceURI>
<pdfaSchema:property><rdf:Seq>
 <rdf:li rdf:parseType="Resource"><pdfaProperty:name>x</pdfaProperty:name><pdfaProperty:valueType>Missing</pdfaProperty:valueType></rdf:li>
 <rdf:li rdf:parseType="Resource"><pdfaProperty:name>y</pdfaProperty:name></rdf:li>
</rdf:Seq></pdfaSchema:property>
<pdfaSchema:valueType><rdf:Seq>
 <rdf:li rdf:parseType="Resource"><pdfaType:type>NoNS</pdfaType:type><pdfaType:field><rdf:Seq><rdf:li rdf:parseType="Resource"><pdfaField:name>n</pdfaField:name><pdfaField:valueType>Text</pdfaField:valueType></rdf:li></rdf:Seq></pdfaType:field></rdf:li>
 <rdf:li rdf:parseType="Resource"><pdfaType:description>no type name</pdfaType:description><pdfaType:field>flat</pdfaType:field><t:other/></rdf:li>
</rdf:Seq></pdfaSchema:valueType>
</rdf:li>
<rdf:li rdf:parseType="Resource"><pdfaSchema:namespaceURI>urn:none/</pdfaSchema:namespaceURI></rdf:li>
<rdf:li rdf:parseType="Resource"><pdfaSchema:property><rdf:Seq/></pdfaSchema:property></rdf:li>
<rdf:li rdf:parseType="Resource"><pdfaSchema:namespaceURI>urn:flat/</pdfaSchema:namespaceURI><pdfaSchema:property>flat</pdfaSchema:property><pdfaSchema:valueType>flat</pdfaSchema:valueType></rdf:li>`

// checkNames returns the names of the checks errs report.
func checkNames(errs []pdf.PDFError) []string {
	var out []string
	for _, e := range errs {
		out = append(out, e.Check().Name())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func TestXMPExtensionDefs(t *testing.T) {
	defs := xmpExtensionDefs(mustParseXMP(t, extDesc(goodSchema+oddSchemas, "")).schemas, nil)
	def := defs["urn:t/"]
	if def == nil || def.props["p"] != "T" || def.props["q"] != "Plain" {
		t.Fatalf("urn:t/ = %+v, want p:T and q:Plain (the first q wins)", def)
	}
	if st, ok := def.types.structs["t"]; !ok || st.ns != "urn:f/" || st.fields["n"] != "integer" {
		t.Errorf("type T = %+v", st)
	}
	if _, ok := def.types.simple["plain"]; !ok {
		t.Error("a value type without fields should be Text")
	}
	odd := defs["urn:odd/"]
	if odd == nil || len(odd.props) != 0 {
		t.Fatalf("urn:odd/ = %+v, want a schema without properties", odd)
	}
	if _, ok := odd.types.structs["nons"]; ok {
		t.Error("a value type with fields but no namespace should define nothing")
	}
	if defs["urn:none/"] != nil || defs["urn:flat/"] != nil || len(defs) != 2 {
		t.Errorf("defs = %v, want only urn:t/ and urn:odd/", defs)
	}
	if len(xmpExtensionDefs(nil, nil)) != 0 || len(xmpExtensionDefs(&xmpNode{}, nil)) != 0 {
		t.Error("no container, or a container that is not an array, should define nothing")
	}
}

// TestXMPExtensionDefsInheritMainTypes verifies that a schema of another
// packet can use the value types the main packet defines for its namespace.
func TestXMPExtensionDefsInheritMainTypes(t *testing.T) {
	main := xmpExtensionDefs(mustParseXMP(t, extDesc(goodSchema, "")).schemas, nil)
	other := `<rdf:li rdf:parseType="Resource"><pdfaSchema:namespaceURI>urn:t/</pdfaSchema:namespaceURI><pdfaSchema:property><rdf:Seq>` +
		`<rdf:li rdf:parseType="Resource"><pdfaProperty:name>s</pdfaProperty:name><pdfaProperty:valueType>T</pdfaProperty:valueType></rdf:li>` +
		`</rdf:Seq></pdfaSchema:property></rdf:li>`
	defs := xmpExtensionDefs(mustParseXMP(t, extDesc(other, "")).schemas, main)
	if defs["urn:t/"].props["s"] != "T" {
		t.Errorf("urn:t/ = %+v, want s of the main packet's type T", defs["urn:t/"])
	}
	if _, ok := main["urn:t/"].types.simple["s"]; ok {
		t.Error("the main packet's types changed")
	}
}

func TestCheckXMPPackagePart2Properties(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props string
		want  []string
	}{
		{"predefined", `<dc:format>a/b</dc:format>`, nil},
		{"predefined wrong type", `<dc:format>ab</dc:format>`, []string{"XMPNoCorrespondingType"}},
		{"undefined", `<xmp:Title>t</xmp:Title>`, []string{"MetadataUndeclaredProperty"}},
		{"extension struct", `<t:p rdf:parseType="Resource" xmlns:f="urn:f/"><f:n>3</f:n></t:p>`, nil},
		{"extension struct wrong field", `<t:p rdf:parseType="Resource" xmlns:f="urn:f/"><f:n>three</f:n></t:p>`, []string{"XMPNoCorrespondingType"}},
		{"extension text", `<t:q>x</t:q>`, nil},
		{"extension undefined name", `<t:r>x</t:r>`, []string{"MetadataUndeclaredProperty"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xmp := extDesc(goodSchema, tc.props)
			got := checkNames(checkObjectXMPPart2([]byte(xmp), nil))
			if !slices.Equal(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckXMPPackagePart2MainDefinitions(t *testing.T) {
	main := xmpExtensionDefs(mustParseXMP(t, extDesc(goodSchema, "")).schemas, nil)
	xmp := desc(`<t:q>x</t:q>`)
	if errs := checkObjectXMPPart2([]byte(xmp), main); len(errs) != 0 {
		t.Errorf("a property the main packet defines: %v", errs)
	}
	xmp = `<?xpacket begin="" bytes="1" encoding="UTF-8"?>` + desc(`<t:q>x</t:q>`)
	got := checkNames(checkObjectXMPPart2([]byte(xmp), nil))
	want := []string{"MetadataUndeclaredProperty", "XPacketBytesAttribute", "XPacketEncodingAttribute"}
	if !slices.Equal(got, want) {
		t.Errorf("checks = %v, want %v", got, want)
	}
}

func TestCheckExtensionSchemaObjects(t *testing.T) {
	li := func(body string) string { return `<rdf:li rdf:parseType="Resource">` + body + `</rdf:li>` }
	schemaHead := `<pdfaSchema:schema>S</pdfaSchema:schema><pdfaSchema:namespaceURI>urn:t/</pdfaSchema:namespaceURI><pdfaSchema:prefix>t</pdfaSchema:prefix>`
	prop := func(body string) string {
		return li(schemaHead + `<pdfaSchema:property><rdf:Seq>` + li(body) + `</rdf:Seq></pdfaSchema:property>`)
	}
	goodProp := `<pdfaProperty:name>p</pdfaProperty:name><pdfaProperty:valueType>Text</pdfaProperty:valueType><pdfaProperty:category>internal</pdfaProperty:category><pdfaProperty:description>d</pdfaProperty:description>`
	vtype := func(body string) string {
		return li(schemaHead + `<pdfaSchema:property><rdf:Seq>` + li(goodProp) + `</rdf:Seq></pdfaSchema:property>` +
			`<pdfaSchema:valueType><rdf:Seq>` + li(body) + `</rdf:Seq></pdfaSchema:valueType>`)
	}
	goodType := `<pdfaType:type>T</pdfaType:type><pdfaType:namespaceURI>urn:f/</pdfaType:namespaceURI><pdfaType:prefix>f</pdfaType:prefix><pdfaType:description>d</pdfaType:description>`
	field := func(body string) string {
		return vtype(goodType + `<pdfaType:field><rdf:Seq>` + li(body) + `</rdf:Seq></pdfaType:field>`)
	}
	for _, tc := range []struct {
		name    string
		schemas string
		want    []string
	}{
		{"valid", goodSchema, nil},
		{"schema missing fields", li(``), []string{"ExtSchemaDefinitionInvalid"}},
		{"schema field not text", li(`<pdfaSchema:schema><rdf:Bag/></pdfaSchema:schema>` + schemaHead[len(`<pdfaSchema:schema>S</pdfaSchema:schema>`):]), []string{"ExtSchemaDefinitionInvalid"}},
		{"schema field prefix", li(`<s2:schema xmlns:s2="` + nsScm + `">S</s2:schema>` + schemaHead[len(`<pdfaSchema:schema>S</pdfaSchema:schema>`):]), []string{"ExtSchemaDefinitionInvalid"}},
		{"schema property not a seq", li(schemaHead + `<pdfaSchema:property><rdf:Bag/></pdfaSchema:property>`), []string{"ExtSchemaDefinitionInvalid"}},
		{"schema valueType prefix", li(schemaHead + `<s2:valueType xmlns:s2="` + nsScm + `"><rdf:Seq/></s2:valueType>`), []string{"ExtSchemaDefinitionInvalid"}},
		{"schema undefined field", li(schemaHead + `<pdfaSchema:extra>x</pdfaSchema:extra><t:other>x</t:other>`), []string{"ExtSchemaUndefinedField"}},
		{"property missing fields", prop(``), []string{"ExtPropertyMissingField", "ExtPropertyUndefinedType"}},
		{"property bad category", prop(strings.Replace(goodProp, "internal", "neither", 1)), []string{"ExtPropertyMissingField"}},
		{"property undefined type", prop(strings.Replace(goodProp, ">Text<", ">Nope<", 1)), []string{"ExtPropertyUndefinedType"}},
		{"property undefined field", prop(goodProp + `<pdfaProperty:extra>x</pdfaProperty:extra>`), []string{"ExtSchemaUndefinedField"}},
		{"value type missing fields", vtype(``), []string{"ExtTypeInvalid"}},
		{"value type field not a seq", vtype(goodType + `<pdfaType:field>flat</pdfaType:field>`), []string{"ExtTypeInvalid"}},
		{"value type undefined field", vtype(goodType + `<pdfaType:extra>x</pdfaType:extra>`), []string{"ExtSchemaUndefinedField"}},
		{"field missing fields", field(``), []string{"ExtFieldInvalid"}},
		{"field undefined type", field(`<pdfaField:name>n</pdfaField:name><pdfaField:valueType>Nope</pdfaField:valueType><pdfaField:description>d</pdfaField:description>`), []string{"ExtFieldInvalid"}},
		{"field undefined field", field(`<pdfaField:name>n</pdfaField:name><pdfaField:valueType>Text</pdfaField:valueType><pdfaField:description>d</pdfaField:description><pdfaField:extra>x</pdfaField:extra>`), []string{"ExtSchemaUndefinedField"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkt := mustParseXMP(t, extDesc(tc.schemas, ""))
			got := checkNames(checkExtensionSchemaObjects(pkt.schemas, xmpExtensionDefs(pkt.schemas, nil)))
			if !slices.Equal(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckExtensionSchemaContainer(t *testing.T) {
	if errs := checkExtensionSchemaObjects(nil, nil); errs != nil {
		t.Errorf("no container: %v", errs)
	}
	for name, xmp := range map[string]string{
		"seq":    rdfDoc(`<rdf:Description` + extNS + `><pdfaExtension:schemas><rdf:Seq/></pdfaExtension:schemas></rdf:Description>`),
		"prefix": rdfDoc(`<rdf:Description xmlns:e="` + nsExt + `"><e:schemas><rdf:Bag/></e:schemas></rdf:Description>`),
		"simple": rdfDoc(`<rdf:Description` + extNS + `><pdfaExtension:schemas>x</pdfaExtension:schemas></rdf:Description>`),
	} {
		pkt := mustParseXMP(t, xmp)
		if got := checkNames(checkExtensionSchemaObjects(pkt.schemas, nil)); !slices.Equal(got, []string{"ExtSchemasNotBag"}) {
			t.Errorf("%s: checks = %v, want ExtSchemasNotBag", name, got)
		}
	}
}

func TestCheckPDFAIdentificationPart2(t *testing.T) {
	id := func(attrs string) string {
		return rdfDoc(`<rdf:Description xmlns:pdfaid="` + pdfaIDNamespace + `" xmlns:id="` + pdfaIDNamespace + `" ` + attrs + `/>`)
	}
	for _, tc := range []struct {
		name string
		xmp  string
		want []string
	}{
		{"valid", id(`pdfaid:part="2" pdfaid:conformance="B" pdfaid:corr="1:2007"`), nil},
		{"missing", desc(`<dc:format>a/b</dc:format>`), []string{"PDFAIdentifierMissing"}},
		{"no part", id(`pdfaid:conformance="B"`), []string{"PDFAPartNumber"}},
		{"wrong part", id(`pdfaid:part="1" pdfaid:conformance="U"`), []string{"PDFAPartNumber"}},
		{"bad conformance", id(`pdfaid:part="2" pdfaid:conformance="b"`), []string{"PDFAConformanceLevel"}},
		{"prefix", id(`id:part="2" id:conformance="A" id:amd="x"`), []string{"PDFAIdentifierNamespace"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkNames(checkPDFAIdentificationPart2(mustParseXMP(t, tc.xmp), a2b()))
			if !slices.Equal(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
	pkt := mustParseXMP(t, desc(`<pdfaid:part xmlns:pdfaid="`+pdfaIDNamespace+`"><rdf:Bag/></pdfaid:part>`))
	if simpleValue(pkt.props[0]) != "" || simpleValue(nil) != "" {
		t.Error("simpleValue of an array or of nothing should be empty")
	}
}

// metadataDoc returns a 2b-shaped PDF whose catalog metadata stream has the
// given dictionary entries and XMP, plus a page carrying a second metadata
// stream with pageXMP when that is not empty.
func metadataDoc(metaDict, xmp, pageXMP string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Metadata 3 0 R >>")
	kids := ""
	if pageXMP != "" {
		kids = "4 0 R"
	}
	b.Obj(2, "<< /Type /Pages /Count "+map[bool]string{true: "1", false: "0"}[pageXMP != ""]+" /Kids ["+kids+"] >>")
	b.Obj(3, fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", metaDict, len(xmp)+1, xmp))
	size := 4
	if pageXMP != "" {
		b.Obj(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Metadata 5 0 R >>")
		b.Obj(5, fmt.Sprintf("<< /Type /Metadata /Subtype /XML /Length %d >>\nstream\n%s\nendstream", len(pageXMP)+1, pageXMP))
		size = 6
	}
	return b.FinishClassic(fmt.Sprintf("<< /Size %d /Root 1 0 R >>", size))
}

func TestVerifyXMPMetadataPart2(t *testing.T) {
	ident := `<rdf:Description xmlns:pdfaid="` + pdfaIDNamespace + `" pdfaid:part="2" pdfaid:conformance="B"/>`
	good := rdfDoc(ident)
	for _, tc := range []struct {
		name, dict, xmp string
		want            []string
	}{
		{"valid", "/Type /Metadata /Subtype /XML", good, nil},
		{"filter allowed", "/Type /Metadata /Subtype /XML /Filter /ASCIIHexDecode", hexOf(good) + ">", nil},
		{"wrong subtype", "/Type /Metadata", good, []string{"MetadataMissing"}},
		{"unreadable", "/Type /Metadata /Subtype /XML /Filter /FlateDecode", good, []string{"XMPStreamUnreadable"}},
		{"bad RDF", "/Type /Metadata /Subtype /XML", rdfDoc(`text`), []string{"PDFAIdentifierMissing", "XMPNotWellFormed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := pdf.OpenBytes(metadataDoc(tc.dict, tc.xmp, ""))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if got := checkNames(xmpMetadataRule(d, nil, &ValidationContext{Spec: a2bSpec})); !slices.Equal(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVerifyXMPMetadataPart2NoPacket(t *testing.T) {
	d, err := pdf.OpenBytes(catalogDoc(""))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got := checkNames(verifyXMPMetadataPart2(d, &ValidationContext{Spec: a2bSpec}))
	if !slices.Equal(got, []string{"MetadataMissing"}) {
		t.Errorf("checks = %v, want MetadataMissing", got)
	}
}

func hexOf(s string) string { return fmt.Sprintf("%x", s) }

func TestCheckNonCatalogXMPStreamsPart2(t *testing.T) {
	main := extDesc(goodSchema, `<pdfaid:part xmlns:pdfaid="`+pdfaIDNamespace+`">2</pdfaid:part>`)
	for _, tc := range []struct {
		name, mainXMP, pageXMP string
		want                   []string
	}{
		{"defined by the main packet", main, desc(`<t:q>x</t:q>`), nil},
		{"undefined", desc(`<dc:format>a/b</dc:format>`), desc(`<t:q>x</t:q>`), []string{"MetadataUndeclaredProperty"}},
		{"bad RDF", main, rdfDoc(`text`), []string{"XMPNotWellFormed"}},
		{"main packet does not parse", rdfDoc(`text`), desc(`<dc:format>a/b</dc:format>`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := pdf.OpenBytes(metadataDoc("/Type /Metadata /Subtype /XML", tc.mainXMP, tc.pageXMP))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			graph, err := d.ResolveGraph()
			if err != nil {
				t.Fatal(err)
			}
			ctx := &ValidationContext{Spec: a2bSpec}
			if got := checkNames(checkNonCatalogXMPStreams(d, graph, ctx)); !slices.Equal(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCheckNonCatalogXMPStreamsMainPacket verifies that the document's own
// packet is read once for several other packets, and that other packets are
// checked when the catalog has no metadata stream.
func TestCheckNonCatalogXMPStreamsMainPacket(t *testing.T) {
	page := desc(`<t:q>x</t:q>`)
	stream := func(xmp string) string {
		return fmt.Sprintf("<< /Type /Metadata /Subtype /XML /Length %d >>\nstream\n%s\nendstream", len(xmp)+1, xmp)
	}
	for _, tc := range []struct {
		name, catalog string
		want          []string
	}{
		{"two packets", "/Metadata 5 0 R", nil},
		{"no catalog packet", "", []string{"MetadataUndeclaredProperty"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := pdfgen.NewBuilder("%PDF-1.7\n")
			b.Obj(1, "<< /Type /Catalog /Pages 2 0 R "+tc.catalog+" >>")
			b.Obj(2, "<< /Type /Pages /Count 2 /Kids [3 0 R 4 0 R] >>")
			b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1 1] /Metadata 6 0 R >>")
			b.Obj(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1 1] /Metadata 7 0 R >>")
			b.Obj(5, stream(extDesc(goodSchema, "")))
			b.Obj(6, stream(page))
			b.Obj(7, stream(page))
			d, err := pdf.OpenBytes(b.FinishClassic("<< /Size 8 /Root 1 0 R >>"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			graph, err := d.ResolveGraph()
			if err != nil {
				t.Fatal(err)
			}
			got := checkNames(checkNonCatalogXMPStreams(d, graph, &ValidationContext{Spec: a2bSpec}))
			if !slices.Equal(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}
}
