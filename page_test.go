package gopdfrab

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"sync"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// pageDoc builds a two-page PDF: page 1 is 100x50 with a red square at its
// lower-left corner and attrs appended to its dict, page 2 is the default.
func pageDoc(t *testing.T, attrs string) *Document {
	t.Helper()
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 50] /Resources << >> /Contents 4 0 R "+attrs+" >>")
	b.StreamObj(4, "<<", []byte("1 0 0 rg\n0 0 20 10 re f\n"))
	b.Obj(5, "<< /Type /Page /Parent 2 0 R /Resources << >> >>")
	doc, err := OpenBytes(b.FinishClassic("<< /Size 6 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { doc.Close() })
	return doc
}

func TestPageSizeAndRange(t *testing.T) {
	doc := pageDoc(t, "")
	if n, err := doc.Pages(); err != nil || n != 2 {
		t.Fatalf("Pages = %d, %v", n, err)
	}
	p, err := doc.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := p.Size(); w != 100 || h != 50 || p.Index() != 0 || p.Rotation() != 0 {
		t.Errorf("page 0: %vx%v index %d rot %d", w, h, p.Index(), p.Rotation())
	}
	p2, _ := doc.Page(1)
	if w, h := p2.Size(); w != 612 || h != 792 {
		t.Errorf("default page = %vx%v", w, h)
	}
	for _, i := range []int{-1, 2} {
		if _, err := doc.Page(i); !errors.Is(err, ErrPageRange) {
			t.Errorf("Page(%d) err = %v", i, err)
		}
	}
}

func TestPageRenderRotations(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	// The red square sits at the lower-left of the unrotated page; each
	// clockwise quarter turn moves it to the next corner, starting at top-left.
	for _, tc := range []struct {
		rot, w, h, x, y int
	}{
		{0, 100, 50, 1, 48},
		{90, 50, 100, 1, 1},
		{180, 100, 50, 98, 1},
		{270, 50, 100, 48, 98},
	} {
		t.Run(fmt.Sprint(tc.rot), func(t *testing.T) {
			doc := pageDoc(t, fmt.Sprintf("/Rotate %d", tc.rot))
			p, _ := doc.Page(0)
			img, err := p.Render(context.Background(), RenderOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if img.Rect.Dx() != tc.w || img.Rect.Dy() != tc.h {
				t.Fatalf("size = %v", img.Rect)
			}
			if got := img.RGBAAt(tc.x, tc.y); got != red {
				t.Errorf("pixel (%d,%d) = %v, want red", tc.x, tc.y, got)
			}
		})
	}
}

func TestPageRenderScaleAndErrors(t *testing.T) {
	doc := pageDoc(t, "/UserUnit 2")
	p, _ := doc.Page(0)
	img, err := p.Render(context.Background(), RenderOptions{Scale: 0.5})
	if err != nil || img.Rect.Dx() != 100 || img.Rect.Dy() != 50 {
		t.Fatalf("scaled render = %v, %v", img.Rect, err)
	}
	for _, s := range []float64{-1, nan(), inf()} {
		if _, err := p.Render(context.Background(), RenderOptions{Scale: s}); err == nil {
			t.Errorf("scale %v accepted", s)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Render(ctx, RenderOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled render err = %v", err)
	}
}

// TestPageRenderCanceledAfter verifies that Render returns context.Canceled
// when the context is canceled during the render, after rasterization.
func TestPageRenderCanceledAfter(t *testing.T) {
	doc := pageDoc(t, "")
	p, _ := doc.Page(0)
	ctx := &lateCtx{Context: context.Background()}
	if _, err := p.Render(ctx, RenderOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

// lateCtx reports cancellation from its second Err call on.
type lateCtx struct {
	context.Context
	n int
}

func (c *lateCtx) Err() error {
	c.n++
	if c.n > 1 {
		return context.Canceled
	}
	return nil
}

func TestPageRenderError(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 50] /Contents 4 0 R >>")
	b.StreamObj(4, "<< /Filter /FlateDecode", []byte("not flate"))
	doc, err := OpenBytes(b.FinishClassic("<< /Size 5 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	p, _ := doc.Page(0)
	if _, err := p.Render(context.Background(), RenderOptions{}); err == nil {
		t.Error("render of an undecodable page succeeded")
	}
}

// TestPageConcurrent verifies that pages of one document can be rendered from
// several goroutines at once. It is meant to be run with -race.
func TestPageConcurrent(t *testing.T) {
	doc := pageDoc(t, "")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := doc.Page(i % 2)
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := p.Render(context.Background(), RenderOptions{Scale: 0.25}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestPagesNoTree(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog >>")
	doc, err := OpenBytes(b.FinishClassic("<< /Size 2 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if n, err := doc.Pages(); err != nil || n != 0 {
		t.Errorf("Pages = %d, %v", n, err)
	}
}

func nan() float64 { var z float64; return z / z }
func inf() float64 { var z float64; return 1 / z }

func TestPageResolveFailure(t *testing.T) {
	doc := pageDoc(t, "/Rotate 90")
	// Either catalog entry Pages reads failing fails it.
	for _, failing := range []string{"Pages", "OCProperties"} {
		doc.resolvePath = func(path []string) (pdf.PDFValue, error) {
			if path[len(path)-1] == failing {
				return nil, errors.New("boom")
			}
			return doc.r.ResolveGraphByPath(path)
		}
		if _, err := doc.Pages(); err == nil {
			t.Errorf("Pages ignored the error resolving %s", failing)
		}
	}
	if _, err := doc.Page(0); err == nil {
		t.Error("Page ignored the resolve error")
	}
	doc.resolvePath = nil
	p, err := doc.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	// A quarter-turn page swaps its displayed width and height.
	if w, h := p.Size(); w != 50 || h != 100 {
		t.Errorf("rotated size = %vx%v", w, h)
	}
}

func TestPageRenderRegionIsAPartOfTheWholePage(t *testing.T) {
	for _, rot := range []int{0, 90, 180, 270} {
		t.Run(fmt.Sprint(rot), func(t *testing.T) {
			doc := pageDoc(t, fmt.Sprintf("/Rotate %d", rot))
			p, _ := doc.Page(0)
			ctx := context.Background()
			whole, err := p.Render(ctx, RenderOptions{Scale: 4})
			if err != nil {
				t.Fatal(err)
			}
			b := whole.Rect
			for _, region := range []image.Rectangle{
				b, image.Rect(0, 0, 30, 20), image.Rect(b.Dx()-25, b.Dy()-17, b.Dx(), b.Dy()),
				image.Rect(10, 5, 90, 70), image.Rect(-20, -20, 40, 30), image.Rect(b.Dx()-10, 3, b.Dx()+50, 60),
			} {
				got, err := p.Render(ctx, RenderOptions{Scale: 4, Region: region})
				if err != nil {
					t.Fatalf("region %v: %v", region, err)
				}
				want := region.Intersect(b)
				if got.Rect.Dx() != want.Dx() || got.Rect.Dy() != want.Dy() {
					t.Fatalf("region %v: size %v, want %v", region, got.Rect, want)
				}
				for y := 0; y < got.Rect.Dy(); y++ {
					for x := 0; x < got.Rect.Dx(); x++ {
						if g, w := got.RGBAAt(x, y), whole.RGBAAt(want.Min.X+x, want.Min.Y+y); g != w {
							t.Fatalf("region %v: pixel (%d,%d) = %v, whole page has %v", region, x, y, g, w)
						}
					}
				}
			}
			if _, err := p.Render(ctx, RenderOptions{Scale: 4, Region: image.Rect(b.Dx()+5, 0, b.Dx()+9, 4)}); err == nil {
				t.Error("a region beside the page was accepted")
			}
		})
	}
}

// textDoc is a 100x50 page showing "Hi" in 20 pt Helvetica with a baseline at
// (10, 30) from the lower left, with attrs added to the page dict.
func textDoc(t *testing.T, attrs string) *Document {
	t.Helper()
	b := pdfgen.NewBuilder("%PDF-1.4\\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 50] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R "+attrs+" >>")
	b.StreamObj(4, "<<", []byte("BT /F1 20 Tf 10 30 Td (Hi) Tj ET"))
	b.Obj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding /FirstChar 72 /LastChar 105 /Widths ["+widths(72, 105, 500)+"] >>")
	doc, err := OpenBytes(b.FinishClassic("<< /Size 6 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { doc.Close() })
	return doc
}

func widths(first, last, w int) string {
	var s string
	for i := first; i <= last; i++ {
		s += fmt.Sprintf("%d ", w)
	}
	return s
}

func TestPageGlyphsFollowTheDisplayedOrientation(t *testing.T) {
	// Unrotated, the glyph cells are 10 wide, from y 4 to 24 from the top. A
	// quarter turn clockwise puts the top of the page on the right.
	for _, tc := range []struct {
		attrs string
		h     Rect
		i     Rect
	}{
		{"", Rect{10, 4, 20, 24}, Rect{20, 4, 30, 24}},
		{"/Rotate 90", Rect{26, 10, 46, 20}, Rect{26, 20, 46, 30}},
		{"/Rotate 180", Rect{80, 26, 90, 46}, Rect{70, 26, 80, 46}},
		{"/Rotate 270", Rect{4, 80, 24, 90}, Rect{4, 70, 24, 80}},
		{"/UserUnit 2", Rect{20, 8, 40, 48}, Rect{40, 8, 60, 48}},
	} {
		t.Run(tc.attrs, func(t *testing.T) {
			p, _ := textDoc(t, tc.attrs).Page(0)
			got, err := p.Glyphs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got[0].Rune != 'H' || got[1].Rune != 'i' {
				t.Fatalf("glyphs = %+v", got)
			}
			near := func(a, b Rect) bool {
				d := func(x, y float64) bool { return x-y < 0.01 && y-x < 0.01 }
				return d(a.X0, b.X0) && d(a.Y0, b.Y0) && d(a.X1, b.X1) && d(a.Y1, b.Y1)
			}
			if !near(got[0].Box, tc.h) || !near(got[1].Box, tc.i) {
				t.Errorf("boxes %+v and %+v, want %+v and %+v", got[0].Box, got[1].Box, tc.h, tc.i)
			}
		})
	}
}

func TestPageGlyphsErrors(t *testing.T) {
	p, _ := textDoc(t, "").Page(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Glyphs(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled before = %v", err)
	}
	// A page whose contents cannot be decoded makes Glyphs return an error.
	b := pdfgen.NewBuilder("%PDF-1.4\\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 10 10] /Contents 4 0 R >>")
	b.StreamObj(4, "<< /Filter /LZWDecode ", []byte{0xFF, 0xFF, 0xFF})
	doc, err := OpenBytes(b.FinishClassic("<< /Size 5 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	bp, _ := doc.Page(0)
	if _, err := bp.Glyphs(context.Background()); err == nil {
		t.Error("undecodable contents gave glyphs")
	}
}
