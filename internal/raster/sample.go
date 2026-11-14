package raster

import (
	"image"
	"math"
)

// sampler reads an image at arbitrary positions with filtering: bilinear
// interpolation when the image is enlarged, and, when it is shrunk, a
// bilinear read from a box-filtered half-size pyramid level, so that a scan
// shown at a fraction of its resolution averages its pixels instead of
// picking one of them. Colours are straight (not premultiplied) in and out;
// filtering happens on premultiplied values so transparent pixels do not
// darken or tint their neighbours.
type sampler struct {
	levels []*image.RGBA // levels[0] is the source; each next one is half-size
}

// newSampler returns a sampler over img, which it does not modify.
func newSampler(img *image.RGBA) *sampler { return &sampler{levels: []*image.RGBA{img}} }

// levelFor returns the pyramid level whose pixels are closest in size to a
// device pixel, given how many source pixels one device pixel spans.
func (s *sampler) levelFor(footprint float64) int {
	if !(footprint > 1) { // also catches NaN
		return 0
	}
	level := int(math.Floor(math.Log2(footprint)))
	for len(s.levels) <= level {
		prev := s.levels[len(s.levels)-1]
		if prev.Rect.Dx() <= 1 && prev.Rect.Dy() <= 1 {
			return len(s.levels) - 1
		}
		s.levels = append(s.levels, halve(prev))
	}
	return level
}

// halve returns img shrunk by two in each direction by averaging 2x2 blocks,
// premultiplied, rounding odd sizes up and clamping at the edges.
func halve(img *image.RGBA) *image.RGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	nw, nh := (w+1)/2, (h+1)/2
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := range nh {
		for x := range nw {
			var r, g, b, a float32
			for dy := range 2 {
				for dx := range 2 {
					px, py := min(2*x+dx, w-1), min(2*y+dy, h-1)
					o := img.PixOffset(img.Rect.Min.X+px, img.Rect.Min.Y+py)
					pa := float32(img.Pix[o+3])
					r += float32(img.Pix[o]) * pa
					g += float32(img.Pix[o+1]) * pa
					b += float32(img.Pix[o+2]) * pa
					a += pa
				}
			}
			oo := out.PixOffset(x, y)
			if a > 0 {
				out.Pix[oo] = uint8(r/a + 0.5)
				out.Pix[oo+1] = uint8(g/a + 0.5)
				out.Pix[oo+2] = uint8(b/a + 0.5)
			}
			out.Pix[oo+3] = uint8(a/4 + 0.5)
		}
	}
	return out
}

// at returns the straight RGB colour, each 0..1, and the alpha, 0..1, at the
// unit-square position (u, v), v measured down from the top, read from the
// given level. With smooth false the nearest pixel is returned as it is, so
// an enlarged image keeps its hard pixel edges. Positions outside the square
// clamp to its edge.
func (s *sampler) at(level int, u, v float64, smooth bool) (rgb [3]float64, alpha float64) {
	img := s.levels[level]
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if !smooth {
		o := img.PixOffset(img.Rect.Min.X+clampPix(int(math.Floor(u*float64(w))), w), img.Rect.Min.Y+clampPix(int(math.Floor(v*float64(h))), h))
		return [3]float64{float64(img.Pix[o]) / 255, float64(img.Pix[o+1]) / 255, float64(img.Pix[o+2]) / 255}, float64(img.Pix[o+3]) / 255
	}
	x, y := u*float64(w)-0.5, v*float64(h)-0.5
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := x-x0, y-y0
	var sum [4]float64 // premultiplied r, g, b and alpha
	for i, wt := range [4]float64{(1 - fx) * (1 - fy), fx * (1 - fy), (1 - fx) * fy, fx * fy} {
		px := clampPix(int(x0)+i%2, w)
		py := clampPix(int(y0)+i/2, h)
		o := img.PixOffset(img.Rect.Min.X+px, img.Rect.Min.Y+py)
		a := float64(img.Pix[o+3]) / 255
		sum[0] += wt * float64(img.Pix[o]) / 255 * a
		sum[1] += wt * float64(img.Pix[o+1]) / 255 * a
		sum[2] += wt * float64(img.Pix[o+2]) / 255 * a
		sum[3] += wt * a
	}
	if sum[3] <= 0 {
		return rgb, 0
	}
	return [3]float64{sum[0] / sum[3], sum[1] / sum[3], sum[2] / sum[3]}, sum[3]
}

// clampPix limits i to a pixel index of an image n pixels across.
func clampPix(i, n int) int { return max(0, min(i, n-1)) }
