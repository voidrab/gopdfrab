package pdfgen

import (
	"bytes"
	"math/rand"
	"strconv"
)

// Corruptor is a named, deterministic transformation that breaks a valid PDF
// in one structurally-meaningful way. Given the same input and rng state it
// produces the same output. It must always return a non-nil slice and must not
// mutate in; callers may reuse in.
type Corruptor struct {
	Name  string
	Apply func(in []byte, rng *rand.Rand) []byte
}

// Corruptors returns the full table of structural corruptors. Targeted
// structural breakage reaches deep parser paths (xref recovery, stream
// framing, object resolution) far more reliably than blind bit-flipping, which
// mostly produces trivially-rejected garbage.
//
// Every corruptor falls back to a generic byte mutation when its target token
// is absent, so no corruptor is ever a silent no-op on an unexpected input.
func Corruptors() []Corruptor {
	return []Corruptor{
		{"truncate", truncate},
		{"garbage-prefix", garbagePrefix},
		{"drop-eof", dropToken("%%EOF")},
		{"drop-trailer", dropToken("trailer")},
		{"drop-xref", dropToken("xref")},
		{"drop-startxref", dropToken("startxref")},
		{"dup-startxref", dupStartxref},
		{"startxref-negative", setStartxref("-1")},
		{"startxref-huge", setStartxref("999999999999")},
		{"startxref-nonnumeric", setStartxref("NOTANOFFSET")},
		{"startxref-offby", startxrefOffBy},
		{"flip-xref-offsets", flipXRefOffsets},
		{"wrong-size", wrongSize},
		{"length-too-long", setLength(1 << 20)},
		{"length-negative", setLengthRaw("-5")},
		{"length-nonint", setLengthRaw("(oops)")},
		{"corrupt-stream-payload", corruptStreamPayload},
		{"unknown-filter", replaceToken("/FlateDecode", "/BogusDecode")},
		{"dangling-ref", replaceToken("2 0 R", "9999 0 R")},
		{"self-ref-root", replaceToken("/Root 1 0 R", "/Root 1 0 R /Self 1 0 R")},
		{"huge-obj-number", replaceToken("1 0 obj", "4294967296 0 obj")},
		{"unbalanced-dict", insertAfter("<<", " << ")},
		{"unbalanced-array", insertAfter("/Kids [", " [ [ [ ")},
		{"unterminated-string", replaceToken("/Type /Catalog", "/Junk (unterminated")},
		{"bad-name-escape", replaceToken("/Catalog", "/Cat#zzalog")},
		{"deep-nesting", deepNesting},
		{"nul-bytes", sprinkleNUL},
		{"bit-flips", bitFlips},
	}
}

// --- structural corruptors -------------------------------------------------

func cp(in []byte) []byte { return append([]byte(nil), in...) }

func truncate(in []byte, rng *rand.Rand) []byte {
	if len(in) < 2 {
		return cp(in)
	}
	return cp(in[:1+rng.Intn(len(in)-1)])
}

func garbagePrefix(in []byte, rng *rand.Rand) []byte {
	n := 1 + rng.Intn(32)
	pre := make([]byte, n)
	for i := range pre {
		pre[i] = byte(rng.Intn(256))
	}
	return append(pre, in...)
}

// dropToken removes the last occurrence of tok, which typically breaks the
// tail-scanning xref/trailer recovery paths.
func dropToken(tok string) func([]byte, *rand.Rand) []byte {
	bt := []byte(tok)
	return func(in []byte, rng *rand.Rand) []byte {
		i := bytes.LastIndex(in, bt)
		if i < 0 {
			return bitFlips(in, rng)
		}
		out := make([]byte, 0, len(in))
		out = append(out, in[:i]...)
		out = append(out, in[i+len(bt):]...)
		return out
	}
}

func dupStartxref(in []byte, rng *rand.Rand) []byte {
	i := bytes.LastIndex(in, []byte("startxref"))
	if i < 0 {
		return bitFlips(in, rng)
	}
	out := cp(in)
	return append(out, in[i:]...)
}

// setStartxref replaces the numeric offset following the last "startxref".
func setStartxref(val string) func([]byte, *rand.Rand) []byte {
	return func(in []byte, rng *rand.Rand) []byte {
		i := bytes.LastIndex(in, []byte("startxref"))
		if i < 0 {
			return bitFlips(in, rng)
		}
		// Locate the digits after startxref (skip the newline).
		j := i + len("startxref")
		for j < len(in) && (in[j] == '\r' || in[j] == '\n' || in[j] == ' ') {
			j++
		}
		k := j
		for k < len(in) && (in[k] == '-' || (in[k] >= '0' && in[k] <= '9')) {
			k++
		}
		out := make([]byte, 0, len(in))
		out = append(out, in[:j]...)
		out = append(out, val...)
		out = append(out, in[k:]...)
		return out
	}
}

func startxrefOffBy(in []byte, rng *rand.Rand) []byte {
	i := bytes.LastIndex(in, []byte("startxref"))
	if i < 0 {
		return bitFlips(in, rng)
	}
	j := i + len("startxref")
	for j < len(in) && (in[j] == '\r' || in[j] == '\n' || in[j] == ' ') {
		j++
	}
	k := j
	for k < len(in) && in[k] >= '0' && in[k] <= '9' {
		k++
	}
	if k == j {
		return bitFlips(in, rng)
	}
	off, err := strconv.Atoi(string(in[j:k]))
	if err != nil {
		return bitFlips(in, rng)
	}
	off += rng.Intn(21) - 10
	return setStartxref(strconv.Itoa(off))(in, rng)
}

// flipXRefOffsets rewrites every 10-digit offset in the classic xref table to
// point somewhere random, so object lookups miss and force recovery.
func flipXRefOffsets(in []byte, rng *rand.Rand) []byte {
	xi := bytes.LastIndex(in, []byte("xref"))
	if xi < 0 {
		return bitFlips(in, rng)
	}
	out := cp(in)
	// Rewrite 10-digit runs immediately followed by " 00000 n".
	pat := []byte(" 00000 n")
	for i := xi; i+18 < len(out); i++ {
		if bytes.Equal(out[i+10:i+10+len(pat)], pat) && allDigits(out[i:i+10]) {
			for d := 0; d < 10; d++ {
				out[i+d] = byte('0' + rng.Intn(10))
			}
		}
	}
	return out
}

func allDigits(b []byte) bool {
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(b) > 0
}

// wrongSize corrupts the trailer /Size value.
func wrongSize(in []byte, rng *rand.Rand) []byte {
	return replaceTokenRegexish(in, rng, "/Size ", "/Size 999999 ")
}

// setLength inflates the first /Length to a large value, overrunning the
// stream body.
func setLength(n int) func([]byte, *rand.Rand) []byte {
	return func(in []byte, rng *rand.Rand) []byte {
		return replaceLengthValue(in, rng, strconv.Itoa(n))
	}
}

func setLengthRaw(val string) func([]byte, *rand.Rand) []byte {
	return func(in []byte, rng *rand.Rand) []byte {
		return replaceLengthValue(in, rng, val)
	}
}

// replaceLengthValue replaces the value after the first "/Length " with val.
func replaceLengthValue(in []byte, rng *rand.Rand, val string) []byte {
	tok := []byte("/Length ")
	i := bytes.Index(in, tok)
	if i < 0 {
		return bitFlips(in, rng)
	}
	j := i + len(tok)
	k := j
	for k < len(in) && (in[k] == '-' || (in[k] >= '0' && in[k] <= '9')) {
		k++
	}
	out := make([]byte, 0, len(in)+len(val))
	out = append(out, in[:j]...)
	out = append(out, val...)
	out = append(out, in[k:]...)
	return out
}

// corruptStreamPayload flips bytes inside the first stream body, breaking Flate
// decoding without changing framing.
func corruptStreamPayload(in []byte, rng *rand.Rand) []byte {
	s := bytes.Index(in, []byte("stream"))
	if s < 0 {
		return bitFlips(in, rng)
	}
	start := s + len("stream")
	if start < len(in) && in[start] == '\r' {
		start++
	}
	if start < len(in) && in[start] == '\n' {
		start++
	}
	end := bytes.Index(in[start:], []byte("endstream"))
	if end <= 0 {
		return bitFlips(in, rng)
	}
	end += start
	out := cp(in)
	for n := 0; n < 8 && end > start; n++ {
		out[start+rng.Intn(end-start)] ^= byte(1 + rng.Intn(255))
	}
	return out
}

// --- token-level corruptors ------------------------------------------------

func replaceToken(old, new string) func([]byte, *rand.Rand) []byte {
	bo := []byte(old)
	return func(in []byte, rng *rand.Rand) []byte {
		if !bytes.Contains(in, bo) {
			return bitFlips(in, rng)
		}
		return bytes.Replace(in, bo, []byte(new), 1)
	}
}

func replaceTokenRegexish(in []byte, rng *rand.Rand, old, new string) []byte {
	bo := []byte(old)
	if !bytes.Contains(in, bo) {
		return bitFlips(in, rng)
	}
	return bytes.Replace(in, bo, []byte(new), 1)
}

func insertAfter(anchor, text string) func([]byte, *rand.Rand) []byte {
	ba := []byte(anchor)
	return func(in []byte, rng *rand.Rand) []byte {
		i := bytes.Index(in, ba)
		if i < 0 {
			return bitFlips(in, rng)
		}
		at := i + len(ba)
		out := make([]byte, 0, len(in)+len(text))
		out = append(out, in[:at]...)
		out = append(out, text...)
		out = append(out, in[at:]...)
		return out
	}
}

// deepNesting injects a deeply nested array to probe recursion/stack limits.
func deepNesting(in []byte, rng *rand.Rand) []byte {
	depth := 500 + rng.Intn(2000)
	nest := make([]byte, 0, depth*2)
	for i := 0; i < depth; i++ {
		nest = append(nest, '[')
	}
	for i := 0; i < depth; i++ {
		nest = append(nest, ']')
	}
	obj := append([]byte("/Junk "), nest...)
	return insertAfter("/Type /Catalog", string(obj))(in, rng)
}

func sprinkleNUL(in []byte, rng *rand.Rand) []byte {
	out := cp(in)
	for n := 0; n < 16 && len(out) > 0; n++ {
		out[rng.Intn(len(out))] = 0
	}
	return out
}

// bitFlips is the generic fallback mutation: flip a handful of random bytes.
func bitFlips(in []byte, rng *rand.Rand) []byte {
	out := cp(in)
	if len(out) == 0 {
		return []byte{byte(rng.Intn(256))}
	}
	for n := 0; n < 8; n++ {
		out[rng.Intn(len(out))] ^= byte(1 << uint(rng.Intn(8)))
	}
	return out
}
