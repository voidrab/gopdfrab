package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// inlineImagePage wraps content in a page dict for the inline-image render tests.
func inlineImagePage(content string) pdf.PDFDict {
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
	}}
}

func TestInlineImageDictKeyExpansion(t *testing.T) {
	name := func(s string) pdf.PDFValue { return pdf.PDFName{Value: s} }

	tests := []struct {
		name   string
		params []pdf.PDFValue
		ok     bool
		want   map[string]pdf.PDFValue
	}{
		{
			name: "abbreviated keys expand",
			params: []pdf.PDFValue{
				name("W"), pdf.PDFInteger(4), name("H"), pdf.PDFInteger(2),
				name("BPC"), pdf.PDFInteger(8), name("CS"), name("RGB"),
				name("F"), name("Fl"), name("IM"), pdf.PDFBoolean(true),
			},
			ok: true,
			want: map[string]pdf.PDFValue{
				"Width": pdf.PDFInteger(4), "Height": pdf.PDFInteger(2),
				"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": name("RGB"),
				"Filter": name("Fl"), "ImageMask": pdf.PDFBoolean(true),
			},
		},
		{
			name: "long spellings pass through",
			params: []pdf.PDFValue{
				name("Width"), pdf.PDFInteger(3), name("Height"), pdf.PDFInteger(3),
				name("DecodeParms"), pdf.NewPDFDict(),
			},
			ok:   true,
			want: map[string]pdf.PDFValue{"Width": pdf.PDFInteger(3), "Height": pdf.PDFInteger(3)},
		},
		{
			name:   "missing width is unusable",
			params: []pdf.PDFValue{name("H"), pdf.PDFInteger(2)},
		},
		{
			name:   "zero height is unusable",
			params: []pdf.PDFValue{name("W"), pdf.PDFInteger(2), name("H"), pdf.PDFInteger(0)},
		},
		{
			name:   "no params at all",
			params: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := inlineImageDict(tc.params, pdf.InlineImageRaw{Data: []byte{1, 2, 3}})
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if !got.HasStream || len(got.RawStream) != 3 {
				t.Errorf("stream not attached: HasStream=%v len=%d", got.HasStream, len(got.RawStream))
			}
			for k, want := range tc.want {
				if got.Entries[k] != want {
					t.Errorf("Entries[%q] = %v, want %v", k, got.Entries[k], want)
				}
			}
		})
	}
}

// TestRenderInlineImageRGB: an inline RGB image paints its samples, with row 0
// at the top of the page and column 0 at the left.
func TestRenderInlineImageRGB(t *testing.T) {
	samples := []byte{
		255, 0, 0, 0, 255, 0, // top row: red, green
		0, 0, 255, 255, 255, 255, // bottom row: blue, white
	}
	content := "q 20 0 0 20 0 0 cm BI /W 2 /H 2 /CS /RGB /BPC 8 ID " + string(samples) + " EI Q"

	img, drops, err := RenderPage(inlineImagePage(content), pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}

	corners := []struct {
		name    string
		x, y    int
		r, g, b uint8
	}{
		{"top-left red", 5, 5, 255, 0, 0},
		{"top-right green", 15, 5, 0, 255, 0},
		{"bottom-left blue", 5, 15, 0, 0, 255},
	}
	for _, c := range corners {
		got := nrgbaAt(t, img, c.x, c.y)
		if got.R != c.r || got.G != c.g || got.B != c.b {
			t.Errorf("%s at (%d,%d) = %v, want (%d,%d,%d)", c.name, c.x, c.y, got, c.r, c.g, c.b)
		}
	}
}

// TestRenderInlineImageStencilMask: an /IM true image carries coverage, not
// colour, so its painted samples take the current fill colour.
func TestRenderInlineImageStencilMask(t *testing.T) {
	// One 1-bit sample, value 0: opaque under the default /Decode [0 1].
	content := "q 0 1 0 rg 20 0 0 20 0 0 cm BI /IM true /W 1 /H 1 /BPC 1 ID \x00 EI Q"

	img, drops, err := RenderPage(inlineImagePage(content), pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.R != 0 || got.G != 255 || got.B != 0 {
		t.Errorf("stencil painted %v, want the green fill colour", got)
	}
}

// TestRenderInlineImageUndecodableDrops: an inline image the decode chain
// cannot handle stays loud rather than silently painting nothing.
func TestRenderInlineImageUndecodableDrops(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"unsupported filter", "q 20 0 0 20 0 0 cm BI /W 2 /H 2 /CS /RGB /F /Xx ID \x01\x02 EI Q"},
		{"no dimensions", "q 20 0 0 20 0 0 cm BI /CS /RGB /BPC 8 ID \x01\x02 EI Q"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, drops, err := RenderPage(inlineImagePage(tc.content), pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
			if err != nil {
				t.Fatalf("RenderPage: %v", err)
			}
			if !hasDrop(drops, dropInlineImage) {
				t.Errorf("drops = %v, want %q", drops, dropInlineImage)
			}
		})
	}
}
