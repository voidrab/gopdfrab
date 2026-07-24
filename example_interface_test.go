package gopdfrab_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/voidrab/gopdfrab"
)

// plainPDF is a minimal one-page PDF. It is a valid PDF but not PDF/A-1b: it
// lacks the XMP metadata, OutputIntent, and other structures PDF/A-1b requires.
const plainPDF = "%PDF-1.4\n" +
	"1 0 obj\n<</Type/Catalog/Pages 2 0 R>>\nendobj\n" +
	"2 0 obj\n<</Type/Pages/Kids[3 0 R]/Count 1>>\nendobj\n" +
	"3 0 obj\n<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]>>\nendobj\n" +
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

	before, _ := gopdfrab.VerifyBytes(src, gopdfrab.PDFA1B)
	fmt.Println("PDF/A-1b before convert:", before.Valid)

	checks := []string{}
	for _, iss := range before.Issues {
		checks = append(checks, iss.Check().Name())
	}

	fmt.Println("Failed checks:", checks)

	res, _ := gopdfrab.ConvertBytes(src, gopdfrab.PDFA1B)
	fmt.Println("PDF/A-1b after convert: ", res.Result.Valid)

	// res.Save("converted.pdf") // optionally write the converted PDF to disk

	// Output:
	// PDF/A-1b before convert: false
	// Failed checks: [FileHeaderComment TrailerID MetadataMissing]
	// PDF/A-1b after convert:  true
}

// ExampleVerify verifies a file on disk against the PDF/A-1b profile.
func ExampleVerify() {
	dir, _ := os.MkdirTemp("", "gopdfrab")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "in.pdf")
	_ = os.WriteFile(path, []byte(plainPDF), 0o644)

	res, _ := gopdfrab.Verify(path, gopdfrab.PDFA1B)
	fmt.Println("valid:", res.Valid)
	// Output:
	// valid: false
}

// ExampleVerifyBytes verifies an in-memory PDF and reports how many checks it
// failed.
func ExampleVerifyBytes() {
	res, _ := gopdfrab.VerifyBytes([]byte(plainPDF), gopdfrab.PDFA1B)
	fmt.Println("valid:", res.Valid)
	fmt.Println("issues:", len(res.Issues))
	// Output:
	// valid: false
	// issues: 3
}

// ExampleConvertBytes rewrites a non-conformant PDF towards PDF/A-1b and
// confirms the result verifies clean.
func ExampleConvertBytes() {
	res, _ := gopdfrab.ConvertBytes([]byte(plainPDF), gopdfrab.PDFA1B)
	fmt.Println("valid after convert:", res.Result.Valid)
	// Output:
	// valid after convert: true
}

// ExampleConvertEach converts a batch of files, streaming each result to a
// callback instead of holding every output in memory.
func ExampleConvertEach() {
	dir, _ := os.MkdirTemp("", "gopdfrab")
	defer os.RemoveAll(dir)
	var paths []string
	for _, name := range []string{"a.pdf", "b.pdf"} {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(plainPDF), 0o644)
		paths = append(paths, p)
	}

	conformant := 0
	_ = gopdfrab.ConvertEach(paths, gopdfrab.PDFA1B, gopdfrab.Options{},
		func(r gopdfrab.FileResult[gopdfrab.ConvertResult]) error {
			if r.Err == nil && r.Result.Result.Valid {
				conformant++
			}
			return nil
		})
	fmt.Println("conformant:", conformant)
	// Output:
	// conformant: 2
}

// ExampleProfile_RemoveCheck narrows PDF/A-1b by dropping the checks a plain PDF
// fails, so the same file then verifies clean against the narrowed profile.
func ExampleProfile_RemoveCheck() {
	p := gopdfrab.PDFA1B.
		RemoveCheck(gopdfrab.Checks.Structure.FileHeaderComment).
		RemoveCheck(gopdfrab.Checks.Structure.TrailerID).
		RemoveCheck(gopdfrab.Checks.Metadata.MetadataMissing)

	res, _ := gopdfrab.VerifyBytes([]byte(plainPDF), p)
	fmt.Println("valid with narrowed profile:", res.Valid)
	// Output:
	// valid with narrowed profile: true
}

// ExampleSetLimits raises the process-wide decoded-output cap.
func ExampleSetLimits() {
	defer gopdfrab.SetLimits(gopdfrab.DefaultLimits()) // restore the default
	gopdfrab.SetLimits(gopdfrab.Limits{MaxDecodedStreamBytes: 64 << 20})
	fmt.Println(gopdfrab.CurrentLimits().MaxDecodedStreamBytes)
	// Output:
	// 67108864
}
