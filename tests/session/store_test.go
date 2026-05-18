package session_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/schema"
	"github.com/snowshine0216/penelope-agent/internal/session"
)

func TestNewSessionCreatesFileAndDirectoryWith0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh", "sessions")
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}
	if !session.IsValidID(s.ID()) {
		t.Fatalf("session id = %q, not a valid id", s.ID())
	}
	if _, err := os.Stat(filepath.Join(dir, s.ID()+".jsonl")); err != nil {
		t.Fatalf("session file not created: %v", err)
	}
}

func TestAppendAndOpenSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	first, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	want := []schema.Message{
		{Role: schema.RoleUser, Content: "hello"},
		{Role: schema.RoleAssistant, Content: "hi back"},
	}
	for _, m := range want {
		if err := first.Append(m); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := session.OpenSession(first.ID(), dir)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer second.Close()

	got := second.Messages()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("msg[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestOpenSessionMissingFileErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := session.OpenSession("20260518-093045-a1b2c3", dir)
	if err == nil {
		t.Fatal("expected error for missing session file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want 'not found'", err)
	}
}

func TestOpenSessionRejectsCorruptLine(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	id := s.ID()
	s.Close()

	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"ok\"}\nnot valid json\n"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	_, err = session.OpenSession(id, dir)
	if err == nil {
		t.Fatal("expected corruption error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error = %q, want mention of line 2", err)
	}
}

func TestOpenSessionInvalidIDRejected(t *testing.T) {
	_, err := session.OpenSession("../etc/passwd", t.TempDir())
	if err == nil {
		t.Fatal("expected invalid-id error")
	}
}

func TestAppendsAreLineDelimited(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.Append(schema.Message{Role: schema.RoleUser, Content: "a"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.Append(schema.Message{Role: schema.RoleAssistant, Content: "b"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	id := s.ID()
	s.Close()

	bytes, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(bytes), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var m schema.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i+1, err)
		}
	}
}

func TestConcurrentAppendKeepsEveryLineParseable(t *testing.T) {
	dir := t.TempDir()
	s, err := session.NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	id := s.ID()

	const writers = 2
	const perWriter = 50

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			peer, err := session.OpenSession(id, dir)
			if err != nil {
				t.Errorf("OpenSession: %v", err)
				return
			}
			defer peer.Close()
			for i := 0; i < perWriter; i++ {
				err := peer.Append(schema.Message{
					Role:    schema.RoleUser,
					Content: strings.Repeat("x", 100),
				})
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	s.Close()

	bytes, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(bytes), "\n"), "\n")
	if len(lines) != writers*perWriter {
		t.Fatalf("got %d lines, want %d", len(lines), writers*perWriter)
	}
	for i, line := range lines {
		var m schema.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d invalid JSON: %v\nline=%q", i+1, err, line)
		}
	}
}

func TestNewInMemorySessionDoesNotPersist(t *testing.T) {
	s := session.NewInMemory()
	defer s.Close()
	if err := s.Append(schema.Message{Role: schema.RoleUser, Content: "transient"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(s.Messages()) != 1 {
		t.Fatalf("len = %d, want 1", len(s.Messages()))
	}
	if s.ID() == "" {
		t.Fatal("in-memory session id should be non-empty for log lines")
	}
}
