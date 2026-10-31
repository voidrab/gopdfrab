package pdf

// ContentKind is the kind of object that holds a content stream.
type ContentKind uint8

const (
	// ContentNone is a dictionary that holds no content stream.
	ContentNone ContentKind = iota
	// ContentPage is a page, whose /Contents is a stream or an array of them.
	ContentPage
	// ContentPattern is a tiling pattern, which is its own content stream.
	ContentPattern
	// ContentForm is a form XObject, which is its own content stream.
	ContentForm
	// ContentType3 is a Type 3 font, whose /CharProcs holds one stream per
	// glyph.
	ContentType3
)

// ContentRoot is one content stream that a dictionary holds, with the
// resources its names resolve against.
type ContentRoot struct {
	Kind      ContentKind
	Stream    PDFDict
	Resources PDFDict

	// Where Stream was found, for Replace: an array element, or a dictionary
	// entry. Both are unset for a pattern or form, which is the stream.
	arr   PDFArray
	index int
	dict  PDFDict
	key   string
}

// Replace stores s where the root's stream was found. It does nothing for a
// pattern or form, whose stream is the dictionary itself and is changed in
// place.
func (r ContentRoot) Replace(s PDFDict) {
	switch {
	case r.arr != nil:
		r.arr[r.index] = s
	case r.dict.Entries != nil:
		r.dict.Entries.Set(r.key, s)
	}
}

// ContentRoots calls fn for every content stream that d itself holds, and
// returns the kind of object d is: a page (each stream of its /Contents, with
// the resources it inherits), a tiling pattern or form XObject (d, with its own
// /Resources), or a Type 3 font (each glyph procedure, with the font's
// /Resources). Entries that are not streams are skipped. The streams a
// dictionary reaches only through its resources are not its own, so a walk
// over the whole graph reaches every content stream exactly once per holder.
func ContentRoots(d PDFDict, fn func(ContentRoot)) ContentKind {
	switch {
	case d.Entries.Get("Type") == PDFName{Value: "Page"}:
		res := PageResources(d)
		switch contents := d.Entries.Get("Contents").(type) {
		case PDFDict:
			if contents.IsStream() {
				fn(ContentRoot{Kind: ContentPage, Stream: contents, Resources: res, dict: d, key: "Contents"})
			}
		case PDFArray:
			for i, item := range contents {
				if s, ok := item.(PDFDict); ok && s.IsStream() {
					fn(ContentRoot{Kind: ContentPage, Stream: s, Resources: res, arr: contents, index: i})
				}
			}
		}
		return ContentPage

	case d.Entries.Get("PatternType") == PDFInteger(1) && d.IsStream():
		res, _ := d.Entries.Get("Resources").(PDFDict)
		fn(ContentRoot{Kind: ContentPattern, Stream: d, Resources: res})
		return ContentPattern

	case d.Entries.Get("Subtype") == PDFName{Value: "Form"} && d.IsStream():
		res, _ := d.Entries.Get("Resources").(PDFDict)
		fn(ContentRoot{Kind: ContentForm, Stream: d, Resources: res})
		return ContentForm

	case d.Entries.Get("Subtype") == PDFName{Value: "Type3"}:
		res, _ := d.Entries.Get("Resources").(PDFDict)
		if procs, ok := d.Entries.Get("CharProcs").(PDFDict); ok {
			for name, v := range procs.Entries.All() {
				if s, ok := v.(PDFDict); ok && s.IsStream() {
					fn(ContentRoot{Kind: ContentType3, Stream: s, Resources: res, dict: procs, key: name})
				}
			}
		}
		return ContentType3
	}
	return ContentNone
}
