package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

var idPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{6}$`)

// NewID returns a session id of the form YYYYMMDD-HHMMSS-XXXXXX, where
// XXXXXX is six lowercase hex chars from crypto/rand. The supplied time
// is formatted in UTC so two processes started at the same wall clock
// see the same prefix regardless of local timezone.
func NewID(now time.Time) string {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		// rand.Read uses /dev/urandom on POSIX and never errors in
		// practice; if it ever does, the deterministic zero suffix
		// is still a valid id and will collide loudly with itself.
		for i := range suffix {
			suffix[i] = 0
		}
	}
	return fmt.Sprintf("%s-%s", now.UTC().Format("20060102-150405"), hex.EncodeToString(suffix))
}

// IsValidID returns true iff s matches the canonical id format.
// The regex also blocks path traversal because / and . are excluded.
func IsValidID(s string) bool {
	return idPattern.MatchString(s)
}
