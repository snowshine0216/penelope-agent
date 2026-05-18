# AUTODEV-LOOP — Per-Session Context History And Pluggable Trimming

## Target

`docs/superpowers/plans/2026-05-18-session-management.md` — a single 9-task,
TDD-style implementation plan covering session persistence (JSONL),
pluggable trimming (`Trimmer` interface + `WindowTrimmer` default),
engine integration, CLI flags, and docs.

## Feature branch

`claude/agitated-thompson-9cd0a2` (current worktree branch). All work lands
here. One PR against `main` after all 9 tasks complete.

## IN-scope items (one item per plan task)

All 9 plan tasks are IN-scope. Each becomes its own commit on this branch
(no per-task sub-branches — the plan itself defines the commit boundaries
and the tasks build on one another sequentially, so the cost/benefit of
spinning a sub-branch per task is negative).

| ID | Task | Commit |
|----|------|--------|
| 001 | Session ID generator | feat(session): generate and validate session ids |
| 002 | Chars/4 token estimator | feat(session): chars/4 token estimator |
| 003 | Trimmer interface + registry | feat(session): trimmer interface and strategy registry |
| 004 | Window trimmer (default strategy) | feat(session): window trimmer with defensive cleanup |
| 005 | POSIX flock + Windows no-op | feat(session): per-append flock with windows no-op |
| 006 | Session type + JSONL store | feat(session): jsonl store with per-append flock |
| 007 | Engine integration | feat(engine): seed and persist via session, trim every turn |
| 008 | CLI wiring | feat(cli): session flags and trim strategy wiring |
| 009 | Docs + close TODOS entry | docs: document session management and close cap-history TODO |

## OUT-of-scope items

None. The plan is single-feature, fully specified, no SME/credential
dependencies.

## Execution mode

Inline (orchestrator). Each task has exact code spelled out in the plan;
per-task subagent dispatch would cost more orchestrator tokens than the
work itself.

## Final validation

After all 9 commits: `go test ./... -count=1` must PASS; `git status
--short` must be empty; open PR against `main`.
