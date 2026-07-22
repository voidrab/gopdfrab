package pdfgen

import (
	"bytes"
	"encoding/ascii85"
	"encoding/hex"
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
		classicWithASCIIHexContent(),
		classicWithASCII85Content(),
		xrefStreamOnly(),
		xrefStreamWithObjStm(),
		incrementalUpdate(),
		encryptedClassicRC4(),
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

// classicWithASCIIHexContent exercises the ASCIIHexDecode filter path.
func classicWithASCIIHexContent() []byte {
	raw := []byte("q Q")
	encoded := []byte(hex.EncodeToString(raw) + ">")
	b := NewBuilder("%PDF-1.7\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<< /Filter /ASCIIHexDecode", encoded)
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

// classicWithASCII85Content exercises the ASCII85Decode filter path.
func classicWithASCII85Content() []byte {
	raw := []byte("q Q")
	var enc bytes.Buffer
	w := ascii85.NewEncoder(&enc)
	w.Write(raw)
	w.Close()
	enc.WriteString("~>")
	b := NewBuilder("%PDF-1.7\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<< /Filter /ASCII85Decode", enc.Bytes())
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

// incrementalUpdate builds a classic-xref PDF followed by an incremental update
// section: object 3 is re-written and a second xref table (chained to the first
// via /Prev) is appended. This exercises the trailer /Prev chain and
// last-revision-wins resolution (followXRefPrevChain).
func incrementalUpdate() []byte {
	var buf bytes.Buffer
	off := func() int { return buf.Len() }

	buf.WriteString("%PDF-1.7\n" + binaryMarker)
	o1 := off()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	o2 := off()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	o3 := off()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> >>\nendobj\n")

	xref1 := off()
	fmt.Fprintf(&buf, "xref\n0 4\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \n", o1, o2, o3)
	buf.WriteString("trailer\n<< /Size 4 /Root 1 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xref1)

	// Incremental update: rewrite object 3 with a larger MediaBox.
	o3b := off()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 400 400] /Resources << >> >>\nendobj\n")

	xref2 := off()
	fmt.Fprintf(&buf, "xref\n3 1\n%010d 00000 n \n", o3b)
	fmt.Fprintf(&buf, "trailer\n<< /Size 4 /Root 1 0 R /Prev %d >>\n", xref1)
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF", xref2)

	return buf.Bytes()
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
