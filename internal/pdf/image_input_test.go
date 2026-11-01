package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"testing"
)

// imageStream returns a stream holding raw under filter, with parms as its
// DecodeParms when not nil.
func imageStream(raw []byte, filter PDFValue, parms PDFValue) PDFDict {
	d := NewStreamDict(nil, raw)
	d.Entries.Set("Filter", filter)
	if parms != nil {
		d.Entries.Set("DecodeParms", parms)
	}
	return d
}

func deflate(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestImageCodecInput(t *testing.T) {
	payload := []byte("jpeg 2000 data")
	jpx := PDFName{Value: "JPXDecode"}
	flate := PDFName{Value: "FlateDecode"}
	hex := PDFName{Value: "ASCIIHexDecode"}
	predictor := NewPDFDict()
	predictor.Entries.Set("Predictor", PDFInteger(2))
	for _, tc := range []struct {
		desc  string
		dict  PDFDict
		codec FilterKind
	}{
		{"raw", imageStream(payload, jpx, nil), FilterJPX},
		{"after Flate", imageStream(deflate(t, payload), PDFArray{flate, jpx}, nil), FilterJPX},
		{"after ASCIIHex", imageStream([]byte("6a70656720323030302064617461>"), PDFArray{hex, jpx}, nil), FilterJPX},
		{"after Flate and ASCIIHex", imageStream(deflate(t, []byte("6a70656720323030302064617461>")), PDFArray{flate, hex, jpx}, nil), FilterJPX},
		{"after a Flate predictor", imageStream(deflate(t, payload), PDFArray{flate, jpx}, PDFArray{predictor, nil}), FilterJPX},
		{"no image codec", imageStream(deflate(t, payload), flate, nil), FilterNone},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			r, codec, err := ImageCodecInput(tc.dict)
			if err != nil {
				t.Fatalf("ImageCodecInput: %v", err)
			}
			if (codec == nil) != (tc.codec == FilterNone) || codec != nil && codec.Kind != tc.codec {
				t.Fatalf("codec = %v, want kind %v", codec, tc.codec)
			}
			got, _ := io.ReadAll(r)
			if tc.desc != "after a Flate predictor" && !bytes.Equal(got, payload) {
				t.Errorf("data = %q, want %q", got, payload)
			}
		})
	}
}

func TestImageCodecInputErrors(t *testing.T) {
	jpx := PDFName{Value: "JPXDecode"}
	flate := PDFName{Value: "FlateDecode"}
	for _, tc := range []struct {
		desc string
		dict PDFDict
		want error
	}{
		{"not a stream", NewPDFDict(), ErrNotAStream},
		{"unknown filter", imageStream(nil, PDFName{Value: "Bogus"}, nil), ErrUnsupportedFilter},
		{"codec not last", imageStream(nil, PDFArray{jpx, flate}, nil), ErrUnsupportedFilter},
		{"not zlib", imageStream([]byte("plain"), PDFArray{flate, jpx}, nil), nil},
		{"bad ASCIIHex", imageStream([]byte("zz>"), PDFArray{PDFName{Value: "ASCIIHexDecode"}, jpx}, nil), nil},
		{"Flate yields nothing before ASCIIHex", imageStream([]byte{0x78, 0x9c, 0xff}, PDFArray{flate, PDFName{Value: "ASCIIHexDecode"}, jpx}, nil), nil},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, _, err := ImageCodecInput(tc.dict)
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
