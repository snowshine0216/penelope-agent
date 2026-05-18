//go:build !windows

package session

import (
	"fmt"
	"syscall"
)

// lockExclusive acquires a process-wide POSIX advisory exclusive lock on
// the given file descriptor. The lock is released when the descriptor is
// closed; the kernel also releases it on process death so there is no
// stale-lock cleanup to do. Called once per Append; serializes concurrent
// writers at the kernel level so each appended JSONL line is intact.
func lockExclusive(fd uintptr) error {
	if err := syscall.Flock(int(fd), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock LOCK_EX: %w", err)
	}
	return nil
}

func unlock(fd uintptr) error {
	if err := syscall.Flock(int(fd), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("flock LOCK_UN: %w", err)
	}
	return nil
}
