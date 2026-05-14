---
name: code-review
description: Systematic pre-commit code review checklist for Go codebases. Checks for correctness, security, test coverage, and style.
aliases:
  - review
  - cr
---

# Code Review Skill

You are performing a systematic code review. Work through each category below in order, then produce a structured report.

## 1. Correctness

- [ ] Does the logic match the intent described in the PR/commit message?
- [ ] Are error return values checked at every call site?
- [ ] Are goroutines or goroutine leaks introduced?
- [ ] Are nil pointer dereferences possible?

## 2. Security

- [ ] Is user-supplied input validated before use (no path traversal, injection)?
- [ ] Are file operations using safe path helpers (`safepath`)?
- [ ] Are secrets or credentials present in the diff?
- [ ] Does any new dependency introduce a known CVE?

## 3. Test Coverage

- [ ] Is there a corresponding test file for every changed source file?
- [ ] Are error branches tested, not just the happy path?
- [ ] Are new public functions covered by at least one unit test?

## 4. Style

- [ ] Function bodies stay under ~50 lines.
- [ ] No mutable package-level state introduced.
- [ ] Comments on exported symbols are present and accurate.

## Output Format

After the checklist, write:

```
## Review Summary
PASS / NEEDS CHANGES / BLOCK

### Issues
- [SEVERITY] file:line — description

### Notes
- Optional observations
```

Severity levels: BLOCK (ship-stopper), WARN (should fix), NOTE (optional).
