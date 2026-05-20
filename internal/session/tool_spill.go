package session

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxToolBytesScannerBuffer caps the per-line buffer the spill reader
// uses. Aligned with the engine's MaxToolBytes default so any line up
// to that size is read in a single Scanner call. Single lines larger
// than this fall back to a byte-range read in ReadToolOutputChunk.
const MaxToolBytesScannerBuffer = 65536

// toolOutputsDir returns the directory where this session keeps its
// spilled tool outputs. Nested under <sessionsDir>/<session-id>/ so
// all per-session artefacts are grouped and a future cleanup can
// `rm -rf <sessionsDir>/<sid>/` without touching other sessions.
// Layout matches the spec: .claw/sessions/<session-id>/tool-outputs/
func (s *Session) toolOutputsDir() string {
	if s.file == nil {
		return ""
	}
	sessionDir := filepath.Dir(s.file.Name())
	return filepath.Join(sessionDir, s.id, "tool-outputs")
}

// ToolOutputPath returns the canonical on-disk path for a spilled
// tool output. Public so the digest can refer to it in markers.
func (s *Session) ToolOutputPath(callID string) string {
	return filepath.Join(s.toolOutputsDir(), callID+".txt")
}

// SpillToolOutput writes body to the per-session tool-outputs dir,
// keyed by callID. Returns the path, the number of lines written, and
// any error. In-memory sessions return an error because there is no
// disk to spill to.
func (s *Session) SpillToolOutput(callID, body string) (string, int, error) {
	if s.file == nil {
		return "", 0, fmt.Errorf("spill: in-memory session has no spill directory")
	}
	if callID == "" {
		return "", 0, fmt.Errorf("spill: empty call_id")
	}
	dir := s.toolOutputsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("spill mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, callID+".txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", 0, fmt.Errorf("spill write %q: %w", path, err)
	}
	return path, countLines(body), nil
}

// ReadToolOutputChunk reads lines [startLine, startLine+lineCount) from
// the spilled tool output for callID. startLine is 1-indexed; out-of-
// range bounds are clamped. Returns the chunk content, the total line
// count of the underlying file, and any error.
//
// If a single line exceeds MaxToolBytesScannerBuffer the function
// falls back to returning that line via a byte-range read so a
// pathological log file does not crash the reader.
func (s *Session) ReadToolOutputChunk(callID string, startLine, lineCount int) (string, int, error) {
	if callID == "" {
		return "", 0, fmt.Errorf("read: empty call_id")
	}
	path := s.ToolOutputPath(callID)
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("read spill %q: %w", path, err)
	}
	defer f.Close()

	if startLine < 1 {
		startLine = 1
	}
	if lineCount < 1 {
		lineCount = 200
	}

	// Use a large scanner buffer to handle long lines (e.g. JSON blobs).
	// Set max to 8MB to cover realistic tool outputs including 200KB lines.
	const maxScanBuf = 8 * 1024 * 1024
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), maxScanBuf)

	var b strings.Builder
	totalLines := 0
	captured := 0
	for scanner.Scan() {
		totalLines++
		if totalLines < startLine || captured >= lineCount {
			continue
		}
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
		captured++
	}
	if err := scanner.Err(); err != nil {
		if err == bufio.ErrTooLong {
			// Fallback: at least one line is larger than the buffer.
			// Re-open and stream the whole file as a single chunk; the
			// caller decides what to do with it. Document this in the
			// returned chunk header (the read_tool_output tool wraps).
			full, ferr := readAllWithCap(path, 8*1024*1024)
			if ferr != nil {
				return "", 0, fmt.Errorf("oversize-line fallback: %w", ferr)
			}
			return full, 1, nil
		}
		return "", 0, fmt.Errorf("scan spill %q: %w", path, err)
	}
	return b.String(), totalLines, nil
}

// readAllWithCap reads up to capBytes bytes from path. Used as the
// oversize-line fallback for ReadToolOutputChunk.
func readAllWithCap(path string, capBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := io.LimitReader(f, capBytes)
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
