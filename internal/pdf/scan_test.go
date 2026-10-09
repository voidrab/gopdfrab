package pdf

import (
	"bytes"
	"math/rand"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

// The regular expressions the hand-written scans replaced, kept as the oracle.
var (
	oracleObjRe  = regexp.MustCompile(`(?:^|[\r\n])(\d+)\s+\d+\s+obj\b`)
	oracleXRefRe = regexp.MustCompile(`(?:^|[\r\n])(xref[\r\n])`)
)

type headerHit struct {
	num int
	off int64
}

func oracleHeaders(data []byte) []headerHit {
	var out []headerHit
	for _, loc := range oracleObjRe.FindAllSubmatchIndex(data, -1) {
		n, err := strconv.Atoi(string(data[loc[2]:loc[3]]))
		if err != nil {
			continue
		}
		out = append(out, headerHit{n, int64(loc[2])})
	}
	return out
}

func scannedHeaders(d *Reader) []headerHit {
	var out []headerHit
	d.scanObjectHeaders(func(n int, off int64) { out = append(out, headerHit{n, off}) })
	return out
}

func setScanChunk(t testing.TB, n int) {
	old := scanChunk
	scanChunk = n
	t.Cleanup(func() { scanChunk = old })
}

// checkScanMatchesOracle runs the header scan over data on the in-memory path
// and on the seek path at several chunk sizes, against the regexp oracle.
func checkScanMatchesOracle(t testing.TB, data []byte) {
	want := oracleHeaders(data)
	mem := &Reader{data: data}
	if got := scannedHeaders(mem); !slices.Equal(got, want) {
		t.Fatalf("in-memory scan of %q:\n got %v\nwant %v", data, got, want)
	}
	for _, chunk := range []int{1, 3, 7, 64} {
		setScanChunk(t, chunk)
		seek := &Reader{file: bytesFileSource{bytes.NewReader(data)}, size: int64(len(data))}
		if got := scannedHeaders(seek); !slices.Equal(got, want) {
			t.Fatalf("seek scan (chunk %d) of %q:\n got %v\nwant %v", chunk, data, got, want)
		}
	}
}

func TestScanObjectHeadersMatchesRegexp(t *testing.T) {
	for _, s := range []string{
		"",
		"1 0 obj",
		"1 0 obj\n<<>>\nendobj\n12 0 obj\r\n2 3 obj\r7 0 objx\n8 0 obj_",
		"\n\n5 0 obj", "x5 0 obj\n 6 0 obj\n\t7 0 obj",
		"\n1\n2\nobj", "\n1 \n2 0 obj", "\n1 0 ob", "\n1 0", "\n1 0 obj/Foo",
		"\n99999999999999999999 0 obj\n3 0 obj",
		"\n4\f0\tobj\n5\v0 obj",
		"%PDF-1.4\n1 0 obj << >> endobj\nstartxref\n0\n%%EOF",
	} {
		checkScanMatchesOracle(t, []byte(s))
	}
}

// TestScanObjectHeadersRandom compares against the oracle on random inputs
// drawn from the bytes the pattern cares about.
func TestScanObjectHeadersRandom(t *testing.T) {
	alphabet := []byte("0123456789 \t\r\n\fobjx_/")
	rng := rand.New(rand.NewSource(1))
	for range 2000 {
		b := make([]byte, rng.Intn(40))
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		checkScanMatchesOracle(t, b)
	}
}

func FuzzScanObjectHeaders(f *testing.F) {
	f.Add([]byte("1 0 obj\n2 0 obj\r\n3 0 objx"))
	f.Fuzz(func(t *testing.T, data []byte) { checkScanMatchesOracle(t, data) })
}

// TestForEachIndexFindsXRefLines pins the xref-keyword scan against its
// regexp oracle on both read paths, including straddled chunk boundaries.
func TestForEachIndexFindsXRefLines(t *testing.T) {
	data := []byte("xref\n0 1\nstartxref\n9\nxref\r\nfoo xref\nxrefs\n\rxref\n")
	var want []int64
	for _, loc := range oracleXRefRe.FindAllSubmatchIndex(data, -1) {
		want = append(want, int64(loc[2]))
	}
	for _, chunk := range []int{1, 2, 5, 1 << 20} {
		setScanChunk(t, chunk)
		for name, d := range map[string]*Reader{
			"bytes": {data: data},
			"seek":  {file: bytesFileSource{bytes.NewReader(data)}, size: int64(len(data))},
		} {
			var got []int64
			d.forEachIndex([]byte("xref"), func(off int64) bool {
				before := off == 0 || isLineEnd(d.windowAt(off-1, 1)[0])
				after := d.windowAt(off+4, 1)
				if before && len(after) == 1 && isLineEnd(after[0]) {
					got = append(got, off)
				}
				return true
			})
			if !slices.Equal(got, want) {
				t.Errorf("%s chunk %d: got %v, want %v", name, chunk, got, want)
			}
		}
	}
}
