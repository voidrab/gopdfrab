package pdf

import (
	"testing"
)

// TestDecodeCacheRemembersFailures pins the negative decode cache: a failed
// decode is answered from the cache, not re-run. The raw bytes are repaired
// in place (same slice, so the same StreamKey) between calls; a re-decode
// would now succeed, so a second error proves the cache answered. Releasing
// the caches forgets the failure.
func TestDecodeCacheRemembersFailures(t *testing.T) {
	for name, decode := range map[string]func(*Reader, PDFDict) ([]byte, error){
		"unlocked":   (*Reader).DecodeStreamCached,
		"concurrent": (*Reader).DecodeStreamCachedConcurrent,
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte("zz>") // invalid hex digits
			dict := PDFDict{
				HasStream: true,
				RawStream: raw,
				Entries:   DictOf(map[string]PDFValue{"Filter": PDFName{Value: "ASCIIHexDecode"}}),
			}
			d := &Reader{}
			if _, err := decode(d, dict); err == nil {
				t.Fatal("expected the invalid hex stream to fail")
			}
			copy(raw, "41>")
			if _, err := decode(d, dict); err == nil {
				t.Fatal("second decode ran again instead of answering from the cache")
			}
			d.ReleaseCaches()
			got, err := decode(d, dict)
			if err != nil || string(got) != "A" {
				t.Fatalf("after ReleaseCaches: %q, %v; want a fresh decode of \"A\"", got, err)
			}
		})
	}
}
