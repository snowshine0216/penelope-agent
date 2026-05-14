# Dynamic Context And Skill Lazy Loading — Design

**Date:** 2026-05-14
**Status:** Draft (awaiting written spec review)

## Context

`penelope-agent` currently seeds each run in `internal/engine/loop.go` with
one hardcoded system message and the user prompt. That keeps the loop small,
but it means the agent cannot honor project-local instructions from
`AGENTS.md` and cannot discover local skills without eagerly loading all skill
instructions into context.

The goal is to add a focused `internal/context` package that composes the
system prompt dynamically. The initial context should include root workdir
instructions and a compact catalog of local skills. Full skill bodies should
load only after the model explicitly chooses a relevant skill through an
internal `load_skill` tool.

## Scope

**In:**
- Load root-only `${workdir}/AGENTS.md` into the system prompt when present.
- Discover local skills under `${workdir}/.claw/skills/*/SKILL.md`.
- Parse and inject only skill frontmatter into the initial skill catalog.
- Let the model choose a skill by calling `load_skill`.
- Load full skill instructions only for selected local skills.
- Keep loaded skill instructions at system-prompt priority on later model calls.
- Preserve a single system message for provider compatibility.
- Unit and filesystem-edge tests for composition, parsing, loading, and engine
  integration.

**Out:**
- Global, bundled, or user-home skills.
- Recursive skill discovery.
- Nested or hierarchical `AGENTS.md`.
- A separate pre-router LLM call before the main agent loop.
- Token budgeting or summarization for very large catalogs.
- Automatically loading `scripts/`, `references/`, or `assets/` content.
- Symlink traversal outside the workdir.

## Decisions

| ID | Decision | Choice | Rationale |
|----|----------|--------|-----------|
| D1 | AGENTS lookup | Root-only `${workdir}/AGENTS.md` | Deterministic, matches the requested v1 behavior, and avoids file-scoped instruction precedence. |
| D2 | Skill discovery | Direct children of `.claw/skills` only | Keeps discovery predictable and avoids recursive context surprises. |
| D3 | Initial skill context | Frontmatter catalog only | Gives the model routing metadata without paying for every skill body. |
| D4 | Skill routing | Main model calls `load_skill` | The model decides relevance using all catalog metadata and the current task. |
| D5 | Loaded skill priority | Recompose the single system prompt | Skill instructions must behave like instructions, not ordinary tool observations. |
| D6 | Loader scope | Local `.claw/skills` only in v1 | Keeps the feature small and leaves global skill support for a later design. |
| D7 | Re-load behavior | Idempotent | Repeated `load_skill` calls return "already loaded" and do not duplicate prompt content. |
| D8 | Tool policy | `load_skill` is serial-only | Loading instructions changes later model behavior and should not race with other work. |
| D9 | Loader barrier | Defer non-loader calls in the same assistant message | Prevents executing work that was chosen before the newly loaded instructions were available. |

## Architecture

Add `internal/context` as the owner of prompt composition and local skill
metadata. File I/O stays at package boundaries; parsing and composition stay
pure and deterministic.

Core units:

- `Composer`: builds the single system prompt from ordered sections.
- `AGENTSLoader`: reads only `${workdir}/AGENTS.md`.
- `SkillCatalogLoader`: scans `${workdir}/.claw/skills/*/SKILL.md` and parses
  only YAML frontmatter.
- `SkillBodyLoader`: loads the markdown instruction body for one selected
  local skill.
- `SkillState`: tracks the available skill catalog and the set of already
  loaded skill bodies.
- `load_skill` internal tool: lets the model select a local skill by exact name.

The engine should no longer hardcode the system prompt. It should ask the
composer for the current system prompt when seeding history and after any
successful skill load. Provider adapters can remain unchanged if the engine
maintains exactly one `schema.RoleSystem` message at index 0.

## Data Flow

Initial setup:

```text
CLI resolves workdir
  -> context builder reads root AGENTS.md if present
  -> context builder scans .claw/skills/*/SKILL.md frontmatter only
  -> composer builds one system prompt
  -> engine seeds [system, user]
```

The initial system prompt is composed in this order:

1. Base penelope-agent identity and operating rules.
2. Root workdir `AGENTS.md` instructions, if present.
3. Local skill catalog from frontmatter only.
4. Skill loading instructions that explain when to call `load_skill`.
5. Loaded skill bodies, initially empty.

During the loop:

```text
model sees normal tools plus load_skill
  -> model may answer directly
  -> model may call normal tools
  -> model may call load_skill({"name":"..."})
  -> engine executes the serial load_skill call
  -> engine records the loaded skill body in SkillState
  -> composer rebuilds the system prompt
  -> next provider call sees the refreshed system message
```

If a model turn asks for `load_skill` alongside other tools, `load_skill` acts
as a turn barrier. The engine executes only the `load_skill` calls, appends a
short deferral observation for each non-loader call in that assistant message,
rebuilds the system prompt, and lets the model request the normal tools again
after seeing the loaded instructions. This preserves provider protocol
requirements while avoiding work chosen under stale instructions.

## Skill Format

Local skills use this v1 structure:

```text
.claw/skills/my-skill/
  SKILL.md
  scripts/
  references/
  assets/
```

Only `SKILL.md` is read automatically. `scripts/`, `references/`, and `assets/`
remain available through normal file tools after the skill instructions explain
how to use them.

`SKILL.md` must start with YAML frontmatter:

```yaml
---
name: my-skill
description: One sentence explaining when to use it.
---
```

Optional fields such as `aliases` can be parsed when present, but v1 behavior
only requires `name` and `description`. A skill with missing or invalid
frontmatter is skipped from the catalog and cannot be loaded by name.

The initial catalog format should stay compact:

```text
## Local Skills

These skills are available under .claw/skills. Initially only metadata is
loaded. Call load_skill with the exact skill name before following a skill's
instructions.

- name: investigate
  description: Systematic debugging with root cause investigation.
- name: release-notes
  description: Draft release notes from git history.
```

When a skill is loaded, inject the markdown body after frontmatter:

```text
## Loaded Skill: investigate

Source: .claw/skills/investigate/SKILL.md

<markdown body after YAML frontmatter>
```

The frontmatter itself should not be duplicated in the loaded body section.

## Engine Integration

Register `load_skill` only when the local skill catalog is non-empty. The tool
implements the existing tool interface, but its successful execution also
produces an engine-side instruction update.

The engine needs a narrow integration point around tool execution:

1. Inspect the assistant message for any `load_skill` calls.
2. If present, execute only the `load_skill` calls in request order.
3. For any non-loader calls in the same assistant message, append a deferral
   tool result telling the model to request the call again after skill loading.
4. If no `load_skill` calls are present, execute normal tool groups as today.
5. Append tool observations to history in the original request order.
6. Detect successful `load_skill` results.
7. Update `SkillState` with newly loaded bodies.
8. Replace the existing system message content with `composer.SystemPrompt()`
   before the next provider call.

The loader result should still be visible to the model as a short tool
observation such as `loaded skill "investigate"`. The full body belongs in the
system prompt, not in the tool result.

Unknown skill names should return an error observation with the valid local
skill names. Already loaded skills should return a non-error observation and
leave the prompt unchanged.

## Error Handling

- Missing `AGENTS.md`: omit the AGENTS section.
- Empty `.claw/skills`: omit the catalog and do not register `load_skill`.
- Invalid skill frontmatter: skip the skill from the catalog.
- Duplicate skill names: keep the first deterministic path in lexical order and
  skip later duplicates.
- Missing body for cataloged skill: return a `load_skill` error observation.
- Path escape or symlink escape: reject the path and keep the skill unloaded.

All discovery order should be lexical so tests and prompt output are stable.

## Testing

Follow TDD. Tests should be fast and deterministic.

Pure unit tests:

- Parse valid frontmatter.
- Reject missing frontmatter.
- Reject missing `name` or `description`.
- Compose prompts with base instructions only.
- Compose prompts with AGENTS, catalog, and loaded skill bodies in order.
- Ensure loaded skill sections are not duplicated.

Filesystem-edge tests:

- Load root-only `AGENTS.md`.
- Do not load parent or nested `AGENTS.md`.
- Discover direct child skill folders only.
- Skip invalid skill files.
- Load body after frontmatter.
- Reject symlink/path escape attempts.

Tool and registry tests:

- `load_skill` loads a valid local skill.
- Unknown skill returns an error with available names.
- Already loaded skill is idempotent.
- `load_skill` is serial-only and not parallel-safe.
- Mixed `load_skill` plus normal tool calls defer the normal calls.

Engine tests:

- Initial provider call includes AGENTS content and skill catalog metadata.
- Initial provider call does not include full skill bodies.
- After `load_skill`, the next provider call includes the loaded skill body in
  the system message.
- A turn that mixes `load_skill` and normal tools does not execute the normal
  tools before the next provider call.
- Repeated `load_skill` calls do not duplicate loaded body sections.
- Existing normal tool execution behavior remains unchanged.

## Risks

- The model may overuse `load_skill` because the catalog is visible. Keep the
  prompt instruction clear: load a skill only when it materially changes how the
  task should be performed.
- Large skill catalogs can still consume context through metadata. v1 accepts
  this and defers token budgeting.
- A malformed but skipped local skill may surprise users. The loader should make
  skipped skills test-visible and can later expose diagnostics through logging.
