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

## Dynamic context

At startup, `claw` composes the system prompt from:

1. The built-in `penelope-agent` operating instructions.
2. `${workdir}/AGENTS.md`, when present.
3. Frontmatter from local skills under `${workdir}/.claw/skills/*/SKILL.md`.
4. Full local skill bodies loaded later through the internal `load_skill` tool.

Only root `${workdir}/AGENTS.md` is loaded. Parent, nested, and global
instruction files are ignored in this version.

## Sessions

Every `claw` run is recorded as a session in `${workdir}/.claw/sessions/<id>.jsonl`.
The id is printed to stderr on the first line:

```
session: 20260518-093045-a1b2c3
```

Resume a conversation by passing the id back:

```bash
go run ./cmd/claw --prompt "another question" --session 20260518-093045-a1b2c3
```

What the model sees each turn is bounded by a trim strategy. The default
(`window`) keeps the last `--max-context-turns` user turns under a
`--max-context-tokens` chars/4 estimate, dropping the oldest user turn
first when either limit is exceeded. Orphan tool messages and dangling
tool_call/result pairs are removed defensively so the view sent to the
provider is always valid.

| Flag | Default | Notes |
|------|---------|-------|
| `--session` | (empty) | empty creates a fresh session; passed id resumes |
| `--sessions-dir` | `${workdir}/.claw/sessions` | override session storage location |
| `--max-context-turns` | `6` | window depth in user turns |
| `--max-context-tokens` | `32000` | estimated-token ceiling (chars/4) |
| `--trim-strategy` | `window` | currently the only built-in strategy |

Concurrent writers to the same session are permitted at the file
integrity layer via per-append `flock(LOCK_EX)`. Two processes resuming
the same session simultaneously may interleave their turns in ways the
trimmer best-effort cleans up; treat "one process per session" as the
recommended pattern.

Windows note: `flock` is not used on Windows, so concurrent writers on
that platform are not protected against torn lines. The project is
POSIX-focused (see the `bash` tool sandboxing notes).

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
| `load_skill` | Load full instructions for a relevant local skill listed in `.claw/skills` | Local skills only. Serial-only; acts as a turn barrier before normal tool work continues. |

Tool calls requested in the same assistant message are executed in
ordered groups. `read_file` opts into parallel execution; `bash`,
`write_file`, `edit_file`, and unknown tools stay serial. Tool results
are appended to model history in the original request order, not
completion order.

## Local skills

Local skills live under the workdir:

```text
.claw/skills/my-skill/
  SKILL.md
  scripts/
  references/
  assets/
```

`SKILL.md` must start with YAML frontmatter:

```yaml
---
name: my-skill
description: One sentence explaining when to use it.
---
```

The initial prompt includes only `name`, `description`, and optional aliases.
When the model decides a skill is relevant, it calls `load_skill` with the exact
skill name. The engine then inserts that skill's markdown body into the system
prompt for subsequent model calls.

## Project layout

```
cmd/claw/         CLI entry point
internal/
  context/        dynamic system prompt composition and local skill lazy loading
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
- Symlinks inside `.claw/skills/` are not followed. A skill directory
  that is itself a symlink, or whose `SKILL.md` is a symlink, is silently
  skipped and will not appear in the catalog or be loadable via `load_skill`.
- Sessions are append-only JSONL. Long sessions accumulate every message
  ever appended even though the model only sees the windowed view.
  A future `claw sessions compact <id>` command could rewrite the file
  to the windowed view; for now, inspection and pruning happen with
  `cat`, `head`, and `rm`.

## License

MIT.
