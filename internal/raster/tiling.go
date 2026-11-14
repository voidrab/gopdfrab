package raster

import (
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Limits on the size of a rendered pattern cell, so that a pattern whose cell
// is as large as the page, or larger, cannot exhaust memory.
const (
	maxTileSide   = 8192
	maxTilePixels = 1 << 22
	// maxTileCacheBytes bounds the rendered cells kept for reuse.
	maxTileCacheBytes = 64 << 20
	// maxTileOverlap bounds how many neighbouring cells are drawn into one
	// when the pattern's bounding box exceeds its step.
	maxTileOverlap = 2
)

// tiling is a parsed tiling pattern (PatternType 1).
type tiling struct {
	bbox         [4]float64 // x0, y0, x1, y1 in pattern space
	xstep, ystep float64    // cell size in pattern space, positive
	uncoloured   bool       // PaintType 2: the content carries no colour
}

// parseTiling reads a tiling pattern's geometry. It reports false for a
// pattern that is not a content stream, has an empty bounding box, or has a
// step of zero or less than a visible size.
func parseTiling(pattern pdf.PDFDict) (*tiling, bool) {
	if !pattern.IsStream() || pdf.DictInt(pattern, "PatternType", 0) != 1 {
		return nil, false
	}
	paint := pdf.DictInt(pattern, "PaintType", 0)
	if paint != 1 && paint != 2 {
		return nil, false
	}
	b, err := pdf.FloatArray(pattern.Entries.Get("BBox"))
	if err != nil || len(b) != 4 {
		return nil, false
	}
	t := &tiling{
		bbox:       [4]float64{math.Min(b[0], b[2]), math.Min(b[1], b[3]), math.Max(b[0], b[2]), math.Max(b[1], b[3])},
		uncoloured: paint == 2,
	}
	xs, okx := pdf.PDFNumberToFloat(pattern.Entries.Get("XStep"))
	ys, oky := pdf.PDFNumberToFloat(pattern.Entries.Get("YStep"))
	t.xstep, t.ystep = math.Abs(xs), math.Abs(ys)
	if !okx || !oky || t.xstep < 1e-6 || t.ystep < 1e-6 || t.bbox[2] <= t.bbox[0] || t.bbox[3] <= t.bbox[1] ||
		math.IsNaN(t.xstep+t.ystep+t.bbox[0]+t.bbox[1]+t.bbox[2]+t.bbox[3]) || math.IsInf(t.xstep+t.ystep, 0) {
		return nil, false
	}
	return t, true
}

// tileKey identifies a rendered pattern cell: the pattern, the pixel size it
// was rendered at, and, for an uncoloured pattern, the colour it was drawn in.
type tileKey struct {
	pattern uintptr
	w, h    int
	rgb     [3]float64
}

// tilingSampler returns a per-pixel colour source for a tiling pattern: the
// pattern's cell, rendered once and repeated across device space through the
// pattern matrix. under is the colour an uncoloured pattern paints in, and
// alpha scales the result. It reports false when the pattern cannot be drawn.
func (r *renderer) tilingSampler(pattern pdf.PDFDict, under [3]float64, alpha float64) (func(x, y int) ([3]float64, float64), bool) {
	t, ok := parseTiling(pattern)
	if !ok {
		return nil, false
	}
	m := r.patternToDevice(pattern)
	inv, ok := m.Invert()
	if !ok {
		return nil, false
	}
	// Render the cell at the resolution it is shown at.
	tw := min(max(int(math.Ceil(t.xstep*math.Hypot(m.A, m.B))), 1), maxTileSide)
	th := min(max(int(math.Ceil(t.ystep*math.Hypot(m.C, m.D))), 1), maxTileSide)
	for tw*th > maxTilePixels {
		tw, th = (tw+1)/2, (th+1)/2
	}
	tile := r.tileImage(pattern, t, tw, th, under)
	if tile == nil {
		return nil, false
	}
	return func(x, y int) ([3]float64, float64) {
		p := inv.Apply(content.Point{X: float64(x) + 0.5, Y: float64(y) + 0.5})
		fu := (p.X - t.bbox[0]) / t.xstep
		fv := (p.Y - t.bbox[1]) / t.ystep
		fu -= math.Floor(fu)
		fv -= math.Floor(fv)
		o := tile.PixOffset(min(int(fu*float64(tw)), tw-1), min(int((1-fv)*float64(th)), th-1))
		a := float64(tile.Pix[o+3]) / 255
		if a == 0 {
			return [3]float64{}, 0
		}
		rgb := under
		if !t.uncoloured {
			rgb = [3]float64{float64(tile.Pix[o]) / 255, float64(tile.Pix[o+1]) / 255, float64(tile.Pix[o+2]) / 255}
		}
		return rgb, alpha * a
	}, true
}

// tileImage returns the pattern cell as a tw x th image on a transparent
// background, rendering it on first use. The cell is the XStep by YStep
// rectangle at the lower left of the pattern's bounding box, with the content
// of neighbouring cells drawn over it where the bounding box is larger than
// the step. It returns nil for a cell that cannot be rendered.
func (r *renderer) tileImage(pattern pdf.PDFDict, t *tiling, tw, th int, under [3]float64) *image.RGBA {
	key := tileKey{pattern: pdf.ValuePointer(pattern.Entries), w: tw, h: th}
	if t.uncoloured {
		key.rgb = under
	}
	if img, ok := r.tiles[key]; ok {
		return img
	}
	if r.depth > maxFormDepth {
		return nil
	}
	data, err := pdf.DecodeStream(pattern)
	if err != nil {
		r.drop(dropTilingPattern)
		return nil
	}
	resources, _ := pattern.Entries.Get("Resources").(pdf.PDFDict)

	tile := image.NewRGBA(image.Rect(0, 0, tw, th))
	sx, sy := float64(tw)/t.xstep, float64(th)/t.ystep
	nx := min(max(int(math.Ceil((t.bbox[2]-t.bbox[0])/t.xstep))-1, 0), maxTileOverlap)
	ny := min(max(int(math.Ceil((t.bbox[3]-t.bbox[1])/t.ystep))-1, 0), maxTileOverlap)

	canvas, base := r.canvas, r.patternBase
	r.canvas = tile
	r.depth++
	for j := -ny; j <= ny; j++ {
		for i := -nx; i <= nx; i++ {
			// Pattern space to tile pixels, for the content of the cell i, j
			// steps away: y runs up in pattern space and down in the image.
			ctm := content.Matrix{A: sx, D: -sy,
				E: (float64(i)*t.xstep - t.bbox[0]) * sx,
				F: (t.bbox[1] + t.ystep - float64(j)*t.ystep) * sy}
			r.patternBase = ctm
			gs := newRenderState(ctm, [4]float64{0, 0, float64(tw), float64(th)})
			gs.clip = intersectRect(gs.clip, deviceBounds(t.bbox[0], t.bbox[1], t.bbox[2]-t.bbox[0], t.bbox[3]-t.bbox[1], ctm, 0))
			if t.uncoloured {
				gs.Fill.RGB, gs.Stroke.RGB = under, under
			}
			r.execContent(data, resources, gs)
		}
	}
	r.depth--
	r.canvas, r.patternBase = canvas, base

	if r.tiles == nil || r.tileBytes+len(tile.Pix) > maxTileCacheBytes {
		r.tiles, r.tileBytes = map[tileKey]*image.RGBA{}, 0
	}
	r.tiles[key] = tile
	r.tileBytes += len(tile.Pix)
	return tile
}
