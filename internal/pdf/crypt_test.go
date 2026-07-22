package pdf

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// The crypt/*.pdf fixtures are real qpdf output, each encrypting base.pdf whose
// content stream and Info /Title both hold the literal marker below. They are a
// true external oracle for every handler revision. isartor-6-1-3-t02-fail-a.pdf
// (RC4-128/R3) in the Isartor corpus is a second, independent one.
const cryptMarker = "SECRET_MARKER_123"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "crypt", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

// decodedContainsMarker reports whether any decrypted, decoded stream holds the
// marker, and returns the resolved trailer /Info /Title string.
func markerAndTitle(t *testing.T, r *Reader) (bool, string) {
	t.Helper()
	found := false
	for objNum := 1; objNum <= 16; objNum++ {
		v, err := r.ResolveReference(PDFRef{ObjNum: objNum})
		if err != nil {
			continue
		}
		if d, ok := v.(PDFDict); ok && d.HasStream {
			if dec, err := DecodeStream(d); err == nil && bytes.Contains(dec, []byte(cryptMarker)) {
				found = true
			}
		}
	}
	var title string
	if info, err := r.ResolveObject(r.EffectiveTrailer().Entries["Info"]); err == nil {
		if d, ok := info.(PDFDict); ok {
			if s, ok := d.Entries["Title"].(PDFString); ok {
				title = s.Value
			}
		}
	}
	return found, title
}

func TestDecryptGoldenFixtures(t *testing.T) {
	cases := []struct {
		file       string
		pw         []byte
		wantV      int
		wantR      int
		wantStrMth cryptMethod
	}{
		{"enc_rc4_40.pdf", nil, 1, 2, cryptRC4},
		{"enc_rc4_128.pdf", nil, 2, 3, cryptRC4},
		{"enc_aesv2.pdf", nil, 4, 4, cryptAESV2},
		{"enc_aesv2_cm.pdf", nil, 4, 4, cryptAESV2},
		{"enc_aesv2_pw.pdf", []byte("userpw"), 4, 4, cryptAESV2},
		{"enc_aesv2_objstm.pdf", nil, 4, 4, cryptAESV2},
		{"enc_aesv3.pdf", nil, 5, 6, cryptAESV3},
		{"enc_aesv3_cm.pdf", nil, 5, 6, cryptAESV3},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			r, err := OpenBytesWithPassword(readFixture(t, c.file), c.pw)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if r.crypt == nil {
				t.Fatal("crypt nil: file not recognised as encrypted")
			}
			if r.crypt.v != c.wantV || r.crypt.r != c.wantR {
				t.Errorf("handler V=%d R=%d, want V=%d R=%d", r.crypt.v, r.crypt.r, c.wantV, c.wantR)
			}
			if r.crypt.strMethod != c.wantStrMth {
				t.Errorf("strMethod=%d, want %d", r.crypt.strMethod, c.wantStrMth)
			}
			if c.file == "enc_aesv2_cm.pdf" && r.crypt.encryptMeta {
				t.Error("EncryptMetadata should be false for cleartext-metadata fixture")
			}
			found, title := markerAndTitle(t, r)
			if !found {
				t.Error("marker not found in any decrypted stream")
			}
			if title != cryptMarker {
				t.Errorf("Info /Title = %q, want %q (string decryption)", title, cryptMarker)
			}
		})
	}
}

func TestDecryptEmptyPasswordViaOpenBytes(t *testing.T) {
	// The zero-argument Open path must transparently decrypt an empty-password
	// file, without any explicit password.
	r, err := OpenBytes(readFixture(t, "enc_aesv3.pdf"))
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	if found, _ := markerAndTitle(t, r); !found {
		t.Error("empty-password file did not decrypt via OpenBytes")
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	// enc_aesv2_pw.pdf has a non-empty user password; the empty password must
	// be rejected with ErrPasswordRequired.
	_, err := OpenBytes(readFixture(t, "enc_aesv2_pw.pdf"))
	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("OpenBytes with wrong password: err=%v, want ErrPasswordRequired", err)
	}
	_, err = OpenBytesWithPassword(readFixture(t, "enc_aesv2_pw.pdf"), []byte("nope"))
	if !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("OpenBytes with bad password: err=%v, want ErrPasswordRequired", err)
	}
}

func TestDecryptOwnerPassword(t *testing.T) {
	// The owner password authenticates via the /O recovery path: Algorithm 7
	// for R4, the R6 owner branch of Algorithm 2.A for R6.
	for _, f := range []string{"enc_aesv2_pw.pdf", "enc_aesv3_pw.pdf"} {
		r, err := OpenBytesWithPassword(readFixture(t, f), []byte("ownerpw"))
		if err != nil {
			t.Fatalf("%s: open with owner password: %v", f, err)
		}
		if found, _ := markerAndTitle(t, r); !found {
			t.Errorf("%s: owner password did not decrypt", f)
		}
	}
}

func TestDecryptR6WrongPassword(t *testing.T) {
	if _, err := OpenBytes(readFixture(t, "enc_aesv3_pw.pdf")); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("R6 empty password: err=%v, want ErrPasswordRequired", err)
	}
}

func TestDecryptIsartorFixture(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "Isartor", "PDFA-1b",
		"6.1 File structure", "6.1.3 File trailer", "isartor-6-1-3-t02-fail-a.pdf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("isartor fixture absent: %v", err)
	}
	r, err := OpenBytes(data)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.crypt == nil {
		t.Fatal("isartor fixture not recognised as encrypted")
	}
	// Every stream must decode cleanly now that RC4 is undone.
	for objNum := 1; objNum <= 20; objNum++ {
		v, err := r.ResolveReference(PDFRef{ObjNum: objNum})
		if err != nil {
			continue
		}
		if d, ok := v.(PDFDict); ok && d.HasStream && !isXRefStream(d) {
			if _, err := DecodeStream(d); err != nil {
				t.Errorf("obj %d: decode after decrypt: %v", objNum, err)
			}
		}
	}
}

func TestUnencryptedFileHasNoHandler(t *testing.T) {
	r, err := OpenBytes(readFixture(t, "base.pdf"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.crypt != nil {
		t.Error("unencrypted file should have nil crypt")
	}
}

func TestCryptFilterMethod(t *testing.T) {
	cf := PDFDict{Entries: map[string]PDFValue{
		"StdCF":  PDFDict{Entries: map[string]PDFValue{"CFM": PDFName{Value: "AESV2"}}},
		"AesCF":  PDFDict{Entries: map[string]PDFValue{"CFM": PDFName{Value: "AESV3"}}},
		"RC4CF":  PDFDict{Entries: map[string]PDFValue{"CFM": PDFName{Value: "V2"}}},
		"NoneCF": PDFDict{Entries: map[string]PDFValue{"CFM": PDFName{Value: "None"}}},
	}}
	cases := []struct {
		name string
		want cryptMethod
	}{
		{"", cryptIdentity},
		{"Identity", cryptIdentity},
		{"Missing", cryptIdentity},
		{"StdCF", cryptAESV2},
		{"AesCF", cryptAESV3},
		{"RC4CF", cryptRC4},
		{"NoneCF", cryptIdentity},
	}
	for _, c := range cases {
		if got := cryptFilterMethod(c.name, cf); got != c.want {
			t.Errorf("cryptFilterMethod(%q)=%d, want %d", c.name, got, c.want)
		}
	}
}

func TestIdentityStmFPassesThrough(t *testing.T) {
	// A V4 handler with StmF=Identity leaves stream bytes untouched.
	h := &stdSecurityHandler{stmMethod: cryptIdentity, strMethod: cryptRC4, fileKey: make([]byte, 16), keyLen: 16}
	in := []byte("plain stream bytes")
	out, err := h.decrypt(in, 7, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Errorf("Identity decrypt changed bytes: %q", out)
	}
	if &out[0] == &in[0] {
		t.Error("Identity decrypt must return a fresh slice, not alias input")
	}
}

func TestAESShortInputYieldsEmpty(t *testing.T) {
	// A stored AES value shorter than one block (no room for the IV) decodes to
	// empty rather than panicking.
	out, err := aesCBCDecrypt(make([]byte, 16), []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("want empty, got %q", out)
	}
}

func TestAESBadCiphertextLength(t *testing.T) {
	// IV present but the trailing ciphertext is not block-aligned.
	if _, err := aesCBCDecrypt(make([]byte, 16), make([]byte, 16+5)); !errors.Is(err, ErrEncrypted) {
		t.Errorf("err=%v, want ErrEncrypted", err)
	}
	if _, err := aesCBCNoPadZeroIV(make([]byte, 16), make([]byte, 5)); !errors.Is(err, ErrEncrypted) {
		t.Errorf("no-pad err=%v, want ErrEncrypted", err)
	}
}

func TestInvalidKeyLengthRejected(t *testing.T) {
	enc := PDFDict{Entries: map[string]PDFValue{
		"Filter": PDFName{Value: "Standard"},
		"V":      PDFInteger(2),
		"R":      PDFInteger(3),
		"Length": PDFInteger(8), // 1 byte -- too short
	}}
	if _, err := newStdSecurityHandler(enc, nil, 1, nil); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("err=%v, want ErrEncrypted", err)
	}
}

func TestObjectKeyR6IsFileKey(t *testing.T) {
	h := &stdSecurityHandler{r: 6, fileKey: bytes.Repeat([]byte{0xAB}, 32)}
	if !bytes.Equal(h.objectKey(3, 0, cryptAESV3), h.fileKey) {
		t.Error("R6 object key must be the file key unchanged")
	}
}

func TestIsXRefStream(t *testing.T) {
	if !isXRefStream(PDFDict{Entries: map[string]PDFValue{"Type": PDFName{Value: "XRef"}}}) {
		t.Error("XRef stream not recognised")
	}
	if isXRefStream(PDFDict{Entries: map[string]PDFValue{"Type": PDFName{Value: "ObjStm"}}}) {
		t.Error("ObjStm wrongly recognised as XRef")
	}
}

func TestDecryptPdfgenSeed(t *testing.T) {
	// The encrypted fuzz seed must open and decrypt cleanly, so the whole-file
	// fuzz targets replay a real decrypt path rather than an undecodable blob.
	var seed []byte
	for _, s := range pdfgen.Seeds() {
		if bytes.Contains(s, []byte("/Encrypt")) {
			seed = s
			break
		}
	}
	if seed == nil {
		t.Fatal("no encrypted seed in pdfgen.Seeds()")
	}
	r, err := OpenBytes(seed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.crypt == nil {
		t.Fatal("seed not recognised as encrypted")
	}
	v, err := r.ResolveReference(PDFRef{ObjNum: 4})
	if err != nil {
		t.Fatalf("resolve content: %v", err)
	}
	dec, err := DecodeStream(v.(PDFDict))
	if err != nil {
		t.Fatalf("decode after decrypt: %v", err)
	}
	if !bytes.Contains(dec, []byte("q")) {
		t.Errorf("content stream decrypted to %q, want the q/Q operators", dec)
	}
}

func TestUnsupportedSecurityHandler(t *testing.T) {
	enc := PDFDict{Entries: map[string]PDFValue{"Filter": PDFName{Value: "Custom"}}}
	_, err := newStdSecurityHandler(enc, nil, 1, nil)
	if !errors.Is(err, ErrEncrypted) {
		t.Fatalf("err=%v, want ErrEncrypted", err)
	}
}
