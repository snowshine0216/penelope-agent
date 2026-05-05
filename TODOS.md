# TODOS

## Engine

### Fix consecutive-assistant-message bug in thinking mode
**Priority:** P1
**Description:** When `--think` is enabled and a turn produces tool calls, the engine appends the think-phase assistant message followed immediately by the act-phase assistant message. Both Anthropic and OpenAI APIs require strict user/assistant alternation — two consecutive assistant messages cause a 400 validation error on the second thinking turn. The bug only triggers in multi-turn sessions with `--think` enabled.
**Fix:** Do not persist the think-phase response in `contextHistory`. Display it locally via `fmt.Printf("[think] ...")` but exclude it from the shared history passed to the provider.

### Add SIGINT / SIGTERM graceful shutdown
**Priority:** P2
**Description:** `main.go` uses `context.Background()` with no signal handler. Ctrl-C kills the process via the default SIGINT handler, leaving in-flight bash child processes as orphans. Use `signal.NotifyContext` so the engine's `ctx.Err()` check fires and the agent can clean up.

### Cap context history to prevent context-window exhaustion
**Priority:** P2
**Description:** `contextHistory` grows unboundedly across turns with no token budget. A 25-turn session with 5 tool calls per turn accumulates ~130 messages. When the model's context window fills, the API returns a fatal error and the entire session is lost. Add a sliding-window trim or summarisation pass before each `Generate` call.

## Tools

### Fix intermediate-symlink escape in safepath
**Priority:** P2
**Description:** `ResolveInWorkDir` calls `filepath.EvalSymlinks` only when the final path already exists. Intermediate directory components that are symlinks (created via the bash tool) are not detected for new file writes. Walk each component and resolve symlinks at each directory level, or check every existing prefix before creating a new file.

### Kill bash process group on timeout / cancellation
**Priority:** P2
**Description:** `exec.CommandContext` sends `SIGKILL` to the bash process only, not its process group. Background subprocesses (`cmd &`) are orphaned when the context is cancelled. Set `SysProcAttr{Setpgid: true}` and kill the process group (`-pid`) on cancellation.

### Honour context cancellation in read_file
**Priority:** P3
**Description:** `read_file` accepts `ctx` but never uses it. `os.Open` and `io.ReadAll` are not context-aware. A named pipe or slow filesystem could block indefinitely regardless of context cancellation. For a simple fix, use `os.File.SetReadDeadline` or read in a goroutine with a select on the done channel.

## Completed

<!-- Items completed in v0.1.0.0 are tracked in CHANGELOG.md -->
