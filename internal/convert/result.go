package convert

import (
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/voidrab/gopdfrab/internal/fidelity"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// ConvertResult implements io.WriterTo.
var _ io.WriterTo = ConvertResult{}

// ConvertResult is a conversion's output, the verdict on that output, and what
// the conversion could not carry over.
type ConvertResult struct {
	// backing holds the converted PDF -- in memory when small, in a temp file
	// when large (see spillWriter). It is a pointer so value copies of a
	// ConvertResult (as FileResult stores) share one backing and a single
	// idempotent Close. Read it via Output/WriteTo/Save.
	backing *outputBacking
	// Result is the verdict on the output, from a final verify pass against
	// the profile that was converted to.
	Result pdf.Result
	// Iterations is how many verify-and-fix rounds the conversion took.
	Iterations int
	// Fidelity is the per-page input-vs-output rendering comparison, populated
	// only when Options.CheckFidelity was set. See PageFidelity.
	Fidelity []fidelity.PageFidelity
	// BlankedPages lists, in ascending order, the 1-based pages that had visible
	// content on the way in and almost none on the way out. It is the one loss a
	// conformance verdict cannot see: the output is valid PDF/A, it is simply no
	// longer the document. Populated only when Options.CheckFidelity was set,
	// since finding it means rendering both sides.
	BlankedPages []int
	// OverpaintedPages lists, in ascending order, the 1-based pages that came
	// out with a large part of them covered that was paper on the way in. A
	// conversion should never draw over a page, so a page here has had
	// something invisible made opaque on top of what was already there.
	// Populated only when Options.CheckFidelity was set.
	OverpaintedPages []int
	// RasterDrops records content the raster fallback could not render when it
	// flattened a page or a transparency group (an unusable shading, an
	// undecodable inline image, a JPEG 2000 or JBIG2 image painted as a gray
	// box, a missing Type 3 glyph, a tiling pattern), so a
	// rasterized conversion does not silently lose it. Empty when nothing was
	// dropped -- including when a page was rasterized losslessly, which
	// RasterizedPages reports instead.
	RasterDrops []RasterDrop
	// RasterizedPages lists, in ascending order, the 1-based pages the raster
	// fallback rebuilt as a flat image. A page here converted only by being
	// rasterized, whether or not anything was dropped doing it, so it stopped
	// being text and vector content.
	RasterizedPages []int
	// LostObjects lists content the conversion could not carry over: an object
	// the reader could not resolve, a stream nothing can decode, an embedded
	// file or annotation the level forbids, the content of an optional content
	// layer the document hides. Each entry says what was lost and where it
	// was. The output is still conformant -- that is what Result reports --
	// but it no longer holds all of the input, so a caller who must not lose
	// anything checks this list.
	LostObjects []pdf.PDFError
}

// addRasterizedPages merges pages into RasterizedPages, kept sorted and
// deduped since the page-level fallback and the whole-document one can both
// rebuild the same page.
func (r *ConvertResult) addRasterizedPages(pages []int) {
	seen := make(map[int]bool, len(r.RasterizedPages)+len(pages))
	merged := make([]int, 0, len(r.RasterizedPages)+len(pages))
	for _, p := range append(append([]int(nil), r.RasterizedPages...), pages...) {
		if !seen[p] {
			seen[p] = true
			merged = append(merged, p)
		}
	}
	sort.Ints(merged)
	r.RasterizedPages = merged
}

// RasterDrop lists the content features the raster fallback dropped on one page.
type RasterDrop struct {
	// Page is the 1-based page the content was dropped from.
	Page int
	// Features names what could not be drawn, one entry per feature.
	Features []string
}

// Residual returns the issues remaining in the output that Convert was unable
// to fix automatically.
func (r ConvertResult) Residual() []pdf.PDFError {
	return r.Result.Issues
}

// Output returns the converted PDF bytes. A large output spilled to a temp file
// is read back here, so prefer WriteTo or Save, which stream without
// materializing a second copy. It errors if there is no output -- only on a
// ConvertResult whose Convert call itself returned an error -- or if a spill
// file cannot be read.
func (r ConvertResult) Output() ([]byte, error) {
	return r.backing.bytes()
}

// WriteTo streams the converted PDF to w, implementing io.WriterTo, and returns
// the number of bytes written. It errors if there is no output, which only
// happens on a ConvertResult whose Convert call itself returned an error.
func (r ConvertResult) WriteTo(w io.Writer) (int64, error) {
	return r.backing.writeTo(w)
}

// Save streams the converted PDF to the given path. It returns an error if
// there is no output to save or the file cannot be written.
//
// The write is atomic: the output goes to a temp file in the same directory,
// which is synced and renamed over path only once complete. A failed Save
// leaves an existing file untouched -- which matters when path is the input
// being converted. An existing file keeps its permissions; a new one gets
// 0644. A symlink at path is written through, not replaced.
func (r ConvertResult) Save(path string) (err error) {
	if r.backing.len() == 0 {
		return errNoOutput
	}
	mode := os.FileMode(0o644)
	if target, lerr := filepath.EvalSymlinks(path); lerr == nil {
		path = target
		if info, serr := os.Stat(path); serr == nil {
			mode = info.Mode().Perm()
		}
	}
	f, err := createSaveFile(filepath.Dir(path), ".gopdfrab-save-*.pdf")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err = r.backing.writeTo(f); err != nil {
		return err
	}
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// saveFile is the slice of *os.File Save uses on its temp file.
type saveFile interface {
	tempFile
	Chmod(mode os.FileMode) error
	Sync() error
}

// createSaveFile creates Save's temp file. A var so tests can inject a chmod,
// sync or close failure, which a real file system cannot be made to produce
// portably.
var createSaveFile = func(dir, pattern string) (saveFile, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Close releases the converted output, removing the spill temp file if one was
// created and dropping any in-memory bytes. It is safe on the zero value and on
// repeated calls. Callers own Close for results from Convert and ConvertAll;
// ConvertEach closes each result after its callback returns.
//
// Output, WriteTo and Save may run concurrently with each other -- each opens
// the spill file separately, sharing no read offset -- but not with Close,
// which releases what they read.
func (r ConvertResult) Close() error {
	return r.backing.close()
}
