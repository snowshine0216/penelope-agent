
## Skill routing

When the user's request matches an available skill, invoke it via the Skill tool. When in doubt, invoke the skill.

Key routing rules:
- Product ideas/brainstorming → invoke /office-hours
- Strategy/scope → invoke /plan-ceo-review
- Architecture → invoke /plan-eng-review
- Design system/plan review → invoke /design-consultation or /plan-design-review
- Full review pipeline → invoke /autoplan
- Bugs/errors → invoke /investigate
- QA/testing site behavior → invoke /qa or /qa-only
- Code review/diff check → invoke /review
- Visual polish → invoke /design-review
- Ship/deploy/PR → invoke /ship or /land-and-deploy
- Save progress → invoke /context-save
- Resume context → invoke /context-restore

## Running the live-provider smoke test

`tests/engine/compact_live_test.go` is gated by the `live_provider` build
tag and is skipped by default. It exercises the full adaptive-compaction
pipeline against a real Claude endpoint and is the canonical "did the
OOM fix really land" gate.

```bash
ANTHROPIC_API_KEY=sk-ant-... \
  go test -tags=live_provider ./tests/engine -run TestCompact_LiveClaude -count=1 -v
```

`ANTHROPIC_API_KEY` is required. Without a key the test skips.

Even without an API key, run `go vet -tags=live_provider ./tests/engine`
before merging changes that touch `internal/compact/`, `internal/engine/`,
or `internal/session/`. The build-tagged file must still compile.
