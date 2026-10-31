package convert

import (
	"encoding/binary"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// jp2Data returns JP2 data whose image header gives nc components of 8 bits
// and whose header box holds two colour specifications, so that a reader has
// no single one to use.
func jp2Data(nc int) []byte {
	box := func(kind string, body ...byte) []byte {
		b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
		return append(append(b, kind...), body...)
	}
	ihdr := make([]byte, 14)
	binary.BigEndian.PutUint16(ihdr[8:], uint16(nc))
	ihdr[10] = 7
	colr := []byte{1, 0, 0, 0, 0, 0, 16}
	header := append(append(box("ihdr", ihdr...), box("colr", colr...)...), box("colr", colr...)...)
	data := []byte{0, 0, 0, 0x0C, 'j', 'P', ' ', ' ', 0x0D, 0x0A, 0x87, 0x0A}
	return append(data, box("jp2h", header...)...)
}

func TestJPXColourSpaceFixer(t *testing.T) {
	image := func(data []byte, extra ...any) pdf.PDFDict {
		d := streamWith(kv("Subtype", nm("Image"), "Filter", nm("JPXDecode")), data)
		for i := 0; i < len(extra); i += 2 {
			d.Entries.Set(extra[i].(string), extra[i+1].(pdf.PDFValue))
		}
		return d
	}
	for _, tc := range []struct {
		desc  string
		image pdf.PDFDict
		wantN int
	}{
		{"gray", image(jp2Data(1)), 1},
		{"RGB", image(jp2Data(3)), 3},
		{"CMYK", image(jp2Data(4)), 4},
		{"two channels", image(jp2Data(2)), 0},
		{"usable colour specification", image(jp2UsableData()), 0},
		{"has a ColorSpace", image(jp2Data(3), "ColorSpace", nm("DeviceRGB")), 0},
		{"undecodable", image(jp2Data(3), "Filter", pdf.PDFArray{nm("FlateDecode"), nm("JPXDecode")}), 0},
		{"not JPX", streamWith(kv("Subtype", nm("Image")), nil), 0},
		{"not an image", streamWith(kv("Subtype", nm("Form"), "Filter", nm("JPXDecode")), nil), 0},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			trailer := kv("Root", kv("Type", nm("Catalog"), "X", tc.image))
			changed, err := jpxColourSpaceFixer{}.Fix(&trailer, nil)
			if err != nil || changed != (tc.wantN != 0) {
				t.Fatalf("Fix = %v, %v, want changed %v", changed, err, tc.wantN != 0)
			}
			if tc.wantN == 0 {
				return
			}
			cs, _ := tc.image.Entries.Get("ColorSpace").(pdf.PDFArray)
			profile, _ := cs[1].(pdf.PDFDict)
			if cs[0] != nm("ICCBased") || profile.Entries.Get("N") != pdf.PDFInteger(tc.wantN) {
				t.Errorf("ColorSpace = %v, want ICCBased with N %d", cs, tc.wantN)
			}
		})
	}
	f := jpxColourSpaceFixer{}
	for _, c := range []pdf.Check{pdf.Checks.Image.JPXColourSpecs, pdf.Checks.Image.JPXColourMethod, pdf.Checks.Image.JPXCIEJab} {
		if !f.Applies(c) {
			t.Errorf("does not apply to %s", c.Name())
		}
	}
	if f.Applies(pdf.Checks.Image.JPXBitDepth) {
		t.Error("applies to JPXBitDepth, which needs the data decoded")
	}
}

func TestICCSpacesShareOneSpacePerModel(t *testing.T) {
	s := iccSpaces{}
	if a, b := s.forModel("rgb"), s.forModel("rgb"); pdf.ValuePointer(a[1]) != pdf.ValuePointer(b[1]) {
		t.Error("two RGB spaces built")
	}
	if s.forModel("lab") != nil {
		t.Error("a space built for an unknown model")
	}
}

func TestBlendingSpaceFixer(t *testing.T) {
	group := func(cs string) pdf.PDFDict {
		return kv("S", nm("Transparency"), "I", pdf.PDFBoolean(true), "CS", nm(cs))
	}
	intent := func(n int) pdf.PDFArray {
		return pdf.PDFArray{kv("S", nm("GTS_PDFA1"), "DestOutputProfile", kv("N", pdf.PDFInteger(n)))}
	}
	defaultRGB := kv("ColorSpace", kv("DefaultRGB", pdf.PDFArray{nm("CalRGB"), kv()}))
	for _, tc := range []struct {
		desc    string
		holder  pdf.PDFDict
		intents pdf.PDFValue
		wantN   int
	}{
		{"page DeviceRGB, CMYK intent", kv("Type", nm("Page"), "Group", group("DeviceRGB")), intent(4), 3},
		{"form DeviceCMYK, RGB intent", kv("Subtype", nm("Form"), "Group", group("DeviceCMYK")), intent(3), 4},
		{"page DeviceGray, no intent", kv("Type", nm("Page"), "Group", group("DeviceGray")), nil, 1},
		{"page DeviceRGB, RGB intent", kv("Type", nm("Page"), "Group", group("DeviceRGB")), intent(3), 0},
		{"page DeviceRGB under DefaultRGB", kv("Type", nm("Page"), "Group", group("DeviceRGB"), "Resources", defaultRGB), nil, 0},
		{"page CalRGB", kv("Type", nm("Page"), "Group", group("CalRGB")), nil, 0},
		{"no group", kv("Type", nm("Page")), nil, 0},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			catalog := kv("Type", nm("Catalog"), "X", tc.holder)
			if tc.intents != nil {
				catalog.Entries.Set("OutputIntents", tc.intents)
			}
			trailer := kv("Root", catalog)
			changed, err := blendingSpaceFixer{spec: a2bSpec}.Fix(&trailer, nil)
			if err != nil || changed != (tc.wantN != 0) {
				t.Fatalf("Fix = %v, %v, want changed %v", changed, err, tc.wantN != 0)
			}
			if tc.wantN == 0 {
				return
			}
			g, _ := tc.holder.Entries.Get("Group").(pdf.PDFDict)
			cs, _ := g.Entries.Get("CS").(pdf.PDFArray)
			profile, _ := cs[1].(pdf.PDFDict)
			if cs[0] != nm("ICCBased") || profile.Entries.Get("N") != pdf.PDFInteger(tc.wantN) {
				t.Errorf("CS = %v, want ICCBased with N %d", cs, tc.wantN)
			}
		})
	}
	if !(blendingSpaceFixer{}).Applies(pdf.Checks.Transparency.DeviceBlendingColourSpace) {
		t.Error("does not apply to DeviceBlendingColourSpace")
	}
}

func TestImageSampleDepthFixerKeepsDepthsTheLevelAllows(t *testing.T) {
	image := streamWith(kv("Subtype", nm("Image"), "Width", pdf.PDFInteger(1), "Height", pdf.PDFInteger(1),
		"ColorSpace", nm("DeviceGray"), "BitsPerComponent", pdf.PDFInteger(16)), []byte{0, 0})
	trailer := kv("Root", kv("Type", nm("Catalog"), "X", image))
	if changed, _ := (imageSampleDepthFixer{spec: a2bSpec}).Fix(&trailer, nil); changed {
		t.Error("Part 2: a 16-bit image rewritten")
	}
	if changed, _ := (imageSampleDepthFixer{spec: a1bSpec}).Fix(&trailer, nil); !changed {
		t.Error("Part 1: a 16-bit image kept")
	}
}

// jp2UsableData returns JP2 data of three channels with one enumerated sRGB
// colour specification, which a reader can use as it is.
func jp2UsableData() []byte {
	data := jp2Data(3)
	// Drop the second colour specification box (15 bytes) and shrink the
	// header box by as much.
	data = data[:len(data)-15]
	n := binary.BigEndian.Uint32(data[12:16])
	binary.BigEndian.PutUint32(data[12:16], n-15)
	return data
}
