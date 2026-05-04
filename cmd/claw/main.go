package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/snowshine0216/penelope-agent/internal/engine"
	"github.com/snowshine0216/penelope-agent/internal/provider"
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
	if userPrompt == "" {
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
	registry.Register(tools.NewBashTool(cwd))

	eng := engine.NewAgentEngine(llm, registry, cwd, *think)
	eng.MaxTurns = *maxTurns

	if err := eng.Run(context.Background(), userPrompt); err != nil {
		log.Fatalf("engine: %v", err)
	}
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
