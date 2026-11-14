package gopdfrab

import (
	"context"
	"errors"
	"fmt"
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
	doc.resolveGraph = func() (pdf.PDFValue, error) { return nil, errors.New("boom") }
	if _, err := doc.Pages(); err == nil {
		t.Error("Pages ignored the resolve error")
	}
	if _, err := doc.Page(0); err == nil {
		t.Error("Page ignored the resolve error")
	}
	doc.resolveGraph = nil
	p, err := doc.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	// A quarter-turn page swaps its displayed width and height.
	if w, h := p.Size(); w != 50 || h != 100 {
		t.Errorf("rotated size = %vx%v", w, h)
	}
}
