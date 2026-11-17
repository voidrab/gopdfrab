package fontsubst

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

// useSource makes get the fonts' source for the rest of the test, counting
// the calls for each file.
func useSource(t *testing.T, get Source) map[string]int {
	t.Helper()
	var mu sync.Mutex
	calls := map[string]int{}
	SetSource(func(name string) ([]byte, error) {
		mu.Lock()
		calls[name]++
		mu.Unlock()
		return get(name)
	})
	t.Cleanup(func() { SetSource(nil) })
	return calls
}

// fontsDir serves the fonts directory.
func fontsDir(name string) ([]byte, error) { return os.ReadFile("fonts/" + name) }

// TestFilesMatchFontsDir verifies that Files names exactly the fonts
// directory's font files.
func TestFilesMatchFontsDir(t *testing.T) {
	entries, err := os.ReadDir("fonts")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".ttf.gz") {
			want = append(want, e.Name())
		}
	}
	if got := slices.Sorted(slices.Values(Files)); !slices.Equal(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
}

// TestSource verifies that a font comes from the source set, fetched once and
// inflated to a TrueType program.
func TestSource(t *testing.T) {
	calls := useSource(t, fontsDir)
	data := NotoSymbols()
	if len(data) < 4 || !bytes.Equal(data[:4], []byte{0, 1, 0, 0}) {
		t.Fatal("the font from the source is not a TrueType program")
	}
	if again := NotoSymbols(); &again[0] != &data[0] {
		t.Error("the font was loaded again on its second use")
	}
	if n := calls["NotoSansSymbols-VariableFont_wght.ttf.gz"]; n != 1 {
		t.Errorf("the source was asked %d times, want 1", n)
	}
}

// TestSourceFailures verifies that a font the source fails to give, or gives
// corrupt, is nil with an error naming the file, and that the next use asks
// again.
func TestSourceFailures(t *testing.T) {
	const name = "LiberationSans-Regular.ttf.gz"
	offline := errors.New("offline")
	for what, get := range map[string]Source{
		"failing": func(string) ([]byte, error) { return nil, offline },
		"corrupt": func(string) ([]byte, error) { return []byte("not gzip"), nil },
	} {
		t.Run(what, func(t *testing.T) {
			useSource(t, get)
			if data := LiberationSansRegular(); data != nil {
				t.Fatal("the font is not nil")
			}
			if _, err := fonts[name].load(); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("load() = %v, want an error naming the file", err)
			}
			useSource(t, fontsDir)
			if LiberationSansRegular() == nil {
				t.Error("the font is still nil from a working source")
			}
		})
	}
}

// TestPreload verifies that Preload fetches every font from the source, once,
// leaves inflating them to their first use, and returns the source's error.
func TestPreload(t *testing.T) {
	calls := useSource(t, fontsDir)
	LiberationSansRegular() // already inflated, so Preload does not fetch it
	if err := Preload(); err != nil {
		t.Fatalf("Preload() = %v", err)
	}
	for _, name := range Files {
		if calls[name] != 1 {
			t.Errorf("%s was fetched %d times, want 1", name, calls[name])
		}
	}
	if f := fonts["LiberationMono-Bold.ttf.gz"]; f.data != nil || f.z == nil {
		t.Error("Preload inflated a font, or did not keep it")
	}
	if libMonoBold() == nil || calls["LiberationMono-Bold.ttf.gz"] != 1 {
		t.Error("a preloaded font was not inflated from what Preload fetched")
	}

	offline := errors.New("offline")
	useSource(t, func(string) ([]byte, error) { return nil, offline })
	if err := Preload(); !errors.Is(err, offline) {
		t.Errorf("Preload() = %v, want the source's error", err)
	}
}

// TestUnknownBuiltIn verifies that a file not among the fonts cannot be
// loaded.
func TestUnknownBuiltIn(t *testing.T) {
	SetSource(nil)
	if _, err := (&font{name: "Missing.ttf.gz"}).load(); err == nil {
		t.Error("an unknown font loaded")
	}
}
