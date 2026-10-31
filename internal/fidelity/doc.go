// Package fidelity compares the rendered appearance of a converted document
// against its input, page by page, using gopdfrab's own rasterizer on both
// sides. Because the same renderer draws both, its operator-coverage gaps are
// symmetric and cancel, so the comparison isolates what the conversion changed
// rather than what the renderer cannot draw. Its purpose is to catch the
// destructive failure a conformance-only metric misses: a page silently
// blanked or gutted while the result still verifies clean.
package fidelity
