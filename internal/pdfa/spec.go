// Package pdfa holds what each conformance level permits: its limits, its
// allowed and forbidden sets, the features it switches on or off, and the
// identity a conforming file declares. verify reads it to decide what to
// report and convert to decide what to repair and with which values, so the
// two cannot drift apart on a level's rules.
package pdfa

import "github.com/voidrab/gopdfrab/internal/pdf"

// Spec is one conformance level's rules. For returns a copy, so a caller may
// edit its own without affecting anyone else's; the Names sets are read-only.
type Spec struct {
	// Level is the conformance level the spec describes.
	Level pdf.LevelType
	// PDFA is false for a level that is plain PDF, with no PDF/A rules.
	PDFA bool

	// Limits are the level's architectural limits.
	Limits Limits

	// RenderingIntents are the rendering intents a file may name.
	RenderingIntents Names
	// ForbiddenActions are the action types (/S) a file may not use.
	ForbiddenActions Names
	// NamedActions are the only names a Named action may carry.
	NamedActions Names
	// AnnotationSubtypes are the annotation subtypes a file may use.
	AnnotationSubtypes Names
	// BlendModes are the blend modes an ExtGState may select.
	BlendModes Names
	// FontFileSubtypes are the /Subtype values an embedded font file may have.
	FontFileSubtypes Names
	// ForbiddenViewerPrefKeys are the ViewerPreferences keys the level's PDF
	// version does not define.
	ForbiddenViewerPrefKeys Names

	// Transparency, OptionalContent, XRefStreams and EmbeddedFiles say
	// whether the level permits each feature.
	Transparency    bool
	OptionalContent bool
	XRefStreams     bool
	EmbeddedFiles   bool

	// ICCMaxMajorVersion is the newest ICC profile major version allowed.
	ICCMaxMajorVersion int
	// OutputIntentSubtype is the /S an OutputIntent must have.
	OutputIntentSubtype string

	// XMPPart and XMPConformance are the pdfaid:part and pdfaid:conformance
	// a converted file declares; XMPConformances are the conformance values
	// a file of this part may declare.
	XMPPart         string
	XMPConformance  string
	XMPConformances Names

	// ModelVersion is the PDF version whose object model applies.
	ModelVersion string
	// MaxPDFVersion is the newest version the file header may name, with the
	// same major version; empty allows any %PDF-N.M header.
	MaxPDFVersion string
	// LinearizedFileID says whether the first ID entry of a linearized
	// file's trailers must agree.
	LinearizedFileID bool
	// FileIDBytes says whether the trailer ID must be an array whose strings
	// hold at least one byte between them, rather than merely be present.
	FileIDBytes bool
	// ExactXRefOffsets says whether the startxref and Prev offsets must
	// locate the cross-reference sections exactly, so that a table the reader
	// had to rebuild by scanning is a violation.
	ExactXRefOffsets bool
}

// Limits are a level's architectural limits. A zero field sets no limit.
type Limits struct {
	MaxInteger, MinInteger int64
	// MaxReal bounds the absolute value of a real number.
	MaxReal             float64
	MaxStringBytes      int
	MaxNameBytes        int
	MaxArrayElements    int
	MaxDictEntries      int
	MaxCID              int
	MaxDeviceNColorants int
	MaxIndirectObjects  int
	// MaxQNesting is the deepest q/Q nesting a content stream may reach.
	MaxQNesting int
}

// For returns the spec of a conformance level. ok is false for a level there
// is no spec of, which is a level nothing can be verified against.
func For(level pdf.LevelType) (s Spec, ok bool) {
	switch level {
	case pdf.A1B:
		return a1b, true
	case pdf.A2B:
		return a2b, true
	case pdf.ObjectModel:
		return objectModel, true
	}
	return Spec{}, false
}

// BlendModeAllowed reports whether a /BM value selects only allowed blend
// modes. The value is a name or an array of names to choose from.
func (s Spec) BlendModeAllowed(bm pdf.PDFValue) bool {
	switch v := bm.(type) {
	case pdf.PDFName:
		return s.BlendModes.Has(v.Value)
	case pdf.PDFArray:
		for _, item := range v {
			name, ok := item.(pdf.PDFName)
			if !ok || !s.BlendModes.Has(name.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// a1b is PDF/A-1b, ISO 19005-1:2005 Level B.
var a1b = Spec{
	Level: pdf.A1B,
	PDFA:  true,
	// ISO 19005-1 Table 1 (6.1.12).
	Limits: Limits{
		MaxInteger:          2147483647,
		MinInteger:          -2147483648,
		MaxReal:             32767,
		MaxStringBytes:      65535,
		MaxNameBytes:        127,
		MaxArrayElements:    8191,
		MaxDictEntries:      4095,
		MaxCID:              65535,
		MaxDeviceNColorants: 8,
		MaxIndirectObjects:  8388607,
		MaxQNesting:         28,
	},
	// 6.2.9.
	RenderingIntents: NewNames("AbsoluteColorimetric", "RelativeColorimetric", "Saturation", "Perceptual"),
	// 6.6.1.
	ForbiddenActions: NewNames("Launch", "Sound", "Movie", "ResetForm", "ImportData", "JavaScript",
		"Hide", "SetOCGState", "Rendition", "Trans", "GoTo3DView", "SetState", "NOP"),
	NamedActions: NewNames("NextPage", "PrevPage", "FirstPage", "LastPage"),
	// 6.5.2.
	AnnotationSubtypes: NewNames("Text", "Link", "FreeText", "Line", "Square", "Circle", "Highlight",
		"Underline", "Squiggly", "StrikeOut", "Stamp", "Ink", "Popup", "Widget", "PrinterMark", "TrapNet"),
	// 6.4.
	BlendModes: NewNames("Normal", "Compatible"),
	// 6.3.2: the only font file subtypes PDF 1.4 defines.
	FontFileSubtypes: NewNames("Type1C", "CIDFontType0C"),
	// Keys introduced after PDF 1.4.
	ForbiddenViewerPrefKeys: NewNames("PrintScaling", "PickTrayByPDFSize", "PrintPageRange", "NumCopies"),
	// 6.2.2: ICC.1:2003-09.
	ICCMaxMajorVersion:  2,
	OutputIntentSubtype: "GTS_PDFA1",
	// 6.7.11.
	XMPPart:         "1",
	XMPConformance:  "B",
	XMPConformances: NewNames("A", "B"),
	ModelVersion:    "1.4",
	// 6.1.3 and 6.1.4.
	LinearizedFileID: true,
	ExactXRefOffsets: true,
}

// a2b is PDF/A-2b, ISO 19005-2:2011 Level B.
var a2b = Spec{
	Level: pdf.A2B,
	PDFA:  true,
	// ISO 19005-2 6.1.13. Arrays and dictionaries have no limit.
	Limits: Limits{
		MaxInteger:          2147483647,
		MinInteger:          -2147483648,
		MaxReal:             3.403e38,
		MaxStringBytes:      32767,
		MaxNameBytes:        127,
		MaxCID:              65535,
		MaxDeviceNColorants: 32,
		MaxIndirectObjects:  8388607,
		MaxQNesting:         28,
	},
	// 6.2.9.
	RenderingIntents: NewNames("AbsoluteColorimetric", "RelativeColorimetric", "Saturation", "Perceptual"),
	// 6.5.1.
	ForbiddenActions: NewNames("Launch", "Sound", "Movie", "ResetForm", "ImportData", "JavaScript",
		"Hide", "SetOCGState", "Rendition", "Trans", "GoTo3DView", "SetState", "NOP"),
	NamedActions: NewNames("NextPage", "PrevPage", "FirstPage", "LastPage"),
	// 6.3.1: ISO 32000-1 Table 169, less 3D, Sound, Screen and Movie.
	AnnotationSubtypes: NewNames("Text", "Link", "FreeText", "Line", "Square", "Circle", "Polygon",
		"PolyLine", "Highlight", "Underline", "Squiggly", "StrikeOut", "Stamp", "Caret", "Ink", "Popup",
		"FileAttachment", "Widget", "PrinterMark", "TrapNet", "Watermark", "Redact"),
	// 6.2.10: ISO 32000-1 Tables 136 and 137.
	BlendModes: NewNames("Normal", "Compatible", "Multiply", "Screen", "Overlay", "Darken", "Lighten",
		"ColorDodge", "ColorBurn", "HardLight", "SoftLight", "Difference", "Exclusion", "Hue",
		"Saturation", "Color", "Luminosity"),
	// 6.2.11.4.1.
	FontFileSubtypes: NewNames("Type1C", "CIDFontType0C", "OpenType"),
	Transparency:     true,
	OptionalContent:  true,
	XRefStreams:      true,
	EmbeddedFiles:    true,
	// 6.2.3: ICC.1:2010.
	ICCMaxMajorVersion:  4,
	OutputIntentSubtype: "GTS_PDFA1",
	// 6.6.4.
	XMPPart:         "2",
	XMPConformance:  "B",
	XMPConformances: NewNames("A", "B", "U"),
	ModelVersion:    "1.7",
	// 6.1.2 and 6.1.3.
	MaxPDFVersion: "1.7",
	FileIDBytes:   true,
}

// objectModel is plain PDF: the object model, and none of PDF/A's rules.
var objectModel = Spec{
	Level:        pdf.ObjectModel,
	ModelVersion: "1.4",
}
