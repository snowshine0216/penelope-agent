package tools

import (
	"fmt"
	"strings"
)

// FuzzyReplace runs the L1->L4 fuzzy match chain against content. It
// returns the new content, the level (1-4) that matched, and an error
// on miss or ambiguity. When replaceAll is true, the uniqueness check
// is relaxed at every level: multiple matches result in multiple
// replacements, and the chain still terminates at the first level
// that produces >=1 match.
func FuzzyReplace(content, oldText, newText string, replaceAll bool) (string, int, error) {
	if out, ok, err := exactReplace(content, oldText, newText, replaceAll); ok || err != nil {
		return out, 1, err
	}

	normContent := normalizeLineEndings(content)
	normOld := normalizeLineEndings(oldText)
	if out, ok, err := exactReplace(normContent, normOld, newText, replaceAll); ok || err != nil {
		return out, 2, err
	}

	return "", 0, fmt.Errorf("old_text not found")
}

// normalizeLineEndings replaces CRLF with LF.
func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// exactReplace handles L1: strings.Count == 1 (or replaceAll for any
// positive count). Returns (newContent, true, nil) on hit,
// ("", false, nil) on miss (so caller can fall through to L2),
// ("", true, err) on ambiguity (multiple matches without replaceAll).
func exactReplace(content, oldText, newText string, replaceAll bool) (string, bool, error) {
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", false, nil
	}
	if count > 1 && !replaceAll {
		return "", true, fmt.Errorf("matched %d places", count)
	}
	return strings.ReplaceAll(content, oldText, newText), true, nil
}
