package session

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/snowshine0216/penelope-agent/internal/schema"
)

// Session owns the canonical conversation history for one conversation.
// On disk it is one JSONL file, append-only. Each Append acquires a
// per-call exclusive flock so concurrent processes serialize at the
// kernel level (see spec D12).
type Session struct {
	id       string
	file     *os.File // nil for in-memory sessions (tests)
	mu       sync.Mutex
	messages []schema.Message
}

// ID returns the session identifier. For persisted sessions this is the
// YYYYMMDD-HHMMSS-XXXXXX string used as the JSONL filename.
func (s *Session) ID() string { return s.id }

// Messages returns a snapshot of the in-memory history (system messages
// are not stored here; they are recomposed by the engine on every run).
func (s *Session) Messages() []schema.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneMessages(s.messages)
}

// Append persists one message and updates the in-memory slice. The
// on-disk write happens under flock; the in-memory update happens under
// the per-session mutex.
func (s *Session) Append(msg schema.Message) error {
	if s.file != nil {
		if err := s.persist(msg); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()
	return nil
}

// Close releases the file handle (and the kernel-held flock with it).
// Safe to call multiple times.
func (s *Session) Close() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *Session) persist(msg schema.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal session message: %w", err)
	}
	data = append(data, '\n')
	if err := lockExclusive(s.file.Fd()); err != nil {
		return err
	}
	defer func() { _ = unlock(s.file.Fd()) }()
	if _, err := s.file.Write(data); err != nil {
		return fmt.Errorf("write session line: %w", err)
	}
	return nil
}

// NewInMemory returns a Session that does not persist to disk. Intended
// for tests; production code uses NewSession or OpenSession.
func NewInMemory() *Session {
	return &Session{id: "in-memory"}
}
