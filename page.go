package gopdfrab

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
)

// ErrPageRange is returned by Document.Page for an index outside the document.
var ErrPageRange = errors.New("gopdfrab: page index out of range")

// pageEntry is one page of the document with the attributes it inherits.
type pageEntry struct {
	dict      pdf.PDFDict
	resources pdf.PDFDict
}

// loadPages resolves the page tree once. The caller holds d.mu.
func (d *Document) loadPages() error {
	if d.index {
		return nil
	}
	resolve := d.r.ResolveGraph
	if d.resolveGraph != nil {
		resolve = d.resolveGraph
	}
	graph, err := resolve()
	if err != nil {
		return err
	}
	trailer, _ := graph.(pdf.PDFDict)
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	if tree, ok := root.Entries.Get("Pages").(pdf.PDFDict); ok {
		pdf.WalkPages(tree, func(page, res pdf.PDFDict, _ [4]float64, _ int) {
			d.pages = append(d.pages, pageEntry{dict: page, resources: res})
		})
	}
	d.oc = content.NewOptionalContent(root)
	d.index = true
	return nil
}

// Pages returns the number of pages found by walking the page tree. Unlike
// PageCount it does not trust the /Count entry.
func (d *Document) Pages() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.loadPages(); err != nil {
		return 0, err
	}
	return len(d.pages), nil
}

// Page returns the page at the zero-based index i, or ErrPageRange. The first
// call walks the page tree. The returned Page is valid until the document is
// closed.
func (d *Document) Page(i int) (*Page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.loadPages(); err != nil {
		return nil, err
	}
	if i < 0 || i >= len(d.pages) {
		return nil, fmt.Errorf("%w: %d of %d", ErrPageRange, i, len(d.pages))
	}
	e := d.pages[i]
	g := pdf.Geometry(e.dict)
	return &Page{doc: d, index: i, entry: e, geom: g}, nil
}

// Page is one page of a Document. Its methods may be called from several
// goroutines; they serialize on the document.
type Page struct {
	doc   *Document
	index int
	entry pageEntry
	geom  pdf.PageGeometry
}

// Index returns the page's zero-based position in the document.
func (p *Page) Index() int { return p.index }

// Rotation returns the page's clockwise display rotation in degrees: 0, 90,
// 180 or 270.
func (p *Page) Rotation() int { return p.geom.Rotate }

// Size returns the displayed size in points (1/72 inch): the crop box scaled
// by the user unit and swapped for a quarter-turn rotation.
func (p *Page) Size() (width, height float64) {
	w := (p.geom.CropBox[2] - p.geom.CropBox[0]) * p.geom.UserUnit
	h := (p.geom.CropBox[3] - p.geom.CropBox[1]) * p.geom.UserUnit
	if p.geom.Rotate%180 != 0 {
		return h, w
	}
	return w, h
}

// RenderOptions controls Page.Render.
type RenderOptions struct {
	// Scale is device pixels per point: 1 is 72 dpi. Zero means 1. The
	// resolution is rounded to whole dpi, and very large pages are rendered
	// at a lower resolution to bound memory.
	Scale float64
}

// Render rasterizes the page, rotated for display, onto an opaque white
// background. It returns ctx.Err() if ctx is done before or after the render;
// a render in progress is not interrupted.
func (p *Page) Render(ctx context.Context, opt RenderOptions) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scale := opt.Scale
	if scale == 0 {
		scale = 1
	}
	if scale < 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		return nil, fmt.Errorf("gopdfrab: invalid render scale %v", scale)
	}
	dpi := max(1, int(math.Round(72*scale*p.geom.UserUnit)))

	p.doc.mu.Lock()
	img, _, err := raster.RenderPage(p.entry.dict, p.entry.resources, p.geom.CropBox, dpi, p.doc.oc)
	p.doc.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rotateClockwise(img, p.geom.Rotate), nil
}

// rotateClockwise returns img turned by deg (0, 90, 180 or 270) clockwise.
func rotateClockwise(img *image.RGBA, deg int) *image.RGBA {
	if deg == 0 {
		return img
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if deg != 180 {
		out = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := range h {
		for x := range w {
			var dx, dy int
			switch deg {
			case 90:
				dx, dy = h-1-y, x
			case 180:
				dx, dy = w-1-x, h-1-y
			default:
				dx, dy = y, w-1-x
			}
			copy(out.Pix[out.PixOffset(dx, dy):][:4], img.Pix[img.PixOffset(x, y):][:4])
		}
	}
	return out
}
