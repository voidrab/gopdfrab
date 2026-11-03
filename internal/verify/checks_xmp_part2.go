package verify

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// xmpSchemaDef is what an extension schema defines for one namespace: the
// value type of each property, and the types to check them against.
type xmpSchemaDef struct {
	props map[string]string
	types *xmpTypeSet
}

// xmpEmptyTypes are the types an extension schema object checks a value
// type name against when its schema defines no properties: the basic types
// without the structured ones.
var xmpEmptyTypes = func() *xmpTypeSet {
	ts := &xmpTypeSet{simple: map[string]*regexp.Regexp{}, structs: map[string]xmpStructType{}}
	for k, v := range xmpBasicSimpleTypes {
		if k != "locale" {
			ts.simple[k] = v
		}
	}
	return ts
}()

// xmpExtensionDefs reads the properties and value types the extension
// schemas in container define, by namespace. A schema starts from the value
// types base defines for its namespace, when it has any. A schema without a
// namespace URI or a property array defines nothing; a later schema for a
// namespace replaces an earlier one.
func xmpExtensionDefs(container *xmpNode, base map[string]*xmpSchemaDef) map[string]*xmpSchemaDef {
	defs := map[string]*xmpSchemaDef{}
	if container == nil || !container.isArray() {
		return defs
	}
	for _, schema := range container.children {
		var props, valueTypes *xmpNode
		ns, hasNS := "", false
		for _, c := range schema.children {
			if c.ns != nsScm {
				continue
			}
			switch c.name {
			case "property":
				if c.isArray() {
					props = c
				}
			case "valueType":
				if c.isArray() {
					valueTypes = c
				}
			case "namespaceURI":
				ns, hasNS = c.value, true
			}
		}
		if !hasNS || props == nil {
			continue
		}
		types := xmpPart2Types
		if b := base[ns]; b != nil {
			types = b.types
		}
		def := &xmpSchemaDef{props: map[string]string{}, types: types.clone()}
		if valueTypes != nil {
			for _, vt := range valueTypes.children {
				def.types.addValueType(vt)
			}
		}
		for _, p := range props.children {
			name := p.child(nsProp, "name")
			valueType := p.child(nsProp, "valueType")
			if name == nil || valueType == nil || !def.types.known(valueType.value) {
				continue
			}
			if _, dup := def.props[name.value]; !dup {
				def.props[name.value] = valueType.value
			}
		}
		defs[ns] = def
	}
	return defs
}

// addValueType adds the value type an extension schema's pdfaType entry
// defines: a structure when it has fields, Text otherwise.
func (ts *xmpTypeSet) addValueType(vt *xmpNode) {
	var name, ns *xmpNode
	fields := map[string]string{}
	for _, c := range vt.children {
		if c.ns != nsType {
			continue
		}
		switch c.name {
		case "type":
			name = c
		case "namespaceURI":
			ns = c
		case "field":
			if !c.isArray() {
				continue
			}
			for _, f := range c.children {
				fn, ft := f.child(nsFld, "name"), f.child(nsFld, "valueType")
				if fn != nil && ft != nil {
					fields[fn.value] = ft.value
				}
			}
		}
	}
	if name == nil {
		return
	}
	t := xmpSimplifyType(name.value)
	switch {
	case len(fields) == 0:
		ts.simple[t] = nil
	case ns != nil:
		ts.structs[t] = xmpStructType{ns: ns.value, fields: fields}
	}
}

// checkXMPPackagePart2 checks one metadata stream's XMP under ISO 19005-2:
// the xpacket header (6.6.2.1), that every property is defined and has its
// defined value type (6.6.2.3.1), and the extension schemas (6.6.2.3.2,
// 6.6.2.3.3). The extension schemas of main, the document's own packet,
// define properties for every packet, those of a packet for it alone; main
// is nil for the document's own packet.
func checkXMPPackagePart2(xmp string, pkt *xmpPacket, main map[string]*xmpSchemaDef) []pdf.PDFError {
	errs := checkXMPHeader(xmp)
	own := xmpExtensionDefs(pkt.schemas, main)
	for _, n := range pkt.props {
		def := own[n.ns]
		if def == nil || def.props[n.name] == "" {
			def = main[n.ns]
		}
		var typ string
		types := xmpPart2Types
		if def != nil && def.props[n.name] != "" {
			typ, types = def.props[n.name], def.types
		} else {
			typ = xmpPart2Properties[n.ns][n.name]
		}
		switch {
		case typ == "":
			errs = append(errs, xmpErr(pdf.Checks.Metadata.MetadataUndeclaredProperty,
				fmt.Sprintf("property %s:%s (%s) is neither predefined nor defined by an extension schema", n.prefix, n.name, n.ns)))
		case !types.valid(n, typ):
			errs = append(errs, xmpErr(pdf.Checks.Metadata.XMPNoCorrespondingType,
				fmt.Sprintf("property %s:%s does not have its value type %s", n.prefix, n.name, typ)))
		}
	}
	return append(errs, checkExtensionSchemaObjects(pkt.schemas, own)...)
}

// The fields each kind of extension schema object may contain.
var (
	extSchemaFields   = []string{"namespaceURI", "prefix", "property", "schema", "valueType"}
	extPropertyFields = []string{"category", "description", "name", "valueType"}
	extTypeFields     = []string{"namespaceURI", "prefix", "field", "description", "type"}
	extFieldFields    = []string{"description", "name", "valueType"}
)

// extObject is an extension schema object being checked: the node, the
// namespace and prefix of its fields, and the problems found so far.
type extObject struct {
	n          *xmpNode
	ns, prefix string
	what       string
	faults     []string
}

// text records a fault unless field is present as a simple value with the
// right prefix.
func (o *extObject) text(field string) *xmpNode {
	c := o.n.child(o.ns, field)
	switch {
	case c == nil:
		o.faults = append(o.faults, field+" is missing")
	case c.form != xmpSimple:
		o.faults = append(o.faults, field+" is not Text")
	case c.prefix != o.prefix:
		o.faults = append(o.faults, field+" does not use the "+o.prefix+" prefix")
	}
	return c
}

// seq records a fault if field is present but not an ordered array with the
// right prefix.
func (o *extObject) seq(field string) {
	c := o.n.child(o.ns, field)
	switch {
	case c == nil:
	case !c.isOrdered():
		o.faults = append(o.faults, field+" is not a Seq")
	case c.prefix != o.prefix:
		o.faults = append(o.faults, field+" does not use the "+o.prefix+" prefix")
	}
}

// valueType records a fault if field names a type types does not know.
func (o *extObject) valueType(field string, types *xmpTypeSet) {
	if c := o.n.child(o.ns, field); c != nil && c.form == xmpSimple && !types.known(c.value) {
		o.faults = append(o.faults, field+" "+c.value+" is not a defined value type")
	}
}

func (o *extObject) report(errs []pdf.PDFError, c pdf.Check) []pdf.PDFError {
	for _, f := range o.faults {
		errs = append(errs, xmpErr(c, o.what+": "+f))
	}
	return errs
}

// undefinedFields reports the fields of o.n outside allowed.
func (o *extObject) undefinedFields(errs []pdf.PDFError, allowed []string) []pdf.PDFError {
	for _, c := range o.n.children {
		if c.ns != o.ns || !slices.Contains(allowed, c.name) {
			errs = append(errs, xmpErr(pdf.Checks.Metadata.ExtSchemaUndefinedField,
				fmt.Sprintf("%s has field %s (%s) its value type does not define", o.what, c.name, c.ns)))
		}
	}
	return errs
}

// checkExtensionSchemaObjects checks the PDF/A extension schema container
// and each schema, property, value type and field in it against the value
// types of ISO 19005-2 6.6.2.3.3 (6.6.2.3.2, 6.6.2.3.3).
func checkExtensionSchemaObjects(container *xmpNode, defs map[string]*xmpSchemaDef) []pdf.PDFError {
	if container == nil {
		return nil
	}
	var errs []pdf.PDFError
	if container.form != xmpBag || container.prefix != "pdfaExtension" {
		errs = append(errs, xmpErr(pdf.Checks.Metadata.ExtSchemasNotBag,
			"pdfaExtension:schemas is not an rdf:Bag with the pdfaExtension prefix"))
	}
	if !container.isArray() {
		return errs
	}
	for _, schema := range container.children {
		types := xmpEmptyTypes
		if ns := schema.child(nsScm, "namespaceURI"); ns != nil && defs[ns.value] != nil {
			types = defs[ns.value].types
		}
		s := &extObject{n: schema, ns: nsScm, prefix: "pdfaSchema", what: "extension schema"}
		errs = s.undefinedFields(errs, extSchemaFields)
		s.text("schema")
		s.text("namespaceURI")
		s.text("prefix")
		s.seq("property")
		s.seq("valueType")
		errs = s.report(errs, pdf.Checks.Metadata.ExtSchemaDefinitionInvalid)

		for _, p := range arrayItems(schema.child(nsScm, "property")) {
			o := &extObject{n: p, ns: nsProp, prefix: "pdfaProperty", what: "extension schema property"}
			errs = o.undefinedFields(errs, extPropertyFields)
			o.text("name")
			if c := o.text("category"); c != nil && c.form == xmpSimple && c.value != "external" && c.value != "internal" {
				o.faults = append(o.faults, "category "+c.value+" is neither external nor internal")
			}
			o.text("description")
			errs = o.report(errs, pdf.Checks.Metadata.ExtPropertyMissingField)
			o.faults = nil
			o.text("valueType")
			o.valueType("valueType", types)
			errs = o.report(errs, pdf.Checks.Metadata.ExtPropertyUndefinedType)
		}
		for _, vt := range arrayItems(schema.child(nsScm, "valueType")) {
			o := &extObject{n: vt, ns: nsType, prefix: "pdfaType", what: "extension schema value type"}
			errs = o.undefinedFields(errs, extTypeFields)
			o.text("type")
			o.text("namespaceURI")
			o.text("prefix")
			o.text("description")
			o.seq("field")
			errs = o.report(errs, pdf.Checks.Metadata.ExtTypeInvalid)
			for _, f := range arrayItems(vt.child(nsType, "field")) {
				o := &extObject{n: f, ns: nsFld, prefix: "pdfaField", what: "extension schema field"}
				errs = o.undefinedFields(errs, extFieldFields)
				o.text("name")
				o.text("valueType")
				o.valueType("valueType", types)
				o.text("description")
				errs = o.report(errs, pdf.Checks.Metadata.ExtFieldInvalid)
			}
		}
	}
	return errs
}

// arrayItems returns the items of n when it is an array.
func arrayItems(n *xmpNode) []*xmpNode {
	if n == nil || !n.isArray() {
		return nil
	}
	return n.children
}

// checkPDFAIdentificationPart2 checks the PDF/A identification schema of the
// document's packet under ISO 19005-2 (6.6.4).
func checkPDFAIdentificationPart2(pkt *xmpPacket, spec pdfa.Spec) []pdf.PDFError {
	var id []*xmpNode
	for _, n := range pkt.props {
		if n.ns == pdfaIDNamespace {
			id = append(id, n)
		}
	}
	if len(id) == 0 {
		return []pdf.PDFError{xmpErr(pdf.Checks.Metadata.PDFAIdentifierMissing,
			"XMP metadata has no PDF/A identification properties")}
	}
	var errs []pdf.PDFError
	part := pkt.prop(pdfaIDNamespace, "part")
	if n, err := strconv.Atoi(simpleValue(part)); err != nil || strconv.Itoa(n) != spec.XMPPart {
		errs = append(errs, xmpErr(pdf.Checks.Metadata.PDFAPartNumber,
			fmt.Sprintf("pdfaid:part is %q, not %s", simpleValue(part), spec.XMPPart)))
	}
	conf := simpleValue(pkt.prop(pdfaIDNamespace, "conformance"))
	if !spec.XMPConformances.Has(conf) {
		errs = append(errs, xmpErr(pdf.Checks.Metadata.PDFAConformanceLevel,
			fmt.Sprintf("invalid PDF/A conformance level %q", conf)))
	}
	for _, name := range []string{"part", "conformance", "amd", "corr"} {
		if n := pkt.prop(pdfaIDNamespace, name); n != nil && n.prefix != "pdfaid" {
			errs = append(errs, xmpErr(pdf.Checks.Metadata.PDFAIdentifierNamespace,
				fmt.Sprintf("PDF/A identification property %s uses prefix %q, not pdfaid", name, n.prefix)))
		}
	}
	return errs
}

// simpleValue returns n's value when it is a simple property.
func simpleValue(n *xmpNode) string {
	if n == nil || n.form != xmpSimple {
		return ""
	}
	return n.value
}

// verifyXMPMetadataPart2 checks the document's metadata stream meta, whose
// decoded content is data or whose decoding failed with readErr, under
// ISO 19005-2 (6.6). XMP that does not parse has no properties, so it has
// no PDF/A identification either.
func verifyXMPMetadataPart2(data []byte, meta pdf.PDFDict, readErr error, spec pdfa.Spec) []pdf.PDFError {
	var errs []pdf.PDFError
	if meta.Entries.Get("Type") != (pdf.PDFName{Value: "Metadata"}) || meta.Entries.Get("Subtype") != (pdf.PDFName{Value: "XML"}) {
		errs = append(errs, xmpErr(pdf.Checks.Metadata.MetadataMissing,
			"the catalog's metadata stream is not of Type Metadata and Subtype XML"))
	}
	if readErr != nil {
		return append(errs, xmpErr(pdf.Checks.Metadata.XMPStreamUnreadable, "unable to read XMP metadata stream"))
	}
	pkt, err := parseXMPPacket(data)
	if err != nil {
		return append(errs,
			xmpErr(pdf.Checks.Metadata.XMPNotWellFormed, "XMP metadata does not parse: "+err.Error()),
			xmpErr(pdf.Checks.Metadata.PDFAIdentifierMissing, "XMP metadata has no PDF/A identification properties"))
	}
	errs = append(errs, checkXMPPackagePart2(string(data), pkt, nil)...)
	return append(errs, checkPDFAIdentificationPart2(pkt, spec)...)
}

// checkObjectXMPPart2 checks the XMP of a metadata stream other than the
// document's under ISO 19005-2, with the extension schemas the document's
// own packet defines in main.
func checkObjectXMPPart2(data []byte, main map[string]*xmpSchemaDef) []pdf.PDFError {
	pkt, err := parseXMPPacket(data)
	if err != nil {
		return []pdf.PDFError{xmpErr(pdf.Checks.Metadata.XMPNotWellFormed, "XMP metadata does not parse: "+err.Error())}
	}
	return checkXMPPackagePart2(string(data), pkt, main)
}
