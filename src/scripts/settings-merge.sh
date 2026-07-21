#!/usr/bin/env bash
#
# Additive hook migration for project-owned .claude/settings.json (BACKLOG
# "agent state at a glance", sol finding: "consumers merge on pin bump" must
# be an implementation, not an operational hope). install.sh seeds the file
# once; when a pin bump introduces NEW harness hook registrations (e.g. the
# agent-state hook's six events), existing consumers pick them up here —
# post-create.sh runs this on every container create, so the rebuild that
# follows a pin bump is the migration.
#
# Merge semantics, deliberately narrow:
#   - hooks only; statusLine/permissions/anything else is never touched
#   - identity is the COMMAND STRING: a command already present anywhere
#     under its event is left alone, wherever the user moved it
#   - missing commands append to the first block with the same matcher
#     (or matcher-less-ness), else as a new block — user blocks are never
#     reordered, edited, or removed
#   - a missing settings file stays missing (seeding is install.sh's job;
#     a project that deleted it said something), invalid JSON warns and
#     bails, and every change lands in the git diff for review like the
#     pin move itself
# Idempotent by construction: a second run is byte-identical.
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh disable=SC1091
source "$script_dir/lib.sh"

settings="$REPO_ROOT/.claude/settings.json"
template="$script_dir/../templates/claude-settings.json"

[ -f "$settings" ] || exit 0
[ -f "$template" ] || exit 0
command -v jq >/dev/null 2>&1 || exit 0

if ! jq -e . "$settings" >/dev/null 2>&1; then
  warn ".claude/settings.json is not valid JSON — hook merge skipped"
  exit 0
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

jq --slurpfile tpl "$template" '
  .hooks //= {}
  | ([$tpl[0].hooks // {} | to_entries[]
      | .key as $e | .value[] | {e: $e, m: (.matcher // null)} as $b
      | .hooks[] | {e: $b.e, m: $b.m, h: .}]) as $adds
  | reduce $adds[] as $a (.;
      if ([.hooks[$a.e][]?.hooks[]?.command] | index($a.h.command)) != null
      then . # already registered somewhere under this event — user placement wins
      else
        .hooks[$a.e] = ((.hooks[$a.e] // []) as $blocks
          | ([$blocks[] | .matcher // null] | index($a.m)) as $i
          | if $i != null
            then $blocks | .[$i].hooks += [$a.h]
            else $blocks + [if $a.m == null
                            then {hooks: [$a.h]}
                            else {matcher: $a.m, hooks: [$a.h]} end]
            end)
      end)
' "$settings" >"$tmp"

if ! cmp -s "$settings" "$tmp"; then
  mv -- "$tmp" "$settings"
  trap - EXIT
  log "Merged new harness hook registrations into .claude/settings.json (additive — review the git diff)"
fi
exit 0
