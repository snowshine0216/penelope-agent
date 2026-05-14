---
name: git-commit
description: Writes a conventional commit message from the current diff. Follows Conventional Commits 1.0 with a scope and optional breaking-change footer.
aliases:
  - commit
  - commit-msg
---

# Git Commit Skill

Generate a conventional commit message from `git diff --staged` (or the diff you provide).

## Steps

1. **Read the diff** — call `bash` with `git diff --staged` if not already provided.
2. **Classify the change type**:
   - `feat` — new user-visible behaviour
   - `fix` — bug correction
   - `refactor` — internal restructure, no behaviour change
   - `test` — adding or correcting tests only
   - `docs` — documentation only
   - `chore` — build scripts, CI, tooling
   - `perf` — performance improvement
3. **Pick a scope** — the package or subsystem most affected (e.g. `engine`, `context`, `tools`).
4. **Write the subject line** — imperative mood, ≤72 chars, no trailing period.
5. **Write the body** (optional) — explain *why*, not *what*. Wrap at 72 chars.
6. **Add breaking-change footer** if any public API changes:
   ```
   BREAKING CHANGE: <description>
   ```

## Output Format

Emit only the raw commit message, ready to paste into `git commit -m`:

```
<type>(<scope>): <subject>

<body — optional>

<footer — optional>
```

## Example

```
fix(engine): serialise load_skill before parallel tool groups

Previously, a load_skill call inside a mixed tool batch could race with
parallel tool calls that expected the skill body to already be in the
system prompt. The engine now detects hasLoadSkillCall and hoists it
into its own serial group before planning the remainder.
```
