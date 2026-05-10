# penelope-agent

A small Go agent loop that drives an LLM through tool use to accomplish
coding tasks in a sandboxed workspace. Forked from `go-tiny-claw`.

## What it does

Given a prompt, the agent:
1. Loads provider config from environment / `.env`.
2. Mounts a tool registry (bash, read_file, write_file).
3. Optionally runs a "thinking" phase (no tools, plan only).
4. Runs an action phase where the model can call tools; parallel-safe
   tool calls in the same turn may run concurrently while mutating or
   unknown tools remain serial.
5. Loops on tool results until the model stops asking for tools or
   `--max-turns` is hit.

## Quickstart

```bash
git clone https://github.com/snowshine0216/penelope-agent.git
cd penelope-agent

cat > .env <<'EOF'
LLM_API_KEY=your-zhipu-api-key
LLM_BASE_URL=https://open.bigmodel.cn/api/paas/v4/
LLM_MODEL=glm-4.5-air
EOF

go run ./cmd/claw --prompt "list the files in this repo"
```

## Configuration

Config is read from environment variables, falling back to `.env` walked
upward from the current directory.

| Var | Default | Notes |
|-----|---------|-------|
| `LLM_API_KEY` | (required) | Compatible: `MINIMAX_API_KEY`, `ZHIPU_API_KEY` |
| `LLM_BASE_URL` | `https://open.bigmodel.cn/api/paas/v4/` | OpenAI-compatible endpoint |
| `LLM_MODEL` | `glm-4.5-air` | Zhipu GLM by default |

## Flags

```
--prompt string      user prompt; if empty, read from stdin
--think              enable a planning phase with tools disabled before each action
--provider string    "openai" or "claude" (default "openai")
--model string       overrides LLM_MODEL
--max-turns int      cap on engine turns per run (default 25)
--max-tokens int     max output tokens, claude only (default 4096)
--workdir string     workspace root; defaults to cwd
```

## Tools

| Tool | Description | Sandbox |
|------|-------------|---------|
| `bash` | Run a shell command in the workdir | **Unsandboxed.** Every command is logged. |
| `read_file` | Read a file in the workdir | Path traversal blocked. Optional `offset`/`limit` for line pagination. |
| `write_file` | Write a file in the workdir | Path traversal blocked. Creates parent dirs. |
| `edit_file` | Apply string replacements to an existing file via fuzzy match (CRLF, whitespace, indentation). Multi-edit atomic; uniqueness enforced. | Path traversal blocked. Refuses non-existent files. |

Tool calls requested in the same assistant message are executed in
ordered groups. `read_file` opts into parallel execution; `bash`,
`write_file`, `edit_file`, and unknown tools stay serial. Tool results
are appended to model history in the original request order, not
completion order.

## Project layout

```
cmd/claw/         CLI entry point
internal/
  engine/         agent loop (thinking + action phases, turn cap, ctx cancel)
  provider/       LLM provider interface, Claude + OpenAI/Zhipu adapters
  schema/         shared message types
  tools/          tool implementations (bash, read_file, write_file) + registry
tests/            external test packages mirroring internal/ structure
docs/             design specs and implementation plans
```

Tests live outside the source tree on purpose — they exercise the public
surface only, which keeps the public API intentional.

## Known limitations

- `bash` is intentionally unsandboxed. The model can run any shell
  command in the workdir. Use a dedicated VM or container for untrusted
  prompts.
- The engine has no automatic retry for provider failures.
- Only OpenAI-compatible (Zhipu / MiniMax) and Anthropic API endpoints
  are supported. No Gemini, no local model adapters yet.

## License

MIT.
