// Convert and verify PDF files for PDF/A conformance.
package gopdfrab

import (
	"github.com/voidrab/gopdfrab/internal/convert"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

type (
	Result            = pdf.Result
	FileResult[T any] = pdf.FileResult[T]
	Profile           = pdf.Profile
	LevelType         = pdf.LevelType
	Check             = pdf.Check
	PDFError          = pdf.PDFError
	ConvertResult     = convert.ConvertResult
)

// PDF conformance levels.
const (
	A1B       = pdf.A1B
	Undefined = pdf.Undefined
	// ObjectModel is a reporting-only level for the generic ISO 32000
	// object-model checks.
	ObjectModel = pdf.ObjectModel
)

// PDF profiles.
var (
	// PDF is the default profile for generic ISO 32000 object-model checks.
	PDF = pdf.PDF
	// PDFA1B is the canonical PDF/A-1b profile
	PDFA1B = pdf.PDFA1B
	// Legacy1B is stricter in some areas and compatible with the original Isartor PDF/A-1b test suite.
	Legacy1B = pdf.Legacy1B
)

// Checks is the registry of every selectable PDF/A check, grouped by area.
var Checks = pdf.Checks

// Errors callers can match with errors.Is on the result of Open/Verify/Convert.
// ErrNotPDF: the input is not a PDF. ErrDamaged: a PDF whose cross-reference or
// trailer structure could not be parsed. ErrEncrypted: an encryption scheme
// gopdfrab does not implement. ErrPasswordRequired: a correct password is needed.
// ErrUnresolvableGraph: Convert could not resolve the object graph even with
// per-object degradation, so no output could be produced (the ConvertResult
// still carries the best-effort verify Result).
var (
	ErrNotPDF            = pdf.ErrNotPDF
	ErrDamaged           = pdf.ErrDamaged
	ErrEncrypted         = pdf.ErrEncrypted
	ErrPasswordRequired  = pdf.ErrPasswordRequired
	ErrUnresolvableGraph = pdf.ErrUnresolvableGraph
)

// Option customizes a Verify or Convert call. Pass any number of the With*
// options as the trailing arguments; the two-argument form (path/data +
// profile) keeps the defaults.
type Option func(*options)

type options struct {
	password      []byte
	rasterDPI     int
	maxIterations int
}

func applyOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

func (o options) convertOptions() convert.Options {
	return convert.Options{Password: o.password, MaxIterations: o.maxIterations, RasterDPI: o.rasterDPI}
}

// WithPassword supplies the user or owner password for an encrypted file. It
// applies to the Open step, so it has no effect on the *Document methods, whose
// file is already open. nil is the empty password.
func WithPassword(password []byte) Option {
	return func(o *options) { o.password = password }
}

// WithRasterDPI sets the resolution (dots per inch) at which Convert rasterizes
// a page or form as a last resort or to flatten transparency. The default is
// 150. Verify ignores it.
func WithRasterDPI(dpi int) Option {
	return func(o *options) { o.rasterDPI = dpi }
}

// WithMaxIterations bounds Convert's verify/fix loop. The default is 4. Verify
// ignores it.
func WithMaxIterations(n int) Option {
	return func(o *options) { o.maxIterations = n }
}

// NewProfile returns an empty profile for the given conformance level.
func NewProfile(level LevelType) *Profile { return pdf.NewProfile(level) }

// ObjectModelOnly returns a profile enabling only the generic ISO 32000
// object-model checks, independent of any PDF/A conformance level -- useful
// for asking "is this even valid PDF" on its own.
func ObjectModelOnly() *Profile { return pdf.ObjectModelOnly() }

// AllChecks returns every registered check with its name, description, and
// clause number.
func AllChecks() []Check { return pdf.AllChecks() }

// CheckByClause looks up the registered check for a specific (clause,
// subclause) pair.
func CheckByClause(clause string, subclause int) (Check, bool) {
	return pdf.CheckByClause(clause, subclause)
}

// ChecksForClause returns every registered check under the given clause.
func ChecksForClause(clause string) []Check { return pdf.ChecksForClause(clause) }

// Verify opens, verifies, and closes a single file.
func Verify(path string, p *Profile, opts ...Option) (Result, error) {
	return verify.VerifyFile(path, p, applyOptions(opts).password)
}

// VerifyBytes is Verify for an in-memory PDF.
func VerifyBytes(data []byte, p *Profile, opts ...Option) (Result, error) {
	return verify.VerifyBytes(data, p, applyOptions(opts).password)
}

// VerifyAll opens, verifies, and closes a batch of files concurrently.
func VerifyAll(paths []string, p *Profile, opts ...Option) ([]FileResult[Result], error) {
	return verify.VerifyAll(paths, p, applyOptions(opts).password)
}

// VerifyObjectModel opens, checks, and closes a single file against the
// generic ISO 32000 object-model checks only, independent of any PDF/A
// conformance level.
func VerifyObjectModel(path string, opts ...Option) (Result, error) {
	return verify.VerifyFile(path, PDF, applyOptions(opts).password)
}

// VerifyObjectModelBytes is VerifyObjectModel for an in-memory PDF.
func VerifyObjectModelBytes(data []byte, opts ...Option) (Result, error) {
	return verify.VerifyBytes(data, PDF, applyOptions(opts).password)
}

// Convert reads the PDF at path and attempts to produce a PDF/A-1b
// conformant rewrite.
func Convert(path string, p *Profile, opts ...Option) (ConvertResult, error) {
	o := applyOptions(opts)
	return convert.Convert(path, p, o.convertOptions())
}

// ConvertBytes is Convert for an in-memory PDF.
func ConvertBytes(data []byte, p *Profile, opts ...Option) (ConvertResult, error) {
	o := applyOptions(opts)
	return convert.ConvertBytes(data, p, o.convertOptions())
}

// ConvertAll opens, converts, and closes a batch of files concurrently.
func ConvertAll(paths []string, p *Profile, opts ...Option) ([]FileResult[ConvertResult], error) {
	o := applyOptions(opts)
	return convert.ConvertAll(paths, p, o.convertOptions())
}

// ConvertObjectModel reads the PDF at path and attempts to produce a rewrite
// conformant with the generic ISO 32000 object-model checks only, independent
// of any PDF/A conformance level -- the conversion counterpart to
// VerifyObjectModel.
func ConvertObjectModel(path string, opts ...Option) (ConvertResult, error) {
	o := applyOptions(opts)
	return convert.Convert(path, PDF, o.convertOptions())
}

// ConvertObjectModelBytes is ConvertObjectModel for an in-memory PDF.
func ConvertObjectModelBytes(data []byte, opts ...Option) (ConvertResult, error) {
	o := applyOptions(opts)
	return convert.ConvertBytes(data, PDF, o.convertOptions())
}

// Document represents an open PDF file.
type Document struct {
	r *pdf.Reader
}

// Open initializes the PDF document at path, decrypting an encrypted file with
// the empty password.
func Open(path string) (*Document, error) { return OpenWithPassword(path, nil) }

// OpenWithPassword is Open with an explicit password for an encrypted file.
// nil is the empty password. It returns an error matching ErrPasswordRequired
// when the password is wrong or missing.
func OpenWithPassword(path string, password []byte) (*Document, error) {
	r, err := pdf.OpenWithPassword(path, password)
	if err != nil {
		return nil, err
	}
	return &Document{r: r}, nil
}

// Close ensures the file handle is released.
func (d *Document) Close() error { return d.r.Close() }

// Verify verifies d against the checks enabled in profile p.
func (d *Document) Verify(p *Profile) (Result, error) { return verify.Verify(d.r, p) }

// VerifyObjectModel checks d against the generic ISO 32000 object-model
// checks only, independent of any PDF/A conformance level.
func (d *Document) VerifyObjectModel() (Result, error) { return d.Verify(PDF) }

// IsPDFA reports whether the document is valid PDF/A-1b. It is equivalent to
// calling Verify(PDFA1B) and checking the result's Valid field.
func (d *Document) IsPDFA() (bool, error) {
	res, err := d.Verify(PDFA1B)
	if err != nil {
		return false, err
	}
	return res.Valid, nil
}

// IsPDF reports whether the document is valid against the generic
// ISO 32000 object-model checks only, independent of any PDF/A conformance
// level. It is equivalent to calling VerifyObjectModel and checking the
// result's Valid field.
func (d *Document) IsPDF() (bool, error) {
	res, err := d.VerifyObjectModel()
	if err != nil {
		return false, err
	}
	return res.Valid, nil
}

// Convert converts d, an already-open document, attempting to produce a
// PDF/A-1b conformant rewrite. WithPassword is ignored (the file is already
// open); WithRasterDPI and WithMaxIterations apply.
func (d *Document) Convert(p *Profile, opts ...Option) (ConvertResult, error) {
	return convert.Run(d.r, p, applyOptions(opts).convertOptions())
}

// ConvertObjectModel converts d against the generic ISO 32000 object-model
// checks only, independent of any PDF/A conformance level.
func (d *Document) ConvertObjectModel(opts ...Option) (ConvertResult, error) {
	return convert.Run(d.r, PDF, applyOptions(opts).convertOptions())
}

// XMPMetadata returns the document's raw XMP metadata packet (Root/Metadata),
// decoded and normalised to UTF-8. It returns an error if the document has no
// XMP metadata stream.
func (d *Document) XMPMetadata() ([]byte, error) { return d.r.XMPMetadata() }

// ClaimedConformance returns the PDF/A part and conformance level the
// document's XMP metadata claims, read from the pdfaid
// namespace. This reflects what the file claims, not whether it actually
// validates — use Verify or IsPDFA to check actual compliance.
func (d *Document) ClaimedConformance() (part, conformance string, err error) {
	return d.r.ClaimedConformance()
}

// PageCount retrieves the page count.
func (d *Document) PageCount() (int, error) { return d.r.PageCount() }

// Version extracts the PDF version from the document header.
func (d *Document) Version() (string, error) { return d.r.Version() }

// Metadata extracts info from the Info dictionary.
func (d *Document) Metadata() (map[string]string, error) { return d.r.Metadata() }
