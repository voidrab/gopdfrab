package verify

import (
	"errors"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

type ValidationContext struct {
	PageIndex   map[int]int
	CurrentPage int
	errs        []pdf.PDFError

	// OutputIntent colour-model coverage (6.2.2 / 6.2.3.3). hasOutputIntent is
	// true when a GTS_PDFA1 output intent exists; the *Covered flags indicate the
	// destination profile colour model (N = 1/3/4).
	hasOutputIntent bool
	rgbCovered      bool
	cmykCovered     bool
	grayCovered     bool

	// ReachableXObjectPtrs is the set of Entries-map pointers of Form XObjects
	// that are actually invoked (via Do) from page or other reachable content
	// streams. Nil means unknown (treat everything as reachable).
	ReachableXObjectPtrs map[uintptr]bool

	// InvisibleOnlyFontPtrs is the set of font dictionary Entries-map pointers
	// that are used for text showing exclusively under rendering mode 3 or 7
	// (invisible) somewhere in the document, and never under any other mode.
	// Such fonts are never actually rendered, so 6.3.3.2 (CIDToGIDMap), 6.3.5
	// (glyph coverage) and 6.3.6 (advance width consistency) do not apply.
	InvisibleOnlyFontPtrs map[uintptr]bool

	// SkipUnusedSimpleFonts mirrors pdf.Profile.SkipUnusedSimpleFonts: when
	// true, 6.3.4 simple-font embedding is only required for fonts that are
	// actually used to show text (i.e. their pointer appears in UsedCharCodes).
	SkipUnusedSimpleFonts bool

	// UsedCharCodes maps a simple (non-composite) font's Entries-map pointer
	// to the set of single-byte character codes actually passed to a
	// text-showing operator somewhere in the document. A subset font's
	// CharSet only needs to list glyphs "used for rendering" (6.3.5); a code
	// with a non-zero Widths entry that is never actually shown need not be
	// present. Nil for a font means no usage info was collected for it.
	UsedCharCodes map[uintptr]map[int]bool

	// UsedCIDs maps a composite CIDFont's descendant-dict Entries-map pointer
	// to the set of CIDs actually passed to a text-showing operator somewhere
	// in the document (decoded as 2-byte big-endian codes, valid for the
	// Identity-H/Identity-V encodings the verifier can decode). A CID listed
	// in the W width array but never shown need not have a glyph in the
	// embedded font program (6.3.5); only collected when the font's Encoding
	// is Identity-H/V — for any other CMap, usage is left unknown so callers
	// fall back to checking every W entry.
	UsedCIDs map[uintptr]map[int]bool

	// pageResources is the Resources dict of the current page. Default* colour
	// spaces defined at page level are inherited by patterns and Form XObjects
	// that do not define their own Default*.
	pageResources pdf.PDFDict

	decodedStreams map[int][]byte
}

// decodeStreamCached decodes dict's stream, caching the result by the
// indirect object number. Dicts without a
// recorded object number are decoded uncached.
func (ctx *ValidationContext) decodeStreamCached(dict pdf.PDFDict) ([]byte, error) {
	ref, ok := dict.Entries["_ref"].(pdf.PDFRef)
	if !ok {
		return pdf.DecodeStream(dict)
	}
	if data, ok := ctx.decodedStreams[ref.ObjNum]; ok {
		return data, nil
	}
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		return nil, err
	}
	if ctx.decodedStreams == nil {
		ctx.decodedStreams = map[int][]byte{}
	}
	ctx.decodedStreams[ref.ObjNum] = data
	return data, nil
}

// isReachableXObject reports whether v is a Form XObject reachable from page
// content via Do operators. Absent reachability info, everything is reachable.
func (ctx *ValidationContext) isReachableXObject(v pdf.PDFDict) bool {
	if ctx.ReachableXObjectPtrs == nil {
		return true
	}
	return ctx.ReachableXObjectPtrs[pdf.ValuePointer(v.Entries)]
}

// isInvisibleOnlyFont reports whether font v is shown only under invisible
// rendering modes (3/7), exempting it from 6.3.3.2/6.3.5/6.3.6 checks.
func (ctx *ValidationContext) isInvisibleOnlyFont(v pdf.PDFDict) bool {
	if ctx.InvisibleOnlyFontPtrs == nil {
		return false
	}
	return ctx.InvisibleOnlyFontPtrs[pdf.ValuePointer(v.Entries)]
}

// simpleFontShown reports whether v was used to show text (its pointer appears
// in UsedCharCodes). Callers should check UsedCharCodes != nil first.
func (ctx *ValidationContext) simpleFontShown(v pdf.PDFDict) bool {
	_, known := ctx.UsedCharCodes[pdf.ValuePointer(v.Entries)]
	return known
}

// usedCodesFor returns the character codes shown for font v and whether usage
// info was collected; if known is false, callers fall back to checking every Widths entry.
func (ctx *ValidationContext) usedCodesFor(v pdf.PDFDict) (codes map[int]bool, known bool) {
	if ctx.UsedCharCodes == nil {
		return nil, false
	}
	codes, known = ctx.UsedCharCodes[pdf.ValuePointer(v.Entries)]
	return codes, known
}

// usedCIDsFor returns the CIDs shown for composite font v and whether usage
// info was collected; if known is false, callers fall back to checking every W entry.
func (ctx *ValidationContext) usedCIDsFor(v pdf.PDFDict) (cids map[int]bool, known bool) {
	if ctx.UsedCIDs == nil {
		return nil, false
	}
	cids, known = ctx.UsedCIDs[pdf.ValuePointer(v.Entries)]
	return cids, known
}

// deviceColourAllowed reports whether a device colour model ("rgb", "cmyk",
// "gray") may be used given the document's OutputIntent coverage (6.2.3.3).
func (ctx *ValidationContext) deviceColourAllowed(model string) bool {
	switch model {
	case "rgb":
		return ctx.rgbCovered
	case "cmyk":
		return ctx.cmykCovered
	case "gray":
		// DeviceGray is permitted in the presence of any PDF/A output intent.
		return ctx.hasOutputIntent
	}
	return true
}

// Issues returns the violations recorded on ctx so far, e.g. for a caller
// that runs a check against a throwaway ValidationContext just to test
// whether it would currently flag something (see fixups_font_subst.go).
func (ctx *ValidationContext) Issues() []pdf.PDFError {
	return ctx.errs
}

func (ctx *ValidationContext) report(err pdf.PDFError) {
	ctx.errs = append(ctx.errs, err)
}

// Report records a single violation of c against obj.
func (ctx *ValidationContext) Report(c pdf.Check, obj pdf.PDFValue, msg string) {
	var ref *pdf.PDFRef
	if dict, ok := obj.(pdf.PDFDict); ok {
		if r, ok := dict.Entries["_ref"].(pdf.PDFRef); ok {
			ref = &r
		}
	}

	var page int
	if ctx == nil {
		page = 0
	} else {
		page = ctx.CurrentPage
	}

	err := pdf.NewError(c, []error{errors.New(msg)}, page, ref)

	ctx.report(err)
}

// ReportErrs records a violation of c against obj carrying multiple
// underlying error messages.
func (ctx *ValidationContext) ReportErrs(c pdf.Check, obj pdf.PDFValue, errs []error) {
	var ref *pdf.PDFRef
	if dict, ok := obj.(pdf.PDFDict); ok {
		if r, ok := dict.Entries["_ref"].(pdf.PDFRef); ok {
			ref = &r
		}
	}

	var page int
	if ctx == nil {
		page = 0
	} else {
		page = ctx.CurrentPage
	}

	err := pdf.NewError(c, errs, page, ref)

	ctx.report(err)
}
