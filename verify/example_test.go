package verify_test

import (
	"fmt"

	"github.com/voidrab/gopdfrab/verify"
)

// Example verifies an ordinary PDF and lists the checks it fails, without
// linking any of gopdfrab's conversion code.
func Example() {
	res, err := verify.VerifyBytes([]byte(plainPDF), verify.PDFA1B)
	if err != nil {
		fmt.Println("could not verify:", err)
		return
	}
	fmt.Println("PDF/A-1b:", res.Valid)
	for _, iss := range res.Issues {
		fmt.Println(iss.Check().Clause(), iss.Check().Name())
	}
	// Output:
	// PDF/A-1b: false
	// 6.1.2 FileHeaderComment
	// 6.1.3 TrailerID
	// 6.7.2 MetadataMissing
}
