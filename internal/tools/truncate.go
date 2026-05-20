package tools

import (
	"fmt"
	"unicode/utf8"
)

// TruncateForLLM returns s unchanged when it fits in maxBytes, otherwise
// returns a head+tail elision with the project's standard marker.
// Existing callers rely on the standard marker text; new callers that
// need a different marker should use TruncateWithMarker directly.
func TruncateForLLM(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	standardMarker := func(elided int) string {
		return fmt.Sprintf("\n\n...[%d bytes elided of %d total]...\n\n", elided, len(s))
	}
	return truncateInternal(s, maxBytes, standardMarker)
}

// TruncateWithMarker is the general form: caller supplies the marker
// string. The marker is inserted between the head and tail slices, and
// the head/tail cuts are backed up to the nearest valid UTF-8 rune
// boundary so the result is always valid UTF-8.
func TruncateWithMarker(s string, maxBytes int, marker string) string {
	if len(s) <= maxBytes {
		return s
	}
	return truncateInternal(s, maxBytes, func(_ int) string { return marker })
}

func truncateInternal(s string, maxBytes int, markerFn func(elided int) string) string {
	if maxBytes <= 0 {
		return markerFn(len(s))
	}
	half := maxBytes / 2
	headEnd := safeRuneBoundaryDown(s, half)
	tailStart := safeRuneBoundaryUp(s, len(s)-half)
	if tailStart <= headEnd {
		tailStart = headEnd
	}
	elided := tailStart - headEnd
	return s[:headEnd] + markerFn(elided) + s[tailStart:]
}

func safeRuneBoundaryDown(s string, max int) int {
	if max >= len(s) {
		return len(s)
	}
	for i := max; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return 0
}

func safeRuneBoundaryUp(s string, min int) int {
	if min <= 0 {
		return 0
	}
	if min >= len(s) {
		return len(s)
	}
	for i := min; i < len(s); i++ {
		if utf8.RuneStart(s[i]) {
			return i
		}
	}
	return len(s)
}
