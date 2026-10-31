package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// iccSpaces builds, on first use, one ICCBased colour space per device
// colour model, so that every repair of a run shares one profile stream per
// model.
type iccSpaces map[string]pdf.PDFArray

// forModel returns the ICCBased colour space standing in for a device colour
// model ("gray", "rgb" or "cmyk").
func (s iccSpaces) forModel(model string) pdf.PDFArray {
	if cs, ok := s[model]; ok {
		return cs
	}
	var cs pdf.PDFArray
	switch model {
	case "gray":
		cs = iccBasedColourSpace(1, grayICCProfile)
	case "rgb":
		cs = iccBasedColourSpace(3, srgbICCProfile)
	case "cmyk":
		cs = iccBasedColourSpace(4, cmykICCProfile)
	default:
		return nil
	}
	s[model] = cs
	return cs
}

// channelModels maps a number of colour channels to the device colour model
// with as many components.
var channelModels = map[int]string{1: "gray", 3: "rgb", 4: "cmyk"}

// --- ISO 19005-2 6.2.8.3: JPEG2000 ---

// jpxColourSpaceFixer gives a JPEG 2000 image without a ColorSpace entry one
// when its data has no single colour specification a reader can use. The
// entry overrides the data's own colour space. It is an ICCBased space with
// as many components as the data has channels. Data with another number of
// channels, or with bit depths the rules do not allow, cannot be repaired
// without decoding it.
type jpxColourSpaceFixer struct{}

func (jpxColourSpaceFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Image.JPXColourSpecs, pdf.Checks.Image.JPXColourMethod, pdf.Checks.Image.JPXCIEJab:
		return true
	}
	return false
}

func (jpxColourSpaceFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	spaces := iccSpaces{}
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Image"}) || d.Entries.Get("ColorSpace") != nil ||
			!pdf.HasFilter(d.Entries.Get("Filter"), pdf.FilterJPX) {
			return false
		}
		s, err := pdf.DecodeStreamFull(d, pdf.DecodeOptions{})
		if err != nil || !s.IsImage() {
			return false
		}
		cs := spaces.forModel(channelModels[verify.ParseJPXHeader(s.Data).Channels])
		if cs == nil {
			return false
		}
		d.Entries.Set("ColorSpace", cs)
		changed = true
		return true
	})
	return changed, nil
}

// --- ISO 19005-2 6.2.10: transparency ---

// blendingSpaceFixer replaces a transparency group's blending colour space
// that is a device colour space no output intent covers with an ICCBased
// space of the same colour model, which the group then blends in instead.
type blendingSpaceFixer struct{ spec *pdfa.Spec }

func (blendingSpaceFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Transparency.DeviceBlendingColourSpace
}

func (f blendingSpaceFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	hasIntent, rgbCovered, cmykCovered := outputIntentCoverage(*trailer, f.spec.OutputIntentSubtype)
	covered := map[string]bool{"gray": hasIntent, "rgb": rgbCovered, "cmyk": cmykCovered}
	spaces := iccSpaces{}
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		cs, resources, ok := verify.BlendingColourSpace(d)
		if !ok {
			return
		}
		model := verify.DeviceColourModel(cs)
		if model == "" || covered[model] || verify.DefaultColorSpaceDefined(model, resources) {
			return
		}
		group, _ := d.Entries.Get("Group").(pdf.PDFDict)
		group.Entries.Set("CS", spaces.forModel(model))
		changed = true
	})
	return changed, nil
}
