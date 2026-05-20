package cmd_test

// Black-box test: invoke the built binary with various flag combinations
// and assert exit code + stderr substring.

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "claw")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/claw")
	cmd.Dir = repoRoot(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	b, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(b))
}

func runFlag(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return stderr.String(), exit
}

func TestRemovedTrimStrategyFlagHardErrors(t *testing.T) {
	bin := buildBinary(t)
	stderr, exit := runFlag(t, bin, "--trim-strategy=window", "--prompt=hi")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr, "--trim-strategy") || !strings.Contains(stderr, "removed") {
		t.Fatalf("stderr missing migration message: %q", stderr)
	}
	if !strings.Contains(stderr, "--compact-") {
		t.Fatalf("stderr missing new-flag hint: %q", stderr)
	}
}

func TestRemovedMaxContextTurnsFlagHardErrors(t *testing.T) {
	bin := buildBinary(t)
	stderr, exit := runFlag(t, bin, "--max-context-turns=6", "--prompt=hi")
	if exit == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(stderr, "--compact-recent-turns") {
		t.Fatalf("stderr missing replacement-flag hint: %q", stderr)
	}
}

func TestRemovedMaxContextTokensFlagHardErrors(t *testing.T) {
	bin := buildBinary(t)
	stderr, exit := runFlag(t, bin, "--max-context-tokens=32000", "--prompt=hi")
	if exit == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(stderr, "--compact-fallback-limit") {
		t.Fatalf("stderr missing replacement-flag hint: %q", stderr)
	}
}

func TestNewCompactFlagsAccepted(t *testing.T) {
	bin := buildBinary(t)
	// We can't actually call a live provider, but flag-parse alone
	// should not fail. Use --help to bypass provider init.
	cmd := exec.Command(bin, "--compact-recent-turns=4",
		"--compact-fallback-limit=32000", "--compact-safety-factor=0.75",
		"--compact-max-tool-bytes=65536", "--help")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // --help exits 0 or 2 depending; just assert flag-parse didn't break
	if strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("new flags missing: %q", stderr.String())
	}
}
