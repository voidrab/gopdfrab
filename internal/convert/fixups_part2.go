package convert

import (
	"math"
	"unicode/utf8"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/writer"
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
// limits. A page whose visible area, the part of the MediaBox inside the
// CropBox, is too large is scaled down through its user unit (/UserUnit, see
// scalePage), so that it keeps its physical size and all its content. A
// MediaBox too small then grows from its lower-left corner, and one too large
// shrinks around the visible area. The other boundaries are clipped to the
// MediaBox, as a reader clips them. A CropBox that is still too small becomes
// the MediaBox; another boundary is dropped, so that it defaults to the
// CropBox.
type pageBoundaryFixer struct{ spec *pdfa.Spec }

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
	scaled := map[uintptr]float64{}
	pdf.WalkPages(pages, func(page, _ pdf.PDFDict, inherited [4]float64, _ int) {
		media := normalizedBox(inherited)
		visible := media
		if crop, ok := pageBox(page, "CropBox", true); ok {
			if c, ok := intersectBoxes(crop, media); ok {
				visible = c
			}
		}
		if k := userUnitScale(visible, hi); k > 1 {
			scalePage(page, media, k)
			scaled[pdf.ValuePointer(page.Entries)] = k
			media, visible = scaleBox(media, 1/k), scaleBox(visible, 1/k)
			changed = true
		}
		if !fits(media) {
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
	if len(scaled) > 0 {
		scaleDestinations(*trailer, scaled)
	}
	return changed, nil
}

// userUnitScale returns the smallest power of two by which a visible area
// must shrink for each of its sides to be at most hi units, or 1 when it fits.
// A power of two keeps the scaled coordinates and the inverse matrix exact.
func userUnitScale(visible [4]float64, hi float64) float64 {
	side := max(visible[2]-visible[0], visible[3]-visible[1])
	k := 1.0
	for side/k > hi {
		k *= 2
	}
	return k
}

// scalePage shrinks a page's user space by k and multiplies its user unit
// (/UserUnit) by k, so that it shows the same content at the same physical
// size. Its boundaries, its content (wrapped in a matching cm), the geometry
// of its annotations and the rectangles of its thread beads are divided by k.
// media is the MediaBox the page inherits, which it then holds itself.
func scalePage(page pdf.PDFDict, media [4]float64, k float64) {
	s := 1 / k
	page.Entries.Set("MediaBox", boxArray(scaleBox(media, s)))
	for _, key := range []string{"CropBox", "BleedBox", "TrimBox", "ArtBox"} {
		if box, ok := pageBox(page, key, key == "CropBox"); ok {
			page.Entries.Set(key, boxArray(scaleBox(box, s)))
		}
	}
	unit, ok := pdf.PDFNumberToFloat(page.Entries.Get("UserUnit"))
	if !ok || unit <= 0 {
		unit = 1
	}
	page.Entries.Set("UserUnit", pdf.RealOf(unit*k))

	if contents := page.Entries.Get("Contents"); contents != nil {
		pre, _ := writer.WriteContentStream([]writer.ContentOp{{Op: "q"}, {Op: "cm", Operands: []pdf.PDFValue{
			pdf.RealOf(s), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.RealOf(s), pdf.PDFInteger(0), pdf.PDFInteger(0),
		}}})
		post, _ := writer.WriteContentStream([]writer.ContentOp{{Op: "Q"}})
		wrapped := pdf.PDFArray{flateContentStream(pre)}
		if arr, ok := contents.(pdf.PDFArray); ok {
			wrapped = append(wrapped, arr...)
		} else {
			wrapped = append(wrapped, contents)
		}
		page.Entries.Set("Contents", append(wrapped, flateContentStream(post)))
	}

	annots, _ := page.Entries.Get("Annots").(pdf.PDFArray)
	for _, a := range annots {
		annot, ok := a.(pdf.PDFDict)
		if !ok {
			continue
		}
		for _, key := range []string{"Rect", "QuadPoints", "Vertices", "L", "CL", "RD", "InkList"} {
			if arr, ok := annot.Entries.Get(key).(pdf.PDFArray); ok {
				annot.Entries.Set(key, scaleNumbers(arr, s))
			}
		}
	}
	beads, _ := page.Entries.Get("B").(pdf.PDFArray)
	for _, b := range beads {
		if bead, ok := b.(pdf.PDFDict); ok {
			if r, ok := bead.Entries.Get("R").(pdf.PDFArray); ok {
				bead.Entries.Set("R", scaleNumbers(r, s))
			}
		}
	}
}

// flateContentStream returns a Flate-encoded content stream holding data.
func flateContentStream(data []byte) pdf.PDFDict {
	d := pdf.NewPDFDict()
	writer.SetStreamFlate(&d, data)
	return d
}

// scaleDestinations divides by its page's scale the coordinates of every
// explicit destination, [page /XYZ left top zoom] and the like, whose page was
// scaled.
func scaleDestinations(trailer pdf.PDFDict, scaled map[uintptr]float64) {
	walkGraph(trailer, map[uintptr]bool{}, nil, func(v pdf.PDFValue) (pdf.PDFValue, bool) {
		dest, ok := v.(pdf.PDFArray)
		if !ok || len(dest) < 2 {
			return nil, false
		}
		page, ok := dest[0].(pdf.PDFDict)
		if !ok {
			return nil, false
		}
		k, ok := scaled[pdf.ValuePointer(page.Entries)]
		if !ok {
			return nil, false
		}
		fit, _ := dest[1].(pdf.PDFName)
		var coords []int
		switch fit.Value {
		case "XYZ":
			coords = []int{2, 3}
		case "FitH", "FitBH", "FitV", "FitBV":
			coords = []int{2}
		case "FitR":
			coords = []int{2, 3, 4, 5}
		}
		for _, i := range coords {
			if i < len(dest) {
				if f, ok := pdf.PDFNumberToFloat(dest[i]); ok {
					dest[i] = number(f / k)
				}
			}
		}
		return nil, false
	})
}

// scaleBox returns b with every coordinate multiplied by s.
func scaleBox(b [4]float64, s float64) [4]float64 {
	return [4]float64{b[0] * s, b[1] * s, b[2] * s, b[3] * s}
}

// scaleNumbers returns arr with every number multiplied by s, and every array
// within it scaled the same way. Other values are kept.
func scaleNumbers(arr pdf.PDFArray, s float64) pdf.PDFArray {
	out := make(pdf.PDFArray, len(arr))
	for i, v := range arr {
		switch val := v.(type) {
		case pdf.PDFArray:
			out[i] = scaleNumbers(val, s)
		default:
			if f, ok := pdf.PDFNumberToFloat(val); ok {
				out[i] = number(f * s)
			} else {
				out[i] = v
			}
		}
	}
	return out
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

// boxArray returns b as a rectangle array.
func boxArray(b [4]float64) pdf.PDFArray {
	arr := make(pdf.PDFArray, 4)
	for i, f := range b {
		arr[i] = number(f)
	}
	return arr
}

// number returns f as an integer when it is integral, else as a real.
func number(f float64) pdf.PDFValue {
	if f == math.Trunc(f) && math.Abs(f) < 1<<31 {
		return pdf.PDFInteger(f)
	}
	return pdf.RealOf(f)
}
