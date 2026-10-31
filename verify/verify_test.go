package verify_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/verify"
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

// passFixture returns a veraPDF PDF/A-1b "pass" file: one that opens, carries
// XMP metadata and claims conformance.
func passFixture(t *testing.T) string {
	t.Helper()
	matches, _ := filepath.Glob("../tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/*-pass-*.pdf")
	if len(matches) == 0 {
		t.Fatal("no veraPDF pass fixture under ../tests/veraPDF")
	}
	return matches[0]
}

// TestSentinels pins each sentinel to its internal identity so errors.Is
// matches whichever package a caller imports.
func TestSentinels(t *testing.T) {
	for name, p := range map[string][2]error{
		"ErrNotPDF":           {verify.ErrNotPDF, pdf.ErrNotPDF},
		"ErrDamaged":          {verify.ErrDamaged, pdf.ErrDamaged},
		"ErrEncrypted":        {verify.ErrEncrypted, pdf.ErrEncrypted},
		"ErrPasswordRequired": {verify.ErrPasswordRequired, pdf.ErrPasswordRequired},
		"ErrInternal":         {verify.ErrInternal, pdf.ErrInternal},
	} {
		if p[0] == nil || !errors.Is(p[1], p[0]) {
			t.Errorf("%s does not match its internal sentinel", name)
		}
	}
}

func TestRegistry(t *testing.T) {
	if p := verify.NewProfile(verify.A1B); p == nil || p.Level != verify.A1B {
		t.Fatalf("NewProfile(A1B) = %v", p)
	}
	if p := verify.ObjectModelOnly(); p.Level != verify.ObjectModel || p == verify.PDF {
		t.Errorf("ObjectModelOnly = %v, want a fresh ObjectModel profile", p)
	}
	all := verify.AllChecks()
	if len(all) == 0 {
		t.Fatal("AllChecks returned no checks")
	}
	c := all[0]
	if got, ok := verify.CheckByClause(c.Clause(), c.Subclause()); !ok || got != c {
		t.Errorf("CheckByClause(%q, %d) = %v, %v", c.Clause(), c.Subclause(), got, ok)
	}
	if len(verify.ChecksForClause(c.Clause())) == 0 {
		t.Errorf("ChecksForClause(%q) returned nothing", c.Clause())
	}
	clause, sub, ok := c.ClauseIn(verify.A1B)
	if !ok {
		t.Fatalf("%s has no clause under A1B", c.Name())
	}
	if got, ok := verify.CheckByClauseIn(verify.A1B, clause, sub); !ok || got != c {
		t.Errorf("CheckByClauseIn(A1B, %q, %d) = %v, %v", clause, sub, got, ok)
	}
	if len(verify.ChecksForClauseIn(verify.A1B, clause)) == 0 {
		t.Errorf("ChecksForClauseIn(A1B, %q) returned nothing", clause)
	}
}

// TestVerifyEntryPoints runs every package-level verify function over one
// conformant file and one plain PDF, and checks they agree with each other.
func TestVerifyEntryPoints(t *testing.T) {
	path := passFixture(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	fromPath, err := verify.Verify(path, verify.PDFA1B)
	if err != nil || !fromPath.Valid {
		t.Fatalf("Verify = (Valid %v, %v), want a valid file", fromPath.Valid, err)
	}
	if res, err := verify.VerifyContext(ctx, path, verify.PDFA1B, verify.Options{}); err != nil || res.Valid != fromPath.Valid {
		t.Errorf("VerifyContext = (Valid %v, %v)", res.Valid, err)
	}
	if res, err := verify.VerifyBytes(data, verify.PDFA1B); err != nil || res.Valid != fromPath.Valid {
		t.Errorf("VerifyBytes = (Valid %v, %v)", res.Valid, err)
	}
	if res, err := verify.VerifyBytesContext(ctx, []byte(plainPDF), verify.PDFA1B, verify.Options{}); err != nil || res.Valid {
		t.Errorf("VerifyBytesContext(plain) = (Valid %v, %v), want issues", res.Valid, err)
	}
	if _, err := verify.VerifyObjectModel(path); err != nil {
		t.Errorf("VerifyObjectModel: %v", err)
	}
	if res, err := verify.VerifyObjectModelBytes([]byte(plainPDF)); err != nil || res.Type != verify.ObjectModel {
		t.Errorf("VerifyObjectModelBytes = (Type %v, %v)", res.Type, err)
	}

	missing := filepath.Join(t.TempDir(), "missing.pdf")
	batch, err := verify.VerifyAll([]string{path, missing}, verify.PDFA1B)
	if err != nil || len(batch) != 2 {
		t.Fatalf("VerifyAll = (%d results, %v)", len(batch), err)
	}
	if batch[0].Err != nil || !batch[0].Result.Valid || batch[1].Err == nil {
		t.Errorf("VerifyAll results = %+v", batch)
	}
	batch, err = verify.VerifyAllContext(ctx, []string{path}, verify.PDFA1B, verify.Options{Workers: 1})
	if err != nil || len(batch) != 1 || batch[0].Err != nil {
		t.Errorf("VerifyAllContext = (%+v, %v)", batch, err)
	}
}

// TestVerifyOptionsPassword confirms Options.Password reaches the open step.
func TestVerifyOptionsPassword(t *testing.T) {
	data, err := os.ReadFile("../internal/pdf/testdata/crypt/enc_aesv2_pw.pdf")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := verify.VerifyBytesContext(ctx, data, verify.PDFA1B, verify.Options{}); !errors.Is(err, verify.ErrPasswordRequired) {
		t.Errorf("without a password: err = %v, want ErrPasswordRequired", err)
	}
	if _, err := verify.VerifyBytesContext(ctx, data, verify.PDFA1B, verify.Options{Password: []byte("ownerpw")}); err != nil {
		t.Errorf("with the password: %v", err)
	}
}

func TestVerifyContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verify.VerifyBytesContext(ctx, []byte(plainPDF), verify.PDFA1B, verify.Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestDocument exercises every Document method on a conformant file.
func TestDocument(t *testing.T) {
	doc, err := verify.Open(passFixture(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	if res, err := doc.Verify(verify.PDFA1B); err != nil || !res.Valid {
		t.Errorf("Verify = (Valid %v, %v)", res.Valid, err)
	}
	if _, err := doc.VerifyContext(context.Background(), verify.Legacy1B); err != nil {
		t.Errorf("VerifyContext: %v", err)
	}
	if res, err := doc.VerifyObjectModel(); err != nil || res.Type != verify.ObjectModel {
		t.Errorf("VerifyObjectModel = (Type %v, %v)", res.Type, err)
	}
	if ok, err := doc.IsPDFA(); err != nil || !ok {
		t.Errorf("IsPDFA = (%v, %v), want (true, nil)", ok, err)
	}
	if _, err := doc.IsPDF(); err != nil {
		t.Errorf("IsPDF: %v", err)
	}
	if xmp, err := doc.XMPMetadata(); err != nil || len(xmp) == 0 {
		t.Errorf("XMPMetadata = (%d bytes, %v)", len(xmp), err)
	}
	if part, _, err := doc.ClaimedConformance(); err != nil || part != "1" {
		t.Errorf("ClaimedConformance part = (%q, %v), want \"1\"", part, err)
	}
	if n, err := doc.PageCount(); err != nil || n < 1 {
		t.Errorf("PageCount = (%d, %v)", n, err)
	}
	if v, err := doc.Version(); err != nil || v == "" {
		t.Errorf("Version = (%q, %v)", v, err)
	}
	// Info is optional in PDF/A; only the passthrough is under test.
	doc.Metadata()

	// IsPDFA and IsPDF fail only when the verify beneath them does, which for
	// their fixed profiles takes an undefined conformance level.
	savedPDFA, savedPDF := verify.PDFA1B, verify.PDF
	verify.PDFA1B, verify.PDF = verify.NewProfile(verify.Undefined), verify.NewProfile(verify.Undefined)
	defer func() { verify.PDFA1B, verify.PDF = savedPDFA, savedPDF }()
	if ok, err := doc.IsPDFA(); err == nil || ok {
		t.Errorf("IsPDFA with undefined profile = (%v, %v), want (false, error)", ok, err)
	}
	if ok, err := doc.IsPDF(); err == nil || ok {
		t.Errorf("IsPDF with undefined profile = (%v, %v), want (false, error)", ok, err)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := verify.Open(filepath.Join(t.TempDir(), "missing.pdf")); err == nil {
		t.Error("Open of a missing file returned nil error")
	}
	if _, err := verify.OpenBytes([]byte("not a pdf")); !errors.Is(err, verify.ErrNotPDF) {
		t.Errorf("OpenBytes(non-PDF) err = %v, want ErrNotPDF", err)
	}
}

func TestOpenWithPassword(t *testing.T) {
	const fixture = "../internal/pdf/testdata/crypt/enc_aesv2_pw.pdf"
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verify.OpenBytesWithPassword(data, nil); !errors.Is(err, verify.ErrPasswordRequired) {
		t.Errorf("OpenBytesWithPassword(nil) err = %v, want ErrPasswordRequired", err)
	}
	if _, err := verify.OpenWithPassword(fixture, nil); !errors.Is(err, verify.ErrPasswordRequired) {
		t.Errorf("OpenWithPassword(nil) err = %v, want ErrPasswordRequired", err)
	}
	doc, err := verify.OpenBytes([]byte(plainPDF))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if err := doc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	doc, err = verify.OpenWithPassword(fixture, []byte("ownerpw"))
	if err != nil {
		t.Fatalf("OpenWithPassword: %v", err)
	}
	defer doc.Close()
	if _, err := doc.Verify(verify.PDFA1B); err != nil {
		t.Errorf("Verify on the decrypted document: %v", err)
	}
}

func TestLimits(t *testing.T) {
	saved := verify.CurrentLimits()
	defer verify.SetLimits(saved)

	want := verify.Limits{MaxDecodedStreamBytes: 1 << 20, MaxResidentBytes: 1 << 10}
	verify.SetLimits(want)
	if got := verify.CurrentLimits(); got != want {
		t.Errorf("CurrentLimits = %+v, want %+v", got, want)
	}
	verify.SetLimits(verify.Limits{})
	if got := verify.CurrentLimits(); got != verify.DefaultLimits() {
		t.Errorf("after reset CurrentLimits = %+v, want the defaults %+v", got, verify.DefaultLimits())
	}
}
