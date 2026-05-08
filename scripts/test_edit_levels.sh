#!/usr/bin/env bash
# test_edit_levels.sh — manual smoke-test for every fuzzy match level and error case.
#
# Usage:
#   ./scripts/test_edit_levels.sh              # run all sections
#   ./scripts/test_edit_levels.sh unit          # unit tests only (fast)
#   ./scripts/test_edit_levels.sh manual        # manual file-level probes only
#
# Requires: go, mktemp (standard on macOS/Linux)
set -euo pipefail

MODE="${1:-all}"
PASS=0; FAIL=0

# ── helpers ──────────────────────────────────────────────────────────────────

green() { printf '\033[32m✓ %s\033[0m\n' "$*"; }
red()   { printf '\033[31m✗ %s\033[0m\n' "$*"; }

check() {
  local label="$1"; local result="$2"; local expected="$3"
  if [[ "$result" == *"$expected"* ]]; then
    green "$label"
    PASS=$(( PASS + 1 ))
  else
    red "$label"
    printf '    expected to contain: %s\n' "$expected"
    printf '    got:                 %s\n' "$result"
    FAIL=$(( FAIL + 1 ))
  fi
}

# ── unit tests (go test -run) ─────────────────────────────────────────────────

run_unit_tests() {
  echo ""
  echo "══════════════════════════════════════════════"
  echo "  UNIT TESTS  (go test ./tests/tools/...)"
  echo "══════════════════════════════════════════════"

  # Helper: run a single -run filter and label it.
  # We use exit code (0=pass, non-zero=fail) rather than grep to avoid
  # pipefail/subshell issues with `set -e`.
  run_case() {
    local label="$1"; local filter="$2"
    local output
    if output=$(go test -race -run "$filter" ./tests/tools/... 2>&1); then
      green "unit: $label"
      PASS=$(( PASS + 1 ))
    else
      red "unit: $label"
      go test -race -run "$filter" ./tests/tools/... -v 2>&1 | grep -E "FAIL|Error|---" || true
      FAIL=$(( FAIL + 1 ))
    fi
  }

  # L1 — exact string match
  run_case "L1 exact unique match"      "TestFuzzyReplaceL1ExactUniqueMatch"
  run_case "L1 multi-match → error"     "TestFuzzyReplaceL1MultiMatchErrors"
  run_case "L1 replaceAll replaces all" "TestFuzzyReplaceL1ReplaceAllReplacesAll"

  # L2 — CRLF normalisation
  run_case "L2 CRLF→LF normalization"  "TestFuzzyReplaceL2CRLFNormalization"
  run_case "L2 ambiguous → error"       "TestFuzzyReplaceL2AmbiguousErrors"
  run_case "L2 replaceAll"              "TestFuzzyReplaceL2ReplaceAll"

  # L3 — TrimSpace on old_text
  run_case "L3 trims model-wrapped old_text"       "TestFuzzyReplaceL3TrimsOldText"
  run_case "L3 ambiguous → error"                   "TestFuzzyReplaceL3AmbiguityErrors"
  run_case "L3 replaceAll"                          "TestFuzzyReplaceL3ReplaceAll"
  run_case "L3 skips all-whitespace old_text"       "TestFuzzyReplaceL3SkipsAllWhitespaceOldText"

  # L4 — line-by-line TrimSpace sliding window with reindent
  run_case "L4 indentation hallucination"               "TestFuzzyReplaceL4IndentationHallucination"
  run_case "L4 ambiguous → error"                       "TestFuzzyReplaceL4AmbiguityErrors"
  run_case "L4 replaceAll preserves per-window indent"  "TestFuzzyReplaceL4ReplaceAllPreservesPerWindowIndent"
  run_case "L4 preserves relative indent in new_text"   "TestFuzzyReplaceL4PreservesRelativeIndentInNewText"
  run_case "L4 old_text longer than content → miss"     "TestFuzzyReplaceL4OldLongerThanContent"
  run_case "L4 first window line all-whitespace"        "TestFuzzyReplaceL4FirstWindowLineAllWhitespace"

  # Total miss
  run_case "Total miss → ErrNotFound, level=-1"  "TestFuzzyReplaceMissReturnsError|TestFuzzyReplaceAllLevelsMiss"

  # edit_file integration
  run_case "edit_file single exact (L1)"             "TestEditFileSingleExactMatch"
  run_case "edit_file multi-edit sequential"         "TestEditFileMultiEditSequential"
  run_case "edit_file multi-edit atomic rollback"    "TestEditFileMultiEditRollback"
  run_case "edit_file missing file → use write_file" "TestEditFileFileMissingNamesWriteFile"
  run_case "edit_file path traversal blocked"        "TestEditFileRejectsPathTraversal"
  run_case "edit_file no-op old==new rejected"       "TestEditFileRejectsNoOpEdit"
  run_case "edit_file empty edits array"             "TestEditFileEmptyEditsArrayErrors"
  run_case "edit_file malformed JSON"                "TestEditFileMalformedArgsErrors"
  run_case "edit_file indentation hallucination (L4) integration" \
           "TestEditFileIndentationHallucinationIntegration"
  run_case "edit_file ambiguous match error"         "TestEditFileAmbiguousMatchError"
  run_case "edit_file stat permission error"         "TestEditFileStatPermissionError"
}

# ── verbose level probes ─────────────────────────────────────────────────────
# Run selected tests with -v so you can read what each level/error case
# actually checks at a glance. Pass/fail is determined by go test exit code.

run_manual_probes() {
  echo ""
  echo "══════════════════════════════════════════════"
  echo "  VERBOSE LEVEL PROBES  (go test -v -run)"
  echo "══════════════════════════════════════════════"

  # vcheck: run a test verbosely, pass/fail by exit code
  vcheck() {
    local label="$1"; local filter="$2"
    local output
    if output=$(go test -race -v -run "$filter" ./tests/tools/... 2>&1); then
      green "$label"
      PASS=$(( PASS + 1 ))
    else
      red "$label"
      echo "$output" | grep -E "FAIL|Error|panic" | head -10 || true
      FAIL=$(( FAIL + 1 ))
    fi
  }

  echo ""
  echo "─── L1: exact string match ───"
  vcheck "L1 unique match"              "TestFuzzyReplaceL1ExactUniqueMatch"
  vcheck "L1 multi-match → error"       "TestFuzzyReplaceL1MultiMatchErrors"
  vcheck "L1 replaceAll"                "TestFuzzyReplaceL1ReplaceAllReplacesAll"
  vcheck "L1 edit_file reports L1=1"    "TestEditFileSingleExactMatch"

  echo ""
  echo "─── L2: CRLF→LF normalisation ───"
  vcheck "L2 CRLF file + LF old_text"  "TestFuzzyReplaceL2CRLFNormalization"
  vcheck "L2 ambiguous → error"         "TestFuzzyReplaceL2AmbiguousErrors"
  vcheck "L2 replaceAll"                "TestFuzzyReplaceL2ReplaceAll"

  echo ""
  echo "─── L3: TrimSpace on old_text ───"
  vcheck "L3 model-wrapped snippet"         "TestFuzzyReplaceL3TrimsOldText"
  vcheck "L3 ambiguous → error"             "TestFuzzyReplaceL3AmbiguityErrors"
  vcheck "L3 replaceAll"                    "TestFuzzyReplaceL3ReplaceAll"
  vcheck "L3 guard: all-whitespace → miss"  "TestFuzzyReplaceL3SkipsAllWhitespaceOldText"

  echo ""
  echo "─── L4: line-by-line TrimSpace + base-indent reindent ───"
  vcheck "L4 indentation hallucination"           "TestFuzzyReplaceL4IndentationHallucination"
  vcheck "L4 edit_file reports L4=1"              "TestEditFileIndentationHallucinationIntegration"
  vcheck "L4 ambiguous → error"                   "TestFuzzyReplaceL4AmbiguityErrors"
  vcheck "L4 replaceAll per-window indent"        "TestFuzzyReplaceL4ReplaceAllPreservesPerWindowIndent"
  vcheck "L4 relative indent in new_text"         "TestFuzzyReplaceL4PreservesRelativeIndentInNewText"
  vcheck "L4 old_text longer than content → miss" "TestFuzzyReplaceL4OldLongerThanContent"

  echo ""
  echo "─── Error cases ───"
  vcheck "Total miss → ErrNotFound"             "TestFuzzyReplaceMissReturnsError"
  vcheck "L1 ambiguous → matched N places"      "TestFuzzyReplaceL1MultiMatchErrors"
  vcheck "L4 ambiguous → matched N places"      "TestFuzzyReplaceL4AmbiguityErrors"
  vcheck "edit_file missing file → write_file"  "TestEditFileFileMissingNamesWriteFile"
  vcheck "edit_file path traversal blocked"     "TestEditFileRejectsPathTraversal"
  vcheck "edit_file no-op old==new rejected"    "TestEditFileRejectsNoOpEdit"
  vcheck "edit_file empty edits array"          "TestEditFileEmptyEditsArrayErrors"
  vcheck "edit_file malformed JSON"             "TestEditFileMalformedArgsErrors"
  vcheck "edit_file ambiguous → disambiguate"   "TestEditFileAmbiguousMatchError"
  vcheck "edit_file stat permission error"      "TestEditFileStatPermissionError"
  vcheck "edit_file multi-edit atomic rollback" "TestEditFileMultiEditRollback"
}

# ── entry point ──────────────────────────────────────────────────────────────

cd "$(dirname "$0")/.."

case "$MODE" in
  unit)   run_unit_tests ;;
  manual) run_manual_probes ;;
  all)    run_unit_tests; run_manual_probes ;;
  *)
    echo "Usage: $0 [all|unit|manual]" >&2
    exit 2
    ;;
esac

echo ""
echo "══════════════════════════════════════════════"
printf '  TOTAL: %d passed, %d failed\n' "$PASS" "$FAIL"
echo "══════════════════════════════════════════════"

[[ "$FAIL" -eq 0 ]]
