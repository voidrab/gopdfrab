package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Inline images (BI ... ID <data> EI) carry the same parameters as an Image
// XObject, but with abbreviated keys and no surrounding stream object. Folding
// them into a synthetic stream dict lets the whole XObject image path --
// DecodeImageRGBA, its filters, predictors, /Decode array and colour-space
// resolution -- serve them unchanged.

// inlineImageKeys maps an inline image's abbreviated parameter keys to their
// Image XObject spellings (ISO 32000-1 Table 93). Filter and colour-space
// *values* need no expansion: LookupFilter and ResolveColor accept both
// spellings already.
var inlineImageKeys = map[string]string{
	"W":   "Width",
	"H":   "Height",
	"BPC": "BitsPerComponent",
	"CS":  "ColorSpace",
	"D":   "Decode",
	"DP":  "DecodeParms",
	"F":   "Filter",
	"IM":  "ImageMask",
	"I":   "Interpolate",
}

// inlineImageDict folds an INLINEIMAGE op's flat (key, value) params and its
// image bytes into a stream-shaped dict equivalent to an Image XObject. ok is
// false when the params carry no usable /Width and /Height.
func inlineImageDict(params []pdf.PDFValue, raw pdf.InlineImageRaw) (pdf.PDFDict, bool) {
	dict := pdf.NewPDFDict()
	for i := 0; i+1 < len(params); i += 2 {
		name, isName := params[i].(pdf.PDFName)
		if !isName {
			continue
		}
		key := name.Value
		if long, ok := inlineImageKeys[key]; ok {
			key = long
		}
		dict.Entries[key] = params[i+1]
	}
	if pdf.DictInt(dict, "Width", 0) <= 0 || pdf.DictInt(dict, "Height", 0) <= 0 {
		return pdf.PDFDict{}, false
	}
	dict.HasStream = true
	dict.RawStream = raw.Data
	return dict, true
}

// paintInlineImage decodes an inline image and composites it exactly as an
// Image XObject, reporting a drop only when it cannot be built or decoded.
func (r *renderer) paintInlineImage(operands []pdf.PDFValue, resources pdf.PDFDict, gs *renderState) {
	params, raw, ok := inlineImageRawOperand(operands)
	if !ok {
		r.drop(dropInlineImage)
		return
	}
	dict, ok := inlineImageDict(params, raw)
	if !ok {
		r.drop(dropInlineImage)
		return
	}
	img, err := DecodeImageRGBA(dict, resources)
	if err != nil {
		r.drop(dropInlineImage)
		return
	}
	r.compositeImage(img, nil, isImageMask(dict), gs)
}
