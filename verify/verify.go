package verify

import (
	"context"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

type (
	// Result is a verification verdict and its issues. It is immutable once
	// returned, so any number of goroutines may read one concurrently.
	//
	// Fields:
	//
	//	Type   the conformance level verified against
	//	Valid  whether the file meets that level
	//	Issues every violation found, each a PDFError
	//
	// Methods: Count, Checks, IssuesByCheck, IssuesForCheck, IssuesOnPage,
	// Summary, MarshalJSON.
	Result = pdf.Result

	// FileResult is one path's outcome from VerifyAll, where T is Result.
	//
	// Fields:
	//
	//	Path   the file this entry reports on
	//	Result that file's outcome, zero if Err is set
	//	Err    why the file could not be processed, nil on success
	FileResult[T any] = pdf.FileResult[T]

	// Profile is the set of checks a Verify applies. A profile is immutable --
	// AddCheck, RemoveCheck and Clear each return a clone -- so one profile may
	// be shared by concurrent Verify calls. PDFA1B, Legacy1B and PDF are the
	// ready-made ones; NewProfile returns an empty one.
	//
	// Fields:
	//
	//	Level                   the conformance level this profile verifies
	//	SkipUnreachableXObjects skip Form XObjects no content stream invokes
	//	SkipUnusedSimpleFonts   only report embedding for fonts actually drawn
	//
	// Methods: Clone, Clear, AddCheck, RemoveCheck, Checks, Has,
	// OnlyObjectModelChecks, Allows, Name, String.
	Profile = pdf.Profile

	// LevelType is a conformance level: A1B for PDF/A-1b, ObjectModel for the
	// generic ISO 32000 checks, Undefined for neither. It is Profile.Level and
	// Result.Type. Verify returns an error for any other level, including
	// Undefined and the empty level of a zero-value Profile.
	LevelType = pdf.LevelType

	// Check is one named, selectable validation rule, identified by the ISO
	// clause it enforces under a conformance level. The Checks registry names
	// every check; AllChecks, CheckByClauseIn and ChecksForClauseIn look them
	// up. All fields are unexported, so a Check is only ever obtained, not
	// built. Clause and Subclause give the PDF/A-1 clause; ClauseIn gives the
	// clause under a given level, and AppliesTo whether there is one.
	//
	// Methods: Name, Description, Clause, Subclause, ClauseIn, AppliesTo, ID,
	// MarshalJSON.
	Check = pdf.Check

	// PDFError is one violation: the check that failed, what was wrong, and
	// where. It implements error. All fields are unexported; read it through
	// its methods.
	//
	// Methods: Check, Messages, Page, IsDocumentLevel, ObjectRef,
	// ObjModelDetail, WithObjModelDetail, String, Error, MarshalJSON.
	PDFError = pdf.PDFError

	// PDFRef is an indirect object reference, returned by PDFError.ObjectRef.
	//
	// Fields:
	//
	//	ObjNum the object number
	//	GenNum the generation number
	PDFRef = pdf.PDFRef

	// ObjModelDetail locates an object-model finding in the Arlington schema,
	// returned by PDFError.ObjModelDetail.
	//
	// Fields:
	//
	//	TypeName the schema type of the reported container
	//	Key      the key, or decimal array index, that was violated
	//	Entry    for an array finding, the owner dict key holding the array
	ObjModelDetail = pdf.ObjModelDetail
)

// PDF conformance levels.
const (
	A1B       = pdf.A1B
	Undefined = pdf.Undefined
	// ObjectModel is a reporting-only level for the generic ISO 32000
	// object-model checks.
	ObjectModel = pdf.ObjectModel
)

// PDF profiles. A profile is immutable -- AddCheck, RemoveCheck and Clear each
// return a clone -- but these variables are not: reassigning one changes the
// default every later Verify in this package uses, for the whole process. They
// are separate from gopdfrab's variables of the same names.
var (
	// PDF is the default profile for generic ISO 32000 object-model checks.
	PDF = pdf.PDF
	// PDFA1B is the canonical PDF/A-1b profile.
	PDFA1B = pdf.PDFA1B
	// Legacy1B is stricter in some areas and compatible with the original
	// Isartor PDF/A-1b test suite.
	Legacy1B = pdf.Legacy1B
)

// Checks is the registry of every selectable PDF/A check, grouped by area. The
// Colour group keeps the ISO spelling, as ISO 19005 and ISO 32000 use it.
var Checks = pdf.Checks

// Errors callers can match with errors.Is on the result of Open/Verify.
// ErrNotPDF: the input is not a PDF. ErrDamaged: a PDF whose cross-reference or
// trailer structure could not be parsed. ErrEncrypted: an encryption scheme
// gopdfrab does not implement. ErrPasswordRequired: a correct password is needed.
// ErrInternal: a defect in gopdfrab, recovered from a panic; please report it
// with the input file.
var (
	ErrNotPDF           = pdf.ErrNotPDF
	ErrDamaged          = pdf.ErrDamaged
	ErrEncrypted        = pdf.ErrEncrypted
	ErrPasswordRequired = pdf.ErrPasswordRequired
	ErrInternal         = pdf.ErrInternal
)

// Options configures a Verify call. The zero value is the default behavior, so
// callers set only the fields they need. The *Context entry points take an
// Options; the two-argument forms use the zero value.
//
// Password applies at the open step, so it has no effect on the *Document
// methods, whose file is already open (use OpenWithPassword for those). Workers
// applies to VerifyAllContext only.
//
// An Options value is only read, never written, so one may be passed to
// concurrent calls.
type Options struct {
	// Password is the user or owner password for an encrypted input. nil is the
	// empty password.
	Password []byte
	// Workers bounds the concurrency of VerifyAllContext. 0 selects the default
	// (runtime.NumCPU).
	Workers int
}

// NewProfile returns an empty profile for the given conformance level, which
// must be A1B or ObjectModel for Verify to accept it.
func NewProfile(level LevelType) *Profile { return pdf.NewProfile(level) }

// ObjectModelOnly returns a fresh profile equal to [PDF]: only the generic ISO
// 32000 object-model checks, independent of any PDF/A conformance level --
// useful for asking "is this even valid PDF" on its own. Use PDF unless the
// caller wants a profile nothing else shares.
func ObjectModelOnly() *Profile { return pdf.ObjectModelOnly() }

// Profiles returns the ready-made profiles: [PDFA1B], [Legacy1B] and [PDF].
func Profiles() []*Profile { return pdf.Profiles() }

// ProfileByName returns the ready-made profile with the given name, as
// [Profile.Name] reports it: "pdfa1b", "legacy1b" or "pdf". ok is false for
// any other name.
func ProfileByName(name string) (p *Profile, ok bool) { return pdf.ProfileByName(name) }

// AllChecks returns every registered check with its name, description, and
// clause number.
func AllChecks() []Check { return pdf.AllChecks() }

// CheckByClause looks up the registered check for a specific PDF/A-1 (clause,
// subclause) pair.
//
// Deprecated: a clause identifies a check only within a conformance level;
// use CheckByClauseIn.
func CheckByClause(clause string, subclause int) (Check, bool) {
	return pdf.CheckByClause(clause, subclause)
}

// ChecksForClause returns every registered check under the given PDF/A-1
// clause.
//
// Deprecated: a clause identifies checks only within a conformance level; use
// ChecksForClauseIn.
func ChecksForClause(clause string) []Check { return pdf.ChecksForClause(clause) }

// CheckByClauseIn looks up the check that enforces (clause, subclause) under
// the given conformance level, e.g. CheckByClauseIn(A1B, "6.3.4", 1).
func CheckByClauseIn(level LevelType, clause string, subclause int) (Check, bool) {
	return pdf.CheckByClauseIn(level, clause, subclause)
}

// ChecksForClauseIn returns every check that enforces the given clause under
// the given conformance level.
func ChecksForClauseIn(level LevelType, clause string) []Check {
	return pdf.ChecksForClauseIn(level, clause)
}

// Verify opens, verifies, and closes a single file.
func Verify(path string, p *Profile) (Result, error) {
	return verify.VerifyFile(path, p, nil)
}

// VerifyContext is Verify honouring ctx cancellation, with o (Options.Password
// is the only field it uses).
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

// VerifyAll opens, verifies, and closes a batch of files concurrently. It runs
// its own workers, each with its own document, so call it from one goroutine
// rather than fanning it out further.
func VerifyAll(paths []string, p *Profile) ([]FileResult[Result], error) {
	return verify.VerifyAll(paths, p, nil)
}

// VerifyAllContext is VerifyAll honouring ctx cancellation and o; a cancelled
// ctx records ctx.Err() for files not yet started. o.Workers bounds how many
// files are verified at once, and so how many documents are held in memory.
func VerifyAllContext(ctx context.Context, paths []string, p *Profile, o Options) ([]FileResult[Result], error) {
	return verify.VerifyAllContext(ctx, paths, p, o.Password, o.Workers)
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

// Document represents an open PDF file.
//
// A Document is not safe for concurrent use: it holds parse and stream-decode
// caches that its methods fill without synchronization. Open one per goroutine
// rather than sharing one. The package-level Verify functions each open their
// own document, so those are safe to call concurrently.
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

// OpenBytes initializes a document from an in-memory PDF, parsing it the same
// way Open does but without touching disk. Close it when done, as with Open.
func OpenBytes(data []byte) (*Document, error) { return OpenBytesWithPassword(data, nil) }

// OpenBytesWithPassword is OpenBytes with an explicit password for an encrypted
// file. nil is the empty password. It returns an error matching
// ErrPasswordRequired when the password is wrong or missing.
func OpenBytesWithPassword(data, password []byte) (*Document, error) {
	r, err := pdf.OpenBytesWithPassword(data, password)
	if err != nil {
		return nil, err
	}
	return &Document{r: r}, nil
}

// Close releases the file handle and any memory mapping. Calls after the
// first return nil.
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

// XMPMetadata returns the document's raw XMP metadata packet (Root/Metadata),
// decoded and normalised to UTF-8. It returns an error if the document has no
// XMP metadata stream.
func (d *Document) XMPMetadata() ([]byte, error) { return d.r.XMPMetadata() }

// ClaimedConformance returns the PDF/A part and conformance level the
// document's XMP metadata claims, read from the pdfaid namespace. This
// reflects what the file claims, not whether it actually validates. Use
// Verify or IsPDFA to check actual compliance.
func (d *Document) ClaimedConformance() (part, conformance string, err error) {
	return d.r.ClaimedConformance()
}

// PageCount retrieves the page count.
func (d *Document) PageCount() (int, error) { return d.r.PageCount() }

// Version extracts the PDF version from the document header.
func (d *Document) Version() (string, error) { return d.r.Version() }

// Metadata extracts info from the Info dictionary.
func (d *Document) Metadata() (map[string]string, error) { return d.r.Metadata() }
