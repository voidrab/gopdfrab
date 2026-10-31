package convert

import (
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// --- ISO 19005-2 6.1.8: names that must be UTF-8 ---

// unicodeNameFixer re-encodes the font, colorant and structure type names that
// are not valid UTF-8. Every byte that is not part of a valid UTF-8 sequence
// is read as Latin-1, so equal names stay equal and a structure type keeps
// matching its role map entry.
type unicodeNameFixer struct{}

func (unicodeNameFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.NameNotUTF8
}

func (unicodeNameFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	fix := func(d pdf.PDFDict, key string) {
		if n, ok := d.Entries.Get(key).(pdf.PDFName); ok {
			if u, ok := utf8Name(n); ok {
				d.Entries.Set(key, u)
				changed = true
			}
		}
	}
	visit := func(d pdf.PDFDict) bool {
		switch d.Entries.Get("Type") {
		case pdf.PDFName{Value: "Font"}:
			fix(d, "BaseFont")
		case pdf.PDFName{Value: "FontDescriptor"}:
			fix(d, "FontName")
		case pdf.PDFName{Value: "StructTreeRoot"}:
			if roleMap, ok := d.Entries.Get("RoleMap").(pdf.PDFDict); ok && renameUTF8Keys(roleMap, true) {
				changed = true
			}
		case pdf.PDFName{Value: "StructElem"}:
			fix(d, "S")
		case nil:
			if _, ok := d.Entries.Get("P").(pdf.PDFDict); ok {
				fix(d, "S")
			}
		}
		if colorants, ok := d.Entries.Get("Colorants").(pdf.PDFDict); ok && renameUTF8Keys(colorants, false) {
			changed = true
		}
		return true
	}
	edit := func(v pdf.PDFValue) (pdf.PDFValue, bool) {
		if arr, ok := v.(pdf.PDFArray); ok && fixColorantNames(arr) {
			changed = true
		}
		return nil, false
	}
	walkGraph(*trailer, map[uintptr]bool{}, visit, edit)
	return changed, nil
}

// fixColorantNames re-encodes the colorant names of a Separation or DeviceN
// colour space array in place.
func fixColorantNames(arr pdf.PDFArray) bool {
	if len(arr) < 2 {
		return false
	}
	changed := false
	switch arr[0] {
	case pdf.PDFName{Value: "Separation"}:
		if n, ok := arr[1].(pdf.PDFName); ok {
			if u, ok := utf8Name(n); ok {
				arr[1], changed = u, true
			}
		}
	case pdf.PDFName{Value: "DeviceN"}:
		names, _ := arr[1].(pdf.PDFArray)
		for i, v := range names {
			if n, ok := v.(pdf.PDFName); ok {
				if u, ok := utf8Name(n); ok {
					names[i], changed = u, true
				}
			}
		}
	}
	return changed
}

// renameUTF8Keys re-encodes the keys of d that are not valid UTF-8, and, if
// values is set, its name values too.
func renameUTF8Keys(d pdf.PDFDict, values bool) bool {
	type rename struct{ from, to string }
	var renames []rename
	changed := false
	for k, v := range d.Entries.All() {
		if u, ok := utf8Name(pdf.PDFName{Value: k}); ok {
			renames = append(renames, rename{k, u.Value})
		}
		if n, ok := v.(pdf.PDFName); ok && values {
			if u, ok := utf8Name(n); ok {
				d.Entries.Set(k, u)
				changed = true
			}
		}
	}
	for _, r := range renames {
		if _, taken := d.Entries.Lookup(r.to); !taken {
			d.Entries.Set(r.to, d.Entries.Get(r.from))
		}
		d.Entries.Del(r.from)
		changed = true
	}
	return changed
}

// utf8Name returns n re-encoded as valid UTF-8, reading each byte outside a
// valid UTF-8 sequence as Latin-1. ok is false when n is valid already.
func utf8Name(n pdf.PDFName) (_ pdf.PDFName, ok bool) {
	raw := pdf.DecodePDFName(n.Value)
	if utf8.Valid(raw) {
		return n, false
	}
	out := make([]byte, 0, len(raw)+4)
	for len(raw) > 0 {
		r, size := utf8.DecodeRune(raw)
		if r == utf8.RuneError && size == 1 {
			r = rune(raw[0])
		}
		out = utf8.AppendRune(out, r)
		raw = raw[size:]
	}
	return pdf.PDFName{Value: pdf.EncodePDFName(out)}, true
}

// --- ISO 19005-2 6.1.12: permissions ---

// permissionsFixer removes the permissions other than UR3 and DocMDP, and the
// digest entries of signature references in a document with DocMDP
// permissions. Each repair runs only for its reported check.
type permissionsFixer struct{}

func (permissionsFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.PermissionsKeys || c == pdf.Checks.Structure.SignatureReferenceDigest
}

func (permissionsFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	reported := reportedChecks(issues)
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	perms, ok := root.Entries.Get("Perms").(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	changed := false
	if reported[pdf.Checks.Structure.PermissionsKeys] {
		perms.Entries.DeleteFunc(func(k string, _ pdf.PDFValue) bool {
			drop := k != "UR3" && k != "DocMDP"
			changed = changed || drop
			return drop
		})
	}
	if reported[pdf.Checks.Structure.SignatureReferenceDigest] {
		walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
			refs, ok := d.Entries.Get("Reference").(pdf.PDFArray)
			if !ok || d.Entries.Get("Type") != (pdf.PDFName{Value: "Sig"}) && d.Entries.Get("ByteRange") == nil {
				return
			}
			for _, r := range refs {
				ref, ok := r.(pdf.PDFDict)
				if !ok {
					continue
				}
				for _, k := range []string{"DigestLocation", "DigestMethod", "DigestValue"} {
					if ref.Entries.Get(k) != nil {
						ref.Entries.Del(k)
						changed = true
					}
				}
			}
		})
	}
	return changed, nil
}

// --- ISO 19005-2 6.1.13: page boundary sizes ---

// pageBoundaryFixer brings each page boundary within the level's size
// limits. A MediaBox too small grows from its lower-left corner; one too
// large shrinks around the visible area, the part inside the CropBox, so
// nothing visible is lost unless that area is itself too large, which is
// reported as lost content. The other boundaries are clipped to the
// MediaBox, as a reader clips them. A CropBox that is still too small
// becomes the MediaBox; another boundary is dropped, so that it defaults to
// the CropBox.
type pageBoundaryFixer struct {
	spec *pdfa.Spec
	lost *[]pdf.PDFError
}

func (pageBoundaryFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.PageBoundarySize
}

func (f pageBoundaryFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	lo, hi := f.spec.Limits.MinPageBoundary, f.spec.Limits.MaxPageBoundary
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	pages, ok := root.Entries.Get("Pages").(pdf.PDFDict)
	if hi == 0 || !ok {
		return false, nil
	}
	fits := func(b [4]float64) bool {
		w, h := b[2]-b[0], b[3]-b[1]
		return w >= lo && w <= hi && h >= lo && h <= hi
	}
	changed := false
	pdf.WalkPages(pages, func(page, _ pdf.PDFDict, inherited [4]float64, pageNum int) {
		media := normalizedBox(inherited)
		crop, hasCrop := pageBox(page, "CropBox", true)
		if !fits(media) {
			visible := media
			if hasCrop {
				if c, ok := intersectBoxes(crop, media); ok {
					visible = c
				}
			}
			if visible[2]-visible[0] > hi || visible[3]-visible[1] > hi {
				reportLost(f.lost, pdf.Checks.Structure.PageBoundarySize, page, fmt.Sprintf(
					"page %d: the visible area is larger than %g units, and is cropped to fit", pageNum, hi))
			}
			media = fitBox(media, visible, lo, hi)
			page.Entries.Set("MediaBox", boxArray(media))
			changed = true
		}
		for _, key := range []string{"CropBox", "BleedBox", "TrimBox", "ArtBox"} {
			box, ok := pageBox(page, key, key == "CropBox")
			if !ok || fits(box) {
				continue
			}
			switch clipped, ok := intersectBoxes(box, media); {
			case ok && fits(clipped):
				page.Entries.Set(key, boxArray(clipped))
			case key == "CropBox":
				// The page may inherit its CropBox, so set the default
				// rather than delete it.
				page.Entries.Set(key, boxArray(media))
			default:
				page.Entries.Del(key)
			}
			changed = true
		}
	})
	return changed, nil
}

// pageBox returns a page's boundary key, normalized, or the value it
// inherits when inherited is set. ok is false when there is none.
func pageBox(page pdf.PDFDict, key string, inherited bool) (_ [4]float64, ok bool) {
	v := page.Entries.Get(key)
	for node := page; v == nil && inherited; {
		parent, ok := node.Entries.Get("Parent").(pdf.PDFDict)
		if !ok {
			break
		}
		v, node = parent.Entries.Get(key), parent
	}
	r, err := pdf.FloatArray(v)
	if err != nil || len(r) != 4 {
		return [4]float64{}, false
	}
	return normalizedBox([4]float64{r[0], r[1], r[2], r[3]}), true
}

// normalizedBox returns b with its lower-left corner first.
func normalizedBox(b [4]float64) [4]float64 {
	return [4]float64{min(b[0], b[2]), min(b[1], b[3]), max(b[0], b[2]), max(b[1], b[3])}
}

// intersectBoxes returns the intersection of two normalized boxes. ok is
// false when they do not overlap.
func intersectBoxes(a, b [4]float64) (_ [4]float64, ok bool) {
	r := [4]float64{max(a[0], b[0]), max(a[1], b[1]), min(a[2], b[2]), min(a[3], b[3])}
	return r, r[0] < r[2] && r[1] < r[3]
}

// fitBox returns a box, within media where the limits allow, that holds as
// much of visible as fits in sides of lo to hi units.
func fitBox(media, visible [4]float64, lo, hi float64) [4]float64 {
	var out [4]float64
	for axis := range 2 {
		start, end := visible[axis], visible[axis+2]
		if media[axis+2]-media[axis] <= hi {
			start, end = media[axis], media[axis+2]
		}
		end = min(end, start+hi)
		if end-start < lo {
			end = start + lo
		}
		out[axis], out[axis+2] = start, end
	}
	return out
}

// boxArray returns b as a rectangle array, with integral coordinates written
// as integers.
func boxArray(b [4]float64) pdf.PDFArray {
	arr := make(pdf.PDFArray, 4)
	for i, f := range b {
		if f == math.Trunc(f) && math.Abs(f) < 1<<31 {
			arr[i] = pdf.PDFInteger(f)
		} else {
			arr[i] = pdf.PDFReal(f)
		}
	}
	return arr
}
