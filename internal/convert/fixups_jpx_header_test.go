package convert

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// jp2BoxBytes returns a box of the given kind and contents.
func jp2BoxBytes(kind string, body []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(out, kind...), body...)
}

// ihdrBox returns an image header box declaring nc components of depth
// bpc+1.
func ihdrBox(nc int, bpc byte) []byte {
	body := make([]byte, 14)
	binary.BigEndian.PutUint32(body[0:], 16) // height
	binary.BigEndian.PutUint32(body[4:], 16) // width
	binary.BigEndian.PutUint16(body[8:], uint16(nc))
	body[10], body[11] = bpc, 7
	return jp2BoxBytes("ihdr", body)
}

// codestream returns the start of a codestream, with a SIZ marker for
// components with the given Ssiz bytes.
func codestream(ssiz ...byte) []byte {
	cs := []byte{0xFF, 0x4F, 0xFF, 0x51}
	cs = binary.BigEndian.AppendUint16(cs, uint16(38+3*len(ssiz)))
	cs = append(cs, make([]byte, 34)...) // Rsiz and the sizes and offsets
	cs = binary.BigEndian.AppendUint16(cs, uint16(len(ssiz)))
	for _, s := range ssiz {
		cs = append(cs, s, 1, 1)
	}
	return append(cs, 0xFF, 0xD9)
}

// jp2File returns a JP2 file with the given header box contents and
// codestream.
func jp2File(header, cs []byte) []byte {
	out := []byte{0, 0, 0, 0x0C, 'j', 'P', ' ', ' ', 0x0D, 0x0A, 0x87, 0x0A}
	out = append(out, jp2BoxBytes("ftyp", []byte("jp2 \x00\x00\x00\x00jp2 "))...)
	out = append(out, jp2BoxBytes("jp2h", header)...)
	return append(out, jp2BoxBytes("jp2c", cs)...)
}

var colrBox = jp2BoxBytes("colr", []byte{1, 0, 0, 0, 0, 0, 16})

func TestCorrectJP2Header(t *testing.T) {
	// A header declaring five components for a codestream of three is
	// corrected, and its colour specification box is kept.
	fixed, ok := correctJP2Header(jp2File(append(ihdrBox(5, 7), colrBox...), codestream(7, 7, 7)))
	if !ok {
		t.Fatal("correctJP2Header did not correct a header with too many components")
	}
	h, _ := verify.JPXImageHeader(jpxImage(fixed))
	if h.Channels != 3 || h.BitDepth != 8 || h.ColourSpecs != 1 {
		t.Errorf("corrected header = %+v", h)
	}

	// A header of depth 41 with a bpcc box, for a codestream of 8-bit
	// components, is corrected, and the bpcc box is removed.
	fixed, ok = correctJP2Header(jp2File(append(ihdrBox(3, 40), jp2BoxBytes("bpcc", []byte{7, 7, 7})...), codestream(7, 7, 7)))
	if !ok {
		t.Fatal("correctJP2Header did not correct a header with a bad depth")
	}
	if h, _ := verify.JPXImageHeader(jpxImage(fixed)); h.BitDepth != 8 || h.BPCC {
		t.Errorf("corrected header = %+v", h)
	}

	header := ihdrBox(3, 7)
	for name, data := range map[string][]byte{
		"already right":         jp2File(header, codestream(7, 7, 7)),
		"two codestream depths": jp2File(header, codestream(7, 15, 7)),
		"signed components":     jp2File(header, codestream(0x87, 0x87, 0x87)),
		"depth beyond 38":       jp2File(header, codestream(40, 40, 40)),
		"two components":        jp2File(header, codestream(7, 7)),
		"no SIZ marker":         jp2File(header, []byte{0xFF, 0x4F, 0xFF, 0x52, 0, 0}),
		"short SIZ marker":      jp2File(header, codestream(7, 7, 7)[:20]),
		"no components":         jp2File(header, codestream()),
		"cut component list":    jp2File(header, codestream(7, 7, 7)[:44]),
		"short ihdr":            jp2File(jp2BoxBytes("ihdr", make([]byte, 10)), codestream(7, 7, 7)),
		"broken header box":     jp2File([]byte{0, 0, 0, 3, 'i', 'h', 'd', 'r'}, codestream(7, 7, 7)),
		"no header box":         append(jp2BoxBytes("ftyp", nil), jp2BoxBytes("jp2c", codestream(7))...),
		"no codestream box":     jp2BoxBytes("jp2h", ihdrBox(5, 7)),
		"trailing bytes":        append(jp2File(ihdrBox(5, 7), codestream(7, 7, 7)), 1, 2, 3),
		"overlong box":          {0, 0, 0, 0x7F, 'j', 'p', '2', 'h'},
	} {
		if _, ok := correctJP2Header(data); ok {
			t.Errorf("%s: correctJP2Header = ok", name)
		}
	}

	// A box with length 0 extends to the end of the file, and only the first
	// header and codestream boxes are read.
	data := jp2File(ihdrBox(5, 7), codestream(7, 7, 7))
	data = append(data, jp2BoxBytes("jp2h", ihdrBox(1, 7))...)
	data = append(data, 0, 0, 0, 0, 'j', 'p', '2', 'c', 0xFF)
	if _, ok := correctJP2Header(data); !ok {
		t.Error("correctJP2Header did not correct the first header box")
	}
}

// jpxImage returns an image XObject holding data with the JPXDecode filter.
func jpxImage(data []byte) pdf.PDFDict {
	d := streamOf(data)
	d.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	d.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	d.Entries.Set("Filter", pdf.PDFName{Value: "JPXDecode"})
	return d
}

func TestJPXHeaderFixer(t *testing.T) {
	f := jpxHeaderFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Image.JPXColourChannels || c == pdf.Checks.Image.JPXBitDepth
		if f.Applies(c) != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), !want, want)
		}
	}

	// Data behind a Flate filter is stored with JPXDecode alone afterwards.
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(jp2File(ihdrBox(5, 7), codestream(7, 7, 7)))
	zw.Close()
	img := jpxImage(z.Bytes())
	img.Entries.Set("Filter", pdf.PDFArray{pdf.PDFName{Value: "FlateDecode"}, pdf.PDFName{Value: "JPXDecode"}})
	img.Entries.Set("DecodeParms", pdf.PDFArray{nil, nil})
	trailer := trailerWith("Img", img)
	if changed, _ := f.Fix(&trailer, nil); !changed {
		t.Fatal("Fix changed nothing")
	}
	if img.Entries.Get("Filter") != (pdf.PDFName{Value: "JPXDecode"}) || img.Entries.Get("DecodeParms") != nil {
		t.Errorf("filters after the fix = %v, %v", img.Entries.Get("Filter"), img.Entries.Get("DecodeParms"))
	}
	if h, _ := verify.JPXImageHeader(img); h.Channels != 3 {
		t.Errorf("corrected header = %+v", h)
	}

	truncated := jpxImage(z.Bytes()[:len(z.Bytes())/2])
	truncated.Entries.Set("Filter", pdf.PDFArray{pdf.PDFName{Value: "FlateDecode"}, pdf.PDFName{Value: "JPXDecode"}})
	badHex := jpxImage([]byte("zz"))
	badHex.Entries.Set("Filter", pdf.PDFArray{pdf.PDFName{Value: "ASCIIHexDecode"}, pdf.PDFName{Value: "JPXDecode"}})
	for name, d := range map[string]pdf.PDFDict{
		"conforming header":  jpxImage(jp2File(ihdrBox(3, 7), codestream(7, 7, 7))),
		"unrepairable":       jpxImage(jp2File(ihdrBox(5, 7), codestream(7, 7, 7, 7, 7))),
		"not an image":       streamOf(jp2File(ihdrBox(5, 7), codestream(7, 7, 7))),
		"truncated Flate":    truncated,
		"undecodable filter": badHex,
	} {
		trailer := trailerWith("Img", d)
		if changed, _ := f.Fix(&trailer, nil); changed {
			t.Errorf("%s: Fix changed the image", name)
		}
	}
}
