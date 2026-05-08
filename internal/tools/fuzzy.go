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

	trimmedOld := strings.TrimSpace(normOld)
	if trimmedOld != "" {
		if out, ok, err := exactReplace(normContent, trimmedOld, newText, replaceAll); ok || err != nil {
			return out, 3, err
		}
	}

	if out, ok, err := lineByLineReplace(normContent, normOld, newText, replaceAll); ok || err != nil {
		return out, 4, err
	}

	return "", -1, fmt.Errorf("old_text not found")
}

// exactReplace handles L1.
func exactReplace(content, oldText, newText string, replaceAll bool) (string, bool, error) {
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", false, nil
	}
	if count > 1 && !replaceAll {
		return "", false, fmt.Errorf("old_text matched %d places", count)
	}
	return strings.ReplaceAll(content, oldText, newText), true, nil
}

// normalizeLineEndings replaces CRLF with LF.
func normalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// lineByLineReplace splits content and oldText by '\n', then slides a
// window of len(oldLines) over the content lines comparing each pair
// after TrimSpace. On a unique match, the replacement is reindented to
// the matched window's base indentation prefix.
func lineByLineReplace(content, oldText, newText string, replaceAll bool) (string, bool, error) {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(contentLines) {
		return "", false, nil
	}

	matches := findLineWindowMatches(contentLines, oldLines)
	if len(matches) == 0 {
		return "", false, nil
	}
	if len(matches) > 1 && !replaceAll {
		return "", false, fmt.Errorf("old_text matched %d places", len(matches))
	}

	// Process matches in reverse order so earlier indices remain valid.
	resultLines := append([]string(nil), contentLines...)
	for i := len(matches) - 1; i >= 0; i-- {
		start := matches[i]
		basePrefix := extractBasePrefix(resultLines[start])
		reindented := reindent(newText, basePrefix)
		newSegmentLines := strings.Split(reindented, "\n")

		head := resultLines[:start]
		tail := resultLines[start+len(oldLines):]
		combined := make([]string, 0, len(head)+len(newSegmentLines)+len(tail))
		combined = append(combined, head...)
		combined = append(combined, newSegmentLines...)
		combined = append(combined, tail...)
		resultLines = combined
	}

	return strings.Join(resultLines, "\n"), true, nil
}

// findLineWindowMatches returns the start indices of every contiguous
// content-line window of length len(oldLines) where each pair compares
// equal after TrimSpace. Match windows are non-overlapping (advance i
// by len(oldLines) after a hit).
func findLineWindowMatches(contentLines, oldLines []string) []int {
	var hits []int
	if len(oldLines) == 0 {
		return hits
	}
	i := 0
	for i+len(oldLines) <= len(contentLines) {
		matched := true
		for j := 0; j < len(oldLines); j++ {
			if strings.TrimSpace(contentLines[i+j]) != strings.TrimSpace(oldLines[j]) {
				matched = false
				break
			}
		}
		if matched {
			hits = append(hits, i)
			i += len(oldLines)
		} else {
			i++
		}
	}
	return hits
}

// extractBasePrefix returns the leading whitespace run of line.
// Edge case: if line consists entirely of whitespace, the full string
// is returned as the prefix. Callers should ensure the first matched
// window line is a non-empty content line to avoid over-indenting.
func extractBasePrefix(line string) string {
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c != ' ' && c != '\t' {
			return line[:i]
		}
	}
	return line
}

// reindent strips the common leading whitespace from non-empty lines
// of text, then prepends basePrefix to every non-empty line. Empty
// lines stay empty (no trailing whitespace is introduced).
func reindent(text, basePrefix string) string {
	lines := strings.Split(text, "\n")
	common := commonLeadingWhitespace(lines)

	out := make([]string, len(lines))
	for i, line := range lines {
		if line == "" {
			out[i] = ""
			continue
		}
		stripped := strings.TrimPrefix(line, common)
		out[i] = basePrefix + stripped
	}
	return strings.Join(out, "\n")
}

// commonLeadingWhitespace returns the longest leading whitespace
// prefix shared by every non-empty line. Returns "" if there are no
// non-empty lines.
func commonLeadingWhitespace(lines []string) string {
	first := true
	prefix := ""
	for _, line := range lines {
		if line == "" {
			continue
		}
		linePrefix := extractBasePrefix(line)
		if first {
			prefix = linePrefix
			first = false
			continue
		}
		prefix = longestCommonPrefix(prefix, linePrefix)
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

// longestCommonPrefix returns the longest string that is a prefix of
// both a and b.
func longestCommonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:n]
}
