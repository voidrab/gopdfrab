package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// dropEncryption is a pre-emptive fixup that drops the trailer /Encrypt
// reference. The reader decrypts every stream and string when it opens an
// encrypted file, so the resolved graph is already plaintext. Left in place,
// the reference fails 6.1.3 and orphans an encryption dictionary that the
// object-model checks reject (for example AESV3's V5/R6, which the PDF/A-1b
// model does not permit).
func dropEncryption(trailer *pdf.PDFDict, _ *pdf.Reader, _ pdfa.Spec) error {
	trailer.Entries.Del("Encrypt")
	return nil
}
