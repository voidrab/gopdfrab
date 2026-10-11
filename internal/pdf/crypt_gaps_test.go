package pdf

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestDecryptAESV2WithShortFileKey: a V4 handler whose crypt filter declares a
// 5-byte key but uses AESV2 derives a 10-byte object key, which is not a valid
// AES key size; that surfaces as an error rather than a panic.
func TestDecryptAESV2WithShortFileKey(t *testing.T) {
	h := &stdSecurityHandler{r: 4, v: 4, keyLen: 5, fileKey: make([]byte, 5),
		stmMethod: cryptAESV2, strMethod: cryptAESV2}
	if _, err := h.decrypt(make([]byte, 32), 1, 0, false); err == nil {
		t.Error("expected an invalid AES key size error")
	}
}

func TestFirstIDBytes(t *testing.T) {
	id := PDFArray{PDFString{Value: "abc"}, PDFString{Value: "def"}}
	withID := PDFDict{Entries: DictOf(map[string]PDFValue{"ID": id})}
	noID := PDFDict{Entries: DictOf(map[string]PDFValue{})}

	for name, tc := range map[string]struct {
		r    *Reader
		want string
	}{
		"main trailer":       {&Reader{trailer: withID}, "abc"},
		"first-page trailer": {&Reader{trailer: noID, firstPageTrailer: withID}, "abc"},
		"absent":             {&Reader{trailer: noID}, ""},
		"not an array":       {&Reader{trailer: PDFDict{Entries: DictOf(map[string]PDFValue{"ID": PDFInteger(1)})}}, ""},
		"empty array":        {&Reader{trailer: PDFDict{Entries: DictOf(map[string]PDFValue{"ID": PDFArray{}})}}, ""},
	} {
		if got := string(tc.r.firstIDBytes()); got != tc.want {
			t.Errorf("%s: firstIDBytes = %q, want %q", name, got, tc.want)
		}
	}
}

// TestEncryptEntryUnresolvable: an /Encrypt reference whose object cannot be
// parsed is an encryption error, not a plaintext open.
func TestEncryptEntryUnresolvable(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>")
	b.Obj(4, "<< /Filter /Standard /O (unterminated")
	data := b.FinishClassic("<< /Size 5 /Root 1 0 R /Encrypt 4 0 R >>")
	if _, err := OpenBytes(data); !errors.Is(err, ErrEncrypted) {
		t.Errorf("err = %v, want ErrEncrypted", err)
	}
}
