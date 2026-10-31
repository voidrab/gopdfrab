package convert

import "github.com/voidrab/gopdfrab/internal/pdf"

// pageTarget addresses a page dict in the graph together with the /Resources
// and /MediaBox in effect for it (resolved up the Pages tree), in page order.
type pageTarget struct {
	dict      pdf.PDFDict
	resources pdf.PDFDict
	mediaBox  [4]float64
}

// orderedPages returns every page in the document in page order, with its
// inherited resources and resolved media box, using the same top-down
// Root/Pages/Kids walk the verifier numbers pages by -- so the slice index
// matches a PDFError's 1-based Page().
func orderedPages(trailer pdf.PDFDict) []pageTarget {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return nil
	}
	pages, ok := root.Entries.Get("Pages").(pdf.PDFDict)
	if !ok {
		return nil
	}

	var out []pageTarget
	pdf.WalkPages(pages, func(node, resources pdf.PDFDict, mediaBox [4]float64, _ int) {
		out = append(out, pageTarget{dict: node, resources: resources, mediaBox: mediaBox})
	})
	return out
}
