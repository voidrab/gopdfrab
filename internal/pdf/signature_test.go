package pdf

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// sigDoc returns a document whose object 2 is sigBody, followed by tail
// after its final %%EOF, and the ByteRange that covers the document's one
// revision, computed from the first "<abcd>" in sigBody, the Contents string.
func sigDoc(t *testing.T, sigBody, tail string) (*Reader, [4]int64) {
	t.Helper()
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 3 0 R >>")
	b.Obj(2, sigBody)
	b.Obj(3, "<< /Type /Pages /Count 0 /Kids [] >>")
	data := append(bytes.Clone(b.FinishClassic("<< /Size 4 /Root 1 0 R >>")), tail...)
	start := int64(bytes.Index(data, []byte("<abcd>")))
	end := start + int64(len("<abcd>"))
	eof := int64(bytes.LastIndex(data, []byte("%%EOF"))) + int64(len("%%EOF"))
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, [4]int64{0, start, end, eof - end}
}

func TestByteRangeCoversRevision(t *testing.T) {
	for _, tc := range []struct {
		name, body, tail string
		shift            int64 // added to the last ByteRange element
		want             bool
	}{
		{"plain", "<< /Contents <abcd> >>", "", 0, true},
		{"every value kind before Contents",
			"<< /A [1 2] /B << /C 1 >> /D 1 0 R /E 1 /F 1.5 /G true /H (s) /I <00> /J /N /K null /Contents <abcd> >>",
			"", 0, true},
		{"literal Contents", "<< /Contents (abcd) >>", "", 0, false},
		{"Contents too short", "<< /Contents <abcd> >>", "", -1, false},
		{"LF after %%EOF", "<< /Contents <abcd> >>", "\n", 1, true},
		{"LF left out", "<< /Contents <abcd> >>", "\n", 0, false},
		{"CR LF after %%EOF", "<< /Contents <abcd> >>", "\r\n", 2, true},
		{"CR after %%EOF", "<< /Contents <abcd> >>", "\r", 1, true},
		{"CR LF partly left out before more bytes", "<< /Contents <abcd> >>", "\r\njunk", 1, true},
		{"too long before more bytes", "<< /Contents <abcd> >>", "\njunk", 2, false},
		{"Contents by reference", "<< /Contents 4 0 R >>", "", 0, false},
		{"not a dictionary", "42", "", 0, false},
		{"key not a name", "<< 1 /Contents <abcd> >>", "", 0, false},
		{"no Contents", "<< /A 1 >>", "", 0, false},
		{"bad array value", "<< /A [ /Contents <abcd> >>", "", 0, false},
		{"bad dictionary value", "<< /A << 1 >> /Contents <abcd> >>", "", 0, false},
		{"keyword value", "<< /A foo /Contents <abcd> >>", "", 0, false},
		{"stray delimiter", "<< /A ] /Contents <abcd> >>", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, br := sigDoc(t, tc.body, tc.tail)
			br[3] += tc.shift
			if got := r.ByteRangeCoversRevision(PDFRef{ObjNum: 2}, br); got != tc.want {
				t.Errorf("ByteRangeCoversRevision(%v) = %v, want %v", br, got, tc.want)
			}
		})
	}
}

func TestByteRangeCoversRevisionRejects(t *testing.T) {
	r, br := sigDoc(t, "<< /Contents <abcd> >>", "")
	if r.ByteRangeCoversRevision(PDFRef{ObjNum: 9}, br) {
		t.Error("an object missing from the xref table covers its revision")
	}
	r.xrefTable[9] = r.xrefTable[2]
	if r.ByteRangeCoversRevision(PDFRef{ObjNum: 9}, br) {
		t.Error("an xref entry pointing at another object covers its revision")
	}
	br[0] = 1
	if r.ByteRangeCoversRevision(PDFRef{ObjNum: 2}, br) {
		t.Error("a range not starting at 0 covers its revision")
	}
}

func TestRevisionEnd(t *testing.T) {
	r, _ := sigDoc(t, "<< /Contents <abcd> >>",
		"\nx %%EOF\n1 0 obj << >> stream\n%%EOF\nendstream endobj\n  %%EOF")
	data, err := r.FullBytes()
	if err != nil {
		t.Fatal(err)
	}
	first := int64(bytes.Index(data, []byte("%%EOF")))
	for _, tc := range []struct {
		from int64
		want int64
	}{
		// From the %%EOF itself: the byte before it is outside the first window.
		{first, first + int64(len("%%EOF\n"))},
		// Past it: the one after "x" and the one in the stream do not count.
		{first + 1, int64(len(data))},
		// From the end of the file no %%EOF is found.
		{int64(len(data)), int64(len(data))},
	} {
		if got, _, _ := r.revisionEnd(tc.from); got != tc.want {
			t.Errorf("revisionEnd(%d) = %d, want %d", tc.from, got, tc.want)
		}
	}
	if next := r.nextXRefSection(int64(len(data))); next != int64(len(data)) {
		t.Errorf("nextXRefSection past every section = %d, want its argument", next)
	}
}

func TestAloneOnLineAtFileStart(t *testing.T) {
	r, _ := sigDoc(t, "<< /Contents <abcd> >>", "")
	if s := (byteScanner{d: r}); s.aloneOnLine() {
		t.Error("the first byte of the file is alone on its line")
	}
}
