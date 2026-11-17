package fontsubst

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// fileSource serves the fonts directory, counting the calls for each file;
// fail, while set, makes it fail instead.
type fileSource struct {
	calls map[string]int
	fail  error
	data  []byte // served for every file in place of the file, when set
}

func (s *fileSource) get(name string) ([]byte, error) {
	s.calls[name]++
	if s.fail != nil {
		return nil, s.fail
	}
	if s.data != nil {
		return s.data, nil
	}
	return os.ReadFile("fonts/" + name)
}

// useSource makes a new fileSource the fonts' source for the rest of the test.
func useSource(t *testing.T) *fileSource {
	t.Helper()
	s := &fileSource{calls: map[string]int{}}
	SetSource(s.get)
	t.Cleanup(func() { SetSource(nil) })
	return s
}

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
	got := slices.Sorted(slices.Values(Files))
	if !slices.Equal(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
}

// TestSource verifies that a font comes from the source set, fetched once and
// inflated to a TrueType program.
func TestSource(t *testing.T) {
	s := useSource(t)
	data := NotoSymbols()
	if len(data) < 4 || !bytes.Equal(data[:4], []byte{0, 1, 0, 0}) {
		t.Fatal("the font from the source is not a TrueType program")
	}
	if again := NotoSymbols(); &again[0] != &data[0] {
		t.Error("the font was loaded again on its second use")
	}
	if n := s.calls[notoSymbolsFile]; n != 1 {
		t.Errorf("the source was asked %d times, want 1", n)
	}
}

// TestSourceFailure verifies that a font the source fails to give is nil, and
// that the next use asks again.
func TestSourceFailure(t *testing.T) {
	s := useSource(t)
	s.fail = errors.New("offline")
	if data := LiberationSansRegular(); data != nil {
		t.Fatal("a font the source failed to give is not nil")
	}
	_, err := load(libSansRegularFile)
	if err == nil || !strings.Contains(err.Error(), libSansRegularFile) || !errors.Is(err, s.fail) {
		t.Errorf("load() = %v, want the source's error naming the file", err)
	}
	s.fail = nil
	if data := LiberationSansRegular(); data == nil {
		t.Error("the font is still nil after the source recovered")
	}
}

// TestSourceCorrupt verifies that a font the source gives corrupt is nil and
// its error names the file.
func TestSourceCorrupt(t *testing.T) {
	s := useSource(t)
	s.data = []byte("not gzip")
	if data := LiberationSansRegular(); data != nil {
		t.Fatal("a corrupt font is not nil")
	}
	if _, err := load(libSansRegularFile); err == nil || !strings.Contains(err.Error(), libSansRegularFile) {
		t.Errorf("load() = %v, want an error naming the file", err)
	}
}

// TestPreload verifies that Preload loads every font from the source, and
// that it returns the source's error.
func TestPreload(t *testing.T) {
	s := useSource(t)
	if err := Preload(); err != nil {
		t.Fatalf("Preload() = %v", err)
	}
	for _, name := range Files {
		if s.calls[name] != 1 {
			t.Errorf("%s was fetched %d times, want 1", name, s.calls[name])
		}
	}

	s = useSource(t)
	s.fail = errors.New("offline")
	if err := Preload(); !errors.Is(err, s.fail) {
		t.Errorf("Preload() = %v, want the source's error", err)
	}
}
