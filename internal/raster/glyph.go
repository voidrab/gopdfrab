package raster

import (
	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/fontprog"
)

// GlyphPath is a glyph outline flattened to straight-line contours, scaled
// to a 1000-unit em (matching ttAdvanceWidth's PDF-units convention).
type GlyphPath struct {
	Contours [][]content.Point
}

// glyphOutlineFromTrueType extracts gid's outline from a parsed TrueType
// sfnt's glyf/loca tables, scaled from font units to a 1000-unit em.
func glyphOutlineFromTrueType(tables map[string][]byte, gid int) (GlyphPath, bool) {
	upm, ok := fontprog.UnitsPerEm(tables)
	if !ok {
		return GlyphPath{}, false
	}
	var s contourSink
	if !fontprog.TrueTypeOutline(tables, gid, &s) {
		return GlyphPath{}, false
	}
	path := s.glyphPath()
	scale := 1000.0 / float64(upm)
	for _, c := range path.Contours {
		for i := range c {
			c[i].X *= scale
			c[i].Y *= scale
		}
	}
	return path, true
}

// contourSink flattens a glyph outline into polygon contours.
type contourSink struct {
	contours [][]content.Point
	cur      []content.Point
	at       content.Point
}

// glyphPath closes the open contour and returns the outline drawn.
func (s *contourSink) glyphPath() GlyphPath {
	s.closeContour()
	return GlyphPath{Contours: s.contours}
}

func (s *contourSink) closeContour() {
	if len(s.cur) > 1 {
		s.contours = append(s.contours, s.cur)
	}
	s.cur = nil
}

func (s *contourSink) MoveTo(x, y float64) {
	s.closeContour()
	s.at = content.Point{X: x, Y: y}
	s.cur = []content.Point{s.at}
}

func (s *contourSink) LineTo(x, y float64) {
	s.at = content.Point{X: x, Y: y}
	s.cur = append(s.cur, s.at)
}

func (s *contourSink) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	p3 := content.Point{X: x3, Y: y3}
	s.cur = append(s.cur, flattenCubic(s.at, content.Point{X: x1, Y: y1}, content.Point{X: x2, Y: y2}, p3, 1.0)...)
	s.at = p3
}

// cffOutline returns glyph gid's outline in prog.
func cffOutline(prog *fontprog.CFFCharstrings, gid int) (GlyphPath, bool) {
	var s contourSink
	if !prog.Outline(gid, &s) {
		return GlyphPath{}, false
	}
	return s.glyphPath(), true
}

// type1Outline returns the named glyph's outline in prog.
func type1Outline(prog *fontprog.Type1Charstrings, name string) (GlyphPath, bool) {
	var s contourSink
	if !prog.Outline(name, &s) {
		return GlyphPath{}, false
	}
	return s.glyphPath(), true
}
