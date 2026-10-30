package raster

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// undecodableStream is a content stream whose filter cannot decode its data.
func undecodableStream() pdf.PDFDict {
	return pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}), []byte{0xFF, 0xFF, 0xFF})
}

func formWithBBox(content string, bbox ...int64) pdf.PDFDict {
	box := pdf.PDFArray{}
	for _, v := range bbox {
		box = append(box, pdf.PDFInteger(v))
	}
	return pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"BBox": box}), []byte(content))
}

func TestRenderPageUndecodableContentFails(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Contents": undecodableStream()})}
	if _, _, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72); err == nil {
		t.Error("RenderPage succeeded on undecodable content")
	}
}

func TestRenderFormContentRejectsBadForms(t *testing.T) {
	noBBox := pdf.NewStreamDict(nil, []byte(""))
	shortBBox := formWithBBox("", 0, 0, 20)
	badStream := formWithBBox("", 0, 0, 20, 20)
	badStream.Entries.Set("Filter", pdf.PDFName{Value: "LZWDecode"})
	badStream.SetStream([]byte{0xFF, 0xFF, 0xFF})

	for name, form := range map[string]pdf.PDFDict{
		"missing BBox":      noBBox,
		"BBox of 3 numbers": shortBBox,
		"undecodable":       badStream,
		"empty BBox":        formWithBBox("", 5, 5, 5, 5),
		"oversized BBox":    formWithBBox("", 0, 0, 30000, 20),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := RenderFormContent(form, pdf.PDFDict{}, 72, InheritedPaint{}); err == nil {
				t.Error("RenderFormContent accepted a form it cannot render")
			}
		})
	}
}

// TestRenderFormContentInheritedPaint covers a form that fills without naming
// a colour: it paints in the colour the invoking stream had reached.
func TestRenderFormContentInheritedPaint(t *testing.T) {
	form := formWithBBox("0 0 20 20 re f", 0, 0, 20, 20)
	canvas, _, _, err := RenderFormContent(form, pdf.PDFDict{}, 72, InheritedPaint{Set: true, Fill: [3]float64{1, 0, 0}})
	if err != nil {
		t.Fatalf("RenderFormContent: %v", err)
	}
	if px := nrgbaAt(t, canvas, 10, 10); px.R != 255 || px.G != 0 || px.B != 0 || px.A != 255 {
		t.Errorf("inherited fill painted %+v, want opaque red", px)
	}
	canvas, _, _, err = RenderFormContent(form, pdf.PDFDict{}, 72, InheritedPaint{})
	if err != nil {
		t.Fatalf("RenderFormContent: %v", err)
	}
	if px := nrgbaAt(t, canvas, 10, 10); px.R != 0 || px.A != 255 {
		t.Errorf("default fill painted %+v, want opaque black", px)
	}
}

func TestPageContentBytesSkipsNonStreamArrayItems(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.PDFArray{
			pdf.PDFInteger(7),
			pdf.NewStreamDict(nil, []byte("q Q")),
		},
	})}
	got, err := PageContentBytes(page)
	if err != nil {
		t.Fatalf("PageContentBytes: %v", err)
	}
	if string(got) != "q Q\n" {
		t.Errorf("got %q, want only the stream's content", got)
	}
}
