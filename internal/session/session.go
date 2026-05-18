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
//
// In-process callers must serialize Append/Close themselves OR rely on
// the per-session mutex below: Append, Close, and Messages all take
// `mu`, so concurrent goroutines see a consistent disk-then-memory
// ordering and never race on `s.file`.
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

// Append persists one message and updates the in-memory slice. Both
// happen under `s.mu` so two goroutines in the same process can call
// Append concurrently without the on-disk and in-memory orderings
// diverging. Cross-process ordering is enforced separately by the
// per-write flock acquired inside persist().
func (s *Session) Append(msg schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		if err := s.persist(msg); err != nil {
			return err
		}
	}
	s.messages = append(s.messages, msg)
	return nil
}

// Close releases the file handle (and the kernel-held flock with it).
// Takes `s.mu` so a concurrent Append is not surprised by `s.file`
// flipping to nil mid-write. Safe to call multiple times.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	defer func() {
		if uerr := unlock(s.file.Fd()); uerr != nil {
			// Logging is not available here; a failed unlock leaves the
			// lock held and the next Append will block on flock. That
			// is loud enough to investigate without dropping a log line
			// from a leaf I/O helper.
			_ = uerr
		}
	}()
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
