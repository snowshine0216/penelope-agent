package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	agentcontext "github.com/snowshine0216/penelope-agent/internal/context"
	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
	agentsession "github.com/snowshine0216/penelope-agent/internal/session"
	"github.com/snowshine0216/penelope-agent/internal/tools"
)

func main() {
	prompt := flag.String("prompt", "", "user prompt; if empty, read from stdin")
	think := flag.Bool("think", false, "enable thinking phase before each action")
	providerName := flag.String("provider", "openai", "provider: openai or claude")
	model := flag.String("model", "", "model id; defaults to LLM_MODEL env or provider default")
	maxTurns := flag.Int("max-turns", 25, "max engine turns per run")
	maxTokens := flag.Int("max-tokens", 4096, "max output tokens (claude only)")
	workDir := flag.String("workdir", "", "workspace root; defaults to cwd")
	sessionID := flag.String("session", "", "resume the named session; empty creates a fresh one")
	sessionsDir := flag.String("sessions-dir", "", "directory for session files; defaults to <workdir>/.claw/sessions")
	maxContextTurns := flag.Int("max-context-turns", 6, "trim window: keep this many recent user turns")
	maxContextTokens := flag.Int("max-context-tokens", 32000, "trim window: hard ceiling on estimated tokens (chars/4)")
	trimStrategy := flag.String("trim-strategy", "window", "trim strategy name; v1 ships only 'window'")
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

	trimmer, err := agentsession.Get(*trimStrategy, agentsession.TrimConfig{
		MaxUserTurns: *maxContextTurns,
		MaxTokens:    *maxContextTokens,
	})
	if err != nil {
		log.Fatalf("trim strategy: %v", err)
	}

	eng := engine.NewAgentEngine(llm, registry, cwd, *think)
	eng.SetContextManager(contextManager)
	eng.SetSession(sess)
	eng.SetTrimmer(trimmer)
	eng.MaxTurns = *maxTurns
	reporter := engine.NewTerminalReporter()

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

// openOrCreateSession resolves the --session flag into a Session.
// Empty id creates a fresh session; non-empty id resumes (hard error
// if the file is missing) and rejects ids that fail format validation
// to block path traversal at the flag boundary.
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
