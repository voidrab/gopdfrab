// Convert and verify PDF files for PDF/A-1b conformance.
package gopdfrab

import (
	"github.com/voidrab/gopdfrab/internal/convert"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// Result, Check and PDFError are detailed in verify.Result/Check/
// PDFError; Profile selects which checks a verification run applies.
type (
	Result            = pdf.Result
	FileResult[T any] = pdf.FileResult[T]
	Profile           = pdf.Profile
	LevelType         = pdf.LevelType
	Check             = pdf.Check
	PDFError          = pdf.PDFError
	ConvertResult     = convert.ConvertResult
)

// A_1B and Undefined are the conformance levels Verify accepts.
const (
	A_1B      = pdf.A_1B
	Undefined = pdf.Undefined
)

// PDFA_1B and Legacy_1B are the built-in profiles; see pdf.PDFA_1B and
// pdf.Legacy_1B.
var (
	PDFA_1B   = pdf.PDFA_1B
	Legacy_1B = pdf.Legacy_1B
)

// Checks is the registry of every selectable PDF/A check, grouped by area
// (Checks.Structure, Checks.Colour, ...).
var Checks = pdf.Checks

// NewProfile returns an empty profile for the given conformance level.
func NewProfile(level LevelType) *Profile { return pdf.NewProfile(level) }

// AllChecks returns every registered check with its name, description, and
// clause number.
func AllChecks() []Check { return pdf.AllChecks() }

// CheckByClause looks up the registered check for a specific (clause,
// subclause) pair, e.g. CheckByClause("6.3.4", 1).
func CheckByClause(clause string, subclause int) (Check, bool) {
	return pdf.CheckByClause(clause, subclause)
}

// ChecksForClause returns every registered check under the given clause
// (e.g. "6.3.4").
func ChecksForClause(clause string) []Check { return pdf.ChecksForClause(clause) }

// Verify opens, verifies, and closes a single file.
func Verify(path string, p *Profile) (Result, error) { return verify.VerifyFile(path, p) }

// VerifyBytes is Verify for an in-memory PDF.
func VerifyBytes(data []byte, p *Profile) (Result, error) { return verify.VerifyBytes(data, p) }

// VerifyAll opens, verifies, and closes a batch of files concurrently.
func VerifyAll(paths []string, p *Profile) ([]FileResult[Result], error) {
	return verify.VerifyAll(paths, p)
}

// Convert reads the PDF at path and attempts to produce a PDF/A-1b
// conformant rewrite.
func Convert(path string, p *Profile) (ConvertResult, error) { return convert.Convert(path, p) }

// ConvertBytes is Convert for an in-memory PDF.
func ConvertBytes(data []byte, p *Profile) (ConvertResult, error) {
	return convert.ConvertBytes(data, p)
}

// ConvertAll opens, converts, and closes a batch of files concurrently.
func ConvertAll(paths []string, p *Profile) ([]FileResult[ConvertResult], error) {
	return convert.ConvertAll(paths, p)
}

// Document represents an open PDF file.
type Document struct {
	r *pdf.Reader
}

// Open initializes the PDF document at path.
func Open(path string) (*Document, error) {
	r, err := pdf.Open(path)
	if err != nil {
		return nil, err
	}
	return &Document{r: r}, nil
}

// Close ensures the file handle is released.
func (d *Document) Close() error { return d.r.Close() }

// Verify verifies d against the checks enabled in profile p.
func (d *Document) Verify(p *Profile) (Result, error) { return verify.Verify(d.r, p) }

// IsPDFA reports whether the document is valid PDF/A-1b. It is equivalent to
// calling Verify(PDFA_1B) and checking the result's Valid field, for callers who
// only need a yes/no answer.
func (d *Document) IsPDFA() (bool, error) {
	res, err := d.Verify(PDFA_1B)
	if err != nil {
		return false, err
	}
	return res.Valid, nil
}

// Convert converts d, an already-open document, attempting to produce a
// PDF/A-1b conformant rewrite; see Convert (the package-level function).
func (d *Document) Convert(p *Profile) (ConvertResult, error) { return convert.Run(d.r, p) }

// XMPMetadata returns the document's raw XMP metadata packet (Root/Metadata),
// decoded and normalised to UTF-8. It returns an error if the document has no
// XMP metadata stream, regardless of whether the document otherwise validates
// as PDF/A.
func (d *Document) XMPMetadata() ([]byte, error) { return d.r.XMPMetadata() }

// ClaimedConformance returns the PDF/A part and conformance level the
// document's XMP metadata claims (e.g. "1", "B"), read from the pdfaid
// namespace. This reflects what the file claims, not whether it actually
// validates — use Verify or IsPDFA to check actual compliance.
func (d *Document) ClaimedConformance() (part, conformance string, err error) {
	return d.r.ClaimedConformance()
}

// GetPageCount retrieves the page count.
func (d *Document) GetPageCount() (int, error) { return d.r.GetPageCount() }

// GetVersion extracts the PDF version from the document header.
func (d *Document) GetVersion() (string, error) { return d.r.GetVersion() }

// GetMetadata extracts info from the Info dictionary.
func (d *Document) GetMetadata() (map[string]string, error) { return d.r.GetMetadata() }
