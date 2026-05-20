# MASTER-PLAN — Adaptive Semantic Compaction

## Mode

spec → single-item loop (N=1). Plan step engages `superpowers:writing-plans` to convert the design spec into a TDD task list at `items/001-plan.md`.

## Phase order

```
[Phase 0  intake + mode detect]  ──  DONE
[Phase 1  decompose + design artifacts]  ──  IN PROGRESS
[Phase 2  per-item loop, N=1]
   ├── 2.1 plan      (writing-plans, Opus subagent)
   ├── 2.2 implement (subagent-driven-development, Sonnet subagent)
   ├── 2.3 ship PR   (gstack-ship → PR against main, NEVER auto-merge)
   ├── 2.4 QA + review (parallel Sonnet subagents)
   ├── 2.5 triage + fix loop (Sonnet fix subagents, no retry budget — only env stops)
   └── 2.6 merge: SKIPPED for this run; PR left open for user (main is protected)
[Phase 3  final validation: go test ./... + handoff]
```

## Model contract (hard rule)

| Subagent role | Model | Why |
| --- | --- | --- |
| Plan author | `opus` | Authoring intent; Sonnet drifts on requirements |
| Implementer (per task in plan) | `sonnet` | Execution against a well-specified plan |
| QA | `sonnet` | Runs tests, evaluates outputs |
| Code reviewer | `sonnet` | Diff-based review |
| Fix subagents | `sonnet` | Targeted fixes against review findings |

Every `Agent(...)` dispatch from the orchestrator MUST include `model=` explicitly.

## Critical preservation rule (load-bearing user constraint)

Every downstream subagent prompt must repeat this verbatim:

> The user explicitly asked: "make sure keep the real e2e test case for me to verify also."
>
> Two test artifacts MUST be preserved through every phase:
>
> 1. **Layer 4** — `testdata/compact/*.jsonl` real-session fixtures + `testdata/compact/golden/*` goldens; tests under `tests/engine/compact_realcase_test.go` run by default.
> 2. **Layer 5** — `//go:build live_provider` smoke test under `tests/engine/compact_live_test.go` (or similar) that hits real Claude API, gated by env `ANTHROPIC_API_KEY`. Documented in `CLAUDE.md` so user can run `go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude` themselves.
>
> NEVER delete, stub, or `t.Skip` these unconditionally. If a fixture can't be produced at full size, commit a smaller representative one and document the gap.

## Tracker

See `PROGRESS.md`.

## Skipped items

See `SKIPPED.md` (empty by intent — N=1, no decomposition).

## Final validation gate

- `go test ./... -count=1` PASS on the feature branch tip
- `git status --short` empty
- PR open from feature branch into `main`, **not merged**
- e2e tests (Layer 4) execute under default test run; Layer 5 compiles and is documented
