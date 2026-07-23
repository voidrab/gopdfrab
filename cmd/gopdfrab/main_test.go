package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI invokes run with a background context and captures stdout/stderr.
func runCLI(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestNoArgsAndHelp(t *testing.T) {
	if code, _, _ := runCLI(); code != exitError {
		t.Errorf("no args: exit=%d, want %d", code, exitError)
	}
	if code, out, _ := runCLI("help"); code != exitValid || !strings.Contains(out, "usage:") {
		t.Errorf("help: exit=%d out=%q", code, out)
	}
	if code, _, err := runCLI("frobnicate"); code != exitError || !strings.Contains(err, "unknown command") {
		t.Errorf("unknown command: exit=%d err=%q", code, err)
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := runCLI("version")
	if code != exitValid || strings.TrimSpace(out) == "" {
		t.Errorf("version: exit=%d out=%q", code, out)
	}
}

func TestVerifyBadProfileAndMissingArgs(t *testing.T) {
	if code, _, err := runCLI("verify", "--profile", "bogus", "x.pdf"); code != exitError || !strings.Contains(err, "unknown profile") {
		t.Errorf("bad profile: exit=%d err=%q", code, err)
	}
	if code, _, _ := runCLI("verify"); code != exitError {
		t.Errorf("verify no paths: exit=%d, want %d", code, exitError)
	}
	if code, _, _ := runCLI("verify", filepath.Join(t.TempDir(), "nope.pdf")); code != exitError {
		t.Errorf("verify missing file: exit=%d, want %d", code, exitError)
	}
}

// veraFixture returns a pass and fail fixture from the committed corpus, or
// skips if the corpus is absent.
func veraFixture(t *testing.T) (pass, fail string) {
	t.Helper()
	dir := filepath.Join("..", "..", "tests", "veraPDF", "PDF_A-1b", "6.1 File structure", "6.1.2 File header")
	pass = filepath.Join(dir, "veraPDF test suite 6-1-2-t01-pass-a.pdf")
	fail = filepath.Join(dir, "veraPDF test suite 6-1-2-t01-fail-a.pdf")
	if _, err := os.Stat(pass); err != nil {
		t.Skip("veraPDF corpus not present")
	}
	return pass, fail
}

func TestVerifyPassFailExitCodes(t *testing.T) {
	pass, fail := veraFixture(t)

	if code, out, _ := runCLI("verify", pass); code != exitValid || !strings.Contains(out, "[PASS]") {
		t.Errorf("verify pass: exit=%d out=%q", code, out)
	}
	if code, out, _ := runCLI("verify", fail); code != exitInvalid || !strings.Contains(out, "[FAIL]") {
		t.Errorf("verify fail: exit=%d out=%q", code, out)
	}
}

func TestVerifyJSON(t *testing.T) {
	_, fail := veraFixture(t)
	code, out, _ := runCLI("verify", "--json", fail)
	if code != exitInvalid {
		t.Errorf("verify --json fail: exit=%d, want %d", code, exitInvalid)
	}
	var rows []verifyFileJSON
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("verify --json output is not valid JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].Result == nil || rows[0].Result.Valid {
		t.Errorf("verify --json rows = %+v, want one invalid result", rows)
	}
}

func TestConvertProducesConformantOutput(t *testing.T) {
	_, fail := veraFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")

	code, stdout, stderr := runCLI("convert", fail, out)
	if code != exitValid {
		t.Fatalf("convert: exit=%d, want %d (stderr=%q)", code, exitValid, stderr)
	}
	if !strings.Contains(stdout, "conformant") {
		t.Errorf("convert stdout = %q, want 'conformant'", stdout)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Errorf("convert did not write a non-empty output: %v", err)
	}

	// The written file must independently verify clean via the CLI.
	if code, _, _ := runCLI("verify", out); code != exitValid {
		t.Errorf("converted output does not re-verify: exit=%d", code)
	}
}

func TestConvertJSON(t *testing.T) {
	_, fail := veraFixture(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	code, stdout, _ := runCLI("convert", "--json", fail, out)
	if code != exitValid {
		t.Fatalf("convert --json: exit=%d, want %d", code, exitValid)
	}
	var row convertJSON
	if err := json.Unmarshal([]byte(stdout), &row); err != nil {
		t.Fatalf("convert --json not valid JSON: %v\n%s", err, stdout)
	}
	if !row.Valid || row.Output != out || row.Iterations < 1 {
		t.Errorf("convert --json = %+v, want valid with output=%s", row, out)
	}
}

func TestConvertDefaultOutputName(t *testing.T) {
	if got := defaultOutput("/a/b/doc.pdf", "pdfa1b"); got != "/a/b/doc.pdfa.pdf" {
		t.Errorf("defaultOutput pdfa1b = %q", got)
	}
	if got := defaultOutput("/a/b/doc.pdf", "pdf"); got != "/a/b/doc.fixed.pdf" {
		t.Errorf("defaultOutput pdf = %q", got)
	}
}

func TestContextCancelledConvert(t *testing.T) {
	_, fail := veraFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb bytes.Buffer
	code := run(ctx, []string{"convert", fail, filepath.Join(t.TempDir(), "x.pdf")}, &out, &errb)
	if code != exitError {
		t.Errorf("cancelled convert: exit=%d, want %d", code, exitError)
	}
}
