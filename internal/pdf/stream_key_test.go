package pdf

import "testing"

// TestStreamKeyTracksFilterChain pins that the decode cache key changes with
// the filter chain, not only with the raw bytes. A fixer that deletes /Filter
// in place must not leave the cache returning stale decoded bytes.
func TestStreamKeyTracksFilterChain(t *testing.T) {
	raw := []byte("41>")
	d := PDFDict{HasStream: true, RawStream: raw, Entries: DictOf(map[string]PDFValue{
		"Filter": PDFName{Value: "ASCIIHexDecode"},
	})}
	r := &Reader{}
	got, err := r.DecodeStreamCached(d)
	if err != nil || string(got) != "A" {
		t.Fatalf("decode = %q, %v; want \"A\"", got, err)
	}
	withFilter, _ := StreamKeyOf(d)

	d.Entries.Del("Filter")
	without, _ := StreamKeyOf(d)
	if withFilter == without {
		t.Fatal("StreamKey unchanged after deleting /Filter")
	}
	got, err = r.DecodeStreamCached(d)
	if err != nil || string(got) != "41>" {
		t.Fatalf("decode after deleting /Filter = %q, %v; want the raw bytes, not the cached decode", got, err)
	}

	d.Entries.Set("Filter", PDFArray{PDFName{Value: "ASCIIHexDecode"}})
	arrKey, _ := StreamKeyOf(d)
	d.Entries.Set("DecodeParms", PDFDict{Entries: NewDict()})
	if parmsKey, _ := StreamKeyOf(d); parmsKey == arrKey || arrKey == without {
		t.Error("StreamKey does not distinguish the filter array or /DecodeParms")
	}
}
