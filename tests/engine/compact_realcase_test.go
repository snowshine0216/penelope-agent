package engine_test

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	"github.com/snowshine0216/penelope-agent/internal/schema"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/compact/golden/* from current behaviour (run from tests/engine package)")

var pathologicalOnce sync.Once
var pathologicalPath string

// TestMain synthesises session-pathological.jsonl at test-time (single
// tool output ~200 MB). We never commit it to git — the helper creates
// the file in TempDir and a global path holds it for the test cases.
func TestMain(m *testing.M) {
	flag.Parse()
	pathologicalOnce.Do(func() {
		dir, err := os.MkdirTemp("", "claw-pathological-*")
		if err != nil {
			panic(err)
		}
		path := filepath.Join(dir, "session-pathological.jsonl")
		f, err := os.Create(path)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		writeMsg := func(m schema.Message) {
			b, _ := json.Marshal(m)
			f.Write(b)
			f.WriteString("\n")
		}
		writeMsg(schema.Message{Role: schema.RoleUser, Content: "find everything"})
		args, _ := json.Marshal(map[string]string{"command": "find /"})
		writeMsg(schema.Message{
			Role: schema.RoleAssistant,
			ToolCalls: []schema.ToolCall{{ID: "call_path_huge", Name: "bash", Arguments: args}},
		})
		huge := strings.Repeat("x", 200*1024*1024) // 200 MB
		writeMsg(schema.Message{Role: schema.RoleTool, Content: huge, ToolCallID: "call_path_huge"})
		writeMsg(schema.Message{Role: schema.RoleUser, Content: "summarise"})
		writeMsg(schema.Message{Role: schema.RoleAssistant, Content: "many files"})
		pathologicalPath = path
	})
	os.Exit(m.Run())
}

func loadFixture(t *testing.T, path string) []schema.Message {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %q: %v", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 256*1024*1024) // accommodate pathological
	var out []schema.Message
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "//") || line == "" {
			continue
		}
		var m schema.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("fixture %q: bad json: %v", path, err)
		}
		out = append(out, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("fixture %q: scan: %v", path, err)
	}
	return out
}

func budgetForClaudeOpus47() int {
	return compact.Budget(compact.BudgetInput{
		Model:        "claude-opus-4-7",
		OutputCap:    4096,
		SafetyFactor: 0.75,
	})
}

// tightBudgetForLayerB returns a budget small enough that a fixture
// containing one 65 KB-truncated tool result will still exceed it and
// force Layer B. This mirrors what happens on real models when the
// session is large relative to the context window.
func tightBudgetForLayerB() int { return 12000 }

// fixtureRoot is the testdata directory relative to the tests/engine package.
// Go tests run with working dir = the package directory (tests/engine), so we
// step up two levels to the repo root.
const fixtureRoot = "../../testdata/compact"

func TestCompact_RealCase(t *testing.T) {
	cases := []struct {
		name               string
		fixture            string
		wantLayerB         bool
		wantSpilledAtLeast int
		budget             int
	}{
		// huge-bash: one 200 KB bash output.  After Layer A truncation to
		// MaxToolBytes (65 KB ≈ 16 K tokens) the session still exceeds the
		// tight budget, so Layer B must engage.
		{"huge-bash", fixtureRoot + "/session-huge-bash.jsonl", true, 0, tightBudgetForLayerB()},
		// many-edits: 50 edit_file calls; Layer A strips the large new_string
		// args down to tiny placeholders, bringing the session comfortably
		// under the full claude-opus-4-7 budget — Layer B must NOT engage.
		{"many-edits", fixtureRoot + "/session-many-edits.jsonl", false, 0, budgetForClaudeOpus47()},
		// mixed-tools: 80 turns of small read/bash/edit/write calls; fits
		// under full budget after Layer A — Layer B must NOT engage.
		{"mixed-tools", fixtureRoot + "/session-mixed-tools.jsonl", false, 0, budgetForClaudeOpus47()},
		// pathological: 200 MB tool output synthesised at test-time.
		// Layer A truncates to 65 KB; the primary assertion is no OOM.
		// The 5-message session stays inside the verbatim window so
		// Layer B does not fold — we assert it does NOT engage here.
		{"pathological", pathologicalPath, false, 0, tightBudgetForLayerB()},
	}

	c := compact.NewCompactor(compact.Config{
		MaxToolBytes:        65536,
		RecentTurnsVerbatim: 4,
	})
	cal := compact.NewCalibrator(0.3)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixture == "" {
				t.Skip("fixture path unset; TestMain may have failed")
			}
			history := loadFixture(t, tc.fixture)
			view, stats := c.View(history, tc.budget, 10, cal)

			if tc.wantLayerB && !stats.LayerBEngaged {
				t.Fatalf("Layer B expected but not engaged: %+v", stats)
			}
			if !tc.wantLayerB && stats.LayerBEngaged {
				t.Fatalf("Layer B engaged but not expected: %+v", stats)
			}

			if tc.name == "pathological" {
				return // do not write goldens for the 200 MB case
			}

			viewPath := filepath.Join(fixtureRoot, "golden", tc.name+".view.txt")
			digestPath := filepath.Join(fixtureRoot, "golden", tc.name+".digest.txt")
			statsPath := filepath.Join(fixtureRoot, "golden", tc.name+".stats.json")
			actualView := renderView(view)
			actualDigest := extractDigest(view)
			actualStats, _ := json.MarshalIndent(stats, "", "  ")

			if *updateGolden {
				_ = os.MkdirAll(filepath.Dir(viewPath), 0o755)
				_ = os.WriteFile(viewPath, []byte(actualView), 0o600)
				_ = os.WriteFile(digestPath, []byte(actualDigest), 0o600)
				_ = os.WriteFile(statsPath, actualStats, 0o600)
				return
			}

			assertFileEquals(t, viewPath, actualView)
			assertFileEquals(t, digestPath, actualDigest)
			assertFileEquals(t, statsPath, string(actualStats))
		})
	}
}

func renderView(view []schema.Message) string {
	var b strings.Builder
	for _, m := range view {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		if len(m.ToolCalls) > 0 {
			b.WriteString(" [calls=")
			for i, c := range m.ToolCalls {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(c.Name)
			}
			b.WriteString("]")
		}
		b.WriteString("\n---\n")
	}
	return b.String()
}

func extractDigest(view []schema.Message) string {
	for _, m := range view {
		if m.Role == schema.RoleAssistant && strings.HasPrefix(m.Content, "## Prior session digest") {
			return m.Content
		}
	}
	return "(no digest)"
}

func assertFileEquals(t *testing.T, path, got string) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q: %v (run go test -run TestCompact_RealCase -update to regenerate)", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch at %s:\nwant=%q\ngot=%q", path, string(want), got)
	}
}
