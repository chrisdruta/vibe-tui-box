#!/usr/bin/env bash
#
# vibe-verdict VERDICT PATH [NOTE...] — append a review verdict as JSONL.
#
# Called from project-owned yazi keybindings (templates/yazi/keymap.toml
# seeds a=approve / r=reject). Baked as /usr/local/bin/vibe-verdict: yazi
# shell commands run in whatever directory is being browsed, so the harness
# checkout path can't be assumed — and that cwd is also why the default
# target lands the verdicts BESIDE the images under review.
# Target file: $VIBE_REVIEW_DECISIONS overrides, else ./.review-decisions.jsonl
set -euo pipefail

usage="usage: vibe-verdict VERDICT PATH [NOTE...]"
verdict="${1:?$usage}"
path="${2:?$usage}"
shift 2
note="${*:-}"

out="${VIBE_REVIEW_DECISIONS:-./.review-decisions.jsonl}"
jq -cn --arg verdict "$verdict" --arg path "$path" --arg note "$note" \
  '{ts: (now | todate), path: $path, verdict: $verdict}
     + (if $note == "" then {} else {note: $note} end)' >>"$out"
