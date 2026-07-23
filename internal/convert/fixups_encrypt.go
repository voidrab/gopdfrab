package convert

import "github.com/voidrab/gopdfrab/internal/pdf"

// The reader decrypts every stream and string when it opens an encrypted file,
// so the resolved graph is already plaintext. Drop the trailer /Encrypt
// reference pre-emptively: left in place it fails 6.1.3 and orphans an
// encryption dictionary the object-model checks reject (e.g. AESV3's V5/R6,
// which PDF/A-1b's model does not permit).
func init() {
	registerPreemptiveFixup(func(trailer *pdf.PDFDict, _ *pdf.Reader) error {
		delete(trailer.Entries, "Encrypt")
		return nil
	})
}
