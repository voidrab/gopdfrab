package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"testing"
)

// encryptKeyMaterial is aesCBCNoPadZeroIV's inverse, for building fixtures.
func encryptKeyMaterial(t *testing.T, key, data []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, data)
	return out
}

// TestAuthenticateR5 verifies that R5 key material, built by the R5 rules
// (plain SHA-256, Adobe Extension Level 3), lets both passwords recover the
// file key. R6's hardened hash is not used for R5.
func TestAuthenticateR5(t *testing.T) {
	fileKey := bytes.Repeat([]byte{0x5A}, 32)
	userPw, ownerPw := []byte("user"), []byte("owner")
	uvs, uks := []byte("uvsalt01"), []byte("uksalt01")
	ovs, oks := []byte("ovsalt01"), []byte("oksalt01")

	u := append(append(hashR5(userPw, uvs, nil), uvs...), uks...)
	ue := encryptKeyMaterial(t, hashR5(userPw, uks, nil), fileKey)
	o := append(append(hashR5(ownerPw, ovs, u), ovs...), oks...)
	oe := encryptKeyMaterial(t, hashR5(ownerPw, oks, u), fileKey)

	for name, pw := range map[string][]byte{"user": userPw, "owner": ownerPw} {
		h := &stdSecurityHandler{v: 5, r: 5, u: u, ue: ue, o: o, oe: oe}
		if err := h.authenticate(pw); err != nil {
			t.Fatalf("%s password: %v", name, err)
		}
		if !bytes.Equal(h.fileKey, fileKey) {
			t.Errorf("%s password recovered key %x, want %x", name, h.fileKey, fileKey)
		}
	}
	h := &stdSecurityHandler{v: 5, r: 5, u: u, ue: ue, o: o, oe: oe}
	if err := h.authenticate([]byte("wrong")); !errors.Is(err, ErrPasswordRequired) {
		t.Errorf("wrong password: err = %v, want ErrPasswordRequired", err)
	}
}

func TestV4KeyLen(t *testing.T) {
	enc := func(encLen, cfLen int) PDFDict {
		filt := map[string]PDFValue{"CFM": PDFName{Value: "AESV2"}}
		if cfLen > 0 {
			filt["Length"] = PDFInteger(cfLen)
		}
		m := map[string]PDFValue{
			"StmF": PDFName{Value: "StdCF"},
			"CF":   PDFDict{Entries: DictOf(map[string]PDFValue{"StdCF": PDFDict{Entries: DictOf(filt)}})},
		}
		if encLen > 0 {
			m["Length"] = PDFInteger(encLen)
		}
		return PDFDict{Entries: DictOf(m)}
	}
	for _, c := range []struct{ encLen, cfLen, want int }{
		{0, 0, 16},   // neither: 128 bits, not the V1-3 default of 40
		{128, 0, 16}, // Encrypt /Length in bits
		{0, 16, 16},  // CF /Length in bytes, as the spec says
		{0, 128, 16}, // CF /Length in bits, as writers also do
		{40, 5, 5},   // CF wins over Encrypt
		{0, 40, 5},
	} {
		if got := v4KeyLen(enc(c.encLen, c.cfLen)); got != c.want {
			t.Errorf("Encrypt /Length %d, CF /Length %d: key length %d, want %d", c.encLen, c.cfLen, got, c.want)
		}
	}
}

// TestUndecryptableStringIsRecorded pins that a string that fails to decrypt
// is kept as stored but reported against its object, once.
func TestUndecryptableStringIsRecorded(t *testing.T) {
	d := &Reader{crypt: &stdSecurityHandler{
		strMethod: cryptAESV2, stmMethod: cryptAESV2, keyLen: 16, fileKey: make([]byte, 16),
	}}
	bad := string(bytes.Repeat([]byte{1}, 20)) // 16-byte IV + 4: not block-aligned
	v := PDFArray{PDFString{Value: bad}, PDFString{Value: bad}}
	d.decryptStrings(v, PDFRef{ObjNum: 9})
	if v[0].(PDFString).Value != bad {
		t.Error("undecryptable string was not kept as stored")
	}
	var n int
	for _, e := range d.Diagnostics() {
		if e.Ref != nil && e.Ref.ObjNum == 9 && e.Kind == DiagStringUndecryptable {
			n++
		}
	}
	if n != 1 {
		t.Errorf("recorded %d diagnostics for object 9, want 1", n)
	}
}
