package pdfgen

import (
	"crypto/md5"
	"crypto/rc4"
	"encoding/binary"
	"fmt"
)

// cryptPad is the 32-byte password padding string (ISO 32000-1 Algorithm 2).
var cryptPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41,
	0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

func rc4Bytes(key, data []byte) []byte {
	c, _ := rc4.NewCipher(key)
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

// encryptedClassicRC4 builds a structurally valid one-page PDF encrypted with
// the Standard security handler (V2/R3, RC4-128, empty user and owner
// passwords). It exercises the decryption read path from every whole-file fuzz
// target that seeds from Seeds(). The encryption is the plain inverse of the
// decryptor in internal/pdf; pdfgen keeps its own copy to avoid importing that
// package (fuzz targets in any package use pdfgen).
func encryptedClassicRC4() []byte {
	const keyLen = 16
	id := []byte("0123456789ABCDEF")
	p := int32(-44)

	iter50 := func(seed []byte) []byte {
		h := seed
		for range 50 {
			s := md5.Sum(h[:keyLen])
			h = s[:]
		}
		return h[:keyLen]
	}

	// /O (Algorithm 3): empty owner and user passwords.
	ownerKey := iter50(md5sum(cryptPad))
	o := append([]byte(nil), cryptPad...)
	for i := 0; i <= 19; i++ {
		o = rc4Bytes(xorByte(ownerKey, byte(i)), o)
	}

	// File key (Algorithm 2).
	var pbuf [4]byte
	binary.LittleEndian.PutUint32(pbuf[:], uint32(p))
	fileKey := iter50(md5sum(cryptPad, o, pbuf[:], id))

	// /U (Algorithm 5).
	u := rc4Bytes(fileKey, md5sum(cryptPad, id))
	for i := 1; i <= 19; i++ {
		u = rc4Bytes(xorByte(fileKey, byte(i)), u)
	}
	u = append(u, make([]byte, 16)...) // pad to 32

	encStream := func(objNum int, data []byte) []byte {
		// Per-object key is min(keyLen+5, 16) bytes; the MD5 sum is 16.
		ok := md5sum(fileKey, []byte{byte(objNum), byte(objNum >> 8), byte(objNum >> 16), 0, 0})
		return rc4Bytes(ok, data)
	}

	b := NewBuilder("%PDF-1.7\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", encStream(4, []byte("q\nQ\n")))
	b.Obj(5, fmt.Sprintf("<< /Filter /Standard /V 2 /R 3 /Length 128 /P %d /O %s /U %s >>",
		p, hexString(o), hexString(u)))
	return b.FinishClassic(fmt.Sprintf("<< /Size 6 /Root 1 0 R /Encrypt 5 0 R /ID [%s %s] >>",
		hexString(id), hexString(id)))
}

func md5sum(parts ...[]byte) []byte {
	h := md5.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func xorByte(key []byte, b byte) []byte {
	out := make([]byte, len(key))
	for i := range key {
		out[i] = key[i] ^ b
	}
	return out
}

func hexString(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2+2)
	out = append(out, '<')
	for _, c := range b {
		out = append(out, hexdigits[c>>4], hexdigits[c&0xf])
	}
	return string(append(out, '>'))
}
