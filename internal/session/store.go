package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// NewSession creates a fresh session inside dir. The directory is created
// with mode 0700 if missing. The returned Session owns an open file
// handle in O_APPEND mode; close it when the run finishes.
func NewSession(dir string) (*Session, error) {
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	id := NewID(time.Now())
	path := filepath.Join(dir, id+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create session %q: %w", id, err)
	}
	return &Session{id: id, file: file}, nil
}

// OpenSession resumes an existing session by id. The id is validated
// against the canonical format to prevent path traversal. The full
// JSONL is parsed into memory; a malformed line aborts with the line
// number to make manual repair possible.
func OpenSession(id string, dir string) (*Session, error) {
	if !IsValidID(id) {
		return nil, fmt.Errorf("invalid session id %q (must match YYYYMMDD-HHMMSS-XXXXXX)", id)
	}
	path := filepath.Join(dir, id+".jsonl")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("session %s not found in %s", id, dir)
		}
		return nil, fmt.Errorf("open session %q: %w", id, err)
	}

	messages, err := readAllMessages(path, id)
	if err != nil {
		return nil, err
	}

	// Spec D12/D13: between readAllMessages (above) and this OpenFile, a
	// peer process may have appended further lines via flock. Those lines
	// are durable on disk but are missing from `messages`. Subsequent
	// Appends from this Session land at the file end via O_APPEND, so the
	// file itself stays consistent — but Messages() reflects a stale view
	// for the rest of this process's lifetime. The trimmer's defensive
	// cleanup is what rescues the provider view when this happens.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session %q: %w", id, err)
	}
	return &Session{id: id, file: file, messages: messages}, nil
}

// MissingError returns true if err is a "session not found" error from
// OpenSession; callers can branch on it for nicer messages.
func MissingError(err error) bool {
	return err != nil && errors.Is(err, os.ErrNotExist)
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sessions dir %q: %w", dir, err)
	}
	return nil
}

func readAllMessages(path string, id string) ([]schema.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session %q for read: %w", id, err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	out := []schema.Message{}
	lineNumber := 0
	for {
		lineNumber++
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := line
			if trimmed[len(trimmed)-1] == '\n' {
				trimmed = trimmed[:len(trimmed)-1]
			}
			if trimmed == "" {
				if err == io.EOF {
					break
				}
				continue
			}
			var msg schema.Message
			if jsonErr := json.Unmarshal([]byte(trimmed), &msg); jsonErr != nil {
				return nil, fmt.Errorf("session %s: corrupt at line %d: %w", id, lineNumber, jsonErr)
			}
			out = append(out, msg)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read session %q: %w", id, err)
		}
	}
	return out, nil
}
