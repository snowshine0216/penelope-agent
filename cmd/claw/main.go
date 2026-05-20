package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/snowshine0216/penelope-agent/internal/compact"
	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

// removedFlag intercepts flags that were removed in the adaptive-compaction
// migration. flag.Set on it always returns a hard error with a hint at the
// replacement.
type removedFlag struct {
	name        string
	replacement string
}

func (r *removedFlag) String() string { return "" }
func (r *removedFlag) Set(string) error {
	return fmt.Errorf("--%s was removed; use --%s (adaptive semantic compaction)", r.name, r.replacement)
}

func main() {
	prompt := flag.String("prompt", "", "user prompt; if empty, read from stdin")
	think := flag.Bool("think", false, "enable thinking phase before each action")
	providerName := flag.String("provider", "openai", "provider: openai or claude")
	model := flag.String("model", "", "model id; defaults to LLM_MODEL env or provider default")
	maxTurns := flag.Int("max-turns", 25, "max engine turns per run")
	maxTokens := flag.Int("max-tokens", 4096, "max output tokens (claude only); also used as Budget OutputCap")
	workDir := flag.String("workdir", "", "workspace root; defaults to cwd")
	sessionID := flag.String("session", "", "resume the named session; empty creates a fresh one")
	sessionsDir := flag.String("sessions-dir", "", "directory for session files; defaults to <workdir>/.claw/sessions")

	// New compact flags.
	compactRecentTurns := flag.Int("compact-recent-turns", 4, "verbatim window: keep this many recent user turns un-stripped")
	compactFallbackLimit := flag.Int("compact-fallback-limit", 32000, "context limit when --model is unknown to the registry")
	compactSafetyFactor := flag.Float64("compact-safety-factor", 0.75, "fraction of the model's context window to consume")
	compactMaxToolBytes := flag.Int("compact-max-tool-bytes", 65536, "tool result boundary cap; over this triggers disk spill")

	// Removed flags — hard error pointing at the replacement.
	flag.Var(&removedFlag{name: "trim-strategy", replacement: "compact-* (the trim strategy registry was removed)"}, "trim-strategy", "REMOVED: see --compact-* flags")
	flag.Var(&removedFlag{name: "max-context-turns", replacement: "compact-recent-turns"}, "max-context-turns", "REMOVED: use --compact-recent-turns")
	flag.Var(&removedFlag{name: "max-context-tokens", replacement: "compact-fallback-limit"}, "max-context-tokens", "REMOVED: use --compact-fallback-limit")

	flag.Parse()

	cwd := *workDir
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			log.Fatalf("get cwd: %v", err)
		}
	}

	userPrompt := *prompt
	if userPrompt == "" && !isTerminal(os.Stdin) {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalf("read stdin: %v", err)
		}
		userPrompt = string(stdin)
	}
	if userPrompt == "" {
		fmt.Fprintln(os.Stderr, "no prompt provided (use --prompt or pipe to stdin)")
		os.Exit(2)
	}

	llm, err := newProvider(*providerName, *model, *maxTokens)
	if err != nil {
		log.Fatalf("init provider: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(cwd))
	registry.Register(tools.NewWriteFileTool(cwd))
	registry.Register(tools.NewEditFileTool(cwd))
	registry.Register(tools.NewBashTool(cwd))

	contextManager, err := agentcontext.NewManager(cwd)
	if err != nil {
		log.Fatalf("init context: %v", err)
	}
	if contextManager.HasSkills() {
		registry.Register(agentcontext.NewLoadSkillTool(contextManager))
	}

	resolvedSessionsDir := *sessionsDir
	if resolvedSessionsDir == "" {
		resolvedSessionsDir = filepath.Join(cwd, ".claw", "sessions")
	}

	// Load optional model-limits override.
	overridesPath := filepath.Join(cwd, ".claw", "model-limits.yaml")
	overrides, err := compact.LoadOverridesYAML(overridesPath)
	if err != nil {
		log.Fatalf("model-limits override: %v", err)
	}

	sess, resumed, err := openOrCreateSession(*sessionID, resolvedSessionsDir)
	if err != nil {
		log.Fatalf("session: %v", err)
	}
	defer sess.Close()
	if resumed {
		fmt.Fprintf(os.Stderr, "session: %s (resumed, %d messages)\n", sess.ID(), len(sess.Messages()))
	} else {
		fmt.Fprintf(os.Stderr, "session: %s\n", sess.ID())
	}

	// Register read_tool_output now that the session exists.
	registry.Register(tools.NewReadToolOutputTool(sess, *compactMaxToolBytes))

	resolvedModel := *model
	if resolvedModel == "" {
		resolvedModel = os.Getenv("LLM_MODEL")
	}
	compactCfg := compact.Config{
		MaxToolBytes:        *compactMaxToolBytes,
		RecentTurnsVerbatim: *compactRecentTurns,
		Overrides:           overrides,
	}
	if _, ok := compact.LookupModelLimit(resolvedModel, overrides); !ok && resolvedModel != "" {
		// Documented in spec §0: unknown model => fallback limit; we
		// inject it into the overrides map so Budget() picks it up.
		overrides[resolvedModel] = *compactFallbackLimit
	}

	eng := engine.NewAgentEngine(llm, registry, cwd, *think)
	eng.SetContextManager(contextManager)
	eng.SetSession(sess)
	eng.SetCompactor(compact.NewCompactor(compactCfg))
	eng.SetCalibrator(compact.NewCalibrator(0.3))
	eng.SetCompactConfig(compactCfg)
	eng.SetModelID(resolvedModel)
	eng.SetOutputCap(*maxTokens)
	eng.SetSafetyFactor(*compactSafetyFactor)
	eng.SetModelLimitOverrides(overrides)
	eng.MaxTurns = *maxTurns

	reporter := engine.NewTerminalReporter()
	reporter.AttachSession(sess)

	if err := eng.Run(context.Background(), userPrompt, reporter); err != nil {
		log.Fatalf("engine: %v", err)
	}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func newProvider(name, model string, maxTokens int) (provider.LLMProvider, error) {
	switch name {
	case "openai":
		return provider.NewOpenAIProvider(model)
	case "claude":
		p, err := provider.NewClaudeProvider(model)
		if err != nil {
			return nil, err
		}
		if maxTokens > 0 {
			p.MaxTokens = int64(maxTokens)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: openai, claude)", name)
	}
}

func openOrCreateSession(id string, dir string) (*agentsession.Session, bool, error) {
	if id == "" {
		s, err := agentsession.NewSession(dir)
		if err != nil {
			return nil, false, err
		}
		return s, false, nil
	}
	if !agentsession.IsValidID(id) {
		return nil, false, fmt.Errorf("invalid session id %q (must match YYYYMMDD-HHMMSS-XXXXXX)", id)
	}
	s, err := agentsession.OpenSession(id, dir)
	if err != nil {
		return nil, false, err
	}
	return s, true, nil
}
