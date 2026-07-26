#!/usr/bin/env bash
# Claude Code subagentStatusLine — one custom row per live subagent in the agent panel:
#   <label> · <model>(<effort>) · <tokens>
# Contract (wired via subagentStatusLine in the payload claude-settings.json): row
# context arrives as JSON on stdin (.tasks[] = {id,name,type,status,label,model,
# tokenCount,...}); emit one {"id":...,"content":...} JSON object per line. Runs
# under a 5s harness timeout, so the whole render is two jq processes total
# (an effort-map pass plus one formatting pass), not per-task pipelines.
#
# The row payload carries the subagent's resolved MODEL but not its effort — effort
# is pinned (if at all) in the agent definition's frontmatter, so for repo-defined
# agents we read `effort:` from .claude/agents/<type>.md; built-ins / unpinned
# agents inherit the session effort and show the model only.
#
# tasks[].model needs Claude Code >= 2.1.205 (absent on older CLIs, and the payload
# has no session-model field to fall back to) — so a missing model drops the
# segment entirely rather than rendering a placeholder.

input=$(cat)
repo="${CLAUDE_PROJECT_DIR:-$PWD}"

MAGENTA=$'\033[0;35m'
GRAY=$'\033[2;37m'
RESET=$'\033[0m'

# Resolve each distinct agent type's effort pin once, into a {type: effort} JSON
# map for the formatting pass. Built by printf, which is safe only because both
# key and value are charset-gated to [A-Za-z0-9_-] before they reach the string.
pairs=""
sep=""
while IFS= read -r type; do
  [[ "$type" =~ ^[A-Za-z0-9_-]+$ ]] || continue
  [ -f "$repo/.claude/agents/$type.md" ] || continue
  # First `effort:` line inside the frontmatter fences, first token of
  # its value — pure bash (this renders per tick, per agent type).
  effort=""
  fences=0
  while IFS= read -r line; do
    case "$line" in
    ---) fences=$((fences + 1)); [ "$fences" -ge 2 ] && break ;;
    effort:*)
      if [ "$fences" -eq 1 ]; then
        read -r effort _ <<<"${line#effort:}"
        break
      fi
      ;;
    esac
  done <"$repo/.claude/agents/$type.md"
  [[ "$effort" =~ ^[A-Za-z0-9_-]+$ ]] || continue
  pairs="${pairs}${sep}\"${type}\":\"${effort}\""
  sep=","
done < <(jq -r '[.tasks[]? | .type // empty] | unique | .[]' <<<"$input")

jq -c --argjson efforts "{${pairs}}" \
  --arg magenta "$MAGENTA" --arg gray "$GRAY" --arg reset "$RESET" '
  # printf-%.0f rounding (half to even), so token counts render byte-identically
  # to the awk formatter this replaced.
  def r0: floor as $f | (. - $f) as $d |
    if $d > 0.5 then $f + 1
    elif $d < 0.5 then $f
    elif ($f % 2) == 0 then $f
    else $f + 1 end;
  def toks: if . >= 1000 then (. / 1000 | r0 | tostring) + "k" else tostring end;
  # NB: "label" is a jq keyword, so no $label variable (jq 1.6 rejects it).
  .tasks[]?
  | ((.label // .description // .name // .type // "agent")
     | if length > 44 then .[0:41] + "..." else . end) as $lbl
  # claude-fable-5 → fable-5 (keep the row tight; the family is what matters visually)
  | ((.model // "") | ltrimstr("claude-")) as $short
  | ($efforts[.type // ""] // "") as $effort
  | (if $short != ""
     then $magenta + $short + $reset
          + (if $effort != "" then $gray + "(" + $effort + ")" + $reset else "" end)
     else "" end) as $seg
  | ($lbl
     + (if $seg != "" then " " + $gray + "·" + $reset + " " + $seg else "" end)
     + " " + $gray + "· " + ((.tokenCount // 0) | toks) + " tok" + $reset) as $content
  | {id: .id, content: $content}
' <<<"$input"
