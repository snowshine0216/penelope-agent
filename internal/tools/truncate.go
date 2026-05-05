package tools

import (
	"fmt"
	"unicode/utf8"
)

// TruncateForLLM returns s unchanged when it fits in maxBytes. Otherwise
// it returns roughly the first half and the last half of the budget joined
// by an elision marker, with both cuts backed up to the nearest valid
// UTF-8 rune boundary so the result is always valid UTF-8.
func TruncateForLLM(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 0 {
		return fmt.Sprintf("...[%d bytes elided]...", len(s))
	}

	half := maxBytes / 2
	headEnd := safeRuneBoundaryDown(s, half)
	tailStart := safeRuneBoundaryUp(s, len(s)-half)

	if tailStart <= headEnd {
		tailStart = headEnd
	}

	elided := tailStart - headEnd
	return s[:headEnd] +
		fmt.Sprintf("\n\n...[%d bytes elided of %d total]...\n\n", elided, len(s)) +
		s[tailStart:]
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
