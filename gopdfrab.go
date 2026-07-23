// Convert and verify PDF files for PDF/A conformance.
package gopdfrab

import (
	"context"

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
	// PageFidelity is one page's input-vs-output rendering comparison,
	// populated in ConvertResult.Fidelity when Options.CheckFidelity is set.
	PageFidelity = convert.PageFidelity
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

// Options configures a Verify or Convert call. The zero value is the default
// behavior, so callers set only the fields they need. The *Context entry points
// take an Options; the two-argument forms use the zero value.
//
// Fields not relevant to an operation are ignored: Verify uses only Password;
// RasterDPI and MaxIterations apply to Convert. Password applies at the open
// step, so it has no effect on the *Document methods, whose file is already
// open (use OpenWithPassword for those).
type Options struct {
	// Password is the user or owner password for an encrypted input. nil is the
	// empty password.
	Password []byte
	// RasterDPI is the resolution at which Convert rasterizes a page or form as
	// a last resort or to flatten transparency. 0 selects the default (150).
	RasterDPI int
	// MaxIterations bounds Convert's verify/fix loop. 0 selects the default (4).
	MaxIterations int
	// CheckFidelity makes Convert render the input and its output and populate
	// ConvertResult.Fidelity with a per-page comparison. Off by default (it
	// roughly doubles the work). Verify ignores it.
	CheckFidelity bool
}

func (o Options) convert() convert.Options {
	return convert.Options{
		Password:      o.Password,
		RasterDPI:     o.RasterDPI,
		MaxIterations: o.MaxIterations,
		CheckFidelity: o.CheckFidelity,
	}
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
func Verify(path string, p *Profile) (Result, error) {
	return verify.VerifyFile(path, p, nil)
}

// VerifyContext is Verify honouring ctx cancellation, with o (Options.Password
// is the only field Verify uses).
func VerifyContext(ctx context.Context, path string, p *Profile, o Options) (Result, error) {
	return verify.VerifyFileContext(ctx, path, p, o.Password)
}

// VerifyBytes is Verify for an in-memory PDF.
func VerifyBytes(data []byte, p *Profile) (Result, error) {
	return verify.VerifyBytes(data, p, nil)
}

// VerifyBytesContext is VerifyBytes honouring ctx cancellation, with o.
func VerifyBytesContext(ctx context.Context, data []byte, p *Profile, o Options) (Result, error) {
	return verify.VerifyBytesContext(ctx, data, p, o.Password)
}

// VerifyAll opens, verifies, and closes a batch of files concurrently.
func VerifyAll(paths []string, p *Profile) ([]FileResult[Result], error) {
	return verify.VerifyAll(paths, p, nil)
}

// VerifyAllContext is VerifyAll honouring ctx cancellation; a cancelled ctx
// records ctx.Err() for files not yet started.
func VerifyAllContext(ctx context.Context, paths []string, p *Profile, o Options) ([]FileResult[Result], error) {
	return verify.VerifyAllContext(ctx, paths, p, o.Password)
}

// VerifyObjectModel opens, checks, and closes a single file against the
// generic ISO 32000 object-model checks only, independent of any PDF/A
// conformance level.
func VerifyObjectModel(path string) (Result, error) {
	return verify.VerifyFile(path, PDF, nil)
}

// VerifyObjectModelBytes is VerifyObjectModel for an in-memory PDF.
func VerifyObjectModelBytes(data []byte) (Result, error) {
	return verify.VerifyBytes(data, PDF, nil)
}

// Convert reads the PDF at path and attempts to produce a PDF/A-1b
// conformant rewrite.
func Convert(path string, p *Profile) (ConvertResult, error) {
	return convert.Convert(path, p, convert.Options{})
}

// ConvertContext is Convert honouring ctx cancellation (checked before each
// verify/fix iteration and each raster pass) and o.
func ConvertContext(ctx context.Context, path string, p *Profile, o Options) (ConvertResult, error) {
	return convert.ConvertContext(ctx, path, p, o.convert())
}

// ConvertBytes is Convert for an in-memory PDF.
func ConvertBytes(data []byte, p *Profile) (ConvertResult, error) {
	return convert.ConvertBytes(data, p, convert.Options{})
}

// ConvertBytesContext is ConvertBytes honouring ctx cancellation and o.
func ConvertBytesContext(ctx context.Context, data []byte, p *Profile, o Options) (ConvertResult, error) {
	return convert.ConvertBytesContext(ctx, data, p, o.convert())
}

// ConvertAll opens, converts, and closes a batch of files concurrently.
func ConvertAll(paths []string, p *Profile) ([]FileResult[ConvertResult], error) {
	return convert.ConvertAll(paths, p, convert.Options{})
}

// ConvertAllContext is ConvertAll honouring ctx cancellation (a cancelled ctx
// records ctx.Err() for files not yet started) and o.
func ConvertAllContext(ctx context.Context, paths []string, p *Profile, o Options) ([]FileResult[ConvertResult], error) {
	return convert.ConvertAllContext(ctx, paths, p, o.convert())
}

// ConvertObjectModel reads the PDF at path and attempts to produce a rewrite
// conformant with the generic ISO 32000 object-model checks only, independent
// of any PDF/A conformance level -- the conversion counterpart to
// VerifyObjectModel.
func ConvertObjectModel(path string) (ConvertResult, error) {
	return convert.Convert(path, PDF, convert.Options{})
}

// ConvertObjectModelBytes is ConvertObjectModel for an in-memory PDF.
func ConvertObjectModelBytes(data []byte) (ConvertResult, error) {
	return convert.ConvertBytes(data, PDF, convert.Options{})
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

// VerifyContext is Verify honouring ctx cancellation.
func (d *Document) VerifyContext(ctx context.Context, p *Profile) (Result, error) {
	return verify.VerifyContext(ctx, d.r, p)
}

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
// PDF/A-1b conformant rewrite.
func (d *Document) Convert(p *Profile) (ConvertResult, error) {
	return convert.Run(d.r, p, convert.Options{})
}

// ConvertContext is Convert honouring ctx cancellation (checked before each
// verify/fix iteration and each raster pass) and o. Options.Password is ignored
// (the file is already open); RasterDPI and MaxIterations apply.
func (d *Document) ConvertContext(ctx context.Context, p *Profile, o Options) (ConvertResult, error) {
	return convert.RunContext(ctx, d.r, p, o.convert())
}

// ConvertObjectModel converts d against the generic ISO 32000 object-model
// checks only, independent of any PDF/A conformance level.
func (d *Document) ConvertObjectModel() (ConvertResult, error) {
	return convert.Run(d.r, PDF, convert.Options{})
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
