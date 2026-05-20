# PROGRESS — Adaptive Semantic Compaction (autodev run)

Updated each time a phase or sub-task changes status. Source of truth for resumability.

## Phases

| Phase | Status | Notes |
| --- | --- | --- |
| 0 — Intake + mode detect | ✅ done | mode = spec; feature branch = `autodev/2026-05-20-adaptive-semantic-compaction` |
| 1 — Design artifacts in run dir | 🟡 in progress | this commit |
| 2.1 — Plan author (Opus subagent → `items/001-plan.md`) | ⬜ pending | |
| 2.2 — Implement (Sonnet subagent driven by plan) | ⬜ pending | |
| 2.3 — Ship PR (against `main`, do NOT merge) | ⬜ pending | |
| 2.4 — QA + code review (parallel Sonnet) | ⬜ pending | |
| 2.5 — Triage + fix loop | ⬜ pending | exit only on QA=PASS ∧ review=PASS/PASS-WITH-NITS |
| 2.6 — Merge | ⏭️ N/A | `main` is protected — PR left open for user |
| 3 — Final validation + handoff | ⬜ pending | |

## Items

| ID | Title | Status | Plan file | Branch | PR |
| --- | --- | --- | --- | --- | --- |
| 001 | Adaptive semantic compaction | in progress | `items/001-plan.md` (pending) | `autodev/2026-05-20-adaptive-semantic-compaction` | — |

## Open obligations

- Plan must call out Layer 4 + Layer 5 e2e test preservation in every implementation task that touches `tests/engine/` or `testdata/compact/`.
- Final validation must run `go test ./... -count=1` AND verify Layer 5 file *compiles* (e.g. `go test -tags=live_provider -run XXX -count=0 ./tests/engine`).
- The PR must be left **unmerged**.
