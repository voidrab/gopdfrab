package pdf_test

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// The targets below fuzz individual decoders and parsers in isolation. Whole-
// file fuzzing (FuzzOpenBytes/FuzzConvertBytes) reaches these only behind many
// layers; hitting them directly with raw bytes / crafted params surfaces filter,
// function, colorspace, and content-scanner crashes far faster. The invariant is
// always the same: no input may panic; returned errors are fine.

// --- filters / codecs ------------------------------------------------------

// FuzzDecodeStream drives the whole-stream filter dispatch by wrapping the
// fuzz bytes in a stream dict under a rotating set of filters.
func FuzzDecodeStream(f *testing.F) {
	f.Add([]byte("q\nQ\n"))
	f.Add([]byte("789c030000000001")) // near-empty zlib-ish
	filters := []string{"FlateDecode", "ASCIIHexDecode", "ASCII85Decode", "LZWDecode", "BogusDecode"}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		for _, name := range filters {
			d := pdf.NewPDFDict()
			d.HasStream = true
			d.RawStream = data
			d.Entries["Filter"] = pdf.PDFName{Value: name}
			d.Entries["Length"] = pdf.PDFInteger(len(data))
			pdf.DecodeStream(d)
		}
	})
}

func FuzzInflateZlib(f *testing.F) {
	f.Add([]byte{0x78, 0x9c, 0x03, 0x00, 0x00, 0x00, 0x00, 0x01})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		pdf.InflateZlib(data)
	})
}

func FuzzDecodeASCIIHex(f *testing.F) {
	f.Add([]byte("48656c6c6f>"))
	f.Add([]byte("z z z"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		pdf.DecodeASCIIHex(data)
	})
}

func FuzzDecodeASCII85(f *testing.F) {
	f.Add([]byte("<~87cURD]~>"))
	f.Add([]byte("zzzz~>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		pdf.DecodeASCII85(data)
	})
}

func FuzzDecodeLZW(f *testing.F) {
	f.Add([]byte{0x80, 0x0b, 0x60, 0x50})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		pdf.DecodeLZW(data)
	})
}

// FuzzDecodeCCITT fuzzes both the encoded data and the DecodeParms, with params
// bounded so the target exercises decoding logic rather than a legitimate
// (already-guarded) huge-allocation rejection.
func FuzzDecodeCCITT(f *testing.F) {
	f.Add([]byte{0x00, 0x01, 0x02, 0x03}, 8, 8, 0)
	f.Fuzz(func(t *testing.T, data []byte, cols, rows, k int) {
		if len(data) > 1<<16 {
			return
		}
		p := pdf.CCITTParams{
			Columns:   clampInt(cols, 0, 4096),
			Rows:      clampInt(rows, 0, 4096),
			K:         clampInt(k, -1, 1),
			ByteAlign: len(data)%2 == 0,
			BlackIs1:  len(data)%3 == 0,
		}
		pdf.DecodeCCITT(data, p)
	})
}

func FuzzUndoPredictor(f *testing.F) {
	f.Add([]byte{0x00, 0x01, 0x02, 0x03, 0x04}, 2, 1, 8)
	f.Fuzz(func(t *testing.T, data []byte, columns, colors, bpc int) {
		if len(data) > 1<<20 {
			return
		}
		columns = clampInt(columns, 0, 4096)
		colors = clampInt(colors, 0, 8)
		bpc = clampInt(bpc, 0, 16)
		pdf.UndoPNGPredictor(data, columns, colors, bpc)
		pdf.UndoTIFFPredictor(data, columns, colors, 8)
	})
}

func FuzzReadBits(f *testing.F) {
	f.Add([]byte{0xff, 0x00, 0xaa, 0x55}, 3, 12)
	f.Fuzz(func(t *testing.T, data []byte, bitOffset, n int) {
		if len(data) > 1<<16 {
			return
		}
		// ReadBits is documented for n<=32; keep n in range and offset sane.
		pdf.ReadBits(data, clampInt(bitOffset, 0, len(data)*8+64), clampInt(n, 0, 32))
	})
}

// --- content stream scanner ------------------------------------------------

func FuzzTokenizeContent(f *testing.F) {
	f.Add([]byte("q 1 0 0 1 0 0 cm BT /F1 12 Tf (hi) Tj ET Q"))
	f.Add([]byte("BI /W 4 /H 4 /BPC 8 ID \x00\x01\x02\x03 EI"))
	f.Add([]byte("[ [ [ (unbalanced"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		pdf.TokenizeContent(data)
		pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {})
	})
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
