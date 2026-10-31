// Package raster renders PDF page and Form XObject content to RGBA images.
//
// It is a complete content-stream renderer: paths, images (including inline
// images and soft masks), shadings and mesh shadings, shading patterns,
// Type 3 fonts, and embedded TrueType, CFF and Type 1 glyph outlines. The
// converter uses it as its last resort: content that cannot be repaired is
// drawn here and embedded as an image, so the render has to match the
// original page. The converter's fidelity gate also renders input and output
// with it to catch pages that a repair blanked.
//
// What the renderer cannot draw it does not guess at: each render returns
// the operators and resources it dropped, so callers can report the loss.
package raster
