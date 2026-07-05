package gopdfrab_test

import (
	"fmt"

	"github.com/voidrab/gopdfrab"
)

// plainPDF is a minimal one-page PDF. It is a valid PDF but not PDF/A-1b: it
// lacks the XMP metadata, OutputIntent, and other structures PDF/A-1b requires.
const plainPDF = "%PDF-1.4\n" +
	"1 0 obj\n<</Type/Catalog/Pages 2 0 R>>\nendobj\n" +
	"2 0 obj\n<</Type/Pages/Kids[3 0 R]/Count 1>>\nendobj\n" +
	"3 0 obj\n<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]>>\nendobj\n" +
	"xref\n0 4\n" +
	"0000000000 65535 f \n" +
	"0000000009 00000 n \n" +
	"0000000054 00000 n \n" +
	"0000000105 00000 n \n" +
	"trailer\n<</Size 4/Root 1 0 R>>\n" +
	"startxref\n170\n%%EOF"

// Example verifies an ordinary PDF, sees that it is not PDF/A-1b, converts
// it, and confirms the conversion produced a conformant file.
func Example() {
	src := []byte(plainPDF)

	before, _ := gopdfrab.VerifyBytes(src, gopdfrab.PDFA_1B)
	fmt.Println("PDF/A-1b before convert:", before.Valid)

	res, _ := gopdfrab.ConvertBytes(src, gopdfrab.PDFA_1B)
	fmt.Println("PDF/A-1b after convert: ", res.Result.Valid)

	// Output:
	// PDF/A-1b before convert: false
	// PDF/A-1b after convert:  true
}
