package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func simpleFontDict(baseFont string, flags int) pdf.PDFDict {
	d := pdf.NewPDFDict()
	d.Entries["Type"] = pdf.PDFName{Value: "Font"}
	d.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	d.Entries["BaseFont"] = pdf.PDFName{Value: baseFont}
	if flags != 0 {
		desc := pdf.NewPDFDict()
		desc.Entries["Flags"] = pdf.PDFInteger(flags)
		d.Entries["FontDescriptor"] = desc
	}
	return d
}

func TestOriginalCodeToUnicodeStandardSymbolFonts(t *testing.T) {
	zapf := simpleFontDict("ZapfDingbats", 4)
	table := mustTable(zapf)
	if table[52] != 0x2714 {
		t.Errorf("ZapfDingbats code 52 = %04X, want 2714 (heavy check mark)", table[52])
	}

	subset := simpleFontDict("ABCDEF+Symbol", 4)
	table = mustTable(subset)
	if table[97] != 0x03B1 {
		t.Errorf("Symbol code 97 = %04X, want 03B1 (alpha)", table[97])
	}
}

func TestOriginalCodeToUnicodeDifferencesWithDingbatNames(t *testing.T) {
	d := simpleFontDict("ZapfDingbats", 4)
	enc := pdf.NewPDFDict()
	enc.Entries["Differences"] = pdf.PDFArray{
		pdf.PDFInteger(52), pdf.PDFName{Value: "a20"},
		pdf.PDFName{Value: "a19"},
	}
	d.Entries["Encoding"] = enc

	table := mustTable(d)
	if table[52] != 0x2714 {
		t.Errorf("Differences code 52 = %04X, want 2714 (a20)", table[52])
	}
	if table[53] != 0x2713 {
		t.Errorf("Differences code 53 = %04X, want 2713 (a19)", table[53])
	}
	// Codes outside Differences keep the dingbats built-in meaning.
	if table[33] != 0x2701 {
		t.Errorf("code 33 = %04X, want 2701 (a1 from builtin)", table[33])
	}
}

func TestOriginalCodeToUnicodeSymbolicCustomIsUnknown(t *testing.T) {
	d := simpleFontDict("MyDingFont", 4)
	table := mustTable(d)
	for cc, u := range table {
		if u != 0 {
			t.Fatalf("symbolic custom font code %d resolved to %04X, want unknown", cc, u)
		}
	}
}

func TestOriginalCodeToUnicodeNonSymbolicDefaults(t *testing.T) {
	// No Encoding at all: matches the fixer's WinAnsi assumption.
	d := simpleFontDict("Helvetica", 32)
	table := mustTable(d)
	if table[0x80] != 0x20AC {
		t.Errorf("no-encoding code 0x80 = %04X, want 20AC (WinAnsi Euro)", table[0x80])
	}

	// Encoding dict without BaseEncoding: the implicit standard base.
	enc := pdf.NewPDFDict()
	enc.Entries["Differences"] = pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "eacute"}}
	d.Entries["Encoding"] = enc
	table = mustTable(d)
	if table[65] != 0x00E9 {
		t.Errorf("Differences code 65 = %04X, want 00E9 (eacute)", table[65])
	}
	if table[0x27] != 0x2019 {
		t.Errorf("dict-without-base code 0x27 = %04X, want 2019 (Standard quoteright)", table[0x27])
	}
}

func TestOriginalCodeToUnicodeToUnicodeFallback(t *testing.T) {
	d := simpleFontDict("MySymbols", 4)
	toUni := pdf.NewPDFDict()
	toUni.HasStream = true
	toUni.RawStream = []byte("beginbfchar\n<34> <2714>\nendbfchar")
	d.Entries["ToUnicode"] = toUni

	table := mustTable(d)
	if table[0x34] != 0x2714 {
		t.Errorf("ToUnicode code 0x34 = %04X, want 2714", table[0x34])
	}
	if table[0x35] != 0 {
		t.Errorf("unmapped code 0x35 = %04X, want unknown", table[0x35])
	}
}

func mustTable(d pdf.PDFDict) [256]uint16 {
	table, _ := originalSimpleFontCodeToUnicode(d)
	return table
}
