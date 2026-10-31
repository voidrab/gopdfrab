package verify

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// jp2Box returns a box of a kind holding body.
func jp2Box(kind string, body ...byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(b, kind...), body...)
}

// jp2File returns JP2 data whose header box holds boxes.
func jp2File(boxes ...[]byte) []byte {
	var header []byte
	for _, b := range boxes {
		header = append(header, b...)
	}
	data := append([]byte{}, jp2Signature...)
	data = append(data, jp2Box("ftyp", 'j', 'p', '2', ' ')...)
	return append(data, jp2Box("jp2h", header...)...)
}

// ihdr returns an image header box for nc components of bpc bits; a bpc of
// 0 gives the 0xFF that marks components of differing depths.
func ihdr(nc int, bpc byte) []byte {
	body := make([]byte, 14)
	binary.BigEndian.PutUint16(body[8:], uint16(nc))
	body[10] = bpc - 1
	return jp2Box("ihdr", body...)
}

// colr returns a colour specification box.
func colr(method, approx byte, enum uint32) []byte {
	body := []byte{method, 0, approx}
	if method == 1 {
		body = binary.BigEndian.AppendUint32(body, enum)
	}
	return jp2Box("colr", body...)
}

func TestReadJPXHeader(t *testing.T) {
	extended := append([]byte{}, jp2Signature...)
	extended = append(extended, 0, 0, 0, 1, 'j', 'p', '2', 'h', 0, 0, 0, 0, 0, 0, 0, 16+22)
	extended = append(extended, ihdr(3, 8)...)
	open := append([]byte{}, jp2Signature...)
	open = append(open, 0, 0, 0, 0, 'j', 'p', '2', 'h')
	open = append(open, ihdr(1, 8)...)

	for _, tc := range []struct {
		desc string
		data []byte
		want JPXHeader
	}{
		{"codestream", []byte{0xFF, 0x4F, 0xFF, 0x51}, JPXHeader{EnumCS: -1}},
		{"no header box", append(append([]byte{}, jp2Signature...), jp2Box("ftyp")...), JPXHeader{EnumCS: -1}},
		{"sRGB", jp2File(ihdr(3, 8), colr(1, 0, 16)), JPXHeader{Channels: 3, BitDepth: 8, ColourSpecs: 1, Method: 1, EnumCS: 16}},
		{"ICC profile", jp2File(ihdr(4, 16), colr(2, 0, 0)), JPXHeader{Channels: 4, BitDepth: 16, ColourSpecs: 1, Method: 2, EnumCS: -1}},
		{"bpcc", jp2File(ihdr(3, 0), jp2Box("bpcc", 7, 7, 7)), JPXHeader{Channels: 3, BitDepth: 0, BPCC: true, EnumCS: -1}},
		{"two specs, one approximate", jp2File(ihdr(3, 8), colr(2, 0, 0), colr(1, 1, 19), colr(1, 1, 16)),
			JPXHeader{Channels: 3, BitDepth: 8, ColourSpecs: 3, ApproxSpecs: 2, Method: 1, EnumCS: 19}},
		{"two specs, none approximate", jp2File(colr(1, 0, 16), colr(1, 0, 17)), JPXHeader{ColourSpecs: 2, EnumCS: -1}},
		{"short image header", jp2File(jp2Box("ihdr", 1, 2)), JPXHeader{EnumCS: -1}},
		{"short colour specification", jp2File(jp2Box("colr", 1)), JPXHeader{EnumCS: -1}},
		{"enumeration cut short", jp2File(jp2Box("colr", 1, 0, 1, 0)), JPXHeader{ColourSpecs: 1, ApproxSpecs: 1, Method: 1, EnumCS: -1}},
		{"box longer than its data", jp2File(ihdr(3, 8), []byte{0, 0, 0, 99, 'c', 'o', 'l', 'r'}), JPXHeader{Channels: 3, BitDepth: 8, EnumCS: -1}},
		{"box shorter than its header", jp2File([]byte{0, 0, 0, 4, 'c', 'o', 'l', 'r'}), JPXHeader{EnumCS: -1}},
		{"extended length", extended, JPXHeader{Channels: 3, BitDepth: 8, EnumCS: -1}},
		{"extended length cut short", append(append([]byte{}, jp2Signature...), 0, 0, 0, 1, 'j', 'p', '2', 'h', 0), JPXHeader{EnumCS: -1}},
		{"header box to the end", open, JPXHeader{Channels: 1, BitDepth: 8, EnumCS: -1}},
		{"inner extended length", jp2File(append([]byte{0, 0, 0, 1, 'i', 'h', 'd', 'r', 0, 0, 0, 0, 0, 0, 0, 30}, ihdr(3, 8)[8:]...)),
			JPXHeader{Channels: 3, BitDepth: 8, EnumCS: -1}},
		{"inner extended length cut short", jp2File([]byte{0, 0, 0, 1, 'i', 'h', 'd', 'r', 0}), JPXHeader{EnumCS: -1}},
		{"inner box to the end", jp2File(append([]byte{0, 0, 0, 0}, ihdr(1, 8)[4:]...)), JPXHeader{Channels: 1, BitDepth: 8, EnumCS: -1}},
		{"other box to the end", append(append([]byte{}, jp2Signature...), 0, 0, 0, 0, 'f', 't', 'y', 'p'), JPXHeader{EnumCS: -1}},
		{"top-level box shorter than its header", append(append([]byte{}, jp2Signature...), 0, 0, 0, 4, 'f', 't', 'y', 'p'), JPXHeader{EnumCS: -1}},
		{"top-level box longer than the data", append(append([]byte{}, jp2Signature...), 0, 0, 0, 99, 'f', 't', 'y', 'p'), JPXHeader{EnumCS: -1}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := readJPXHeader(bytes.NewReader(tc.data)); got != tc.want {
				t.Errorf("readJPXHeader = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestValidateJPXImage(t *testing.T) {
	image := func(data []byte, extra ...any) pdf.PDFDict {
		d := pdf.NewPDFDict()
		d.SetStream(data)
		d.Entries.Set("Subtype", name("Image"))
		d.Entries.Set("Filter", name("JPXDecode"))
		for i := 0; i < len(extra); i += 2 {
			d.Entries.Set(extra[i].(string), extra[i+1].(pdf.PDFValue))
		}
		return d
	}
	good := jp2File(ihdr(3, 8), colr(1, 0, 16))
	img := pdf.Checks.Image
	all := []pdf.Check{img.JPXColourChannels, img.JPXBitDepth, img.JPXColourSpecs, img.JPXColourMethod, img.JPXCIEJab}
	s := a1b()
	for _, tc := range []struct {
		desc string
		ctx  *ValidationContext
		dict pdf.PDFDict
		want []pdf.Check
	}{
		{"conforming", a2bContext(), image(good), nil},
		{"two channels", a2bContext(), image(jp2File(ihdr(2, 8), colr(1, 0, 16))), []pdf.Check{img.JPXColourChannels}},
		{"bit depth 41", a2bContext(), image(jp2File(ihdr(3, 41), colr(1, 0, 16))), []pdf.Check{img.JPXBitDepth}},
		{"bpcc box", a2bContext(), image(jp2File(ihdr(3, 8), jp2Box("bpcc"), colr(1, 0, 16))), []pdf.Check{img.JPXBitDepth}},
		{"two specifications", a2bContext(), image(jp2File(ihdr(3, 8), colr(1, 0, 16), colr(1, 0, 16))),
			[]pdf.Check{img.JPXColourSpecs, img.JPXColourMethod}},
		{"method 4", a2bContext(), image(jp2File(ihdr(3, 8), colr(4, 0, 0))), []pdf.Check{img.JPXColourMethod}},
		{"CIEJab", a2bContext(), image(jp2File(ihdr(3, 8), colr(1, 0, 19))), []pdf.Check{img.JPXCIEJab}},
		{"CIEJab under a ColorSpace", a2bContext(), image(jp2File(ihdr(3, 8), colr(1, 0, 19)), "ColorSpace", name("DeviceRGB")), nil},
		{"not JPX", a2bContext(), kv("Subtype", name("Image")), nil},
		{"not an image", a2bContext(), kv("Subtype", name("Form"), "Filter", name("JPXDecode")), nil},
		{"undecodable", a2bContext(), image(good, "Filter", pdf.PDFArray{name("FlateDecode"), name("JPXDecode")}), nil},
		{"Part 1", &ValidationContext{Spec: &s}, image(nil), nil},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			validateJPXImage(tc.dict, tc.ctx)
			for _, c := range all {
				want := false
				for _, w := range tc.want {
					want = want || w == c
				}
				if got := hasCheck(tc.ctx, c); got != want {
					t.Errorf("%s reported = %v, want %v", c.Name(), got, want)
				}
			}
		})
	}
}

// tripwire is a reader that records whether it was read.
type tripwire struct{ read bool }

func (t *tripwire) Read([]byte) (int, error) {
	t.read = true
	return 0, io.EOF
}

// TestReadJPXHeaderStopsAtTheHeaderBox verifies that readJPXHeader reads
// nothing past the header box, so the codestream after it is never read.
func TestReadJPXHeaderStopsAtTheHeaderBox(t *testing.T) {
	codestream := &tripwire{}
	h := readJPXHeader(io.MultiReader(bytes.NewReader(jp2File(ihdr(3, 8), colr(1, 0, 16))), codestream))
	if h.Channels != 3 {
		t.Errorf("Channels = %d, want 3", h.Channels)
	}
	if codestream.read {
		t.Error("the data after the header box was read")
	}
}
