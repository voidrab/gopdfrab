package verify

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/voidrab/gopdfrab/internal/arlington"
	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

type ValidationContext struct {
	PageIndex   map[int]int
	CurrentPage int
	errs        []pdf.PDFError

	// run is the caller's context, polled by the graph walk so a cancelled
	// verify stops early; nil never cancels. See stopped.
	run       context.Context
	polls     int
	cancelled bool

	// keyScratch is a shared stack of dict keys reused by every sortedKeys
	// call during the graph walk, so deterministic key iteration costs no
	// allocation per dict. Frames are stacked: each caller records the
	// length before its call and restores it afterwards.
	keyScratch []string

	// type1Cache memoizes the per-font-program CharStrings extraction shared
	// by the CharSet and metrics checks; see type1ProgramFor.
	type1Cache map[*byte]fontprog.Type1Program

	// OutputIntent colour-model coverage (6.2.2 / 6.2.3.3). hasOutputIntent is
	// true when a GTS_PDFA1 output intent exists; the *Covered flags indicate the
	// destination profile colour model (N = 1/3/4).
	hasOutputIntent bool
	rgbCovered      bool
	cmykCovered     bool
	grayCovered     bool
	// uncoveredDeviceColour is set once a device colour space no output
	// intent covers has been reported.
	uncoveredDeviceColour bool

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

	// schemaOnly restricts verifyDocument's walk to the generic object-model
	// schema checks, skipping every PDF/A-specific family whose findings the
	// profile would filter out anyway (see Profile.OnlyObjectModelChecks).
	schemaOnly bool

	// dictRules are the dict rules the profile enables, run on each dict of
	// the walk. Nil, in a context built outside a verify, runs them all.
	dictRules []dictRule

	// resourceScope is the resource dictionary in force where the walk
	// currently is: the nearest /Resources at or above it. Default* colour
	// spaces (6.2.3.3) are looked up there, so content in a form XObject with
	// its own /Resources is not excused by the page's Default*, while a form
	// without any inherits the enclosing one.
	resourceScope pdf.PDFDict
	// parentResourceScope is the scope that was in force above the nearest
	// dict that brought its own /Resources: what a form XObject, tiling
	// pattern or Type 3 font with its own resources would inherit from.
	parentResourceScope pdf.PDFDict
	// inherit is the inheritance of the content stream being checked
	// (ISO 19005-2 6.2.2).
	inherit inheritance
	// separations holds, by colorant name, the first Separation colour space
	// the walk met (ISO 19005-2 6.2.4.4).
	separations map[string]*separation

	// undecodable is the set of streams already reported as
	// Structure.StreamUndecodable, so a stream several checks read is flagged
	// once per verify pass rather than once per read. Lives here rather than
	// on the Reader because throwaway contexts have no reader at all.
	undecodable map[pdf.StreamKey]bool

	// Spec is the conformance level's rules the checks apply. A check that
	// reads it on a context without one panics: a nil Spec has no rules to
	// fall back on, and guessing PDF/A-1b's would hide a missing one.
	Spec *pdfa.Spec

	// model caches the Arlington model Spec.ModelVersion names; see
	// objectModel.
	model *arlington.Model

	// stream collects the reports of the content stream being checked, or is
	// nil outside one. See collapsingStreamReports.
	stream *streamReports

	// reader backs decodeStreamCached with the Reader's run-scoped decode
	// cache, so unchanged content streams stay decoded at most once across
	// convert's fixer iterations (which reuse the same Reader). Nil for
	// throwaway contexts built outside a real verify pass, which then decode
	// uncached.
	reader *pdf.Reader

	// docMDP caches hasDocMDP once it is known.
	docMDP *bool
}

// hasDocMDP reports whether the catalog's permissions dictionary holds DocMDP,
// which restricts the keys of signature references (ISO 19005-2 6.1.12). A
// context without a reader has no catalog to hold it.
func (ctx *ValidationContext) hasDocMDP() bool {
	if ctx.docMDP == nil {
		has := false
		if ctx.reader != nil {
			_, err := ctx.reader.ResolveGraphByPath([]string{"Root", "Perms", "DocMDP"})
			has = err == nil
		}
		ctx.docMDP = &has
	}
	return *ctx.docMDP
}

// objectModel returns the Arlington object model of the context's level: the
// one for Spec.ModelVersion. It panics, like any read of Spec, on a context
// without a Spec, and on a version no model is vendored for.
func (c *ValidationContext) objectModel() *arlington.Model {
	if c.model == nil || c.model.Version() != c.Spec.ModelVersion {
		c.model = arlington.ModelFor(c.Spec.ModelVersion)
		if c.model == nil {
			panic("verify: no Arlington model for PDF " + c.Spec.ModelVersion)
		}
	}
	return c.model
}

// stopped reports whether the run has been cancelled. It is called once per
// walked node, so it consults ctx only every 256 calls.
func (c *ValidationContext) stopped() bool {
	if c.cancelled || c.run == nil {
		return c.cancelled
	}
	c.polls++
	if c.polls&255 == 0 && c.run.Err() != nil {
		c.cancelled = true
	}
	return c.cancelled
}

// NewContext returns a ValidationContext whose stream decodes and scans go
// through d's run-scoped caches; a nil d yields uncached decoding. It has no
// Spec: set one before running a check that applies a level's rules.
func NewContext(d *pdf.Reader) *ValidationContext {
	return &ValidationContext{reader: d}
}

// decodeStreamCached decodes dict's stream, caching the result via ctx.reader
// (see pdf.Reader.DecodeStreamCached) when available, and reporting
// Structure.StreamUndecodable when the filter chain is broken. A check that
// cannot read its input must never look like a check that passed.
func (ctx *ValidationContext) decodeStreamCached(dict pdf.PDFDict) ([]byte, error) {
	var data []byte
	var err error
	if ctx.reader == nil {
		data, err = pdf.DecodeStream(dict)
	} else {
		data, err = ctx.reader.DecodeStreamCached(dict)
	}
	if err != nil {
		ctx.reportUndecodable(dict, err)
	}
	return data, err
}

// reportUndecodable records a broken stream, once per ValidationContext.
//
// Two outcomes are deliberately not reported. A chain ending in an image codec
// is well-formed rather than broken -- it just has no byte representation --
// and without that exemption every DCT-encoded image would be flagged. A dict
// that carries no stream at all is not a stream object, so 6.1.7 does not
// apply; the callers that hit this are defensive guards against a mistyped
// entry, which the object-model checks report against the schema instead.
func (ctx *ValidationContext) reportUndecodable(dict pdf.PDFDict, err error) {
	if errors.Is(err, pdf.ErrEncodedImage) || errors.Is(err, pdf.ErrNotAStream) {
		return
	}
	if key, ok := pdf.StreamKeyOf(dict); ok {
		if ctx.undecodable[key] {
			return
		}
		if ctx.undecodable == nil {
			ctx.undecodable = map[pdf.StreamKey]bool{}
		}
		ctx.undecodable[key] = true
	}
	ctx.Report(pdf.Checks.Structure.StreamUndecodable, dict,
		fmt.Sprintf("stream could not be decoded: %v", err))
}

// scanStream reports dict's content-stream operators to fn, going through
// ctx.reader (see pdf.Reader.ScanStreamFunc) when there is one so an unchanged
// stream is lexed at most once across a verify pass and convert's fixer
// iterations, and streamed rather than materialized once past the budget.
//
// Operands are valid only for the duration of the call; see ScanStreamFunc.
func (ctx *ValidationContext) scanStream(dict pdf.PDFDict, fn func(op string, operands []pdf.PDFValue)) error {
	if ctx.reader == nil {
		data, err := pdf.DecodeStream(dict)
		if err != nil {
			ctx.reportUndecodable(dict, err)
			return err
		}
		pdf.NewContentScanner(data).Scan(fn)
		return nil
	}
	if err := ctx.reader.ScanStreamFunc(dict, fn); err != nil {
		ctx.reportUndecodable(dict, err)
		return err
	}
	return nil
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
// part2 reports whether the context verifies a level of ISO 19005-2, whose
// graphics rules need state the Part 1 checks do not.
func (ctx *ValidationContext) part2() bool {
	return ctx.Spec != nil && pdf.Checks.Colour.InheritedResources.AppliesTo(ctx.Spec.Level)
}

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
	if ctx.stream != nil {
		ctx.stream.add(c, obj, msg)
		return
	}
	ctx.report(ctx.newError(c, obj, []error{errors.New(msg)}))
}

// streamReports holds the reports of one content stream: for each check, its
// first occurrence and the number of further occurrences.
type streamReports struct {
	order []pdf.Check
	first map[pdf.Check]streamReport
}

type streamReport struct {
	obj  pdf.PDFValue
	msg  string
	more int
}

func (s *streamReports) add(c pdf.Check, obj pdf.PDFValue, msg string) {
	if r, seen := s.first[c]; seen {
		r.more++
		s.first[c] = r
		return
	}
	s.order = append(s.order, c)
	s.first[c] = streamReport{obj: obj, msg: msg}
}

// collapsingStreamReports runs check, which checks one content stream, and
// reports each violated check once: at its first occurrence, with a message
// giving the number of further occurrences. This bounds the number of issues
// per stream, which would otherwise grow with the number of operators.
func (ctx *ValidationContext) collapsingStreamReports(check func()) {
	s := &streamReports{first: map[pdf.Check]streamReport{}}
	ctx.stream = s
	check()
	ctx.stream = nil
	for _, c := range s.order {
		r := s.first[c]
		msg := r.msg
		if r.more > 0 {
			msg = fmt.Sprintf("%s (and %d more in this content stream)", msg, r.more)
		}
		ctx.Report(c, r.obj, msg)
	}
}

// ReportObjModel records a violation of c against obj carrying the Arlington
// schema location (typeName, key) that fixers use to target the repair. key is
// the offending dict key, or the decimal index for array-element findings.
func (ctx *ValidationContext) ReportObjModel(c pdf.Check, obj pdf.PDFValue, typeName, key, msg string) {
	err := ctx.newError(c, obj, []error{errors.New(msg)})
	ctx.report(err.WithObjModelDetail(pdf.ObjModelDetail{TypeName: typeName, Key: key}))
}

// ReportObjModelElem is ReportObjModel for an array-element finding: typeName is the
// array's Arlington type, idx the element index, and entry the owner dict's key holding
// the array -- "" when the array is nested inside another array (not directly
// addressable), which fixers must leave as residuals.
func (ctx *ValidationContext) ReportObjModelElem(c pdf.Check, obj pdf.PDFValue, typeName, entry string, idx int, msg string) {
	err := ctx.newError(c, obj, []error{errors.New(msg)})
	ctx.report(err.WithObjModelDetail(pdf.ObjModelDetail{TypeName: typeName, Key: strconv.Itoa(idx), Entry: entry}))
}

// refOf returns the reference of obj, or nil unless obj is a dict that has
// one.
func refOf(obj pdf.PDFValue) *pdf.PDFRef {
	if dict, ok := obj.(pdf.PDFDict); ok {
		if r, ok := dict.Ref(); ok {
			return &r
		}
	}
	return nil
}

// withRef returns errs reported against obj, for findings built before the
// object was known.
func withRef(errs []pdf.PDFError, obj pdf.PDFValue) []pdf.PDFError {
	ref := refOf(obj)
	if ref == nil {
		return errs
	}
	for i, e := range errs {
		var msgs []error
		for _, m := range e.Messages() {
			msgs = append(msgs, errors.New(m))
		}
		errs[i] = pdf.NewError(e.Check(), msgs, e.Page(), ref)
	}
	return errs
}

// newError builds a PDFError against obj, resolving the owner ref and current page.
func (ctx *ValidationContext) newError(c pdf.Check, obj pdf.PDFValue, errs []error) pdf.PDFError {
	ref := refOf(obj)

	var page int
	if ctx != nil {
		page = ctx.CurrentPage
	}

	return pdf.NewError(c, errs, page, ref)
}

// ReportErrs records a violation of c against obj carrying multiple
// underlying error messages.
func (ctx *ValidationContext) ReportErrs(c pdf.Check, obj pdf.PDFValue, errs []error) {
	ctx.report(ctx.newError(c, obj, errs))
}
