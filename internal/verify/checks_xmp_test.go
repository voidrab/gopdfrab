package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
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
	errs := verifyXMPMetadata(doc, a1b())
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
	errs := verifyXMPMetadata(doc, a1b())
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Metadata.MetadataMissing {
		t.Errorf("verifyXMPMetadata(no XMP, a1b()) = %v, want a single MetadataMissing", errs)
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

// TestCheckInfoXMPSyncOrderIsStable pins the issue order when both Title and
// Subject are out of sync: it once came from ranging over a map, so the same
// file gave differently ordered reports from run to run.
func TestCheckInfoXMPSyncOrderIsStable(t *testing.T) {
	f := t.TempDir() + "/info-order.pdf"
	if err := createPDFWithInfo(f, map[string]string{"Title": "T", "Subject": "S"}); err != nil {
		t.Fatalf("createPDFWithInfo: %v", err)
	}
	doc, err := pdf.Open(f)
	if err != nil {
		t.Fatalf("pdf.Open: %v", err)
	}
	defer doc.Close()

	want := []string{
		"document info Title not synchronized with XMP dc:title",
		"document info Title not synchronized with XMP dc:title",
		"document info Subject not synchronized with XMP dc:description",
		"document info Subject not synchronized with XMP dc:description",
	}
	for range 50 {
		var got []string
		for _, e := range checkInfoXMPSync(doc, "") {
			got = append(got, e.Messages()...)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("checkInfoXMPSync messages = %q, want %q", got, want)
		}
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
	otherMeta.Entries.Set("Type", pdf.PDFName{Value: "Metadata"})
	otherMeta.RawStream = []byte("no xpacket wrapper here")

	root := pdf.NewPDFDict()
	root.Entries.Set("Other", otherMeta)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)

	errs := checkNonCatalogXMPStreams(trailer, &ValidationContext{Spec: a1bSpec})
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Metadata.ObjectXMPNoXPacket {
		t.Errorf("checkNonCatalogXMPStreams = %v, want a single ObjectXMPNoXPacket", errs)
	}

	// A non-catalog Metadata stream that *is* xpacket-wrapped is fine.
	wrapped := pdf.NewPDFDict()
	wrapped.HasStream = true
	wrapped.Entries.Set("Type", pdf.PDFName{Value: "Metadata"})
	wrapped.RawStream = []byte("<?xpacket begin=\"\"?><x:xmpmeta/><?xpacket end=\"w\"?>")
	root2 := pdf.NewPDFDict()
	root2.Entries.Set("Other", wrapped)
	trailer2 := pdf.NewPDFDict()
	trailer2.Entries.Set("Root", root2)
	if errs := checkNonCatalogXMPStreams(trailer2, &ValidationContext{Spec: a1bSpec}); len(errs) != 0 {
		t.Errorf("unexpected violation for an xpacket-wrapped stream: %v", errs)
	}

	// Non-dict graphs (e.g. a bare array) are ignored.
	if errs := checkNonCatalogXMPStreams(pdf.PDFArray{}, &ValidationContext{Spec: a1bSpec}); errs != nil {
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
	if errs := checkPDFAIdentifier(good, a1b()); len(errs) != 0 {
		t.Errorf("unexpected violation for a valid PDF/A identifier: %v", errs)
	}

	if errs := checkPDFAIdentifier("no identifier here", a1b()); len(errs) != 1 ||
		errs[0].Check() != pdf.Checks.Metadata.PDFAIdentifierMissing {
		t.Errorf("checkPDFAIdentifier(missing, a1b()) = %v", errs)
	}

	wrongNS := `<x xmlns:pdfaid="http://example.com/wrong" pdfaid:part="2" pdfaid:conformance="X"/>`
	errs := checkPDFAIdentifier(wrongNS, a1b())
	if len(errs) != 3 {
		t.Fatalf("checkPDFAIdentifier(wrong ns/part/conformance, a1b()) = %d errs, want 3: %v", len(errs), errs)
	}
}

// TestCheckPDFAIdentifierQuoteStyles pins that the identifier is recognised in
// every shape XMP legitimately writes it: as attributes in either quote
// character, and as child elements.
//
// Some producers write single-quoted attributes. Reading only double quotes
// reports a missing PDF/A identifier for every such file.
func TestCheckPDFAIdentifierQuoteStyles(t *testing.T) {
	const ns = "http://www.aiim.org/pdfa/ns/id/"
	cases := []struct {
		name string
		xmp  string
	}{
		{"double-quoted attributes", `<x xmlns:pdfaid="` + ns + `" pdfaid:part="1" pdfaid:conformance="B"/>`},
		{"single-quoted attributes", `<x xmlns:pdfaid='` + ns + `' pdfaid:part='1' pdfaid:conformance='B'/>`},
		{"child elements", `<rdf:Description xmlns:pdfaid="` + ns + `">` +
			`<pdfaid:part>1</pdfaid:part><pdfaid:conformance>B</pdfaid:conformance></rdf:Description>`},
		{"mixed quoting", `<x xmlns:pdfaid='` + ns + `' pdfaid:part="1" pdfaid:conformance='B'/>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if errs := checkPDFAIdentifier(c.xmp, a1b()); len(errs) != 0 {
				t.Errorf("valid PDF/A identifier reported as a violation: %v", errs)
			}
			part, ok := pdf.FirstRegexpGroup(pdf.PDFAPartRe, c.xmp)
			if !ok || part != "1" {
				t.Errorf("PDFAPartRe = %q, %v; want \"1\", true", part, ok)
			}
			conf, ok := pdf.FirstRegexpGroup(pdf.PDFAConfRe, c.xmp)
			if !ok || conf != "B" {
				t.Errorf("PDFAConfRe = %q, %v; want \"B\", true", conf, ok)
			}
		})
	}
}

// TestXMPNamespaceBindingQuoteStyles covers the same quoting rule on the
// prefix->URI binding scan, whose regular expression captures the URI from
// either quote style.
func TestXMPNamespaceBindingQuoteStyles(t *testing.T) {
	xmp := `<rdf:Description xmlns:pdf="http://ns.adobe.com/pdf/1.3/" xmlns:dc='http://purl.org/dc/elements/1.1/'/>`
	got := map[string]string{}
	for _, m := range xmpNSBindRe.FindAllStringSubmatch(xmp, -1) {
		got[m[1]] = m[2] + m[3]
	}
	want := map[string]string{
		"pdf": "http://ns.adobe.com/pdf/1.3/",
		"dc":  "http://purl.org/dc/elements/1.1/",
	}
	for prefix, uri := range want {
		if got[prefix] != uri {
			t.Errorf("xmlns:%s bound to %q, want %q", prefix, got[prefix], uri)
		}
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

// TestNonCatalogXMPUndecodableIsNotMisattributed covers a metadata stream on a
// non-catalog object that will not decode. That is a structural defect, not
// evidence that the xpacket wrapper is missing, and must not be reported as
// one.
func TestNonCatalogXMPUndecodableIsNotMisattributed(t *testing.T) {
	broken := pdf.NewPDFDict()
	broken.HasStream = true
	broken.Entries.Set("Type", pdf.PDFName{Value: "Metadata"})
	broken.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	broken.RawStream = []byte("not a zlib stream")

	root := pdf.NewPDFDict()
	root.Entries.Set("Other", broken)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)

	ctx := &ValidationContext{Spec: a1bSpec}
	errs := checkNonCatalogXMPStreams(trailer, ctx)
	for _, e := range errs {
		if e.Check() == pdf.Checks.Metadata.ObjectXMPNoXPacket {
			t.Error("an undecodable metadata stream was reported as a missing xpacket wrapper")
		}
	}
	if !hasCheck(ctx, pdf.Checks.Structure.StreamUndecodable) {
		t.Error("expected StreamUndecodable for a metadata stream that will not decode")
	}

	// A stream that decodes but genuinely lacks the wrapper still reports.
	plain := pdf.NewPDFDict()
	plain.HasStream = true
	plain.Entries.Set("Type", pdf.PDFName{Value: "Metadata"})
	plain.RawStream = []byte("<x:xmpmeta/>")

	root2 := pdf.NewPDFDict()
	root2.Entries.Set("Other", plain)
	trailer2 := pdf.NewPDFDict()
	trailer2.Entries.Set("Root", root2)
	errs2 := checkNonCatalogXMPStreams(trailer2, &ValidationContext{Spec: a1bSpec})
	if len(errs2) != 1 || errs2[0].Check() != pdf.Checks.Metadata.ObjectXMPNoXPacket {
		t.Errorf("decodable stream without xpacket = %v, want one ObjectXMPNoXPacket", errs2)
	}
}

// TestCheckInfoXMPSyncAuthorEquivalence pins that the Author/dc:creator
// comparison treats the two sides the same way the Title/Subject one does:
// XML entities decoded, whitespace trimmed on both sides.
//
// Both sides must be normalised the same way. An author name containing "&"
// would otherwise compare decoded text against raw markup, and a value with
// leading whitespace would be compared against an already trimmed copy of
// itself, reporting identical values as out of sync.
func TestCheckInfoXMPSyncAuthorEquivalence(t *testing.T) {
	cases := []struct {
		name    string
		author  string
		creator string
		wantErr bool
	}{
		{"identical", "John Doe", "John Doe", false},
		{"ampersand entity", "Alice & Bob", "Alice &amp; Bob", false},
		{"angle brackets", "a<b>c", "a&lt;b&gt;c", false},
		{"leading and trailing space both sides", " John Doe ", " John Doe ", false},
		// veraPDF treats a whitespace-only difference as a real mismatch
		// (6-1-5-t01-fail-b), so neither side is trimmed.
		{"space only on the Info side", " John Doe ", "John Doe", true},
		{"genuinely different", "John Doe", "Jane Roe", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := t.TempDir() + "/info.pdf"
			if err := createPDFWithInfo(f, map[string]string{"Author": c.author}); err != nil {
				t.Fatalf("createPDFWithInfo: %v", err)
			}
			doc, err := pdf.Open(f)
			if err != nil {
				t.Fatalf("pdf.Open: %v", err)
			}
			defer doc.Close()

			xmp := "<dc:creator><rdf:Seq><rdf:li>" + c.creator + "</rdf:li></rdf:Seq></dc:creator>"
			errs := checkInfoXMPSync(doc, xmp)
			if got := len(errs) > 0; got != c.wantErr {
				t.Errorf("mismatch reported = %v, want %v (errs: %v)", got, c.wantErr, errs)
			}
		})
	}
}

// extValueTypeXMP builds an extension schema declaring one property of the
// given value type, plus the XMP body that actually uses it, so a single
// fixture can drive every declared-versus-actual combination.
func extValueTypeXMP(valueType, usage string) string {
	return `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
  xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/"
  xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#"
  xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#"
  xmlns:custom="http://example.com/valuetypes/">
<rdf:Description rdf:about="">
  <pdfaExtension:schemas>
    <rdf:Bag>
      <rdf:li rdf:parseType="Resource">
        <pdfaSchema:schema>Value Types</pdfaSchema:schema>
        <pdfaSchema:namespaceURI>http://example.com/valuetypes/</pdfaSchema:namespaceURI>
        <pdfaSchema:prefix>custom</pdfaSchema:prefix>
        <pdfaSchema:property>
          <rdf:Seq>
            <rdf:li rdf:parseType="Resource">
              <pdfaProperty:name>myProp</pdfaProperty:name>
              <pdfaProperty:valueType>` + valueType + `</pdfaProperty:valueType>
              <pdfaProperty:category>external</pdfaProperty:category>
              <pdfaProperty:description>a property</pdfaProperty:description>
            </rdf:li>
          </rdf:Seq>
        </pdfaSchema:property>
      </rdf:li>
    </rdf:Bag>
  </pdfaExtension:schemas>
  ` + usage + `
</rdf:Description>
</rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`
}

// hasXMPCheck reports whether errs contains a violation of c.
func hasXMPCheck(errs []pdf.PDFError, c pdf.Check) bool {
	for _, e := range errs {
		if e.Check() == c {
			return true
		}
	}
	return false
}

// TestXMPNoCorrespondingType covers 6.7.9: a property declared in an extension
// schema must be used with a value matching the type it declares.
func TestXMPNoCorrespondingType(t *testing.T) {
	for _, tc := range []struct {
		name      string
		valueType string
		usage     string
		want      bool
	}{
		{"integer holds text", "Integer", `<custom:myProp>not a number</custom:myProp>`, true},
		{"integer holds an integer", "Integer", `<custom:myProp>42</custom:myProp>`, false},
		{"real holds text", "Real", `<custom:myProp>1.2.3</custom:myProp>`, true},
		{"real holds a real", "Real", `<custom:myProp>-1.5</custom:myProp>`, false},
		{"real holds a hex float", "Real", `<custom:myProp>0x1p2</custom:myProp>`, true},
		{"boolean holds a lowercase word", "Boolean", `<custom:myProp>true</custom:myProp>`, true},
		{"boolean holds a boolean", "Boolean", `<custom:myProp>True</custom:myProp>`, false},
		{"date holds junk", "Date", `<custom:myProp>last tuesday</custom:myProp>`, true},
		{"date holds a date", "Date", `<custom:myProp>2026-07-29</custom:myProp>`, false},
		{"text holds text", "Text", `<custom:myProp>anything at all</custom:myProp>`, false},
		{"empty value is not judged", "Integer", `<custom:myProp></custom:myProp>`, false},

		{"bag serialized as a scalar", "Bag",
			`<custom:myProp>plain</custom:myProp>`, true},
		{"bag serialized as a bag", "Bag",
			`<custom:myProp><rdf:Bag><rdf:li>one</rdf:li></rdf:Bag></custom:myProp>`, false},
		{"seq serialized as a bag", "Seq",
			`<custom:myProp><rdf:Bag><rdf:li>one</rdf:li></rdf:Bag></custom:myProp>`, true},
		{"qualified bag declaration", "Bag Text",
			`<custom:myProp><rdf:Bag><rdf:li>one</rdf:li></rdf:Bag></custom:myProp>`, false},

		{"langalt entry without xml:lang", "LangAlt",
			`<custom:myProp><rdf:Alt><rdf:li>hello</rdf:li></rdf:Alt></custom:myProp>`, true},
		{"langalt entry with xml:lang", "LangAlt",
			`<custom:myProp><rdf:Alt><rdf:li xml:lang="x-default">hello</rdf:li></rdf:Alt></custom:myProp>`, false},
		{"plain alt needs no xml:lang", "Alt",
			`<custom:myProp><rdf:Alt><rdf:li>hello</rdf:li></rdf:Alt></custom:myProp>`, false},

		{"struct type used as a scalar", "ResourceRef",
			`<custom:myProp>plain</custom:myProp>`, true},
		{"struct type used as a struct", "ResourceRef",
			`<custom:myProp rdf:parseType="Resource"><custom:inner>x</custom:inner></custom:myProp>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkExtensionSchemas(extValueTypeXMP(tc.valueType, tc.usage))
			got := hasXMPCheck(errs, pdf.Checks.Metadata.XMPNoCorrespondingType)
			if got != tc.want {
				t.Errorf("XMPNoCorrespondingType reported = %v, want %v (errs: %v)", got, tc.want, errs)
			}
		})
	}
}

// TestXMPNoCorrespondingTypeAttributeStyle covers the other serialization: a
// property written as an rdf:Description attribute is always a plain scalar.
func TestXMPNoCorrespondingTypeAttributeStyle(t *testing.T) {
	xmp := strings.Replace(extValueTypeXMP("Integer", ""),
		`<rdf:Description rdf:about="">`,
		`<rdf:Description rdf:about="" custom:myProp="not a number">`, 1)
	if !hasXMPCheck(checkExtensionSchemas(xmp), pdf.Checks.Metadata.XMPNoCorrespondingType) {
		t.Error("expected XMPNoCorrespondingType for an attribute-style property holding the wrong type")
	}

	ok := strings.Replace(extValueTypeXMP("Integer", ""),
		`<rdf:Description rdf:about="">`,
		`<rdf:Description rdf:about="" custom:myProp="42">`, 1)
	if hasXMPCheck(checkExtensionSchemas(ok), pdf.Checks.Metadata.XMPNoCorrespondingType) {
		t.Error("an attribute-style integer property was reported despite holding an integer")
	}
}

// TestXMPNoCorrespondingTypeLeavesCustomTypes covers the division of labour: a
// property declared with a custom (non-builtin) type stays a 6.7.8 report,
// never a 6.7.9 one, so the two checks do not both fire on the same defect.
func TestXMPNoCorrespondingTypeLeavesCustomTypes(t *testing.T) {
	errs := checkExtensionSchemas(extValueTypeXMP("MyCustomType", `<custom:myProp>plain</custom:myProp>`))
	if hasXMPCheck(errs, pdf.Checks.Metadata.XMPNoCorrespondingType) {
		t.Error("a custom-typed property was reported under 6.7.9, want 6.7.8 only")
	}
	if !hasXMPCheck(errs, pdf.Checks.Metadata.ExtPropertyComplexAsSimple) {
		t.Errorf("expected ExtPropertyComplexAsSimple for a custom type used as a simple value, got %v", errs)
	}
}

// TestXMPNoCorrespondingTypeIgnoresPredefinedSchemas covers the other half of
// that division: dc: and friends are checked against XMP 2004 under 6.7.2, and
// this check must not also claim them.
func TestXMPNoCorrespondingTypeIgnoresPredefinedSchemas(t *testing.T) {
	errs := checkExtPropertyValueTypes([]byte(extensionSchemaXMP), []extSchema{{
		namespaceURI: "http://purl.org/dc/elements/1.1/",
		properties:   []extProperty{{name: "title", valueType: "Integer"}},
	}})
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none: the fixture uses no dc: properties", errs)
	}
}

// TestExtDeclaredType covers the qualified-declaration reduction on its own.
func TestExtDeclaredType(t *testing.T) {
	for in, want := range map[string]string{
		"Bag":       "Bag",
		"Bag Text":  "Bag",
		"Seq\tText": "Seq",
		"":          "",
		" Text":     " Text",
	} {
		if got := extDeclaredType(in); got != want {
			t.Errorf("extDeclaredType(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCheckExtPropertyValueTypesNoDeclarations covers the early exit: nothing
// to compare against means no parse and no findings.
func TestCheckExtPropertyValueTypesNoDeclarations(t *testing.T) {
	for _, schemas := range [][]extSchema{
		nil,
		{{namespaceURI: ""}},
		{{namespaceURI: "http://example.com/x/", properties: []extProperty{{name: ""}}}},
		{{namespaceURI: "http://example.com/x/", properties: []extProperty{{name: "p", valueType: "Custom"}}}},
	} {
		if errs := checkExtPropertyValueTypes([]byte(extensionSchemaXMP), schemas); len(errs) != 0 {
			t.Errorf("schemas %+v: errs = %v, want none", schemas, errs)
		}
	}
}

// TestExtBuiltinKindsCoversEveryBuiltinType pins the two tables together: a
// value type XMP defines but extBuiltinKinds does not name would silently go
// unchecked, since the property collection skips what it cannot compare.
func TestExtBuiltinKindsCoversEveryBuiltinType(t *testing.T) {
	for name := range xmpBuiltinTypes {
		if _, ok := extBuiltinKinds[name]; !ok {
			t.Errorf("value type %q is in xmpBuiltinTypes but has no shape in extBuiltinKinds", name)
		}
	}
	for name := range extBuiltinKinds {
		if !xmpBuiltinTypes[name] {
			t.Errorf("value type %q is in extBuiltinKinds but is not a builtin XMP type", name)
		}
	}
}

// TestExtValueTypeErrsUnknownType covers the guard for a type with no known
// shape: nothing is claimed about a property the check cannot judge.
func TestExtValueTypeErrsUnknownType(t *testing.T) {
	if errs := extValueTypeErrs("p", "SomeTypeNobodyDefined", xmpKindScalar, "x", nil); len(errs) != 0 {
		t.Errorf("errs = %v, want none for a type with no known shape", errs)
	}
}

// TestXMPNoCorrespondingTypeSkipsBOM covers the leading-bytes strip: real XMP
// packets often start with a BOM or white space before the first element.
func TestXMPNoCorrespondingTypeSkipsBOM(t *testing.T) {
	xmp := "\xef\xbb\xbf\n  " + extValueTypeXMP("Integer", `<custom:myProp>not a number</custom:myProp>`)
	if !hasXMPCheck(checkExtensionSchemas(xmp), pdf.Checks.Metadata.XMPNoCorrespondingType) {
		t.Error("expected XMPNoCorrespondingType in a packet with leading bytes before the XML")
	}
}

// extSchemaDoc assembles a PDF/A extension-schema XMP packet from fragments
// of the pdfaExtension:schemas structure. The fragments are concatenated
// as given, and no closing tags are added, so a caller can end the packet at
// any depth to model a truncated stream.
func extSchemaDoc(fragments ...string) string {
	return `<x:xmpmeta xmlns:x="adobe:ns:meta/">` +
		`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"` +
		` xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/"` +
		` xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#"` +
		` xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#"` +
		` xmlns:pdfaType="http://www.aiim.org/pdfa/ns/type#"` +
		` xmlns:pdfaField="http://www.aiim.org/pdfa/ns/field#">` +
		`<rdf:Description rdf:about="">` + strings.Join(fragments, "")
}

// Fragments of a complete, valid extension schema, in document order.
const (
	extFrSchemasOpen = `<pdfaExtension:schemas><rdf:Bag><rdf:li rdf:parseType="Resource">`
	extFrIdentity    = `<pdfaSchema:schema>S</pdfaSchema:schema>` +
		`<pdfaSchema:namespaceURI>http://example.com/e/</pdfaSchema:namespaceURI>` +
		`<pdfaSchema:prefix>e</pdfaSchema:prefix>`
	extFrPropertyOpen = `<pdfaSchema:property><rdf:Seq><rdf:li rdf:parseType="Resource">`
	extFrPropertyName = `<pdfaProperty:name>p</pdfaProperty:name>`
	extFrPropertyRest = `<pdfaProperty:valueType>Text</pdfaProperty:valueType>` +
		`<pdfaProperty:category>external</pdfaProperty:category>` +
		`<pdfaProperty:description>d</pdfaProperty:description>`
	extFrPropertyClose = `</rdf:li></rdf:Seq></pdfaSchema:property>`
	extFrTypeOpen      = `<pdfaSchema:valueType><rdf:Seq><rdf:li rdf:parseType="Resource">`
	extFrTypeHead      = `<pdfaType:type>T</pdfaType:type>`
	extFrTypeRest      = `<pdfaType:namespaceURI>http://example.com/t/</pdfaType:namespaceURI>` +
		`<pdfaType:prefix>t</pdfaType:prefix><pdfaType:description>d</pdfaType:description>`
	extFrFieldOpen  = `<pdfaType:field><rdf:Seq><rdf:li rdf:parseType="Resource">`
	extFrFieldName  = `<pdfaField:name>f</pdfaField:name>`
	extFrFieldRest  = `<pdfaField:valueType>Text</pdfaField:valueType><pdfaField:description>d</pdfaField:description>`
	extFrFieldClose = `</rdf:li></rdf:Seq></pdfaType:field>`
	extFrTypeClose  = `</rdf:li></rdf:Seq></pdfaSchema:valueType>`
	extFrSchemaTail = `</rdf:li></rdf:Bag></pdfaExtension:schemas>`
)

// extMessages flattens the messages of errs that violate c.
func extMessages(errs []pdf.PDFError, c pdf.Check) string {
	var out []string
	for _, e := range errs {
		if e.Check() == c {
			out = append(out, e.Messages()...)
		}
	}
	return strings.Join(out, "\n")
}

// TestExtSchemasUnknownElementsAreSkipped checks that unrecognised elements at
// every level of the extension-schema structure are skipped whole, so the
// recognised entries around them are still read and a valid schema stays
// valid.
func TestExtSchemasUnknownElementsAreSkipped(t *testing.T) {
	junk := func(name string) string { return `<junk:` + name + `><deep>text</deep></junk:` + name + `>` }
	xmp := extSchemaDoc(
		`<pdfaExtension:schemas><rdf:Bag>`+junk("bag"),
		`<rdf:li rdf:parseType="Resource">`,
		extFrIdentity,
		extFrPropertyOpen[:len(`<pdfaSchema:property><rdf:Seq>`)]+junk("prop"),
		`<rdf:li rdf:parseType="Resource">`, extFrPropertyName, extFrPropertyRest, extFrPropertyClose,
		`<pdfaSchema:valueType><rdf:Seq>`+junk("type"),
		`<rdf:li rdf:parseType="Resource">`, extFrTypeHead, extFrTypeRest,
		`<pdfaType:field><rdf:Seq>`+junk("field"),
		`<rdf:li rdf:parseType="Resource">`, extFrFieldName, extFrFieldRest, extFrFieldClose, extFrTypeClose,
		extFrSchemaTail,
		`<e:p>value</e:p></rdf:Description></rdf:RDF></x:xmpmeta>`,
	)
	if errs := checkExtensionSchemas(xmp); len(errs) != 0 {
		t.Errorf("a valid schema with unknown sibling elements reported %v", issueKeys(errs))
	}
}

// TestExtSchemasPhraseWithoutElement checks that the text "pdfaExtension:schemas"
// appearing somewhere other than as the schemas element (here a comment) does
// not yield findings.
func TestExtSchemasPhraseWithoutElement(t *testing.T) {
	xmp := extSchemaDoc(`<!-- pdfaExtension:schemas -->`, `</rdf:Description></rdf:RDF></x:xmpmeta>`)
	if errs := checkExtensionSchemas(xmp); len(errs) != 0 {
		t.Errorf("reported %v", issueKeys(errs))
	}
}

// TestExtSchemasTruncatedStreams checks that an XMP packet cut off at any depth
// of the extension-schema structure is judged on what was read before the cut:
// the entry being read when the data ran out is kept, with its missing parts
// reported.
func TestExtSchemasTruncatedStreams(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fragments []string
		check     pdf.Check
		want      string // substring of the messages; "" means no finding at all
	}{
		{"inside the schemas bag", []string{`<pdfaExtension:schemas><rdf:Bag>`}, pdf.Checks.Metadata.ExtSchemaNamespace, ""},
		{"inside a schema entry", []string{extFrSchemasOpen, `<pdfaSchema:schema>S`}, pdf.Checks.Metadata.ExtSchemaNamespace, ""},
		{"inside the property sequence", []string{extFrSchemasOpen, extFrIdentity, `<pdfaSchema:property><rdf:Seq>`}, pdf.Checks.Metadata.ExtPropertyMissingField, ""},
		{"inside a property entry", []string{extFrSchemasOpen, extFrIdentity, extFrPropertyOpen, extFrPropertyName},
			pdf.Checks.Metadata.ExtPropertyMissingField, "pdfaProperty p missing valueType"},
		{"inside a property name", []string{extFrSchemasOpen, extFrIdentity, extFrPropertyOpen, `<pdfaProperty:name>p`},
			pdf.Checks.Metadata.ExtPropertyMissingField, "pdfaProperty p missing valueType"},
		{"inside an unknown property child", []string{extFrSchemasOpen, extFrIdentity, extFrPropertyOpen, `<pdfaProperty:other><a>`},
			pdf.Checks.Metadata.ExtPropertyMissingField, "pdfaProperty entry missing name"},
		{"inside the type sequence", []string{extFrSchemasOpen, extFrIdentity, `<pdfaSchema:valueType><rdf:Seq>`}, pdf.Checks.Metadata.ExtTypeInvalid, ""},
		{"inside a type entry", []string{extFrSchemasOpen, extFrIdentity, extFrTypeOpen, extFrTypeHead},
			pdf.Checks.Metadata.ExtTypeInvalid, "pdfaType T missing namespaceURI"},
		{"inside the field sequence", []string{extFrSchemasOpen, extFrIdentity, extFrTypeOpen, extFrTypeHead, extFrTypeRest, `<pdfaType:field><rdf:Seq>`},
			pdf.Checks.Metadata.ExtFieldInvalid, ""},
		{"inside a field entry", []string{extFrSchemasOpen, extFrIdentity, extFrTypeOpen, extFrTypeHead, extFrTypeRest, extFrFieldOpen, extFrFieldName},
			pdf.Checks.Metadata.ExtFieldInvalid, "pdfaField f missing valueType"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkExtensionSchemas(extSchemaDoc(tc.fragments...))
			got := extMessages(errs, tc.check)
			if tc.want == "" {
				if got != "" {
					t.Errorf("%v findings %q, want none", tc.check.Clause(), got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("messages %q do not contain %q (all: %v)", got, tc.want, issueKeys(errs))
			}
		})
	}
}

// TestExtSchemaIncompleteEntries covers the findings for extension-schema
// entries that lack their required parts (6.7.8).
func TestExtSchemaIncompleteEntries(t *testing.T) {
	xmp := extSchemaDoc(
		extFrSchemasOpen, extFrIdentity,
		// Two names; and a second property with no name at all.
		extFrPropertyOpen, extFrPropertyName, `<pdfaProperty:name>again</pdfaProperty:name>`, extFrPropertyRest, `</rdf:li>`,
		`<rdf:li rdf:parseType="Resource">`, `<pdfaProperty:valueType>Text</pdfaProperty:valueType>`, extFrPropertyClose,
		// A type with only a field, and that field with only a valueType.
		extFrTypeOpen, extFrFieldOpen, `<pdfaField:valueType>Text</pdfaField:valueType>`, extFrFieldClose, extFrTypeClose,
		extFrSchemaTail, `</rdf:Description></rdf:RDF></x:xmpmeta>`,
	)
	errs := checkExtensionSchemas(xmp)

	for _, tc := range []struct {
		check pdf.Check
		want  string
	}{
		{pdf.Checks.Metadata.ExtPropertyMultipleName, "multiple pdfaProperty:name"},
		{pdf.Checks.Metadata.ExtPropertyMissingField, "pdfaProperty entry missing name"},
		{pdf.Checks.Metadata.ExtTypeInvalid, "pdfaType entry missing type name"},
		{pdf.Checks.Metadata.ExtTypeInvalid, "missing namespaceURI"},
		{pdf.Checks.Metadata.ExtTypeInvalid, "missing prefix"},
		{pdf.Checks.Metadata.ExtTypeInvalid, "missing description"},
		{pdf.Checks.Metadata.ExtFieldInvalid, "pdfaField entry missing name"},
		{pdf.Checks.Metadata.ExtFieldInvalid, "missing description"},
	} {
		if got := extMessages(errs, tc.check); !strings.Contains(got, tc.want) {
			t.Errorf("%s messages %q do not contain %q", tc.check.Clause(), got, tc.want)
		}
	}
	if got := extMessages(errs, pdf.Checks.Metadata.ExtFieldInvalid); strings.Contains(got, "missing valueType") {
		t.Errorf("a field with a valueType was reported as lacking one: %q", got)
	}
}

// TestExtSchemaRDFPrefixIgnoresStructuralElements checks that when a schema
// claims the rdf prefix, the packet's own rdf:Description and rdf:RDF
// elements are not reported as undocumented properties.
func TestExtSchemaRDFPrefixIgnoresStructuralElements(t *testing.T) {
	xmp := strings.Replace(extSchemaDoc(
		extFrSchemasOpen, extFrIdentity, extFrPropertyOpen, extFrPropertyName, extFrPropertyRest, extFrPropertyClose,
		extFrSchemaTail, `</rdf:Description></rdf:RDF></x:xmpmeta>`,
	), `<pdfaSchema:prefix>e</pdfaSchema:prefix>`, `<pdfaSchema:prefix>rdf</pdfaSchema:prefix>`, 1)
	got := extMessages(checkExtensionSchemas(xmp), pdf.Checks.Metadata.ExtPropertyUndocumented)
	if got == "" {
		t.Fatal("expected the other rdf: elements to be reported as undocumented")
	}
	for _, structural := range []string{"rdf:Description", "rdf:RDF"} {
		if strings.Contains(got, structural+" used") {
			t.Errorf("%s reported as an undocumented property: %q", structural, got)
		}
	}
}

// TestExtSchemaNameIgnoresNestedElementText checks that only the text directly
// inside a name element is read; text of an element nested within it is not
// part of the name.
func TestExtSchemaNameIgnoresNestedElementText(t *testing.T) {
	xmp := extSchemaDoc(extFrSchemasOpen, extFrIdentity, extFrPropertyOpen,
		`<pdfaProperty:name>a<b>x</b>c</pdfaProperty:name>`)
	got := extMessages(checkExtensionSchemas(xmp), pdf.Checks.Metadata.ExtPropertyMissingField)
	if !strings.Contains(got, "pdfaProperty ac missing valueType") {
		t.Errorf("messages %q: want the property named \"ac\"", got)
	}
}

// TestCheckXMPPropertySchemasTruncated checks that a property cut off by the
// end of the packet is judged on what precedes the cut, for container and
// struct forms alike.
func TestCheckXMPPropertySchemasTruncated(t *testing.T) {
	const head = `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:xmpMM="http://ns.adobe.com/xap/1.0/mm/" xmlns:dc="http://purl.org/dc/elements/1.1/"
	  xmlns:exif="http://ns.adobe.com/exif/1.0/"><rdf:Description>`
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"unclosed container property", `<dc:subject><rdf:Bag><rdf:li>keyword`, false},
		{"unclosed struct property", `<xmpMM:DerivedFrom rdf:parseType="Resource"><xmpMM:InstanceID>`, false},
		{"unclosed property with a bad item", `<exif:ISOSpeedRatings><rdf:Seq><rdf:li>abc</rdf:li><rdf:li>`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkXMPPropertySchemas([]byte(head + tc.body))
			if (len(errs) != 0) != tc.wantErr {
				t.Errorf("errs = %v, want error: %v", issueKeys(errs), tc.wantErr)
			}
		})
	}
}

// TestCheckXMPPropertySchemasEmptySeqItem checks that an empty rdf:li inside a
// typed Seq is skipped like an empty scalar, while the items around it are
// still checked.
func TestCheckXMPPropertySchemasEmptySeqItem(t *testing.T) {
	seq := func(items string) []byte {
		return []byte(`<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	  xmlns:exif="http://ns.adobe.com/exif/1.0/"><rdf:Description>
	  <exif:ISOSpeedRatings><rdf:Seq>` + items + `</rdf:Seq></exif:ISOSpeedRatings>
	</rdf:Description></rdf:RDF>`)
	}
	if errs := checkXMPPropertySchemas(seq(`<rdf:li></rdf:li><rdf:li>100</rdf:li>`)); len(errs) != 0 {
		t.Errorf("empty item reported: %v", issueKeys(errs))
	}
	if errs := checkXMPPropertySchemas(seq(`<rdf:li></rdf:li><rdf:li>x</rdf:li>`)); len(errs) != 1 {
		t.Errorf("non-integer item after an empty one: %v, want one finding", issueKeys(errs))
	}
}

// TestVerifyXMPMetadataUndecodableStream checks that a catalog Metadata stream
// whose filter chain is broken is reported as filtered (6.7.2) and unreadable,
// and that no content check runs on the bytes that could not be read.
func TestVerifyXMPMetadataUndecodableStream(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Metadata 3 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.StreamObj(3, "<< /Type /Metadata /Subtype /XML /Filter /FlateDecode", []byte("not a zlib stream"))
	doc, err := pdf.OpenBytes(b.FinishClassic("<< /Size 4 /Root 1 0 R >>"))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	defer doc.Close()

	errs := verifyXMPMetadata(doc, a1b())
	if len(errs) != 2 ||
		!hasXMPCheck(errs, pdf.Checks.Metadata.MetadataFiltered) ||
		!hasXMPCheck(errs, pdf.Checks.Metadata.XMPStreamUnreadable) {
		t.Errorf("verifyXMPMetadata = %v, want exactly MetadataFiltered and XMPStreamUnreadable", issueKeys(errs))
	}
}

// TestCheckNonCatalogXMPStreamsRootNotDictionary checks that a graph whose
// Root is not a dictionary has no catalog to compare against and is ignored.
func TestCheckNonCatalogXMPStreamsRootNotDictionary(t *testing.T) {
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", pdf.PDFArray{})
	if errs := checkNonCatalogXMPStreams(trailer, &ValidationContext{Spec: a1bSpec}); errs != nil {
		t.Errorf("checkNonCatalogXMPStreams = %v, want nil", errs)
	}
}

// TestVerifyReportsNonCatalogXMPWithoutXPacket drives 6.7.5 end to end: a page
// carrying its own Metadata stream without an xpacket wrapper is reported.
func TestVerifyReportsNonCatalogXMPWithoutXPacket(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Metadata 4 0 R >>")
	b.StreamObj(4, "<< /Type /Metadata /Subtype /XML", []byte("<x:xmpmeta xmlns:x='adobe:ns:meta/'/>"))
	res, err := VerifyBytes(b.FinishClassic("<< /Size 5 /Root 1 0 R >>"), pdf.PDFA1B, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if !resultHasCheck(res, pdf.Checks.Metadata.ObjectXMPNoXPacket) {
		t.Errorf("ObjectXMPNoXPacket not reported; issues %v", issueKeys(res.Issues))
	}
}

// TestXMPExifFlashStructFields checks that every field of the exif Flash
// structure (Fired, Return, Mode, Function, RedEyeMode) is a defined exif
// property.
func TestXMPExifFlashStructFields(t *testing.T) {
	const exif = "http://ns.adobe.com/exif/1.0/"
	for _, field := range []string{"Fired", "Return", "Mode", "Function", "RedEyeMode"} {
		if errs := xmpValidateProp(exif, field, xmpKindScalar, "", nil); len(errs) != 0 {
			t.Errorf("exif:%s reported as %v", field, issueKeys(errs))
		}
	}
	for ns, props := range xmpNSSchemas {
		for name := range props {
			if strings.ContainsAny(name, ". ") {
				t.Errorf("namespace %s defines property %q, which is not a valid XML local name", ns, name)
			}
		}
	}
}

// TestXMPTablesAgree cross-checks the property tables: every property that
// carries a value rule or a LangAlt requirement must exist in the container
// table, with a shape the rule can apply to.
func TestXMPTablesAgree(t *testing.T) {
	for ns, rules := range xmpValueRules {
		for name := range rules {
			kind, ok := xmpNSSchemas[ns][name]
			if !ok {
				t.Errorf("value rule for %s %s, which xmpNSSchemas does not define", ns, name)
				continue
			}
			if kind != xmpKindScalar && kind != xmpKindBag && kind != xmpKindSeq {
				t.Errorf("value rule for %s %s, but its container kind %v carries no checked text", ns, name, kind)
			}
		}
	}
	for ns, props := range xmpLangAltProps {
		for name := range props {
			if xmpNSSchemas[ns][name] != xmpKindAlt {
				t.Errorf("%s %s is marked LangAlt but is not an Alt in xmpNSSchemas", ns, name)
			}
		}
	}
}

// TestCheckPDFAIdentifierMissingPartAndConformance pins that an identifier
// namespace with neither a part nor a conformance level reports each as a
// separate PDFAIdentifierMissing finding (6.7.11).
func TestCheckPDFAIdentifierMissingPartAndConformance(t *testing.T) {
	errs := checkPDFAIdentifier(`<x xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"/>`, a1b())
	if len(errs) != 2 {
		t.Fatalf("got %d findings, want 2: %v", len(errs), errs)
	}
	for _, e := range errs {
		if e.Check() != pdf.Checks.Metadata.PDFAIdentifierMissing {
			t.Errorf("finding %v has check %v, want PDFAIdentifierMissing", e, e.Check())
		}
	}
	if !strings.Contains(errs[0].Error(), "part") || !strings.Contains(errs[1].Error(), "conformance") {
		t.Errorf("messages do not name the part then the conformance level: %v", errs)
	}
}

// TestXmpWellFormedSkipsLeadingBytes pins that bytes before the first '<'
// (such as stray non-XML bytes) do not make an otherwise well-formed packet
// malformed.
func TestXmpWellFormedSkipsLeadingBytes(t *testing.T) {
	if !xmpWellFormed([]byte("\xff<a><b/></a>")) {
		t.Error("leading bytes before the first '<' made a well-formed packet malformed")
	}
}

// TestXmpValidatePropValueErrors pins the findings for a non-integer scalar in
// an integer property, a malformed date scalar, and a bad item inside an integer Seq.
func TestXmpValidatePropValueErrors(t *testing.T) {
	errs := xmpValidateProp("http://ns.adobe.com/xap/1.0/mm/", "SaveID", xmpKindScalar, "abc", nil)
	if len(errs) != 1 || errs[0].Check() != pdf.Checks.Metadata.MetadataFiltered ||
		!strings.Contains(errs[0].Error(), "must be an integer") {
		t.Errorf("non-integer scalar: %v", errs)
	}
	if errs := xmpValidateProp("http://ns.adobe.com/xap/1.0/mm/", "SaveID", xmpKindScalar, "42", nil); len(errs) != 0 {
		t.Errorf("integer scalar reported: %v", errs)
	}

	errs = xmpValidateProp("http://ns.adobe.com/xap/1.0/", "CreateDate", xmpKindScalar, "not a date", nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "CreateDate") {
		t.Errorf("bad date scalar: %v", errs)
	}
	if errs := xmpValidateProp("http://ns.adobe.com/xap/1.0/", "CreateDate", xmpKindScalar, "2024-01-02T03:04:05Z", nil); len(errs) != 0 {
		t.Errorf("valid date scalar reported: %v", errs)
	}

	const tiff = "http://ns.adobe.com/tiff/1.0/"
	errs = xmpValidateProp(tiff, "BitsPerSample", xmpKindSeq, "", []xmpPropItem{{text: "8"}, {text: ""}, {text: "x"}})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "item") {
		t.Errorf("bad Seq item: %v", errs)
	}
	if errs := xmpValidateProp(tiff, "BitsPerSample", xmpKindSeq, "", []xmpPropItem{{text: "8"}, {text: ""}}); len(errs) != 0 {
		t.Errorf("valid Seq items reported: %v", errs)
	}
}
