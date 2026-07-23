package pdf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
)

// cryptMethod is the cipher a crypt filter applies.
type cryptMethod int

const (
	cryptIdentity cryptMethod = iota
	cryptRC4
	cryptAESV2 // AES-128-CBC
	cryptAESV3 // AES-256-CBC
)

// passwordPad is the 32-byte padding string (ISO 32000-1 Algorithm 2, step a).
var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41,
	0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// stdSecurityHandler decrypts a document sealed with the Standard security
// handler (ISO 32000-1 §7.6.3, ISO 32000-2 for R6), once a password has
// authenticated. It holds the derived file key and the per-string/per-stream
// cipher, and is applied per object by the resolver.
type stdSecurityHandler struct {
	r, v, keyLen  int    // keyLen in bytes
	o, u, oe, ue  []byte // raw dict entries; oe/ue R6 only
	p             int32
	encryptMeta   bool
	idBytes       []byte // trailer /ID[0], raw
	encryptObjNum int
	fileKey       []byte
	strMethod     cryptMethod
	stmMethod     cryptMethod
}

// stringBytes returns a string value's raw bytes. Literal strings are already
// decoded (PDFString.Value holds bytes); hex strings still hold hex text.
func stringBytes(v PDFValue) []byte {
	switch s := v.(type) {
	case PDFString:
		return []byte(s.Value)
	case PDFHexString:
		return DecodePDFHexStringBytes(s.Value)
	}
	return nil
}

func dictBytes(dict PDFDict, key string) []byte {
	return stringBytes(dict.Entries[key])
}

func dictName(dict PDFDict, key string) (string, bool) {
	if n, ok := dict.Entries[key].(PDFName); ok {
		return n.Value, true
	}
	return "", false
}

func dictBool(dict PDFDict, key string, def bool) bool {
	if b, ok := dict.Entries[key].(PDFBoolean); ok {
		return bool(b)
	}
	return def
}

// newStdSecurityHandler parses the Encrypt dictionary, derives the file key,
// and authenticates password (nil == empty). It returns ErrPasswordRequired
// when no password variant authenticates and ErrEncrypted for a handler it
// does not implement.
func newStdSecurityHandler(enc PDFDict, id0 []byte, encObjNum int, password []byte) (*stdSecurityHandler, error) {
	if name, _ := dictName(enc, "Filter"); name != "Standard" {
		return nil, fmt.Errorf("%w: unsupported security handler %q", ErrEncrypted, name)
	}
	h := &stdSecurityHandler{
		v:             DictInt(enc, "V", 0),
		r:             DictInt(enc, "R", 0),
		o:             dictBytes(enc, "O"),
		u:             dictBytes(enc, "U"),
		oe:            dictBytes(enc, "OE"),
		ue:            dictBytes(enc, "UE"),
		p:             int32(DictInt(enc, "P", 0)),
		encryptMeta:   dictBool(enc, "EncryptMetadata", true),
		idBytes:       id0,
		encryptObjNum: encObjNum,
	}

	switch h.v {
	case 1:
		h.keyLen = 5
	case 5:
		h.keyLen = 32
	default:
		h.keyLen = DictInt(enc, "Length", 40) / 8
	}
	if h.v != 5 && (h.keyLen < 5 || h.keyLen > 16) {
		return nil, fmt.Errorf("%w: invalid key length %d", ErrEncrypted, h.keyLen*8)
	}

	if h.v >= 4 {
		cf, _ := enc.Entries["CF"].(PDFDict)
		stmf, _ := dictName(enc, "StmF")
		strf, _ := dictName(enc, "StrF")
		h.stmMethod = cryptFilterMethod(stmf, cf)
		h.strMethod = cryptFilterMethod(strf, cf)
	} else {
		h.stmMethod = cryptRC4
		h.strMethod = cryptRC4
	}

	if err := h.authenticate(password); err != nil {
		return nil, err
	}
	return h, nil
}

// cryptFilterMethod maps a StmF/StrF name to its cipher via the /CF dict.
func cryptFilterMethod(name string, cf PDFDict) cryptMethod {
	if name == "" || name == "Identity" {
		return cryptIdentity
	}
	filt, ok := cf.Entries[name].(PDFDict)
	if !ok {
		return cryptIdentity
	}
	cfm, _ := dictName(filt, "CFM")
	switch cfm {
	case "V2":
		return cryptRC4
	case "AESV2":
		return cryptAESV2
	case "AESV3":
		return cryptAESV3
	default:
		return cryptIdentity
	}
}

func (h *stdSecurityHandler) authenticate(password []byte) error {
	if h.r >= 5 {
		key, err := h.deriveKeyR6(password)
		if err != nil {
			return err
		}
		h.fileKey = key
		return nil
	}
	if key := h.deriveKeyR234(password); h.authUserR234(key) {
		h.fileKey = key
		return nil
	}
	if userPw, ok := h.authOwnerR234(password); ok {
		h.fileKey = h.deriveKeyR234(userPw)
		return nil
	}
	return ErrPasswordRequired
}

func padPassword(pw []byte) []byte {
	out := make([]byte, 32)
	n := copy(out, pw)
	copy(out[n:], passwordPad)
	return out
}

// deriveKeyR234 computes the file key for R2-4 (Algorithm 2).
func (h *stdSecurityHandler) deriveKeyR234(pw []byte) []byte {
	hsh := md5.New()
	hsh.Write(padPassword(pw))
	hsh.Write(h.o)
	var pbuf [4]byte
	binary.LittleEndian.PutUint32(pbuf[:], uint32(h.p))
	hsh.Write(pbuf[:])
	hsh.Write(h.idBytes)
	if h.r >= 4 && !h.encryptMeta {
		hsh.Write([]byte{0xff, 0xff, 0xff, 0xff})
	}
	sum := hsh.Sum(nil)
	if h.r >= 3 {
		for range 50 {
			s := md5.Sum(sum[:h.keyLen])
			sum = s[:]
		}
	}
	key := make([]byte, h.keyLen)
	copy(key, sum)
	return key
}

// authUserR234 checks key against /U (Algorithm 4 for R2, Algorithm 5 for R3+).
func (h *stdSecurityHandler) authUserR234(key []byte) bool {
	if h.r == 2 {
		c, err := rc4.NewCipher(key)
		if err != nil {
			return false
		}
		out := make([]byte, 32)
		c.XORKeyStream(out, passwordPad)
		return len(h.u) >= 32 && bytes.Equal(out, h.u[:32])
	}
	hsh := md5.New()
	hsh.Write(passwordPad)
	hsh.Write(h.idBytes)
	out := hsh.Sum(nil)
	c, err := rc4.NewCipher(key)
	if err != nil {
		return false
	}
	c.XORKeyStream(out, out)
	for i := 1; i <= 19; i++ {
		out = rc4XORWithKeyByte(out, key, byte(i))
	}
	return len(h.u) >= 16 && bytes.Equal(out, h.u[:16])
}

// authOwnerR234 recovers the user password from /O and validates it
// (Algorithm 7). It returns the recovered padded user password on success.
func (h *stdSecurityHandler) authOwnerR234(pw []byte) ([]byte, bool) {
	hsh := md5.New()
	hsh.Write(padPassword(pw))
	sum := hsh.Sum(nil)
	if h.r >= 3 {
		for range 50 {
			s := md5.Sum(sum[:h.keyLen])
			sum = s[:]
		}
	}
	rc4Key := make([]byte, h.keyLen)
	copy(rc4Key, sum)

	user := make([]byte, len(h.o))
	copy(user, h.o)
	if h.r == 2 {
		c, err := rc4.NewCipher(rc4Key)
		if err != nil {
			return nil, false
		}
		c.XORKeyStream(user, user)
	} else {
		for i := 19; i >= 0; i-- {
			user = rc4XORWithKeyByte(user, rc4Key, byte(i))
		}
	}
	if key := h.deriveKeyR234(user); h.authUserR234(key) {
		return user, true
	}
	return nil, false
}

// rc4XORWithKeyByte RC4-transforms data with each key byte XORed by b.
func rc4XORWithKeyByte(data, key []byte, b byte) []byte {
	k := make([]byte, len(key))
	for j := range key {
		k[j] = key[j] ^ b
	}
	c, err := rc4.NewCipher(k)
	if err != nil {
		return data
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// deriveKeyR6 retrieves the 32-byte file key for R6 (ISO 32000-2 Algorithm 2.A),
// trying the user password then the owner password.
func (h *stdSecurityHandler) deriveKeyR6(pw []byte) ([]byte, error) {
	if len(pw) > 127 {
		pw = pw[:127]
	}
	if len(h.u) >= 48 {
		if bytes.Equal(hash2B(pw, h.u[32:40], nil), h.u[:32]) {
			ik := hash2B(pw, h.u[40:48], nil)
			return aesCBCNoPadZeroIV(ik, h.ue)
		}
	}
	if len(h.o) >= 48 && len(h.u) >= 48 {
		if bytes.Equal(hash2B(pw, h.o[32:40], h.u[:48]), h.o[:32]) {
			ik := hash2B(pw, h.o[40:48], h.u[:48])
			return aesCBCNoPadZeroIV(ik, h.oe)
		}
	}
	return nil, ErrPasswordRequired
}

// hash2B is the R6 hardened hash (ISO 32000-2 Algorithm 2.B).
func hash2B(pw, salt, udata []byte) []byte {
	s := sha256.Sum256(concat(pw, salt, udata))
	k := s[:]
	var e []byte
	for i := 0; i < 64 || int(e[len(e)-1]) > i-32; i++ {
		k1 := bytes.Repeat(concat(pw, k, udata), 64)
		cb, err := aes.NewCipher(k[:16])
		if err != nil {
			return nil
		}
		e = make([]byte, len(k1))
		cipher.NewCBCEncrypter(cb, k[16:32]).CryptBlocks(e, k1)
		sum := 0
		for j := range 16 {
			sum += int(e[j])
		}
		switch sum % 3 {
		case 0:
			h := sha256.Sum256(e)
			k = h[:]
		case 1:
			h := sha512.Sum384(e)
			k = h[:]
		case 2:
			h := sha512.Sum512(e)
			k = h[:]
		}
	}
	return k[:32]
}

func concat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// objectKey derives the per-object key (Algorithm 1). R6 uses the file key
// directly.
func (h *stdSecurityHandler) objectKey(objNum, genNum int, m cryptMethod) []byte {
	if h.r >= 5 {
		return h.fileKey
	}
	hsh := md5.New()
	hsh.Write(h.fileKey)
	b := []byte{
		byte(objNum), byte(objNum >> 8), byte(objNum >> 16),
		byte(genNum), byte(genNum >> 8),
	}
	hsh.Write(b)
	if m == cryptAESV2 {
		hsh.Write([]byte{0x73, 0x41, 0x6c, 0x54}) // "sAlT"
	}
	sum := hsh.Sum(nil)
	return sum[:min(h.keyLen+5, 16)]
}

// decrypt returns a fresh slice holding data decrypted for the given object.
// It never mutates data, which may alias a read-only memory map.
func (h *stdSecurityHandler) decrypt(data []byte, objNum, genNum int, isString bool) ([]byte, error) {
	m := h.stmMethod
	if isString {
		m = h.strMethod
	}
	switch m {
	case cryptIdentity:
		return append([]byte(nil), data...), nil
	case cryptRC4:
		c, err := rc4.NewCipher(h.objectKey(objNum, genNum, m))
		if err != nil {
			return nil, err
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out, nil
	case cryptAESV2, cryptAESV3:
		return aesCBCDecrypt(h.objectKey(objNum, genNum, m), data)
	}
	return nil, fmt.Errorf("%w: unknown crypt method", ErrEncrypted)
}

// aesCBCDecrypt decrypts a stored value: a 16-byte IV prefix then AES-CBC
// ciphertext, PKCS#7-unpadded leniently.
func aesCBCDecrypt(key, data []byte) ([]byte, error) {
	if len(data) < aes.BlockSize {
		return []byte{}, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	iv, ct := data[:aes.BlockSize], data[aes.BlockSize:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: AES ciphertext not block-aligned", ErrEncrypted)
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
	if n := len(out); n > 0 {
		if pad := int(out[n-1]); pad > 0 && pad <= aes.BlockSize && pad <= n {
			out = out[:n-pad]
		}
	}
	return out, nil
}

// setupDecryption builds d.crypt from the trailer's /Encrypt entry, if any.
// It runs at the end of initializeStructure, while d.crypt is still nil, so
// the Encrypt dictionary and trailer /ID are read without decryption.
func (d *Reader) setupDecryption() error {
	encRef := d.trailer.Entries["Encrypt"]
	if encRef == nil {
		encRef = d.EffectiveTrailer().Entries["Encrypt"]
	}
	if encRef == nil {
		return nil
	}

	encObjNum := -1
	if ref, ok := encRef.(PDFRef); ok {
		encObjNum = ref.ObjNum
	}
	encVal, err := d.ResolveObject(encRef)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEncrypted, err)
	}
	enc, ok := encVal.(PDFDict)
	if !ok {
		return fmt.Errorf("%w: Encrypt is not a dictionary", ErrEncrypted)
	}

	h, err := newStdSecurityHandler(enc, d.firstIDBytes(), encObjNum, d.password)
	if err != nil {
		return err
	}
	d.crypt = h
	return nil
}

// firstIDBytes returns the raw bytes of the trailer /ID[0], which feeds key
// derivation and is itself never encrypted.
func (d *Reader) firstIDBytes() []byte {
	id := d.trailer.Entries["ID"]
	if id == nil {
		id = d.EffectiveTrailer().Entries["ID"]
	}
	arr, ok := id.(PDFArray)
	if !ok || len(arr) == 0 {
		return nil
	}
	return stringBytes(arr[0])
}

// shouldDecrypt reports whether object ref's parsed value must be decrypted.
// The Encrypt dictionary's own object is exempt (its strings are not encrypted).
func (d *Reader) shouldDecrypt(ref PDFRef) bool {
	return d.crypt != nil && ref.ObjNum != d.crypt.encryptObjNum
}

// decryptStream replaces m.RawStream with its decrypted bytes. Cross-reference
// streams are never encrypted and are left untouched.
func (d *Reader) decryptStream(m *PDFDict, ref PDFRef) error {
	if !m.HasStream || len(m.RawStream) == 0 || isXRefStream(*m) {
		return nil
	}
	dec, err := d.crypt.decrypt(m.RawStream, ref.ObjNum, ref.GenNum, false)
	if err != nil {
		return err
	}
	m.RawStream = dec
	return nil
}

// decryptStrings decrypts every string in v for object ref, in place, recursing
// through dicts (skipping the synthetic _ref) and arrays. Indirect references
// are left alone -- they decrypt when their own object resolves.
func (d *Reader) decryptStrings(v PDFValue, ref PDFRef) PDFValue {
	switch t := v.(type) {
	case PDFString, PDFHexString:
		// Decrypted plaintext is raw bytes regardless of how the ciphertext was
		// written, so both spellings collapse to a decoded literal string.
		return PDFString{Value: string(d.decryptStr(stringBytes(t), ref))}
	case PDFArray:
		for i := range t {
			t[i] = d.decryptStrings(t[i], ref)
		}
	case PDFDict:
		for k, val := range t.Entries {
			if k == "_ref" {
				continue
			}
			t.Entries[k] = d.decryptStrings(val, ref)
		}
	}
	return v
}

func (d *Reader) decryptStr(b []byte, ref PDFRef) []byte {
	out, err := d.crypt.decrypt(b, ref.ObjNum, ref.GenNum, true)
	if err != nil {
		return b
	}
	return out
}

func isXRefStream(m PDFDict) bool {
	n, ok := m.Entries["Type"].(PDFName)
	return ok && n.Value == "XRef"
}

// aesCBCNoPadZeroIV decrypts key material (UE/OE) with a zero IV and no padding.
func aesCBCNoPadZeroIV(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: bad key material", ErrEncrypted)
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, data)
	return out, nil
}
