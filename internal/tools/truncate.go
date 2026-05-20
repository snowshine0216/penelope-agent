package tools

import (
	"github.com/snowshine0216/penelope-agent/internal/truncate"
)

// TruncateForLLM returns s unchanged when it fits in maxBytes, otherwise
// returns a head+tail elision with the project's standard marker.
// Delegates to internal/truncate to avoid the compact→tools→session cycle.
func TruncateForLLM(s string, maxBytes int) string {
	return truncate.ForLLM(s, maxBytes)
}

// TruncateWithMarker is the general form: caller supplies the marker
// string. Delegates to internal/truncate to avoid the compact→tools→session cycle.
func TruncateWithMarker(s string, maxBytes int, marker string) string {
	return truncate.WithMarker(s, maxBytes, marker)
}
