package pdf

import (
	"fmt"
	"strconv"
)

// Check is a named, selectable PDF/A validation rule, identified by a
// (clause, subclause) pair and grouped into categories under Checks.
type Check struct {
	id          int    // unique sequential ID (never 0 for registered checks)
	name        string // CamelCase identifier
	description string // human-readable rule summary
	clause      string // PDF/A-1 clause, e.g. "6.4"
	subclause   int    // internal sub-rule number within the clause
}

// Name returns the CamelCase identifier of this check (e.g. "ImageWithSoftMask").
func (c Check) Name() string { return c.name }

// Description returns a human-readable summary of what this check enforces.
func (c Check) Description() string { return c.description }

// Clause returns the PDF/A-1 specification clause number (e.g. "6.1.2").
func (c Check) Clause() string { return c.clause }

// Subclause returns the internal sub-rule index within the clause.
func (c Check) Subclause() int { return c.subclause }

// ID returns this check's unique sequential registration ID, used by Profile
// as a compact key for its enabled-check set.
func (c Check) ID() int { return c.id }

// --- Check group types --------------------------------------------------

type structureChecks struct {
	// 6.1.2 File header
	FileHeaderSignature     Check
	FileHeaderComment       Check
	FileHeaderCommentLength Check
	FileHeaderCommentBytes  Check
	// 6.1.3 File trailer
	TrailerID      Check // merged with 6.1.3-4 (linearized PDF)
	TrailerEncrypt Check
	TrailerEOF     Check
	// 6.1.4 Cross-reference table
	XRefKeyword                Check
	XRefSubsectionHeader       Check
	XRefSubsectionHeaderFormat Check
	XRefStream                 Check
	// 6.1.5 Document information dictionary
	InfoDictUnreadable  Check
	InfoDictEmptyValues Check
	InfoDictXMPMismatch Check
	// 6.1.6 Metadata stream
	GraphResolutionFailure Check
	HexStringInvalidChar   Check
	HexStringOddLength     Check
	// 6.1.7 Stream objects (external file references, EOL framing)
	StreamFileSpec          Check
	StreamFileFilter        Check
	StreamFileDecodeParams  Check
	StreamKeywordEOL        Check
	EndstreamEOL            Check
	StreamLengthIncludesEOL Check
	StreamLengthMismatch    Check
	// 6.1.8 Object framing
	ObjectFraming Check
	// 6.1.10 LZW compression
	StreamLZWFilter      Check
	InlineImageLZWFilter Check
	// 6.1.11 Embedded files
	EmbeddedFileSpec Check
	EmbeddedFiles    Check
	// 6.1.12 Architectural limits
	NameTooLong             Check
	IntegerOutOfRange       Check
	RealOutOfRange          Check
	ArrayTooLarge           Check
	DictTooLarge            Check
	CMapCIDOutOfRange       Check
	StringTooLong           Check
	DeviceNColorants        Check
	IndirectObjectsExceeded Check
	GraphicsStateNesting    Check
	// 6.1.13 Optional content
	OptionalContent Check
	// 6.1.2 File header — post-1.4 ViewerPreferences keys
	PostPDF14ViewerPref Check
}

type colourChecks struct {
	// 6.2.2 OutputIntent
	OutputIntentNotArray          Check
	OutputIntentNotDict           Check
	OutputIntentInvalidS          Check
	OutputIntentWrongS            Check
	OutputIntentMissingIdentifier Check
	OutputIntentMultipleProfiles  Check
	OutputIntentUnresolvedProfile Check
	OutputIntentInvalidProfile    Check
	OutputIntentMissingN          Check
	OutputIntentInvalidN          Check
	OutputIntentICCVersion        Check // contains profile and components mismatch checks
	// 6.2.3.2 ICCBased colour spaces
	ICCBasedComponentsMismatch Check // TODO add convert
	// 6.2.3.3 Device colour spaces
	DeviceColourSpaceUsage    Check
	DeviceColourContentStream Check
	// 6.2.3.4 Separation / DeviceN
	SeparationAlternateColour Check
	// 6.2.9 Rendering intent
	RenderingIntent Check
	// 6.2.10 PDF operators
	UndefinedOperator Check
}

type imageChecks struct {
	// 6.2.4 Image XObjects
	ImageInterpolate          Check
	ImageAlternates           Check
	ImageOPI                  Check
	ImageRenderingIntent      Check
	ImageBitsPerComponent     Check
	ImageMaskBitsPerComponent Check
	// 6.2.5 Form XObjects
	FormOPI        Check
	FormSubtype2PS Check // Subtype2=PS additional entry
	FormPSEntry    Check // PS passthrough key (also caught as 6.2.7 in strict profile)
	// 6.2.6 Reference XObjects
	ReferenceXObject Check
	// 6.2.7 PostScript XObjects
	FormPostScript    Check
	PostScriptXObject Check
}

type transparencyChecks struct {
	// 6.2.8 Graphics state
	TransferFunction         Check
	DefaultTransferFunction  Check
	ExtGStateRenderingIntent Check
	// 6.4 Transparency (soft masks, blend modes, alpha, groups)
	SoftMaskExtGState Check
	BlendMode         Check
	StrokingAlpha     Check
	NonStrokingAlpha  Check
	TransparencyGroup Check
	ImageWithSoftMask Check
}

type fontChecks struct {
	// 6.3.2 Font dictionary and program validity
	FontType            Check // unused, wontdo
	InvalidSubtype      Check
	FontBaseFont        Check // TODO add convert
	SimpleFontFirstChar Check // unused, wontdo
	SimpleFontLastChar  Check // unused, wontdo
	SimpleFontWidths    Check // unused, wontdo
	FontFileSubtype     Check // TODO add check
	InvalidProgram      Check
	// 6.3.3.1 CIDSystemInfo consistency
	CIDSystemInfoMismatch Check
	// 6.3.3.2 CIDToGIDMap
	CIDToGIDMapMissing Check
	// 6.3.3.3 CMap embedding
	CMapNotEmbedded       Check
	CMapWModeInconsistent Check
	// 6.3.4 Font embedding
	SimpleNotEmbedded Check
	CIDNotEmbedded    Check
	// 6.3.8 ToUnicode (Level A)
	ToUnicodeMissing Check // TODO level A
	// 6.3.5 Subset coverage
	SubsetGlyphCoverage Check
	Type1SubsetCharSet  Check
	CIDSubsetCIDSet     Check
	// 6.3.6 Advance widths
	AdvanceWidthMismatch Check
	// 6.3.7 TrueType encoding
	TrueTypeEncoding         Check
	SymbolicTrueTypeEncoding Check
	SymbolicTrueTypeCmap     Check
}

type annotationChecks struct {
	// 6.5.2 Annotation subtypes
	DisallowedSubtype Check
	// 6.5.3 Annotation dictionaries
	PrintFlagNotSet        Check
	HiddenFlagSet          Check
	InvisibleFlagSet       Check
	NoViewFlagSet          Check
	OpacityNotOne          Check
	MissingAppearance      Check
	AppearanceMissingN     Check
	AppearanceExtraEntries Check
	AppearanceNNotStream   Check
	ColourWithoutIntent    Check
}

type actionChecks struct {
	// 6.6.1 Action types
	ForbiddenActionType   Check
	DisallowedNamedAction Check
	// 6.6.2 Additional actions
	AdditionalActions Check
}

type metadataChecks struct {
	// 6.7.2 Metadata stream
	MetadataMissing            Check
	MetadataFiltered           Check
	MetadataPropertyType       Check
	MetadataUndeclaredProperty Check
	// 6.7.3 Info / XMP synchronisation
	InfoXMPSync Check
	// 6.7.5 xpacket header
	XPacketBytesAttribute    Check
	XPacketEncodingAttribute Check
	ObjectXMPNoXPacket       Check
	// 6.7.8 Extension schemas
	ExtSchemaNamespace         Check
	ExtSchemaWrongPrefixURI    Check
	ExtSchemasNotBag           Check
	ExtPropertyMultipleName    Check
	ExtPropertyMissingField    Check
	ExtPropertyComplexAsSimple Check
	ExtTypeInvalid             Check
	ExtFieldInvalid            Check
	ExtPropertyUndocumented    Check
	ExtPropertyUndefinedType   Check
	// 6.7.9 XMP well-formedness
	XMPStreamUnreadable    Check
	XMPNotWellFormed       Check
	XMPNoCorrespondingType Check // TODO add check
	// 6.7.11 PDF/A identifier
	PDFAIdentifierMissing           Check
	PDFAIdentifierNamespace         Check
	PDFAConformanceLevel            Check
	PDFAPartNumber                  Check
	PDFAIdentifierUndefinedProperty Check
}

type logicalStructureChecks struct {
	// 6.8.2.2 Tagged PDF
	TaggedMarkInfo Check // TODO level A
	// 6.8.3.3 Structure tree
	StructTreeRoot Check // TODO level A
	// 6.8.3.4 Role map
	RoleMapStandardType Check // TODO level A
	RoleMapCircular     Check // TODO level A
	// 6.8.4 Natural language
	LangIdentifier Check // TODO level A
}

type formChecks struct {
	// 6.9 Interactive forms
	NeedAppearances         Check
	XFA                     Check
	FieldAction             Check
	FieldAdditionalActions  Check
	WidgetMissingAppearance Check
}

// ObjectModelClause is the synthetic clause the generic object-model checks
// register under, distinguishing them from numeric PDF/A clauses.
const ObjectModelClause = "objmodel"

// objectModelChecks are generic ISO 32000 object-model checks, driven by the
// Arlington PDF Model table (internal/arlington) rather than by hand-written
// per-clause logic. They catch "this isn't even valid PDF", orthogonal to the
// PDF/A-specific restrictions the other check groups enforce.
type objectModelChecks struct {
	MissingRequiredKey      Check
	WrongValueType          Check
	DisallowedValue         Check
	IndirectRequired        Check
	KeyIntroducedAfterPDF14 Check
}

type checksRegistry struct {
	Structure        structureChecks
	Colour           colourChecks
	Image            imageChecks
	Transparency     transparencyChecks
	Font             fontChecks
	LogicalStructure logicalStructureChecks
	Annotation       annotationChecks
	Action           actionChecks
	Metadata         metadataChecks
	Form             formChecks
	ObjectModel      objectModelChecks
}

var Checks checksRegistry

var catalogByPair map[string]Check

var catalogByName map[string]Check

var allChecksCatalog []Check

var checkIDCounter int

// AllChecks returns every registered check in catalog order.
func AllChecks() []Check {
	out := make([]Check, len(allChecksCatalog))
	copy(out, allChecksCatalog)
	return out
}

// CheckByClause looks up the registered check for a specific (clause,
// subclause) pair, e.g. CheckByClause("6.3.4", 1). ok is false if no check is
// registered for that pair.
func CheckByClause(clause string, subclause int) (c Check, ok bool) {
	key := clause + "/" + strconv.Itoa(subclause)
	c, ok = catalogByPair[key]
	return c, ok
}

// ChecksForClause returns every registered check under the given clause
// (e.g. "6.3.4"), in catalog order.
func ChecksForClause(clause string) []Check {
	var out []Check
	for _, c := range allChecksCatalog {
		if c.clause == clause {
			out = append(out, c)
		}
	}
	return out
}

func newCheck(name, description, clause string, subclause int) Check {
	checkIDCounter++
	c := Check{
		id:          checkIDCounter,
		name:        name,
		description: description,
		clause:      clause,
		subclause:   subclause,
	}
	key := clause + "/" + strconv.Itoa(subclause)
	if _, exists := catalogByPair[key]; exists {
		panic(fmt.Sprintf("checks_catalog: duplicate registration for %s", key))
	}
	catalogByPair[key] = c
	catalogByName[name] = c
	allChecksCatalog = append(allChecksCatalog, c)
	return c
}

// CheckByName looks up a registered check by its CamelCase identifier (e.g.
// "ObjectFraming"), used to map a pdf.Reader's parse-time StructError
// diagnostics -- which only carry that name, to avoid this package's check
// registry being a dependency of the pdf package -- back to a
func CheckByName(name string) (c Check, ok bool) {
	c, ok = catalogByName[name]
	return c, ok
}

func init() {
	catalogByPair = make(map[string]Check)
	catalogByName = make(map[string]Check)

	Checks = checksRegistry{
		Structure: structureChecks{
			FileHeaderSignature: newCheck(
				"FileHeaderSignature",
				"The file header must begin with the %PDF-1.x signature",
				"6.1.2", 1),
			FileHeaderComment: newCheck(
				"FileHeaderComment",
				"The file header must be followed by a comment line starting with %",
				"6.1.2", 2),
			FileHeaderCommentLength: newCheck(
				"FileHeaderCommentLength",
				"The header comment line must be at least 5 characters long",
				"6.1.2", 3),
			FileHeaderCommentBytes: newCheck(
				"FileHeaderCommentBytes",
				"All bytes in the header comment must have a value greater than 127 (binary file indicator)",
				"6.1.2", 4),
			TrailerID: newCheck(
				"TrailerID",
				"The trailer dictionary must contain an ID entry; in linearized files ID[0] must be consistent across all trailers",
				"6.1.3", 1),
			TrailerEncrypt: newCheck(
				"TrailerEncrypt",
				"The trailer dictionary must not contain an Encrypt entry",
				"6.1.3", 2),
			TrailerEOF: newCheck(
				"TrailerEOF",
				"No data shall follow the last %%EOF marker except a single optional end-of-line marker",
				"6.1.3", 3),
			XRefKeyword: newCheck(
				"XRefKeyword",
				"The cross-reference section must begin with the xref keyword",
				"6.1.4", 1),
			XRefSubsectionHeader: newCheck(
				"XRefSubsectionHeader",
				"The xref keyword must be followed by a cross-reference subsection header",
				"6.1.4", 2),
			XRefSubsectionHeaderFormat: newCheck(
				"XRefSubsectionHeaderFormat",
				"The cross-reference subsection header must match the 'startObj count' format with a single space separator",
				"6.1.4", 3),
			XRefStream: newCheck(
				"XRefStream",
				"Cross-reference streams must not be used",
				"6.1.4", 4),
			InfoDictUnreadable: newCheck(
				"InfoDictUnreadable",
				"The document information dictionary must be readable",
				"6.1.5", 1),
			InfoDictEmptyValues: newCheck(
				"InfoDictEmptyValues",
				"Document information dictionary entries must not have empty string values",
				"6.1.5", 2),
			InfoDictXMPMismatch: newCheck(
				"InfoDictXMPMismatch",
				"Document information dictionary entries must match the corresponding XMP metadata values",
				"6.1.5", 3),
			GraphResolutionFailure: newCheck(
				"GraphResolutionFailure",
				"The document object graph must be fully resolvable",
				"6.1.6", 0),
			HexStringInvalidChar: newCheck(
				"HexStringInvalidChar",
				"Hexadecimal strings must contain only valid hex characters (0-9, A-F, a-f)",
				"6.1.6", 1),
			HexStringOddLength: newCheck(
				"HexStringOddLength",
				"Hexadecimal strings must contain an even number of non-white-space characters",
				"6.1.6", 2),
			StreamFileSpec: newCheck(
				"StreamFileSpec",
				"Stream objects must not specify an external file reference via the F key",
				"6.1.7", 1),
			StreamFileFilter: newCheck(
				"StreamFileFilter",
				"Stream objects must not specify external filters via the FFilter key",
				"6.1.7", 2),
			StreamFileDecodeParams: newCheck(
				"StreamFileDecodeParams",
				"Stream objects must not specify external decode parameters via the FDecodeParms key",
				"6.1.7", 3),
			StreamKeywordEOL: newCheck(
				"StreamKeywordEOL",
				"The 'stream' keyword must be followed by a single EOL marker (CRLF or LF)",
				"6.1.7", 4),
			EndstreamEOL: newCheck(
				"EndstreamEOL",
				"The 'endstream' keyword must be preceded by an EOL marker",
				"6.1.7", 5),
			StreamLengthIncludesEOL: newCheck(
				"StreamLengthIncludesEOL",
				"The stream Length value must not include the EOL marker preceding endstream",
				"6.1.7", 6),
			StreamLengthMismatch: newCheck(
				"StreamLengthMismatch",
				"The stream Length value must match the actual number of bytes between stream and endstream",
				"6.1.7", 7),
			ObjectFraming: newCheck(
				"ObjectFraming",
				"Objects must follow the 'N G obj … endobj' framing syntax",
				"6.1.8", 1),
			StreamLZWFilter: newCheck(
				"StreamLZWFilter",
				"Stream objects must not use the LZWDecode filter",
				"6.1.10", 1),
			InlineImageLZWFilter: newCheck(
				"InlineImageLZWFilter",
				"Inline images must not use the LZW filter",
				"6.1.10", 2),
			EmbeddedFileSpec: newCheck(
				"EmbeddedFileSpec",
				"Dictionaries must not contain an EF (embedded file specification) key",
				"6.1.11", 1),
			EmbeddedFiles: newCheck(
				"EmbeddedFiles",
				"Dictionaries must not contain an EmbeddedFiles key",
				"6.1.11", 2),
			NameTooLong: newCheck(
				"NameTooLong",
				"Name objects and dictionary keys must not exceed 127 bytes",
				"6.1.12", 1),
			IntegerOutOfRange: newCheck(
				"IntegerOutOfRange",
				"Integer values must be within the range [-2^31, 2^31-1]",
				"6.1.12", 2),
			RealOutOfRange: newCheck(
				"RealOutOfRange",
				"Real values must have an absolute value not exceeding 32,767.0",
				"6.1.12", 8),
			ArrayTooLarge: newCheck(
				"ArrayTooLarge",
				"Arrays must not contain more than 8,191 elements",
				"6.1.12", 3),
			DictTooLarge: newCheck(
				"DictTooLarge",
				"Dictionaries must not contain more than 4,095 entries",
				"6.1.12", 4),
			CMapCIDOutOfRange: newCheck(
				"CMapCIDOutOfRange",
				"CMap character identifier (CID) values must not exceed 65,535",
				"6.1.12", 5),
			StringTooLong: newCheck(
				"StringTooLong",
				"String objects must not exceed 65,535 bytes",
				"6.1.12", 6),
			DeviceNColorants: newCheck(
				"DeviceNColorants",
				"DeviceN colour spaces must not exceed 8 colorants",
				"6.1.12", 7),
			IndirectObjectsExceeded: newCheck(
				"IndirectObjectsExceeded",
				"The number of indirect objects in the file must not exceed 8,388,607",
				"6.1.12", 9),
			GraphicsStateNesting: newCheck(
				"GraphicsStateNesting",
				"Graphics state nesting via q/Q operators must not exceed 28 levels",
				"6.1.12", 10),
			OptionalContent: newCheck(
				"OptionalContent",
				"The document catalog must not contain an OCProperties entry (optional content is not permitted in PDF/A-1)",
				"6.1.13", 1),
			PostPDF14ViewerPref: newCheck(
				"PostPDF14ViewerPref",
				"ViewerPreferences must not contain keys introduced after PDF 1.4 (e.g. PrintScaling, PickTrayByPDFSize)",
				"6.1.2", 5),
		},

		Colour: colourChecks{
			OutputIntentNotArray: newCheck(
				"OutputIntentNotArray",
				"The OutputIntents entry must be an array",
				"6.2.2", 1),
			OutputIntentNotDict: newCheck(
				"OutputIntentNotDict",
				"Each OutputIntents array entry must be a dictionary",
				"6.2.2", 2),
			OutputIntentInvalidS: newCheck(
				"OutputIntentInvalidS",
				"Each OutputIntent dictionary must have a valid S (subtype) name entry",
				"6.2.2", 3),
			OutputIntentWrongS: newCheck(
				"OutputIntentWrongS",
				"The OutputIntent S entry must be /GTS_PDFA1",
				"6.2.2", 4),
			OutputIntentMissingIdentifier: newCheck(
				"OutputIntentMissingIdentifier",
				"Each OutputIntent must contain an OutputConditionIdentifier entry",
				"6.2.2", 5),
			OutputIntentMultipleProfiles: newCheck(
				"OutputIntentMultipleProfiles",
				"When multiple OutputIntents contain a DestOutputProfile, all must reference the same indirect object",
				"6.2.2", 6),
			OutputIntentUnresolvedProfile: newCheck(
				"OutputIntentUnresolvedProfile",
				"The OutputIntent DestOutputProfile must be resolvable",
				"6.2.2", 7),
			OutputIntentInvalidProfile: newCheck(
				"OutputIntentInvalidProfile",
				"The OutputIntent DestOutputProfile must be a dictionary",
				"6.2.2", 8),
			OutputIntentMissingN: newCheck(
				"OutputIntentMissingN",
				"The OutputIntent ICC profile stream must contain an N (number of colour components) entry",
				"6.2.2", 9),
			OutputIntentInvalidN: newCheck(
				"OutputIntentInvalidN",
				"The OutputIntent ICC profile N must be 1, 3, or 4",
				"6.2.2", 10),
			OutputIntentICCVersion: newCheck(
				"OutputIntentICCVersion",
				"The OutputIntent ICC profile must conform to ICC.1:2003-09 (version ≤ 2.x)",
				"6.2.2", 11),
			ICCBasedComponentsMismatch: newCheck(
				"ICCBasedComponentsMismatch",
				"An ICCBased colour space N entry must be 1, 3, or 4 and match the component count of the embedded ICC profile",
				"6.2.3.2", 1),
			DeviceColourSpaceUsage: newCheck(
				"DeviceColourSpaceUsage",
				"Device colour spaces used in image or shading XObjects require a matching OutputIntent",
				"6.2.3.3", 1),
			DeviceColourContentStream: newCheck(
				"DeviceColourContentStream",
				"Device colour spaces used in content streams require a matching OutputIntent",
				"6.2.3.3", 2),
			SeparationAlternateColour: newCheck(
				"SeparationAlternateColour",
				"Separation and DeviceN alternate colour spaces must not reduce to an uncovered device colour space",
				"6.2.3.4", 1),
			RenderingIntent: newCheck(
				"RenderingIntent",
				"Rendering intent must be one of AbsoluteColorimetric, RelativeColorimetric, Saturation, or Perceptual",
				"6.2.9", 1),
			UndefinedOperator: newCheck(
				"UndefinedOperator",
				"Content streams must not use operators not defined in the PDF Reference",
				"6.2.10", 1),
		},

		Image: imageChecks{
			ImageInterpolate: newCheck(
				"ImageInterpolate",
				"Image XObject Interpolate entry must not be true",
				"6.2.4", 1),
			ImageAlternates: newCheck(
				"ImageAlternates",
				"Image XObjects must not contain an Alternates entry",
				"6.2.4", 2),
			ImageOPI: newCheck(
				"ImageOPI",
				"Image XObjects must not contain an OPI entry",
				"6.2.4", 3),
			ImageRenderingIntent: newCheck(
				"ImageRenderingIntent",
				"Image XObject Intent must be a valid PDF rendering intent",
				"6.2.4", 4),
			ImageBitsPerComponent: newCheck(
				"ImageBitsPerComponent",
				"An image XObject BitsPerComponent value must be 1, 2, 4, or 8",
				"6.2.4", 5),
			ImageMaskBitsPerComponent: newCheck(
				"ImageMaskBitsPerComponent",
				"An image mask BitsPerComponent value must be 1",
				"6.2.4", 6),
			FormOPI: newCheck(
				"FormOPI",
				"Form XObjects must not contain an OPI entry",
				"6.2.5", 1),
			FormSubtype2PS: newCheck(
				"FormSubtype2PS",
				"Form XObjects must not have a Subtype2=PS entry",
				"6.2.5", 2),
			FormPSEntry: newCheck(
				"FormPSEntry",
				"Form XObjects must not contain a PostScript passthrough (PS) entry",
				"6.2.5", 3),
			ReferenceXObject: newCheck(
				"ReferenceXObject",
				"Reference XObjects (/Ref) are not permitted in PDF/A-1",
				"6.2.6", 1),
			FormPostScript: newCheck(
				"FormPostScript",
				"Form XObjects must not contain a PostScript passthrough (PS) entry",
				"6.2.7", 1),
			PostScriptXObject: newCheck(
				"PostScriptXObject",
				"PostScript XObjects (/Subtype /PS) are not permitted in PDF/A-1",
				"6.2.7", 2),
		},

		Transparency: transparencyChecks{
			TransferFunction: newCheck(
				"TransferFunction",
				"ExtGState dictionaries must not contain a TR (transfer function) entry",
				"6.2.8", 1),
			DefaultTransferFunction: newCheck(
				"DefaultTransferFunction",
				"ExtGState TR2 entry, when present, must be /Default",
				"6.2.8", 2),
			ExtGStateRenderingIntent: newCheck(
				"ExtGStateRenderingIntent",
				"ExtGState RI entry, when present, must be one of the four standard rendering intents",
				"6.2.8", 3),
			SoftMaskExtGState: newCheck(
				"SoftMaskExtGState",
				"ExtGState SMask entry must be /None (soft masks introduce transparency)",
				"6.4", 1),
			BlendMode: newCheck(
				"BlendMode",
				"ExtGState blend mode (BM) must be /Normal or /Compatible",
				"6.4", 2),
			StrokingAlpha: newCheck(
				"StrokingAlpha",
				"ExtGState stroking alpha (CA) must be 1.0",
				"6.4", 3),
			NonStrokingAlpha: newCheck(
				"NonStrokingAlpha",
				"ExtGState non-stroking alpha (ca) must be 1.0",
				"6.4", 4),
			TransparencyGroup: newCheck(
				"TransparencyGroup",
				"Transparency groups (/Group with /S /Transparency) are not permitted in PDF/A-1",
				"6.4", 5),
			ImageWithSoftMask: newCheck(
				"ImageWithSoftMask",
				"Image XObjects must not contain a soft mask (SMask) other than /None",
				"6.4", 6),
		},

		Font: fontChecks{
			FontType: newCheck(
				"FontType",
				"A font dictionary must have a Type entry with the value Font",
				"6.3.2", 1),
			InvalidSubtype: newCheck(
				"InvalidSubtype",
				"Font dictionaries must have a valid Subtype entry (Type1, MMType1, TrueType, Type3, Type0, CIDFontType0, CIDFontType2)",
				"6.3.2", 2),
			FontBaseFont: newCheck(
				"FontBaseFont",
				"A font dictionary must have a BaseFont name entry (except Type3 fonts)",
				"6.3.2", 3),
			SimpleFontFirstChar: newCheck(
				"SimpleFontFirstChar",
				"A non-standard simple font dictionary must have a FirstChar entry",
				"6.3.2", 4),
			SimpleFontLastChar: newCheck(
				"SimpleFontLastChar",
				"A non-standard simple font dictionary must have a LastChar entry",
				"6.3.2", 5),
			SimpleFontWidths: newCheck(
				"SimpleFontWidths",
				"A non-standard simple font dictionary must have a Widths array of size LastChar − FirstChar + 1",
				"6.3.2", 6),
			FontFileSubtype: newCheck(
				"FontFileSubtype",
				"An embedded font file Subtype, when present, must be Type1C or CIDFontType0C",
				"6.3.2", 7),
			InvalidProgram: newCheck(
				"InvalidProgram",
				"Embedded font programs must be valid according to their respective font format specifications",
				"6.3.2", 8),
			CIDSystemInfoMismatch: newCheck(
				"CIDSystemInfoMismatch",
				"An embedded CMap's CIDSystemInfo must match the descendant CIDFont's CIDSystemInfo",
				"6.3.3.1", 1),
			CIDToGIDMapMissing: newCheck(
				"CIDToGIDMapMissing",
				"CIDFontType2 fonts must specify a CIDToGIDMap",
				"6.3.3.2", 1),
			CMapNotEmbedded: newCheck(
				"CMapNotEmbedded",
				"CMap references must be one of the predefined CMaps or embedded in the file",
				"6.3.3.3", 1),
			CMapWModeInconsistent: newCheck(
				"CMapWModeInconsistent",
				"An embedded CMap's WMode must be consistent with the font's actual writing mode",
				"6.3.3.3", 2),
			SimpleNotEmbedded: newCheck(
				"SimpleNotEmbedded",
				"Type1, MMType1, and TrueType font programs must be embedded",
				"6.3.4", 1),
			CIDNotEmbedded: newCheck(
				"CIDNotEmbedded",
				"CIDFont programs (CIDFontType0, CIDFontType2) must be embedded",
				"6.3.4", 2),
			SubsetGlyphCoverage: newCheck(
				"SubsetGlyphCoverage",
				"Subset fonts must embed all glyphs referenced in the document",
				"6.3.5", 1),
			Type1SubsetCharSet: newCheck(
				"Type1SubsetCharSet",
				"Type1 subset font descriptors must include a CharSet entry listing the embedded glyph names",
				"6.3.5", 2),
			CIDSubsetCIDSet: newCheck(
				"CIDSubsetCIDSet",
				"CID subset font descriptors must include a CIDSet entry",
				"6.3.5", 3),
			AdvanceWidthMismatch: newCheck(
				"AdvanceWidthMismatch",
				"Advance widths in the embedded font program must match the PDF Widths array",
				"6.3.6", 1),
			TrueTypeEncoding: newCheck(
				"TrueTypeEncoding",
				"Non-symbolic TrueType fonts must use MacRomanEncoding or WinAnsiEncoding",
				"6.3.7", 1),
			SymbolicTrueTypeEncoding: newCheck(
				"SymbolicTrueTypeEncoding",
				"Symbolic TrueType fonts must not specify an Encoding entry",
				"6.3.7", 2),
			SymbolicTrueTypeCmap: newCheck(
				"SymbolicTrueTypeCmap",
				"Symbolic TrueType fonts must have exactly one cmap subtable",
				"6.3.7", 3),
			ToUnicodeMissing: newCheck(
				"ToUnicodeMissing",
				"Fonts must include a ToUnicode CMap unless they use a predefined encoding or character collection (Level A)",
				"6.3.8", 1),
		},

		LogicalStructure: logicalStructureChecks{
			TaggedMarkInfo: newCheck(
				"TaggedMarkInfo",
				"The document catalog must include a MarkInfo dictionary with Marked set to true (Level A)",
				"6.8.2.2", 1),
			StructTreeRoot: newCheck(
				"StructTreeRoot",
				"The document catalog must contain a StructTreeRoot entry describing the structure hierarchy (Level A)",
				"6.8.3.3", 1),
			RoleMapStandardType: newCheck(
				"RoleMapStandardType",
				"Non-standard structure types must be mapped to a standard type in the role map (Level A)",
				"6.8.3.4", 1),
			RoleMapCircular: newCheck(
				"RoleMapCircular",
				"The structure type role map must not contain a circular mapping (Level A)",
				"6.8.3.4", 2),
			LangIdentifier: newCheck(
				"LangIdentifier",
				"A Lang entry, where present, must be a valid RFC 1766 language identifier (Level A)",
				"6.8.4", 1),
		},

		Annotation: annotationChecks{
			DisallowedSubtype: newCheck(
				"DisallowedSubtype",
				"Only annotation subtypes permitted by PDF/A-1 may be used",
				"6.5.2", 1),
			PrintFlagNotSet: newCheck(
				"PrintFlagNotSet",
				"Annotations must have the Print flag set",
				"6.5.3", 1),
			HiddenFlagSet: newCheck(
				"HiddenFlagSet",
				"Annotations must not have the Hidden flag set",
				"6.5.3", 2),
			InvisibleFlagSet: newCheck(
				"InvisibleFlagSet",
				"Annotations must not have the Invisible flag set",
				"6.5.3", 3),
			NoViewFlagSet: newCheck(
				"NoViewFlagSet",
				"Annotations must not have the NoView flag set",
				"6.5.3", 4),
			OpacityNotOne: newCheck(
				"OpacityNotOne",
				"Annotation constant opacity (CA) must be 1.0",
				"6.5.3", 5),
			MissingAppearance: newCheck(
				"MissingAppearance",
				"Non-Popup/Link annotations must have an appearance dictionary containing a normal (N) appearance stream",
				"6.5.3", 6),
			AppearanceMissingN: newCheck(
				"AppearanceMissingN",
				"Annotation appearance dictionaries must contain an N entry",
				"6.5.3", 7),
			AppearanceExtraEntries: newCheck(
				"AppearanceExtraEntries",
				"Annotation appearance dictionaries must only contain the N entry",
				"6.5.3", 8),
			AppearanceNNotStream: newCheck(
				"AppearanceNNotStream",
				"Annotation appearance N value must be a stream",
				"6.5.3", 9),
			ColourWithoutIntent: newCheck(
				"ColourWithoutIntent",
				"Annotation colour arrays must only use device colour models covered by an OutputIntent",
				"6.5.3", 10),
		},

		Action: actionChecks{
			ForbiddenActionType: newCheck(
				"ForbiddenActionType",
				"Forbidden action types (Launch, Sound, Movie, JavaScript, etc.) must not be used",
				"6.6.1", 1),
			DisallowedNamedAction: newCheck(
				"DisallowedNamedAction",
				"Named actions must use only permitted names: NextPage, PrevPage, FirstPage, LastPage",
				"6.6.1", 2),
			AdditionalActions: newCheck(
				"AdditionalActions",
				"Additional-actions (AA) dictionaries are not permitted in PDF/A-1",
				"6.6.2", 1),
		},

		Metadata: metadataChecks{
			MetadataMissing: newCheck(
				"MetadataMissing",
				"The document catalog must contain a Metadata entry pointing to an XMP metadata stream; the rdf:RDF root element must be present with the correct prefix",
				"6.7.2", 1),
			MetadataFiltered: newCheck(
				"MetadataFiltered",
				"The Metadata stream must not be filtered; xmp:Title is not a valid XMP property (use dc:title instead)",
				"6.7.2", 2),
			MetadataPropertyType: newCheck(
				"MetadataPropertyType",
				"dc:description must use the LangAlt (rdf:Alt) value type, not plain text",
				"6.7.2", 3),
			MetadataUndeclaredProperty: newCheck(
				"MetadataUndeclaredProperty",
				"Custom-namespace XMP properties require an extension schema declaration (pdfaExtension:schemas)",
				"6.7.2", 4),
			InfoXMPSync: newCheck(
				"InfoXMPSync",
				"Document information dictionary text and date entries must be synchronized with their XMP counterparts",
				"6.7.3", 1),
			XPacketBytesAttribute: newCheck(
				"XPacketBytesAttribute",
				"The XMP xpacket processing instruction must not contain a bytes attribute",
				"6.7.5", 1),
			XPacketEncodingAttribute: newCheck(
				"XPacketEncodingAttribute",
				"The XMP xpacket processing instruction must not contain an encoding attribute",
				"6.7.5", 2),
			ObjectXMPNoXPacket: newCheck(
				"ObjectXMPNoXPacket",
				"Non-catalog XMP metadata streams must be wrapped in xpacket processing instructions (6.7.5)",
				"6.7.5", 3),
			ExtSchemaNamespace: newCheck(
				"ExtSchemaNamespace",
				"Custom-namespace properties require extension schema declarations; extension-schema namespace URIs must use their conventional prefixes",
				"6.7.8", 1),
			ExtSchemaWrongPrefixURI: newCheck(
				"ExtSchemaWrongPrefixURI",
				"Extension-schema prefixes must be bound to their designated namespace URIs",
				"6.7.8", 2),
			ExtSchemasNotBag: newCheck(
				"ExtSchemasNotBag",
				"The pdfaExtension:schemas container must be an rdf:Bag, not an rdf:Seq",
				"6.7.8", 3),
			ExtPropertyMultipleName: newCheck(
				"ExtPropertyMultipleName",
				"Each extension schema property entry must have exactly one pdfaProperty:name element",
				"6.7.8", 4),
			ExtPropertyMissingField: newCheck(
				"ExtPropertyMissingField",
				"Extension schema property entries must provide name, valueType, category, and description",
				"6.7.8", 5),
			ExtPropertyComplexAsSimple: newCheck(
				"ExtPropertyComplexAsSimple",
				"Properties declared with a complex value type must use rdf:parseType='Resource' in the XMP data",
				"6.7.8", 6),
			ExtTypeInvalid: newCheck(
				"ExtTypeInvalid",
				"Extension schema value-type entries must provide typeName, namespaceURI, prefix, and description",
				"6.7.8", 7),
			ExtFieldInvalid: newCheck(
				"ExtFieldInvalid",
				"Extension schema field entries must provide a valid name, valueType, and description",
				"6.7.8", 8),
			ExtPropertyUndocumented: newCheck(
				"ExtPropertyUndocumented",
				"Properties used under a custom namespace must be documented in the extension schema",
				"6.7.8", 9),
			ExtPropertyUndefinedType: newCheck(
				"ExtPropertyUndefinedType",
				"Extension schema properties must reference only built-in or explicitly defined value types",
				"6.7.8", 10),
			XMPStreamUnreadable: newCheck(
				"XMPStreamUnreadable",
				"The XMP metadata stream must be readable",
				"6.7.9", 1),
			XMPNotWellFormed: newCheck(
				"XMPNotWellFormed",
				"The XMP metadata must be well-formed XML",
				"6.7.9", 2),
			XMPNoCorrespondingType: newCheck(
				"XMPNoCorrespondingType",
				"XMP property does not correspond to defined type",
				"6.7.9", 3),
			PDFAIdentifierMissing: newCheck(
				"PDFAIdentifierMissing",
				"XMP metadata must contain the pdfaid namespace with pdfaid:part and pdfaid:conformance entries",
				"6.7.11", 1),
			PDFAIdentifierNamespace: newCheck(
				"PDFAIdentifierNamespace",
				"The pdfaid namespace URI must be the correct PDF/A identifier namespace",
				"6.7.11", 2),
			PDFAConformanceLevel: newCheck(
				"PDFAConformanceLevel",
				"The pdfaid:conformance value must be 'A' or 'B'",
				"6.7.11", 3),
			PDFAPartNumber: newCheck(
				"PDFAPartNumber",
				"The pdfaid:part value must be '1'",
				"6.7.11", 4),
			PDFAIdentifierUndefinedProperty: newCheck(
				"PDFAIdentifierUndefinedProperty",
				"The pdfaid namespace must only contain the part, conformance, and amd properties",
				"6.7.11", 5),
		},

		Form: formChecks{
			NeedAppearances: newCheck(
				"NeedAppearances",
				"The AcroForm NeedAppearances entry must not be true",
				"6.9", 1),
			XFA: newCheck(
				"XFA",
				"The AcroForm must not contain an XFA entry",
				"6.9", 2),
			FieldAction: newCheck(
				"FieldAction",
				"Form fields and widget annotations must not contain an A (action) entry",
				"6.9", 3),
			FieldAdditionalActions: newCheck(
				"FieldAdditionalActions",
				"Form fields and widget annotations must not contain an AA (additional actions) entry",
				"6.9", 4),
			WidgetMissingAppearance: newCheck(
				"WidgetMissingAppearance",
				"Form field widget annotations (with FT) must have an appearance dictionary (AP)",
				"6.9", 5),
		},

		ObjectModel: objectModelChecks{
			MissingRequiredKey: newCheck(
				"MissingRequiredKey",
				"A dictionary is missing a key the ISO 32000 object model requires for its type",
				ObjectModelClause, 1),
			WrongValueType: newCheck(
				"WrongValueType",
				"A key's value is not one of the ISO 32000 object model's allowed types for it",
				ObjectModelClause, 2),
			DisallowedValue: newCheck(
				"DisallowedValue",
				"A key's value is not one of the ISO 32000 object model's enumerated legal values for it",
				ObjectModelClause, 3),
			IndirectRequired: newCheck(
				"IndirectRequired",
				"A key whose value the ISO 32000 object model requires to be an indirect reference is a direct object",
				ObjectModelClause, 4),
			KeyIntroducedAfterPDF14: newCheck(
				"KeyIntroducedAfterPDF14",
				"A dictionary contains a key the ISO 32000 object model introduced after PDF 1.4",
				ObjectModelClause, 5),
		},
	}
}
