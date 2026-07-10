package pdfgen

import (
	"bytes"
	"fmt"
)

// binaryMarker is the conventional second header line whose high bytes mark the
// file as binary (ISO 32000 7.5.2).
const binaryMarker = "%\xc2\xb5\xc2\xb6\n"

// Seeds returns a fresh set of structurally-valid PDF documents covering the
// distinct read paths the parser supports: classic cross-reference tables,
// cross-reference streams, object streams, and a metadata-carrying document.
// They serve both as valid baselines and as the bases the corruptors mutate
// into broken inputs. Each call returns freshly-allocated byte slices so
// callers may mutate them in place.
func Seeds() [][]byte {
	return [][]byte{
		minimalClassic(),
		classicWithMetadata(),
		classicWithFlateContent(),
		xrefStreamOnly(),
		xrefStreamWithObjStm(),
	}
}

// minimalClassic is the smallest structurally valid one-page PDF using a
// classic cross-reference table (cf. the former test-only createValidPDF /
// writeMinimalPDF helpers).
func minimalClassic() []byte {
	b := NewBuilder("%PDF-1.7\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte("q\nQ\n"))
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

// classicWithMetadata carries an Info dictionary and an XMP /Metadata stream,
// exercising the metadata and claimed-conformance read paths.
func classicWithMetadata() []byte {
	const xmp = `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>` +
		`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF ` +
		`xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about="" ` +
		`xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">` +
		`<pdfaid:part>1</pdfaid:part><pdfaid:conformance>B</pdfaid:conformance>` +
		`</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`

	b := NewBuilder("%PDF-1.7\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /Metadata 5 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte("q\nQ\n"))
	b.StreamObj(5, "<< /Type /Metadata /Subtype /XML", []byte(xmp))
	b.Obj(6, "<< /Title (Fuzz Seed) /Producer (gopdfrab pdfgen) >>")
	return b.FinishClassic("<< /Size 7 /Root 1 0 R /Info 6 0 R >>")
}

// classicWithFlateContent uses a FlateDecode-compressed content stream so the
// filter/stream-decoding path is present in the seed corpus.
func classicWithFlateContent() []byte {
	content := deflate([]byte("q 1 0 0 1 20 20 cm BT /F1 12 Tf (hi) Tj ET Q"))
	b := NewBuilder("%PDF-1.7\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<< /Filter /FlateDecode", content)
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

// xrefStreamOnly builds a one-page PDF whose cross-reference section is a PDF
// 1.5+ cross-reference stream (self-referential object 5) with no literal
// "trailer" keyword, ported from the former buildXRefStreamOnlyPDF test helper.
func xrefStreamOnly() []byte {
	b := NewBuilder("%PDF-1.5\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte("q\nQ\n"))

	const w0, w1, w2 = 1, 4, 1
	var raw bytes.Buffer
	raw.Write(beField(0, w0+w1+w2)) // object 0: free
	for objNum := 1; objNum <= 4; objNum++ {
		raw.Write([]byte{1})
		raw.Write(beField(int(b.OffsetOf(objNum)), w1))
		raw.Write([]byte{0})
	}
	xrefOffset := b.Len()
	raw.Write([]byte{1})
	raw.Write(beField(int(xrefOffset), w1))
	raw.Write([]byte{0})

	dictHead := fmt.Sprintf("<< /Type /XRef /Size 6 /W [%d %d %d] /Root 1 0 R /Filter /FlateDecode", w0, w1, w2)
	b.StreamObj(5, dictHead, deflate(raw.Bytes()))
	return b.FinishStartxref(xrefOffset)
}

// xrefStreamWithObjStm packs the Catalog, Pages and Page dicts into a
// compressed object stream (object 6), referenced by a cross-reference stream
// (object 7), ported from the former buildXRefStreamWithObjStmPDF test helper.
func xrefStreamWithObjStm() []byte {
	b := NewBuilder("%PDF-1.5\n" + binaryMarker)

	obj1 := "<< /Type /Catalog /Pages 2 0 R >>"
	obj2 := "<< /Type /Pages /Kids [3 0 R] /Count 1 >>"
	obj3 := "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>"

	off1 := 0
	off2 := off1 + len(obj1) + 1
	off3 := off2 + len(obj2) + 1
	header := fmt.Sprintf("1 %d 2 %d 3 %d ", off1, off2, off3)
	objStmData := header + obj1 + " " + obj2 + " " + obj3

	b.StreamObj(6, fmt.Sprintf("<< /Type /ObjStm /N 3 /First %d /Filter /FlateDecode", len(header)),
		deflate([]byte(objStmData)))
	b.StreamObj(4, "<<", []byte("q\nQ\n"))

	const w0, w1, w2 = 1, 4, 1
	var raw bytes.Buffer
	raw.Write(beField(0, w0+w1+w2)) // object 0: free
	writeCompressed := func(idx int) {
		raw.Write([]byte{2})
		raw.Write(beField(6, w1))
		raw.Write([]byte{byte(idx)})
	}
	writeCompressed(0) // object 1
	writeCompressed(1) // object 2
	writeCompressed(2) // object 3
	writeClassic := func(num int) {
		raw.Write([]byte{1})
		raw.Write(beField(int(b.OffsetOf(num)), w1))
		raw.Write([]byte{0})
	}
	writeClassic(4)
	writeClassic(6)

	xrefOffset := b.Len()
	raw.Write([]byte{1}) // object 7: itself
	raw.Write(beField(int(xrefOffset), w1))
	raw.Write([]byte{0})

	dictHead := fmt.Sprintf("<< /Type /XRef /Size 8 /W [%d %d %d] /Index [0 8] /Root 1 0 R /Filter /FlateDecode", w0, w1, w2)
	b.StreamObj(7, dictHead, deflate(raw.Bytes()))
	return b.FinishStartxref(xrefOffset)
}
