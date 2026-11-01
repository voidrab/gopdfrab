package verify

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// dictRule is a family of checks run once on each dict of the walk.
type dictRule struct {
	// checks are the checks fn can report. A profile that enables none of
	// them skips the rule. TestDictRulesDeclareTheirChecks keeps the list
	// exact.
	checks []pdf.Check
	fn     func(v pdf.PDFDict, ctx *ValidationContext)
}

// docRule is a family of document-level checks, run once per verify.
type docRule struct {
	// checks are the checks fn can report, as for dictRule.
	checks []pdf.Check
	fn     func(d *pdf.Reader, graph pdf.PDFValue, ctx *ValidationContext) []pdf.PDFError
}

// dictRules run on each dict, in this order.
var dictRules = []dictRule{
	{
		checks: []pdf.Check{
			pdf.Checks.Structure.StreamFileDecodeParams, pdf.Checks.Structure.StreamFileFilter,
			pdf.Checks.Structure.StreamFileSpec, pdf.Checks.Structure.StreamFilterNotAllowed,
			pdf.Checks.Structure.StreamLZWFilter,
		},
		fn: validateStreamObject,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Structure.EmbeddedFileSpec, pdf.Checks.Structure.EmbeddedFiles,
		},
		fn: validateObject,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Action.DisallowedNamedAction, pdf.Checks.Action.ForbiddenActionType,
		},
		fn: validateActions,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Action.AdditionalActions,
		},
		fn: validateAdditionalActions,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Transparency.BlendMode, pdf.Checks.Transparency.DefaultTransferFunction,
			pdf.Checks.Transparency.ExtGStateRenderingIntent,
			pdf.Checks.Transparency.HalftoneExtGState, pdf.Checks.Transparency.HalftoneName,
			pdf.Checks.Transparency.HalftoneTransferFunction, pdf.Checks.Transparency.HalftoneType,
			pdf.Checks.Transparency.NonStrokingAlpha, pdf.Checks.Transparency.SoftMaskExtGState,
			pdf.Checks.Transparency.StrokingAlpha, pdf.Checks.Transparency.TransferFunction,
		},
		fn: validateExtGState,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Transparency.TransparencyGroup,
		},
		fn: validateTransparencyGroup,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Transparency.TransparencyWithoutBlendingSpace,
		},
		fn: validatePageTransparency,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Transparency.DeviceBlendingColourSpace,
		},
		fn: validateBlendingColourSpace,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Image.JPXBitDepth, pdf.Checks.Image.JPXCIEJab,
			pdf.Checks.Image.JPXColourChannels, pdf.Checks.Image.JPXColourMethod,
			pdf.Checks.Image.JPXColourSpecs,
		},
		fn: validateJPXImage,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Image.FormOPI, pdf.Checks.Image.FormPSEntry, pdf.Checks.Image.FormPostScript,
			pdf.Checks.Image.FormSubtype2PS, pdf.Checks.Image.ImageAlternates,
			pdf.Checks.Image.ImageBitsPerComponent, pdf.Checks.Image.ImageInterpolate,
			pdf.Checks.Image.ImageMaskBitsPerComponent, pdf.Checks.Image.ImageOPI,
			pdf.Checks.Image.ImageRenderingIntent, pdf.Checks.Image.PostScriptXObject,
			pdf.Checks.Image.ReferenceXObject, pdf.Checks.Transparency.ImageWithSoftMask,
		},
		fn: validateXObjectDict,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Annotation.AppearanceExtraEntries, pdf.Checks.Annotation.AppearanceMissingN,
			pdf.Checks.Annotation.AppearanceNNotStream, pdf.Checks.Annotation.ColourWithoutIntent,
			pdf.Checks.Annotation.DisallowedSubtype, pdf.Checks.Annotation.HiddenFlagSet,
			pdf.Checks.Annotation.InvisibleFlagSet, pdf.Checks.Annotation.MissingAppearance,
			pdf.Checks.Annotation.NoViewFlagSet, pdf.Checks.Annotation.OpacityNotOne,
			pdf.Checks.Annotation.PrintFlagNotSet, pdf.Checks.Form.WidgetMissingAppearance,
		},
		fn: validateAnnotation,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Form.FieldAction, pdf.Checks.Form.FieldAdditionalActions,
		},
		fn: validateFormField,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Colour.DeviceColourBase, pdf.Checks.Colour.DeviceColourSpaceUsage,
		},
		fn: validateColourSpaceUsage,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Colour.DeviceColourBase, pdf.Checks.Colour.DeviceColourContentStream,
			pdf.Checks.Colour.ICCBasedCMYKOverprint, pdf.Checks.Colour.InheritedResources,
			pdf.Checks.Colour.RenderingIntent,
			pdf.Checks.Colour.UndefinedOperator, pdf.Checks.Image.ImageInterpolate,
			pdf.Checks.Image.ImageRenderingIntent,
			pdf.Checks.Structure.GraphicsStateNesting, pdf.Checks.Structure.HexStringInvalidChar,
			pdf.Checks.Structure.HexStringOddLength, pdf.Checks.Structure.InlineImageFilterNotAllowed,
			pdf.Checks.Structure.InlineImageLZWFilter,
			pdf.Checks.Structure.IntegerOutOfRange, pdf.Checks.Structure.RealOutOfRange,
			pdf.Checks.Structure.RealTooCloseToZero,
			pdf.Checks.Structure.StreamUndecodable, pdf.Checks.Structure.StringTooLong,
		},
		fn: validateContentStreams,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Font.AdvanceWidthMismatch, pdf.Checks.Font.CIDNotEmbedded,
			pdf.Checks.Font.CIDSubsetCIDSet, pdf.Checks.Font.CIDSystemInfoMismatch,
			pdf.Checks.Font.CIDToGIDMapMissing, pdf.Checks.Font.CMapNotEmbedded,
			pdf.Checks.Font.CMapWModeInconsistent, pdf.Checks.Font.FontBaseFont,
			pdf.Checks.Font.FontFileSubtype, pdf.Checks.Font.InvalidProgram,
			pdf.Checks.Font.InvalidSubtype, pdf.Checks.Font.SimpleNotEmbedded,
			pdf.Checks.Font.SubsetGlyphCoverage, pdf.Checks.Font.SymbolicTrueTypeCmap,
			pdf.Checks.Font.SymbolicTrueTypeEncoding, pdf.Checks.Font.TrueTypeEncoding,
			pdf.Checks.Font.Type1SubsetCharSet, pdf.Checks.Font.UseCMapNotPredefined,
			pdf.Checks.Font.NonSymbolicTrueTypeCmap, pdf.Checks.Font.NotdefGlyphReference,
			pdf.Checks.Structure.StreamUndecodable,
		},
		fn: ValidateFontDict,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Structure.CMapCIDOutOfRange, pdf.Checks.Structure.StreamUndecodable,
		},
		fn: validateCMapStream,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Structure.PostPDF14ViewerPref,
		},
		fn: validateViewerPreferences,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Structure.NameNotUTF8,
		},
		fn: validateUnicodeNames,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Structure.SignatureReferenceDigest,
		},
		fn: validateSignatureReferences,
	},
	{
		checks: []pdf.Check{
			pdf.Checks.Structure.PageBoundarySize,
		},
		fn: validatePageBoundaries,
	},
}

// preWalkDocRules run before the walk, and postWalkDocRules after it.
var (
	preWalkDocRules = []docRule{
		{
			checks: []pdf.Check{
				pdf.Checks.Structure.InfoDictEmptyValues, pdf.Checks.Structure.InfoDictUnreadable,
				pdf.Checks.Structure.InfoDictXMPMismatch,
			},
			fn: docInfoRule,
		},
		{
			checks: []pdf.Check{
				pdf.Checks.Structure.PermissionsKeys,
			},
			fn: permissionsRule,
		},
	}
	postWalkDocRules = []docRule{
		{
			checks: []pdf.Check{
				pdf.Checks.Structure.OptionalContent,
			},
			fn: optionalContentRule,
		},
		{
			checks: []pdf.Check{
				pdf.Checks.Colour.ICCBasedComponentsMismatch,
				pdf.Checks.Colour.OutputIntentICCVersion, pdf.Checks.Colour.OutputIntentInvalidN,
				pdf.Checks.Colour.OutputIntentInvalidProfile,
				pdf.Checks.Colour.OutputIntentInvalidS,
				pdf.Checks.Colour.OutputIntentMissingIdentifier,
				pdf.Checks.Colour.OutputIntentMissingN,
				pdf.Checks.Colour.OutputIntentDestOutputProfileRef,
				pdf.Checks.Colour.OutputIntentMultipleProfiles,
				pdf.Checks.Colour.OutputIntentNotArray, pdf.Checks.Colour.OutputIntentNotDict,
				pdf.Checks.Colour.OutputIntentUnresolvedProfile,
				pdf.Checks.Colour.OutputIntentWrongS,
			},
			fn: outputIntentRule,
		},
		{
			checks: []pdf.Check{
				pdf.Checks.Colour.DeviceColourWithoutPDFAIntent,
			},
			fn: deviceColourWithoutPDFAIntentRule,
		},
		{
			checks: []pdf.Check{
				pdf.Checks.Form.NeedAppearances, pdf.Checks.Form.XFA,
			},
			fn: interactiveFormRule,
		},
		{
			checks: []pdf.Check{
				pdf.Checks.Metadata.ExtFieldInvalid, pdf.Checks.Metadata.ExtPropertyComplexAsSimple,
				pdf.Checks.Metadata.ExtPropertyMissingField,
				pdf.Checks.Metadata.ExtPropertyMultipleName,
				pdf.Checks.Metadata.ExtPropertyUndefinedType,
				pdf.Checks.Metadata.ExtPropertyUndocumented, pdf.Checks.Metadata.ExtSchemaNamespace,
				pdf.Checks.Metadata.ExtSchemaWrongPrefixURI, pdf.Checks.Metadata.ExtSchemasNotBag,
				pdf.Checks.Metadata.ExtTypeInvalid, pdf.Checks.Metadata.InfoXMPSync,
				pdf.Checks.Metadata.MetadataFiltered, pdf.Checks.Metadata.MetadataMissing,
				pdf.Checks.Metadata.MetadataPropertyType,
				pdf.Checks.Metadata.MetadataUndeclaredProperty,
				pdf.Checks.Metadata.PDFAConformanceLevel, pdf.Checks.Metadata.PDFAIdentifierMissing,
				pdf.Checks.Metadata.PDFAIdentifierNamespace,
				pdf.Checks.Metadata.PDFAIdentifierUndefinedProperty,
				pdf.Checks.Metadata.PDFAPartNumber, pdf.Checks.Metadata.XMPNoCorrespondingType,
				pdf.Checks.Metadata.XMPNotWellFormed, pdf.Checks.Metadata.XMPStreamUnreadable,
				pdf.Checks.Metadata.XPacketBytesAttribute,
				pdf.Checks.Metadata.XPacketEncodingAttribute,
				pdf.Checks.Structure.InfoDictXMPMismatch,
			},
			fn: xmpMetadataRule,
		},
		{
			checks: []pdf.Check{
				pdf.Checks.Metadata.ObjectXMPNoXPacket, pdf.Checks.Structure.StreamUndecodable,
			},
			fn: objectXMPRule,
		},
	}
)

// enabled reports whether p enables any of checks.
func enabled(p *pdf.Profile, checks []pdf.Check) bool {
	for _, c := range checks {
		if p.Has(c) {
			return true
		}
	}
	return false
}

// enabledRules returns the rules of which p enables at least one check. It
// returns rules itself when p enables them all, and never nil.
func enabledRules[R dictRule | docRule](rules []R, p *pdf.Profile, checks func(R) []pdf.Check) []R {
	for i, r := range rules {
		if enabled(p, checks(r)) {
			continue
		}
		out := append(make([]R, 0, len(rules)-1), rules[:i]...)
		for _, r := range rules[i+1:] {
			if enabled(p, checks(r)) {
				out = append(out, r)
			}
		}
		return out
	}
	return rules
}

// enabledDictRules returns the dict rules ctx runs.
func (ctx *ValidationContext) enabledDictRules() []dictRule {
	if ctx.dictRules == nil {
		return dictRules
	}
	return ctx.dictRules
}

// runDocRules runs rules in order and reports their findings on ctx, stopping
// early once the run is cancelled. A document-level finding has no page; it
// names the object it was found in.
func runDocRules(rules []docRule, d *pdf.Reader, graph pdf.PDFValue, ctx *ValidationContext) {
	for _, r := range rules {
		if ctx.stopped() {
			return
		}
		for _, e := range r.fn(d, graph, ctx) {
			ctx.report(e)
		}
	}
}

// The document rules adapt the document-level families to docRule.

func docInfoRule(_ *pdf.Reader, graph pdf.PDFValue, _ *ValidationContext) []pdf.PDFError {
	return verifyDocumentInformationDictionary(graph)
}

func optionalContentRule(d *pdf.Reader, _ pdf.PDFValue, ctx *ValidationContext) []pdf.PDFError {
	return verifyOptionalContent(d, *ctx.Spec)
}

func outputIntentRule(d *pdf.Reader, _ pdf.PDFValue, ctx *ValidationContext) []pdf.PDFError {
	return verifyOutputIntent(d, *ctx.Spec)
}

func interactiveFormRule(d *pdf.Reader, _ pdf.PDFValue, _ *ValidationContext) []pdf.PDFError {
	return verifyInteractiveForms(d)
}

func xmpMetadataRule(d *pdf.Reader, _ pdf.PDFValue, ctx *ValidationContext) []pdf.PDFError {
	return verifyXMPMetadata(d, *ctx.Spec)
}

func objectXMPRule(_ *pdf.Reader, graph pdf.PDFValue, ctx *ValidationContext) []pdf.PDFError {
	return checkNonCatalogXMPStreams(graph, ctx)
}
