package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/snowshine0216/penelope-agent/internal/compact"
)

// compactEventsPath returns the per-session audit log path. Located
// adjacent to the JSONL history rather than nested so a `ls` of the
// sessions dir surfaces every artefact for one session.
func (s *Session) compactEventsPath() string {
	if s.file == nil {
		return ""
	}
	dir := filepath.Dir(s.file.Name())
	return filepath.Join(dir, s.id+"-compact-events.jsonl")
}

// AppendCompactEvent appends one CompactStats line to the per-session
// audit log. Uses the same per-write flock pattern as Session.persist
// so concurrent processes serialize at the kernel level. In-memory
// sessions are a no-op (audit logging requires a stable on-disk path).
func (s *Session) AppendCompactEvent(stats compact.CompactStats) error {
	if s.file == nil {
		return nil
	}
	path := s.compactEventsPath()
	data, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("marshal compact event: %w", err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open compact audit %q: %w", path, err)
	}
	defer f.Close()
	if err := lockExclusive(f.Fd()); err != nil {
		return err
	}
	defer func() { _ = unlock(f.Fd()) }()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write compact audit: %w", err)
	}
	return nil
}
