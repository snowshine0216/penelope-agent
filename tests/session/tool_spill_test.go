package session_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestSpillRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	body := "line 1\nline 2\nline 3\n"
	path, lines, err := sess.SpillToolOutput("call_abc", body)
	if err != nil {
		t.Fatalf("spill: %v", err)
	}
	if lines != 3 {
		t.Fatalf("lines = %d, want 3", lines)
	}
	if !strings.Contains(path, "call_abc.txt") {
		t.Fatalf("path = %q, want suffix call_abc.txt", path)
	}

	chunk, total, err := sess.ReadToolOutputChunk("call_abc", 1, 200)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if total != 3 {
		t.Fatalf("totalLines = %d, want 3", total)
	}
	if !strings.Contains(chunk, "line 1") || !strings.Contains(chunk, "line 3") {
		t.Fatalf("chunk missing data: %q", chunk)
	}
}

func TestSpillChunkBoundaries(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	var b strings.Builder
	for i := 1; i <= 100; i++ {
		b.WriteString("line ")
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	if _, _, err := sess.SpillToolOutput("c", b.String()); err != nil {
		t.Fatalf("spill: %v", err)
	}

	chunk, total, err := sess.ReadToolOutputChunk("c", 50, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if total != 100 {
		t.Fatalf("totalLines = %d, want 100", total)
	}
	if !strings.Contains(chunk, "line 50") {
		t.Fatalf("missing line 50: %q", chunk)
	}
	if !strings.Contains(chunk, "line 59") {
		t.Fatalf("missing line 59: %q", chunk)
	}
	if strings.Contains(chunk, "line 60") {
		t.Fatalf("contains line 60: %q", chunk)
	}
}

func TestSpillMissingCallID(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	if _, _, err := sess.ReadToolOutputChunk("never_spilled", 1, 200); err == nil {
		t.Fatal("expected error for unknown call_id")
	}
}

func TestSpillVeryLongLine(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	huge := strings.Repeat("x", 200_000) + "\n"
	if _, _, err := sess.SpillToolOutput("big", huge); err != nil {
		t.Fatalf("spill: %v", err)
	}
	chunk, total, err := sess.ReadToolOutputChunk("big", 1, 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if total != 1 {
		t.Fatalf("totalLines = %d, want 1", total)
	}
	if len(chunk) < 100_000 {
		t.Fatalf("chunk truncated: len=%d", len(chunk))
	}
}

func TestSpillFileLocation(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	if _, _, err := sess.SpillToolOutput("c1", "x"); err != nil {
		t.Fatalf("spill: %v", err)
	}
	// Spec layout: .claw/sessions/<session-id>/tool-outputs/<call-id>.txt
	expected := filepath.Join(dir, sess.ID(), "tool-outputs", "c1.txt")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("spill file not at spec path %q: %v", expected, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := []byte{}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
