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
//
// Regenerating the two password-matrix fixtures (qpdf 12+ needs
// --allow-weak-crypto for RC4):
//
//	qpdf --allow-weak-crypto --encrypt --user-password=userpw \
//	     --owner-password=ownerpw --bits=40 -- base.pdf enc_rc4_40_pw.pdf
//	qpdf --encrypt --user-password="$(printf 'p%.0s' {1..127})" \
//	     --owner-password=ownerpw --bits=256 -- base.pdf enc_aesv3_longpw.pdf
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

// TestCleartextMetadataStreamIsNotDecrypted covers /EncryptMetadata false
// (ISO 32000-1 7.6.3.3): the metadata stream is left in the clear so an indexer
// can read it without the password, so decrypting it anyway yields garbage --
// and under AES it does not even decode, because the plaintext is not
// block-aligned.
//
// The two older cleartext-metadata fixtures set the flag but carry no metadata
// stream at all, so they pinned the parsed flag and not the behaviour it
// governs. This fixture has one.
func TestCleartextMetadataStreamIsNotDecrypted(t *testing.T) {
	r, err := OpenBytes(readFixture(t, "enc_aesv3_cm_meta.pdf"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if r.crypt == nil || r.crypt.encryptMeta {
		t.Fatalf("fixture should be encrypted with EncryptMetadata false (crypt=%v)", r.crypt)
	}

	data, meta, err := r.RawXMP()
	if err != nil {
		t.Fatalf("RawXMP: %v", err)
	}
	if !meta.HasStream {
		t.Fatal("catalog /Metadata is not a stream")
	}
	if !bytes.Contains(data, []byte("<x:xmpmeta")) || !bytes.Contains(data, []byte("pdfaid")) {
		t.Errorf("metadata stream did not survive as cleartext XMP: %q", firstBytes(data, 80))
	}

	// The rest of the document is still encrypted and must still decrypt.
	if found, _ := markerAndTitle(t, r); !found {
		t.Error("content stream did not decrypt")
	}
}

func firstBytes(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// TestDecryptOwnerPasswordR2 covers the R2 arm of Algorithm 7, where /O is
// unwrapped with a single RC4 pass rather than the 20 iterations R3+ uses.
// The other owner-password fixtures are all R4/R6, so without this the R2
// branch would go unexercised.
func TestDecryptOwnerPasswordR2(t *testing.T) {
	data := readFixture(t, "enc_rc4_40_pw.pdf")

	r, err := OpenBytesWithPassword(data, []byte("ownerpw"))
	if err != nil {
		t.Fatalf("open with owner password: %v", err)
	}
	if r.crypt.r != 2 {
		t.Fatalf("fixture R=%d, want an R2 file", r.crypt.r)
	}
	if found, title := markerAndTitle(t, r); !found || title != cryptMarker {
		t.Errorf("owner password did not decrypt R2 (marker=%v title=%q)", found, title)
	}

	// The user password still works, and a wrong one is still rejected.
	if _, err := OpenBytesWithPassword(data, []byte("userpw")); err != nil {
		t.Errorf("open with user password: %v", err)
	}
	if _, err := OpenBytesWithPassword(data, []byte("nope")); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("R2 bad password: err=%v, want ErrPasswordRequired", err)
	}
}

// TestDecryptR6PasswordTruncatedAt127 pins Algorithm 2.A's password limit: an
// R6 password is truncated to 127 bytes before hashing, so anything appended
// past that must not change the outcome.
func TestDecryptR6PasswordTruncatedAt127(t *testing.T) {
	data := readFixture(t, "enc_aesv3_longpw.pdf")
	pw := bytes.Repeat([]byte("p"), 127)

	r, err := OpenBytesWithPassword(data, pw)
	if err != nil {
		t.Fatalf("open with the exact 127-byte password: %v", err)
	}
	if found, _ := markerAndTitle(t, r); !found {
		t.Fatal("127-byte password did not decrypt")
	}

	long := append(append([]byte(nil), pw...), []byte("ignored past the limit")...)
	r2, err := OpenBytesWithPassword(data, long)
	if err != nil {
		t.Fatalf("open with a %d-byte password: %v", len(long), err)
	}
	if found, _ := markerAndTitle(t, r2); !found {
		t.Error("password past 127 bytes was not truncated")
	}
}

// TestStringBytesForms: /O, /U and /OE may be written as literal or hex
// strings. Both must yield the same raw bytes, since the key derivation hashes
// them directly.
func TestStringBytesForms(t *testing.T) {
	want := []byte{0xDE, 0xAD, 0x00, 0xBE}
	if got := stringBytes(PDFString{Value: string(want)}); !bytes.Equal(got, want) {
		t.Errorf("literal string = % x, want % x", got, want)
	}
	if got := stringBytes(PDFHexString{Value: "DEAD00BE"}); !bytes.Equal(got, want) {
		t.Errorf("hex string = % x, want % x", got, want)
	}
	if got := stringBytes(PDFInteger(7)); got != nil {
		t.Errorf("non-string = % x, want nil", got)
	}

	dict := PDFDict{Entries: map[string]PDFValue{
		"O":    PDFString{Value: string(want)},
		"CFM":  PDFName{Value: "AESV2"},
		"Flag": PDFBoolean(false),
	}}
	if got := dictBytes(dict, "O"); !bytes.Equal(got, want) {
		t.Errorf("dictBytes(O) = % x, want % x", got, want)
	}
	if name, ok := dictName(dict, "CFM"); !ok || name != "AESV2" {
		t.Errorf("dictName(CFM) = %q, %v; want \"AESV2\", true", name, ok)
	}
	if _, ok := dictName(dict, "Absent"); ok {
		t.Error("dictName found a missing key")
	}
	if _, ok := dictName(dict, "Flag"); ok {
		t.Error("dictName accepted a non-name value")
	}
	if got := dictBool(dict, "Flag", true); got {
		t.Error("dictBool ignored the stored false")
	}
	if got := dictBool(dict, "Absent", true); !got {
		t.Error("dictBool did not fall back to the default")
	}
}

// TestDecryptUnknownMethod: a crypt method outside the supported set is an
// error, not a silent pass-through of ciphertext as if it were plaintext.
func TestDecryptUnknownMethod(t *testing.T) {
	h := &stdSecurityHandler{stmMethod: cryptMethod(99), strMethod: cryptMethod(99), keyLen: 16}
	if _, err := h.decrypt([]byte("data"), 1, 0, false); !errors.Is(err, ErrEncrypted) {
		t.Errorf("stream decrypt err = %v, want ErrEncrypted", err)
	}
	if _, err := h.decrypt([]byte("data"), 1, 0, true); !errors.Is(err, ErrEncrypted) {
		t.Errorf("string decrypt err = %v, want ErrEncrypted", err)
	}
}

// TestEncryptEntryNotADictionary: /Encrypt pointing at something other than a
// dictionary must be reported as an encryption error rather than opening the
// document as if it were plaintext.
func TestEncryptEntryNotADictionary(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	b.Obj(4, "42") // not a dictionary
	data := b.FinishClassic("<< /Size 5 /Root 1 0 R /Encrypt 4 0 R >>")

	if _, err := OpenBytes(data); !errors.Is(err, ErrEncrypted) {
		t.Errorf("err = %v, want ErrEncrypted", err)
	}
}
