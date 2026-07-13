#!/bin/bash
# Claude Code subagentStatusLine — one custom row per live subagent in the agent panel:
#   <label> · <model>(<effort>) · <tokens>
# Contract (wired via subagentStatusLine in the seeded .claude/settings.json): row
# context arrives as JSON on stdin (.tasks[] = {id,name,type,status,label,model,
# tokenCount,...}); emit one {"id":...,"content":...} JSON object per line. Runs
# under a 5s harness timeout.
#
# The row payload carries the subagent's resolved MODEL but not its effort — effort
# is pinned (if at all) in the agent definition's frontmatter, so for repo-defined
# agents we read `effort:` from .claude/agents/<type>.md; built-ins / unpinned
# agents inherit the session effort and show the model only.

input=$(cat)
repo="${CLAUDE_PROJECT_DIR:-$PWD}"

MAGENTA=$'\033[0;35m'
GRAY=$'\033[2;37m'
RESET=$'\033[0m'

echo "$input" | jq -c '.tasks[]?' | while IFS= read -r task; do
  label=$(jq -r '.label // .description // .name // .type // "agent"' <<<"$task")
  type=$(jq -r '.type // empty' <<<"$task")
  model=$(jq -r '.model // empty' <<<"$task")
  tok=$(jq -r '.tokenCount // 0' <<<"$task")

  # claude-fable-5 → fable-5 (keep the row tight; the family is what matters visually)
  short="${model#claude-}"

  effort=""
  if [[ "$type" =~ ^[A-Za-z0-9_-]+$ ]] && [ -f "$repo/.claude/agents/$type.md" ]; then
    effort=$(awk '/^---$/{n++; next} n==1 && /^effort:/ {print $2; exit}' "$repo/.claude/agents/$type.md")
  fi

  seg="${MAGENTA}${short:-model?}${RESET}"
  [ -n "$effort" ] && seg="${seg}${GRAY}(${effort})${RESET}"

  toks=$(awk -v n="$tok" 'BEGIN { if (n >= 1000) printf "%.0fk", n / 1000; else printf "%d", n }')
  label=$(awk -v s="$label" 'BEGIN { if (length(s) > 44) s = substr(s, 1, 41) "..."; print s }')

  content="${label} ${GRAY}·${RESET} ${seg} ${GRAY}· ${toks} tok${RESET}"
  jq -cn --argjson t "$task" --arg content "$content" '{id: $t.id, content: $content}'
done
