package gopdfrab

import (
	"context"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// Link is a clickable area of a page.
type Link struct {
	// Box is the area, on the displayed page like Glyph.Box.
	Box Rect
	// URI is the address the link opens, or empty for a link within the
	// document.
	URI string
	// Page is the zero-based index of the page an internal link goes to, or -1
	// for a link that does not go to a page of this document.
	Page int
	// Y is, for an internal link, how far down the displayed target page the
	// destination lies, in points, or 0 if the link names no position.
	Y float64
}

// Links returns the links of the page, in the order the page lists them. A link
// whose action or destination cannot be understood is left out.
func (p *Page) Links(ctx context.Context) ([]Link, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.doc.mu.Lock()
	defer p.doc.mu.Unlock()
	annots, _ := p.entry.dict.Entries.Get("Annots").(pdf.PDFArray)
	w, h := p.geom.CropBox[2]-p.geom.CropBox[0], p.geom.CropBox[3]-p.geom.CropBox[1]
	var out []Link
	for _, a := range annots {
		annot, ok := a.(pdf.PDFDict)
		if !ok {
			continue
		}
		if st, _ := annot.Entries.Get("Subtype").(pdf.PDFName); st.Value != "Link" {
			continue
		}
		rect, err := pdf.FloatArray(annot.Entries.Get("Rect"))
		if err != nil || len(rect) != 4 {
			continue
		}
		// From PDF space, origin at the lower left of the crop box, to the
		// unrotated page with its origin at the upper left.
		x0, x1 := min(rect[0], rect[2])-p.geom.CropBox[0], max(rect[0], rect[2])-p.geom.CropBox[0]
		y0, y1 := p.geom.CropBox[3]-max(rect[1], rect[3]), p.geom.CropBox[3]-min(rect[1], rect[3])
		link := Link{Box: displayRect([4]float64{x0, y0, x1, y1}, w, h, p.geom.Rotate, p.geom.UserUnit), Page: -1}
		if !p.doc.linkTarget(annot, &link) {
			continue
		}
		out = append(out, link)
	}
	return out, nil
}

// linkTarget fills in where annot goes, and reports whether it goes anywhere
// that could be understood. The caller holds d.mu.
func (d *Document) linkTarget(annot pdf.PDFDict, link *Link) bool {
	dest := annot.Entries.Get("Dest")
	if action, ok := annot.Entries.Get("A").(pdf.PDFDict); ok {
		kind, _ := action.Entries.Get("S").(pdf.PDFName)
		switch kind.Value {
		case "URI":
			link.URI = pdfText(action.Entries.Get("URI"))
			return link.URI != ""
		case "GoTo":
			dest = action.Entries.Get("D")
		default:
			return false
		}
	}
	return d.resolveDest(dest, link)
}

// resolveDest sets the page and position of link from a destination: an array
// whose first element is the page, or a name that stands for one.
func (d *Document) resolveDest(dest pdf.PDFValue, link *Link) bool {
	switch v := dest.(type) {
	case pdf.PDFName:
		return d.resolveDest(d.namedDest(v.Value), link)
	case pdf.PDFString, pdf.PDFHexString:
		return d.resolveDest(d.namedDest(pdfText(v)), link)
	case pdf.PDFDict: // a named destination may be a dictionary holding it
		return d.resolveDest(v.Entries.Get("D"), link)
	case pdf.PDFArray:
		if len(v) == 0 {
			return false
		}
		target, ok := v[0].(pdf.PDFDict)
		if !ok {
			return false
		}
		for i, e := range d.pages {
			if pdf.ValuePointer(e.dict.Entries) == pdf.ValuePointer(target.Entries) {
				link.Page, link.Y = i, destY(e.dict, v)
				return true
			}
		}
	}
	return false
}

// destY returns how far down the displayed page the destination array dest
// puts the top of the view, in points, or 0 if it names no position. Only the
// forms with a top coordinate, /XYZ and /FitH, do.
func destY(page pdf.PDFDict, dest pdf.PDFArray) float64 {
	if len(dest) < 2 {
		return 0
	}
	kind, _ := dest[1].(pdf.PDFName)
	var top pdf.PDFValue
	switch {
	case kind.Value == "XYZ" && len(dest) > 3:
		top = dest[3]
	case (kind.Value == "FitH" || kind.Value == "FitBH") && len(dest) > 2:
		top = dest[2]
	}
	y, ok := pdf.PDFNumberToFloat(top)
	if !ok {
		return 0
	}
	g := pdf.Geometry(page)
	w, h := g.CropBox[2]-g.CropBox[0], g.CropBox[3]-g.CropBox[1]
	at := g.CropBox[3] - y // from the top of the unrotated page
	return displayRect([4]float64{0, at, 0, at}, w, h, g.Rotate, g.UserUnit).Y0
}

// namedDest returns the destination called name, from the catalog's /Dests
// dictionary or its /Names /Dests name tree, or nil.
func (d *Document) namedDest(name string) pdf.PDFValue {
	if dests, ok := d.root.Entries.Get("Dests").(pdf.PDFDict); ok {
		if v := dests.Entries.Get(name); v != nil {
			return v
		}
	}
	if names, ok := d.root.Entries.Get("Names").(pdf.PDFDict); ok {
		if tree, ok := names.Entries.Get("Dests").(pdf.PDFDict); ok {
			return nameTreeLookup(tree, name, 0)
		}
	}
	return nil
}

// nameTreeLookup finds key in a name tree: a node holds its names and values
// in /Names, or children in /Kids, each covering the keys between its /Limits.
func nameTreeLookup(node pdf.PDFDict, key string, depth int) pdf.PDFValue {
	if depth > 32 {
		return nil
	}
	if pairs, ok := node.Entries.Get("Names").(pdf.PDFArray); ok {
		for i := 0; i+1 < len(pairs); i += 2 {
			if pdfText(pairs[i]) == key {
				return pairs[i+1]
			}
		}
	}
	kids, _ := node.Entries.Get("Kids").(pdf.PDFArray)
	for _, k := range kids {
		kid, ok := k.(pdf.PDFDict)
		if !ok {
			continue
		}
		if limits, ok := kid.Entries.Get("Limits").(pdf.PDFArray); ok && len(limits) == 2 {
			if key < pdfText(limits[0]) || key > pdfText(limits[1]) {
				continue
			}
		}
		if v := nameTreeLookup(kid, key, depth+1); v != nil {
			return v
		}
	}
	return nil
}

// pdfText returns the bytes of a PDF string, hex or literal, as a Go string,
// and the empty string for any other value.
func pdfText(v pdf.PDFValue) string {
	switch s := v.(type) {
	case pdf.PDFString:
		return s.Value
	case pdf.PDFHexString:
		return string(pdf.DecodePDFHexStringBytes(s.Value))
	}
	return ""
}
