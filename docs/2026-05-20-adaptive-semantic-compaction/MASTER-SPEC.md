# MASTER-SPEC — Adaptive Semantic Compaction

## Target

`docs/superpowers/specs/2026-05-20-adaptive-semantic-compaction-design.md` — single approved design spec to replace the current `internal/session/{window,trim,tokens}.go` "drop oldest user turn" trimmer with a deterministic, semantics-preserving compactor; cap large tool outputs at the engine boundary with disk spill + paged retrieval; surface real model token usage; report compaction stats per turn.

## Mode

**spec** (single feature; goals/architecture authored, but no step-by-step plan with concrete commands → `superpowers:writing-plans` will produce `items/001-plan.md`).

## Run metadata

| Field | Value |
| --- | --- |
| Base branch | `main` (protected — never auto-merge) |
| Feature branch | `autodev/2026-05-20-adaptive-semantic-compaction` |
| Run dir | `docs/2026-05-20-adaptive-semantic-compaction/` |
| Date | 2026-05-20 |
| Orchestrator model | Opus 4.7 (1M ctx) — session default, respected |
| Spec/plan subagent model | `opus` (per autodev model contract) |
| Impl/QA/review/fix subagent model | `sonnet` |

## Critical user constraint (this turn)

**"Make sure keep the real e2e test case for me to verify also."**

This is a load-bearing user instruction. The implementation plan and every downstream phase MUST preserve:

1. **Layer 4 — Real-case verification fixtures** (`testdata/compact/*.jsonl` + `golden/`) captured from real `claw` runs. These live in-repo, run under default `go test ./...`, and validate the compactor against committed real-session fixtures.
2. **Layer 5 — Opt-in live-provider smoke test** (`//go:build live_provider`) — actually calls the real Claude API to demonstrate that a single huge tool output (e.g. `find / | head -100000`) no longer OOMs and that `read_tool_output` retrieves spilled chunks across turns. Gated by build tag so default `go test ./...` skips it; user runs it manually with `go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude`.

Implementation must NOT delete, weaken, or stub these. If a sub-task encounters trouble producing the fixtures, the right move is to commit a smaller representative fixture and document the gap — NOT to delete the test.

## IN-scope items

| ID | Title | Sub-branch | Notes |
| --- | --- | --- | --- |
| 001 | Adaptive semantic compaction (whole spec) | (feature branch — work lands here directly; per `AUTODEV-LOOP` precedent for single-feature runs) | Plan will decompose into TDD sub-tasks that each commit independently |

N=1 (spec mode). The plan step decomposes into ~15-20 TDD sub-tasks; each is a commit on this feature branch, not a sub-PR.

## OUT-of-scope items

| ID | Title | Reason |
| --- | --- | --- |
| — | Persistent cross-session calibrator learning | Explicit non-goal in spec |
| — | Mid-session model switching | Explicit non-goal |
| — | Pluggable strategy registry | Explicit non-goal |
| — | "Legacy window" escape hatch | Explicit non-goal |
| — | Move `internal/context/` into compaction | Spec clarifies `internal/context/` is out of scope |

## Final validation

After the implementation plan completes:

- `go test ./... -count=1` PASSES (default tags — Layers 1, 2, 3, 4).
- Layer 5 (`live_provider` tag) compiles and is documented in `CLAUDE.md` for user-driven verification.
- `git status --short` is clean.
- A PR exists from `autodev/2026-05-20-adaptive-semantic-compaction` against `main` for the user to review and merge themselves (no auto-merge to protected branch).
- The user can run the e2e tests they asked to keep.

## Protected-branch rule

`main` is the default branch and is protected. autodev will NEVER `gh pr merge` this PR into `main`. The PR is opened and left for the user.
