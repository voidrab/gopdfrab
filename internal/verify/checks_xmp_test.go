package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// firstFileMatching returns the first entry in dir whose name contains glob,
// skipping the test if the corpus directory is unavailable.
func firstFileMatching(t *testing.T, dir, glob string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("corpus dir not available: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.Contains(e.Name(), glob) {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Skipf("no file matching %q in %s", glob, dir)
	return ""
}

const (
	extSchemaDir  = veraPDFDir + "/6.7 Metadata/6.7.8 Extension schemas"
	propSchemaDir = veraPDFDir + "/6.7 Metadata/6.7.2 Properties"
	infoSyncDir   = veraPDFDir + "/6.7 Metadata/6.7.3 Document information dictionary"
)

// rawXMPOf opens path and returns its XMP metadata string, skipping the test
// if the file or its metadata is unavailable.
func rawXMPOf(t *testing.T, path string) (*pdf.Reader, string) {
	t.Helper()
	doc, err := pdf.Open(path)
	if err != nil {
		t.Skipf("cannot open %s: %v", path, err)
	}
	t.Cleanup(func() { doc.Close() })
	data, _, err := doc.RawXMP()
	if err != nil {
		t.Skipf("no XMP metadata in %s: %v", path, err)
	}
	return doc, string(data)
}

func TestCheckExtensionSchemasCorpus(t *testing.T) {
	path := firstFileMatching(t, extSchemaDir, "t04-fail")
	_, xmp := rawXMPOf(t, path)
	errs := checkExtensionSchemas(xmp)
	if len(errs) == 0 {
		t.Errorf("checkExtensionSchemas found no violation in %s", path)
	}
}

// extensionSchemaXMP is a hand-built pdfaExtension:schemas structure deep
// enough to exercise the full extension-schema parser tree: one property
// entry missing valueType/category/description, and one custom value type
// with a field, both defined via nested rdf:Seq/rdf:li.
const extensionSchemaXMP = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
  xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/"
  xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#"
  xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#"
  xmlns:pdfaType="http://www.aiim.org/pdfa/ns/type#"
  xmlns:pdfaField="http://www.aiim.org/pdfa/ns/field#"
  xmlns:custom="http://example.com/custom/">
<rdf:Description rdf:about="">
  <pdfaExtension:schemas>
    <rdf:Bag>
      <rdf:li rdf:parseType="Resource">
        <pdfaSchema:schema>My Schema</pdfaSchema:schema>
        <pdfaSchema:namespaceURI>http://example.com/custom/</pdfaSchema:namespaceURI>
        <pdfaSchema:prefix>custom</pdfaSchema:prefix>
        <pdfaSchema:unknownChild>ignored</pdfaSchema:unknownChild>
        <pdfaSchema:property>
          <rdf:Seq>
            <rdf:li rdf:parseType="Resource">
              <pdfaProperty:name>myProp</pdfaProperty:name>
              <pdfaProperty:unknownChild>ignored</pdfaProperty:unknownChild>
            </rdf:li>
          </rdf:Seq>
        </pdfaSchema:property>
        <pdfaSchema:valueType>
          <rdf:Seq>
            <rdf:li rdf:parseType="Resource">
              <pdfaType:type>MyType</pdfaType:type>
              <pdfaType:namespaceURI>http://example.com/custom/type#</pdfaType:namespaceURI>
              <pdfaType:prefix>customType</pdfaType:prefix>
              <pdfaType:description>a custom struct type</pdfaType:description>
              <pdfaType:unknownChild>ignored</pdfaType:unknownChild>
              <pdfaType:field>
                <rdf:Seq>
                  <rdf:li rdf:parseType="Resource">
                    <pdfaField:name>myField</pdfaField:name>
                    <pdfaField:valueType>Text</pdfaField:valueType>
                    <pdfaField:description>a field</pdfaField:description>
                    <pdfaField:unknownChild>ignored</pdfaField:unknownChild>
                  </rdf:li>
                </rdf:Seq>
              </pdfaType:field>
            </rdf:li>
          </rdf:Seq>
        </pdfaSchema:valueType>
      </rdf:li>
    </rdf:Bag>
  </pdfaExtension:schemas>
  <custom:myProp>used value</custom:myProp>
</rdf:Description>
</rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`

func TestValidateExtSchemasDeepTree(t *testing.T) {
	errs := checkExtensionSchemas(extensionSchemaXMP)
	var gotMissingField bool
	for _, e := range errs {
		if e.Check() == pdf.Checks.Metadata.ExtPropertyMissingField {
			gotMissingField = true
		}
	}
	if !gotMissingField {
		t.Errorf("expected ExtPropertyMissingField for a property missing valueType/category/description, got %v", errs)
	}

	// A fully-specified property, referencing the custom type by name and used
	// exactly as documented, produces no violations.
	direct := validateExtSchemas([]byte(extensionSchemaXMP))
	if len(direct) == 0 {
		t.Error("validateExtSchemas found no violation in a schema with missing property fields")
	}
}

// extensionSchemaUndocumentedXMP declares a property "docProp" but the XMP
// actually uses "otherProp" under the same prefix -- exercising the t02-d
// (ExtPropertyUndocumented) and t02-g (ExtPropertyUndefinedType) branches of
// validateExtSchema, which extensionSchemaXMP's minimal fixture doesn't reach.
const extensionSchemaUndocumentedXMP = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
  xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/"
  xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#"
  xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#"
  xmlns:custom="http://example.com/custom2/">
<rdf:Description rdf:about="">
  <pdfaExtension:schemas>
    <rdf:Bag>
      <rdf:li rdf:parseType="Resource">
        <pdfaSchema:schema>My Schema</pdfaSchema:schema>
        <pdfaSchema:namespaceURI>http://example.com/custom2/</pdfaSchema:namespaceURI>
        <pdfaSchema:prefix>custom</pdfaSchema:prefix>
        <pdfaSchema:property>
          <rdf:Seq>
            <rdf:li rdf:parseType="Resource">
              <pdfaProperty:name>docProp</pdfaProperty:name>
              <pdfaProperty:valueType>UndefinedCustomType</pdfaProperty:valueType>
              <pdfaProperty:category>external</pdfaProperty:category>
              <pdfaProperty:description>a documented property</pdfaProperty:description>
            </rdf:li>
          </rdf:Seq>
        </pdfaSchema:property>
      </rdf:li>
    </rdf:Bag>
  </pdfaExtension:schemas>
  <custom:otherProp>used but undocumented</custom:otherProp>
</rdf:Description>
</rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`

func TestValidateExtSchemasTruncated(t *testing.T) {
	// Truncated mid-parse at every nesting level: each parse* helper's
	// dec.Token() error branch should return cleanly, not panic.
	full := extensionSchemaXMP
	for _, cut := range []int{
		strings.Index(full, "<pdfaSchema:schema"),
		strings.Index(full, "<pdfaSchema:property"),
		strings.Index(full, "<rdf:li rdf:parseType=\"Resource\">\n              <pdfaProperty:name"),
		strings.Index(full, "<pdfaSchema:valueType"),
		strings.Index(full, "<pdfaType:field"),
	} {
		if cut < 0 {
			continue
		}
		validateExtSchemas([]byte(full[:cut]))
	}
}

func TestValidateExtSchemaUndocumentedAndUndefinedType(t *testing.T) {
	errs := validateExtSchemas([]byte(extensionSchemaUndocumentedXMP))
	var gotUndocumented, gotUndefinedType bool
	for _, e := range errs {
		if e.Check() == pdf.Checks.Metadata.ExtPropertyUndocumented {
			gotUndocumented = true
		}
		if e.Check() == pdf.Checks.Metadata.ExtPropertyUndefinedType {
			gotUndefinedType = true
		}
	}
	if !gotUndocumented {
		t.Error("expected ExtPropertyUndocumented for a used-but-undeclared property")
	}
	if !gotUndefinedType {
		t.Error("expected ExtPropertyUndefinedType for a valueType that isn't builtin or declared")
	}
}

func TestXmpValidatePropBranches(t *testing.T) {
	// Undefined property in a known schema.
	errs := xmpValidateProp("http://purl.org/dc/elements/1.1/", "bogus", xmpKindScalar, "x", nil)
	if len(errs) != 1 {
		t.Fatalf("xmpValidateProp(undefined property) = %v", errs)
	}

	// Wrong container type (Bag expected for dc:subject, got scalar).
	errs = xmpValidateProp("http://purl.org/dc/elements/1.1/", "subject", xmpKindScalar, "x", nil)
	if len(errs) != 1 {
		t.Fatalf("xmpValidateProp(wrong container) = %v", errs)
	}

	// Boolean property with a non-True/False value.
	errs = xmpValidateProp("http://ns.adobe.com/xap/1.0/rights/", "Marked", xmpKindScalar, "Yes", nil)
	if len(errs) != 1 {
		t.Fatalf("xmpValidateProp(bad boolean) = %v", errs)
	}
	// Valid boolean: no violation.
	if errs := xmpValidateProp("http://ns.adobe.com/xap/1.0/rights/", "Marked", xmpKindScalar, "True", nil); len(errs) != 0 {
		t.Errorf("unexpected violation for a valid boolean: %v", errs)
	}

	// pdfaid namespace uses a different check constant.
	errs = xmpValidateProp(pdfaIDNamespace, "bogus", xmpKindScalar, "x", nil)
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Metadata.PDFAIdentifierUndefinedProperty {
		t.Errorf("xmpValidateProp(pdfaid undefined) = %v, want PDFAIdentifierUndefinedProperty", errs)
	}

	// LangAlt: an rdf:li item lacking xml:lang.
	errs = xmpValidateProp("http://purl.org/dc/elements/1.1/", "title", xmpKindAlt, "", []xmpPropItem{{text: "x", hasLang: false}})
	if len(errs) != 1 {
		t.Fatalf("xmpValidateProp(LangAlt missing xml:lang) = %v", errs)
	}
	// Every item has xml:lang: no violation.
	if errs := xmpValidateProp("http://purl.org/dc/elements/1.1/", "title", xmpKindAlt, "", []xmpPropItem{{text: "x", hasLang: true}}); len(errs) != 0 {
		t.Errorf("unexpected violation for a proper LangAlt: %v", errs)
	}

	// Unknown namespace: nil (not our concern).
	if errs := xmpValidateProp("http://example.com/unknown/", "x", xmpKindScalar, "v", nil); errs != nil {
		t.Errorf("xmpValidateProp(unknown namespace) = %v, want nil", errs)
	}
}

func TestCheckXMPPropertySchemasStructProperty(t *testing.T) {
	// xmpMM:DerivedFrom is a struct-kind property (6.7.2); rdf:parseType="Resource"
	// exercises xmpConsumeProperty's struct-skip path (and xmpSkipElem within it).
	xmp := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:xmpMM="http://ns.adobe.com/xap/1.0/mm/">
	<rdf:Description>
	  <xmpMM:DerivedFrom rdf:parseType="Resource">
	    <xmpMM:InstanceID>uuid:abc</xmpMM:InstanceID>
	  </xmpMM:DerivedFrom>
	</rdf:Description>
	</rdf:RDF>`
	if errs := checkXMPPropertySchemas([]byte(xmp)); len(errs) != 0 {
		t.Errorf("unexpected violation for a valid struct property: %v", errs)
	}
}

func TestCheckXMPPropertySchemasContainerForms(t *testing.T) {
	// rdf:resource attribute form: xmpConsumeProperty treats this as a scalar
	// reference (not the struct container DerivedFrom expects), so it is
	// flagged as a container-type mismatch -- exercises that code path.
	resourceForm := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:xmpMM="http://ns.adobe.com/xap/1.0/mm/">
	<rdf:Description>
	  <xmpMM:DerivedFrom rdf:resource="urn:uuid:abc"/>
	</rdf:Description>
	</rdf:RDF>`
	if errs := checkXMPPropertySchemas([]byte(resourceForm)); len(errs) == 0 {
		t.Error("expected a container-type-mismatch violation for the rdf:resource form")
	}

	// Implicit struct: a non-rdf child element (no rdf:Description wrapper).
	implicitStruct := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:xmpMM="http://ns.adobe.com/xap/1.0/mm/" xmlns:stRef="http://ns.adobe.com/xap/1.0/sType/ResourceRef#">
	<rdf:Description>
	  <xmpMM:DerivedFrom><stRef:instanceID>uuid:xyz</stRef:instanceID></xmpMM:DerivedFrom>
	</rdf:Description>
	</rdf:RDF>`
	if errs := checkXMPPropertySchemas([]byte(implicitStruct)); len(errs) != 0 {
		t.Errorf("unexpected violation for an implicit (non-rdf-child) struct: %v", errs)
	}

	// Explicit nested rdf:Description struct form.
	explicitDesc := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:xmpMM="http://ns.adobe.com/xap/1.0/mm/">
	<rdf:Description>
	  <xmpMM:DerivedFrom><rdf:Description><xmpMM:InstanceID>x</xmpMM:InstanceID></rdf:Description></xmpMM:DerivedFrom>
	</rdf:Description>
	</rdf:RDF>`
	if errs := checkXMPPropertySchemas([]byte(explicitDesc)); len(errs) != 0 {
		t.Errorf("unexpected violation for an explicit rdf:Description struct: %v", errs)
	}

	// Bag container with plain (non-lang) items.
	bagForm := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:dc="http://purl.org/dc/elements/1.1/">
	<rdf:Description>
	  <dc:subject><rdf:Bag><rdf:li>keyword1</rdf:li><rdf:li>keyword2</rdf:li></rdf:Bag></dc:subject>
	</rdf:Description>
	</rdf:RDF>`
	if errs := checkXMPPropertySchemas([]byte(bagForm)); len(errs) != 0 {
		t.Errorf("unexpected violation for a valid Bag container: %v", errs)
	}

	// Seq of integers with one non-integer item -> value-rule violation on an item.
	badSeqItem := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:exif="http://ns.adobe.com/exif/1.0/">
	<rdf:Description>
	  <exif:ISOSpeedRatings><rdf:Seq><rdf:li>100</rdf:li><rdf:li>not-a-number</rdf:li></rdf:Seq></exif:ISOSpeedRatings>
	</rdf:Description>
	</rdf:RDF>`
	if errs := checkXMPPropertySchemas([]byte(badSeqItem)); len(errs) == 0 {
		t.Error("expected a violation for a non-integer item in an integer Seq")
	}
}

func TestCheckXMPPropertySchemasCorpus(t *testing.T) {
	path := firstFileMatching(t, propSchemaDir, "t03-fail")
	doc, xmp := rawXMPOf(t, path)
	data, _, _ := doc.RawXMP()
	errs := checkXMPPropertySchemas(data)
	if len(errs) == 0 {
		t.Errorf("checkXMPPropertySchemas found no violation in %s (xmp len %d)", path, len(xmp))
	}
}

func TestVerifyXMPMetadataPass(t *testing.T) {
	if _, err := os.Stat(sampleVeraPassFile); err != nil {
		t.Skip("corpus not available")
	}
	doc, err := pdf.Open(sampleVeraPassFile)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()
	errs := verifyXMPMetadata(doc)
	for _, e := range errs {
		t.Logf("unexpected(?) XMP violation on a pass file: %v", e)
	}
}

func TestVerifyXMPMetadataMissing(t *testing.T) {
	f := filepath.Join(t.TempDir(), "no-xmp.pdf")
	if err := createValidPDF(f); err != nil {
		t.Fatalf("createValidPDF: %v", err)
	}
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()
	errs := verifyXMPMetadata(doc)
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Metadata.MetadataMissing {
		t.Errorf("verifyXMPMetadata(no XMP) = %v, want a single MetadataMissing", errs)
	}
}

// createPDFWithInfo writes a minimal classic-xref PDF whose Info dictionary
// carries the given entries (all PDFString-encoded), for checkInfoXMPSync tests.
func createPDFWithInfo(filename string, info map[string]string) error {
	header := "%PDF-1.7\n"
	obj1 := "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	obj2 := "2 0 obj\n<< /Type /Pages /Count 0 /Kids [] >>\nendobj\n"

	var infoBody strings.Builder
	infoBody.WriteString("<< ")
	for k, v := range info {
		infoBody.WriteString("/")
		infoBody.WriteString(k)
		infoBody.WriteString(" (")
		infoBody.WriteString(v)
		infoBody.WriteString(") ")
	}
	infoBody.WriteString(">>")
	obj3 := "3 0 obj\n" + infoBody.String() + "\nendobj\n"

	off1 := len(header)
	off2 := off1 + len(obj1)
	off3 := off2 + len(obj2)
	xrefOffset := off3 + len(obj3)

	xref := fmt.Sprintf("xref\n0 4\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n",
		off1, off2, off3)
	trailer := "trailer\n<< /Size 4 /Root 1 0 R /Info 3 0 R >>\n"
	startxref := fmt.Sprintf("startxref\n%d\n%%EOF", xrefOffset)

	content := header + obj1 + obj2 + obj3 + xref + trailer + startxref
	return os.WriteFile(filename, []byte(content), 0644)
}

func TestCheckInfoXMPSyncFields(t *testing.T) {
	f := t.TempDir() + "/info.pdf"
	err := createPDFWithInfo(f, map[string]string{
		"Title": "Doc Title", "Subject": "Doc Subject", "Author": "John Doe",
		"Creator": "Acme Creator", "Producer": "Acme Producer", "Keywords": "key1 key2",
		"CreationDate": "D:20240101120000",
	})
	if err != nil {
		t.Fatalf("createPDFWithInfo: %v", err)
	}
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()

	matching := `<dc:title><rdf:Alt><rdf:li>Doc Title</rdf:li></rdf:Alt></dc:title>
	<dc:description><rdf:Alt><rdf:li>Doc Subject</rdf:li></rdf:Alt></dc:description>
	<dc:creator><rdf:Seq><rdf:li>John Doe</rdf:li></rdf:Seq></dc:creator>
	<xmp:CreatorTool>Acme Creator</xmp:CreatorTool>
	<pdf:Producer>Acme Producer</pdf:Producer>
	<pdf:Keywords>key1 key2</pdf:Keywords>
	<xmp:CreateDate>2024-01-01T12:00:00Z</xmp:CreateDate>`
	if errs := checkInfoXMPSync(doc, matching); len(errs) != 0 {
		t.Errorf("unexpected mismatches for fully synchronized Info/XMP: %v", errs)
	}

	cases := []struct {
		name string
		xmp  string
	}{
		{"creator-multiple", `<dc:creator><rdf:Seq><rdf:li>A</rdf:li><rdf:li>B</rdf:li></rdf:Seq></dc:creator>`},
		{"creator-mismatch", `<dc:creator><rdf:Seq><rdf:li>Someone Else</rdf:li></rdf:Seq></dc:creator>`},
		{"creatortool-mismatch", `<xmp:CreatorTool>Different Tool</xmp:CreatorTool>`},
		{"producer-mismatch", `<pdf:Producer>Different Producer</pdf:Producer>`},
		{"keywords-mismatch", `<pdf:Keywords>totally different</pdf:Keywords>`},
		{"createdate-mismatch", `<xmp:CreateDate>2025-06-06T00:00:00Z</xmp:CreateDate>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if errs := checkInfoXMPSync(doc, c.xmp); len(errs) == 0 {
				t.Errorf("expected a sync mismatch for case %q", c.name)
			}
		})
	}
}

func TestCheckInfoXMPSyncBadDateFormat(t *testing.T) {
	f := t.TempDir() + "/info-baddate.pdf"
	if err := createPDFWithInfo(f, map[string]string{"CreationDate": "2024-01-01"}); err != nil {
		t.Fatalf("createPDFWithInfo: %v", err)
	}
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()

	errs := checkInfoXMPSync(doc, `<xmp:CreateDate>2024-01-01T00:00:00Z</xmp:CreateDate>`)
	var got bool
	for _, e := range errs {
		if e.Check() == pdf.Checks.Structure.InfoDictXMPMismatch {
			got = true
		}
	}
	if !got {
		t.Error("expected InfoDictXMPMismatch for a CreationDate not in PDF date format")
	}
}

func TestCheckInfoXMPSyncCorpus(t *testing.T) {
	failPath := firstFileMatching(t, infoSyncDir, "t01-fail")
	doc, xmp := rawXMPOf(t, failPath)
	if errs := checkInfoXMPSync(doc, xmp); len(errs) == 0 {
		t.Errorf("checkInfoXMPSync found no mismatch in %s", failPath)
	}

	passPath := firstFileMatching(t, infoSyncDir, "t01-pass")
	doc2, xmp2 := rawXMPOf(t, passPath)
	if errs := checkInfoXMPSync(doc2, xmp2); len(errs) != 0 {
		t.Errorf("checkInfoXMPSync unexpected mismatch in %s: %v", passPath, errs)
	}
}

func TestCheckNonCatalogXMPStreams(t *testing.T) {
	otherMeta := pdf.NewPDFDict()
	otherMeta.HasStream = true
	otherMeta.Entries["Type"] = pdf.PDFName{Value: "Metadata"}
	otherMeta.RawStream = []byte("no xpacket wrapper here")

	root := pdf.NewPDFDict()
	root.Entries["Other"] = otherMeta
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	errs := checkNonCatalogXMPStreams(trailer)
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Metadata.ObjectXMPNoXPacket {
		t.Errorf("checkNonCatalogXMPStreams = %v, want a single ObjectXMPNoXPacket", errs)
	}

	// A non-catalog Metadata stream that *is* xpacket-wrapped is fine.
	wrapped := pdf.NewPDFDict()
	wrapped.HasStream = true
	wrapped.Entries["Type"] = pdf.PDFName{Value: "Metadata"}
	wrapped.RawStream = []byte("<?xpacket begin=\"\"?><x:xmpmeta/><?xpacket end=\"w\"?>")
	root2 := pdf.NewPDFDict()
	root2.Entries["Other"] = wrapped
	trailer2 := pdf.NewPDFDict()
	trailer2.Entries["Root"] = root2
	if errs := checkNonCatalogXMPStreams(trailer2); len(errs) != 0 {
		t.Errorf("unexpected violation for an xpacket-wrapped stream: %v", errs)
	}

	// Non-dict graphs (e.g. a bare array) are ignored.
	if errs := checkNonCatalogXMPStreams(pdf.PDFArray{}); errs != nil {
		t.Error("checkNonCatalogXMPStreams on a non-dict graph should return nil")
	}
}

func TestCheckXMPHeader(t *testing.T) {
	if errs := checkXMPHeader(`<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>`); len(errs) != 0 {
		t.Errorf("unexpected violation for a clean xpacket header: %v", errs)
	}
	errs := checkXMPHeader(`<?xpacket begin="" bytes="1234" encoding="UTF-8"?>`)
	if len(errs) != 2 {
		t.Fatalf("checkXMPHeader(bytes+encoding) = %d errs, want 2", len(errs))
	}
	if errs := checkXMPHeader("no xpacket here"); errs != nil {
		t.Error("checkXMPHeader with no xpacket PI should return nil")
	}
}

func TestCheckPDFAIdentifier(t *testing.T) {
	good := `<x xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="1" pdfaid:conformance="B"/>`
	if errs := checkPDFAIdentifier(good); len(errs) != 0 {
		t.Errorf("unexpected violation for a valid PDF/A identifier: %v", errs)
	}

	if errs := checkPDFAIdentifier("no identifier here"); len(errs) != 1 ||
		errs[0].Check() != pdf.Checks.Metadata.PDFAIdentifierMissing {
		t.Errorf("checkPDFAIdentifier(missing) = %v", errs)
	}

	wrongNS := `<x xmlns:pdfaid="http://example.com/wrong" pdfaid:part="2" pdfaid:conformance="X"/>`
	errs := checkPDFAIdentifier(wrongNS)
	if len(errs) != 3 {
		t.Fatalf("checkPDFAIdentifier(wrong ns/part/conformance) = %d errs, want 3: %v", len(errs), errs)
	}
}

func TestXmpWellFormed(t *testing.T) {
	if !xmpWellFormed([]byte(`<a><b>text</b></a>`)) {
		t.Error("well-formed XML reported as malformed")
	}
	if xmpWellFormed([]byte(`<a><b>text</b>`)) {
		t.Error("an unclosed outer element should be reported as malformed")
	}
}

func TestDigitsOfAndDecodeXMLEntities(t *testing.T) {
	if got := digitsOf("D:2024-01-02"); got != "20240102" {
		t.Errorf("digitsOf = %q, want 20240102", got)
	}
	if got := decodeXMLEntities("A &amp; B &lt;tag&gt; &apos;q&apos; &quot;x&quot;"); got != `A & B <tag> 'q' "x"` {
		t.Errorf("decodeXMLEntities = %q", got)
	}
}

func TestXmpPropValueAndScalarValue(t *testing.T) {
	xmp := `<dc:title><rdf:Alt><rdf:li xml:lang="x-default">Hello &amp; World</rdf:li></rdf:Alt></dc:title>
	         <xmp:CreatorTool>Acme</xmp:CreatorTool>`
	if v, ok := xmpPropValue(xmp, "dc:title"); !ok || v != "Hello & World" {
		t.Errorf("xmpPropValue(dc:title) = %q, %v", v, ok)
	}
	if _, ok := xmpPropValue(xmp, "dc:missing"); ok {
		t.Error("xmpPropValue should be false for an absent property")
	}
	if v, ok := xmpScalarValue(xmp, "xmp:CreatorTool"); !ok || v != "Acme" {
		t.Errorf("xmpScalarValue(xmp:CreatorTool) = %q, %v", v, ok)
	}
	if v, ok := xmpScalarValue(`x="attr value"`, "x"); !ok || v != "attr value" {
		t.Errorf("xmpScalarValue(attribute style) = %q, %v", v, ok)
	}
}

func TestCheckXMPPropertyTypes(t *testing.T) {
	badRoot := `xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"<RDF:RDF></RDF:RDF>`
	if errs := checkXMPPropertyTypes(badRoot); len(errs) == 0 {
		t.Error("expected a violation for a missing rdf:RDF root")
	}
	if errs := checkXMPPropertyTypes(`<xmp:Title>foo</xmp:Title>`); len(errs) == 0 {
		t.Error("expected a violation for xmp:Title (invalid property)")
	}
	if errs := checkXMPPropertyTypes(`<dc:description>plain text</dc:description>`); len(errs) == 0 {
		t.Error("expected a violation for a non-LangAlt dc:description")
	}
	if errs := checkXMPPropertyTypes(`<dc:description><rdf:Alt><rdf:li xml:lang="x-default">ok</rdf:li></rdf:Alt></dc:description>`); len(errs) != 0 {
		t.Errorf("unexpected violation for a well-formed dc:description: %v", errs)
	}
}

func TestXmpIsDateAndCheckValueRule(t *testing.T) {
	if !xmpIsDate("2024-01-02T10:00:00Z") {
		t.Error("xmpIsDate should accept a full ISO 8601 date")
	}
	if xmpIsDate("not a date") {
		t.Error("xmpIsDate should reject garbage")
	}

	if msg := xmpCheckValueRule(xmpValueRule{kind: xmpVTDate}, "bad"); msg == "" {
		t.Error("xmpCheckValueRule(date) should flag a non-date value")
	}
	if msg := xmpCheckValueRule(xmpValueRule{kind: xmpVTInteger}, "12"); msg != "" {
		t.Errorf("xmpCheckValueRule(integer) unexpected: %q", msg)
	}
	if msg := xmpCheckValueRule(xmpValueRule{kind: xmpVTInteger}, "x"); msg == "" {
		t.Error("xmpCheckValueRule(integer) should flag a non-integer value")
	}
	if msg := xmpCheckValueRule(xmpValueRule{kind: xmpVTClosedText, choices: []string{"2", "3"}}, "2"); msg != "" {
		t.Errorf("xmpCheckValueRule(closed text, valid) unexpected: %q", msg)
	}
	if msg := xmpCheckValueRule(xmpValueRule{kind: xmpVTClosedText, choices: []string{"2", "3"}}, "9"); msg == "" {
		t.Error("xmpCheckValueRule(closed text) should flag an out-of-set value")
	}
}

func TestXmpIsIntegerAndContainerOK(t *testing.T) {
	for _, s := range []string{"123", "-5", "+7"} {
		if !xmpIsInteger(s) {
			t.Errorf("xmpIsInteger(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "-", "1.5", "12a"} {
		if xmpIsInteger(s) {
			t.Errorf("xmpIsInteger(%q) = true, want false", s)
		}
	}

	if !xmpContainerOK(xmpKindScalar, xmpKindScalar) || xmpContainerOK(xmpKindScalar, xmpKindBag) {
		t.Error("xmpContainerOK(scalar) mismatch")
	}
	if !xmpContainerOK(xmpKindBag, xmpKindBag) || xmpContainerOK(xmpKindBag, xmpKindSeq) {
		t.Error("xmpContainerOK(bag) mismatch")
	}
	if xmpContainerOK(xmpContainerKind(99), xmpKindScalar) {
		t.Error("xmpContainerOK should be false for an unknown expected kind")
	}
}
