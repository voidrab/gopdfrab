package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab"
)

// errWriter fails every write, standing in for a closed pipe or a full disk so
// the encoder-error branches of the printers are reachable.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestProfileByName(t *testing.T) {
	for _, tc := range []struct {
		name string
		want *gopdfrab.Profile
	}{
		{"pdfa1b", gopdfrab.PDFA1B},
		{"legacy1b", gopdfrab.Legacy1B},
		{"pdf", gopdfrab.PDF},
		{"bogus", nil},
		{"", nil},
	} {
		if got := profileByName(tc.name); got != tc.want {
			t.Errorf("profileByName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPasswordBytes(t *testing.T) {
	t.Setenv(passwordEnv, "")
	if got := passwordBytes(""); got != nil {
		t.Errorf("passwordBytes(\"\") = %v, want nil", got)
	}
	if got := passwordBytes("secret"); string(got) != "secret" {
		t.Errorf("passwordBytes(\"secret\") = %q, want \"secret\"", got)
	}

	// The environment variable keeps the password out of the process list;
	// an explicit flag still wins over it.
	t.Setenv(passwordEnv, "fromenv")
	if got := passwordBytes(""); string(got) != "fromenv" {
		t.Errorf("passwordBytes with $%s set = %q, want \"fromenv\"", passwordEnv, got)
	}
	if got := passwordBytes("flag"); string(got) != "flag" {
		t.Errorf("flag value = %q, want it to win over the environment", got)
	}
}

// TestCollectPDFsWalksDirectories covers the directory arm: nested .pdf files
// are found by extension regardless of case, non-PDFs are skipped, and a plain
// file argument passes through even without the extension.
func TestCollectPDFsWalksDirectories(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path string) string {
		if err := os.WriteFile(path, []byte("%PDF-1.4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := write(filepath.Join(dir, "a.pdf"))
	b := write(filepath.Join(nested, "b.PDF"))
	write(filepath.Join(dir, "notes.txt"))
	loose := write(filepath.Join(dir, "loose.bin"))

	got, err := collectPDFs([]string{dir})
	if err != nil {
		t.Fatalf("collectPDFs(dir): %v", err)
	}
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Errorf("collectPDFs(dir) = %v, want [%s %s]", got, a, b)
	}

	// A file argument is taken verbatim, extension or not.
	got, err = collectPDFs([]string{loose})
	if err != nil {
		t.Fatalf("collectPDFs(file): %v", err)
	}
	if len(got) != 1 || got[0] != loose {
		t.Errorf("collectPDFs(file) = %v, want [%s]", got, loose)
	}

	if _, err := collectPDFs([]string{filepath.Join(dir, "gone")}); err == nil {
		t.Error("collectPDFs on a missing path returned no error")
	}
}

// TestCollectPDFsUnreadableSubdirectory checks that a directory the walk
// cannot read fails the whole collection rather than being skipped silently.
func TestCollectPDFsUnreadableSubdirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions enforced for the current user")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if got, err := collectPDFs([]string{dir}); !errors.Is(err, os.ErrPermission) || got != nil {
		t.Errorf("collectPDFs over an unreadable subdirectory = %v, %v; want nil, a permission error", got, err)
	}
}

func TestVerifyFlagParseError(t *testing.T) {
	if code, _, _ := runCLI("verify", "--nosuchflag", "x.pdf"); code != exitError {
		t.Errorf("verify --nosuchflag: exit=%d, want %d", code, exitError)
	}
}

func TestVerifyEmptyDirectory(t *testing.T) {
	code, _, stderr := runCLI("verify", t.TempDir())
	if code != exitError || !strings.Contains(stderr, "no PDF files found") {
		t.Errorf("verify on an empty dir: exit=%d stderr=%q", code, stderr)
	}
}

// TestVerifyReportsPerFileErrors checks the per-file error arms of both
// printers: a file that cannot be opened as a PDF is reported, not fatal, and
// forces exit code 2.
func TestVerifyReportsPerFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.pdf")
	if err := os.WriteFile(path, []byte("not a pdf at all"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, _ := runCLI("verify", path)
	if code != exitError || !strings.Contains(stdout, "\nERROR ") {
		t.Errorf("verify garbage: exit=%d stdout=%q", code, stdout)
	}
	if !strings.Contains(stdout, "summary: pass=0 fail=0 error=1 files=1") {
		t.Errorf("verify garbage summary missing from %q", stdout)
	}

	code, stdout, _ = runCLI("verify", "--json", path)
	if code != exitError {
		t.Errorf("verify --json garbage: exit=%d, want %d", code, exitError)
	}
	var rows []verifyFileJSON
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("verify --json output is not valid JSON: %v\n%s", err, stdout)
	}
	if len(rows) != 1 || rows[0].Error == "" || rows[0].Result != nil {
		t.Errorf("verify --json rows = %+v, want one error row", rows)
	}
}

func TestVerifyCancelledContext(t *testing.T) {
	pass, _ := veraFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb strings.Builder
	if code := run(ctx, []string{"verify", pass}, &out, &errb); code != exitError {
		t.Errorf("cancelled verify: exit=%d, want %d", code, exitError)
	}
}

func TestVerifyJSONEncodeError(t *testing.T) {
	results := []gopdfrab.FileResult[gopdfrab.Result]{{Path: "x.pdf"}}
	var stderr strings.Builder
	if code := printVerifyJSON(results, errWriter{}, &stderr); code != exitError {
		t.Errorf("printVerifyJSON with a failing writer: exit=%d, want %d", code, exitError)
	}
	if stderr.String() == "" {
		t.Error("printVerifyJSON did not report the write failure on stderr")
	}
}

func TestConvertFlagErrors(t *testing.T) {
	if code, _, _ := runCLI("convert", "--nosuchflag", "x.pdf"); code != exitError {
		t.Errorf("convert --nosuchflag: exit=%d, want %d", code, exitError)
	}
	if code, _, stderr := runCLI("convert", "--profile", "bogus", "x.pdf"); code != exitError || !strings.Contains(stderr, "unknown profile") {
		t.Errorf("convert --profile bogus: exit=%d stderr=%q", code, stderr)
	}
	if code, _, _ := runCLI("convert"); code != exitError {
		t.Errorf("convert with no input: exit=%d, want %d", code, exitError)
	}
	// Giving -o and a positional output together is rejected.
	if code, _, stderr := runCLI("convert", "-o", "a.pdf", "x.pdf", "b.pdf"); code != exitError || !strings.Contains(stderr, "output path once") {
		t.Errorf("convert with -o and a positional output: exit=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runCLI("convert", "x.pdf", "b.pdf", "c.pdf"); code != exitError || !strings.Contains(stderr, "output path once") {
		t.Errorf("convert with three positionals: exit=%d stderr=%q", code, stderr)
	}
}

// TestConvertDerivesOutputPath exercises the branch where neither -o nor a
// positional output is given, so defaultOutput names the file.
func TestConvertDerivesOutputPath(t *testing.T) {
	_, fail := veraFixture(t)
	data, err := os.ReadFile(fail)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(input, data, 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI("convert", input)
	if code != exitValid {
		t.Fatalf("convert: exit=%d, want %d (stderr=%q)", code, exitValid, stderr)
	}
	want := filepath.Join(filepath.Dir(input), "doc.pdfa.pdf")
	if info, err := os.Stat(want); err != nil || info.Size() == 0 {
		t.Errorf("convert did not write %s: %v", want, err)
	}
	if !strings.Contains(stdout, want) {
		t.Errorf("convert stdout = %q, want it to name %s", stdout, want)
	}
}

func TestConvertSaveError(t *testing.T) {
	_, fail := veraFixture(t)
	out := filepath.Join(t.TempDir(), "no-such-dir", "out.pdf")
	code, _, stderr := runCLI("convert", "-o", out, fail)
	if code != exitError || !strings.Contains(stderr, "write") {
		t.Errorf("convert to an unwritable path: exit=%d stderr=%q", code, stderr)
	}
}

// nonConformantResult builds a ConvertResult that still carries issues, by
// borrowing a real verifier verdict. Convert normally fixes the corpus
// fixtures, so the residual-reporting arms of the printers are otherwise
// unreachable from the CLI.
func nonConformantResult(t *testing.T) gopdfrab.ConvertResult {
	t.Helper()
	_, fail := veraFixture(t)
	res, err := gopdfrab.Verify(fail, gopdfrab.PDFA1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Valid || len(res.Issues) == 0 {
		t.Fatalf("fixture verified clean; want a failing verdict to reuse")
	}
	return gopdfrab.ConvertResult{Result: res, Iterations: 4}
}

func TestConvertPrintersReportResidualIssues(t *testing.T) {
	cr := nonConformantResult(t)

	var stdout strings.Builder
	if code := printConvertText("in.pdf", "out.pdf", "pdfa1b", false, cr, &stdout); code != exitInvalid {
		t.Errorf("printConvertText: exit=%d, want %d", code, exitInvalid)
	}
	if !strings.Contains(stdout.String(), "result: residual issues=") {
		t.Errorf("printConvertText output = %q, want a residual count", stdout.String())
	}
	if !strings.Contains(stdout.String(), strconv.Quote(cr.Residual()[0].Messages()[0])) {
		t.Errorf("printConvertText did not list the residual issue: %q", stdout.String())
	}

	stdout.Reset()
	var stderr strings.Builder
	if code := printConvertJSON("in.pdf", "out.pdf", cr, &stdout, &stderr); code != exitInvalid {
		t.Errorf("printConvertJSON: exit=%d, want %d", code, exitInvalid)
	}
	var row convertJSON
	if err := json.Unmarshal([]byte(stdout.String()), &row); err != nil {
		t.Fatalf("printConvertJSON output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if row.Valid || row.IssueCount != len(cr.Residual()) {
		t.Errorf("printConvertJSON row = %+v, want invalid with %d issues", row, len(cr.Residual()))
	}
}

// TestConvertPrintersReportLostContent verifies that a conformant conversion
// which lost an object still reports the loss. The loss is not a residual
// issue, since the file that was written is valid, so it is printed on its own
// line and carried in its own JSON field.
func TestConvertPrintersReportLostContent(t *testing.T) {
	lost := nonConformantResult(t).Residual()[:1]
	cr := gopdfrab.ConvertResult{
		Result:      gopdfrab.Result{Valid: true},
		LostObjects: lost,
	}

	var stdout strings.Builder
	if code := printConvertText("in.pdf", "out.pdf", "pdfa1b", false, cr, &stdout); code != exitValid {
		t.Errorf("printConvertText: exit=%d, want %d for a conformant output", code, exitValid)
	}
	if !strings.Contains(stdout.String(), "lost: objects=1 distinct=1") ||
		!strings.Contains(stdout.String(), strconv.Quote(lost[0].Messages()[0])) {
		t.Errorf("printConvertText output = %q, want the lost object reported", stdout.String())
	}

	stdout.Reset()
	var stderr strings.Builder
	if code := printConvertJSON("in.pdf", "out.pdf", cr, &stdout, &stderr); code != exitValid {
		t.Errorf("printConvertJSON: exit=%d, want %d", code, exitValid)
	}
	var row convertJSON
	if err := json.Unmarshal([]byte(stdout.String()), &row); err != nil {
		t.Fatalf("printConvertJSON output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !row.Valid || row.IssueCount != 0 || len(row.Lost) != 1 {
		t.Errorf("printConvertJSON row = %+v, want valid, no issues, one lost object", row)
	}
}

func TestConvertJSONEncodeError(t *testing.T) {
	var stderr strings.Builder
	cr := gopdfrab.ConvertResult{Result: gopdfrab.Result{Valid: true}}
	if code := printConvertJSON("in.pdf", "out.pdf", cr, errWriter{}, &stderr); code != exitError {
		t.Errorf("printConvertJSON with a failing writer: exit=%d, want %d", code, exitError)
	}
	if stderr.String() == "" {
		t.Error("printConvertJSON did not report the write failure on stderr")
	}
}

// TestConvertPrintersReportRasterization verifies that a conformant conversion
// which rebuilt pages as images, and could not draw everything on them, says so
// in both outputs. Without that a page whose picture became a gray box reads as
// a clean conversion.
func TestConvertPrintersReportRasterization(t *testing.T) {
	cr := gopdfrab.ConvertResult{
		Result:          gopdfrab.Result{Valid: true},
		Iterations:      3,
		RasterizedPages: []int{1, 2, 3, 7},
		RasterDrops: []gopdfrab.RasterDrop{
			{Page: 7, Features: []string{"tiling pattern"}},
			{Page: 2, Features: []string{"tiling pattern", "JPEG 2000 image"}},
			{Page: 3, Features: []string{"JPEG 2000 image"}},
		},
	}

	var stdout strings.Builder
	if code := printConvertText("in.pdf", "out.pdf", "pdfa1b", false, cr, &stdout); code != exitValid {
		t.Errorf("printConvertText: exit=%d, want %d", code, exitValid)
	}
	want := "profile: pdfa1b\n\n" +
		"input: in.pdf\noutput: out.pdf\niterations: 3\n" +
		"rasterized: pages=1-3,7\n" +
		"dropped: pages=2-3,7 features=2\n" +
		"  pages=2-3  \"JPEG 2000 image\"\n" +
		"  pages=2,7  \"tiling pattern\"\n" +
		"result: conformant\n"
	if stdout.String() != want {
		t.Errorf("printConvertText output =\n%s\nwant\n%s", stdout.String(), want)
	}

	stdout.Reset()
	var stderr strings.Builder
	if code := printConvertJSON("in.pdf", "out.pdf", cr, &stdout, &stderr); code != exitValid {
		t.Errorf("printConvertJSON: exit=%d, want %d", code, exitValid)
	}
	var row convertJSON
	if err := json.Unmarshal([]byte(stdout.String()), &row); err != nil {
		t.Fatalf("printConvertJSON output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !slices.Equal(row.RasterizedPages, cr.RasterizedPages) || len(row.RasterDrops) != 3 ||
		row.RasterDrops[1].Page != 2 || !slices.Equal(row.RasterDrops[1].Features, []string{"tiling pattern", "JPEG 2000 image"}) {
		t.Errorf("printConvertJSON row = %+v, want the rasterized pages and every drop", row)
	}
	for _, key := range []string{`"rasterizedPages"`, `"rasterDrops"`, `"page"`, `"features"`} {
		if !strings.Contains(stdout.String(), key) {
			t.Errorf("JSON lacks key %s:\n%s", key, stdout.String())
		}
	}
}
