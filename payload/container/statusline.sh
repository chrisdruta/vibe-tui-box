#!/usr/bin/env bash
# Claude Code statusLine — matches the container shell prompt style:
#   <user> <arrow> <cwd (last 4 dirs)> (<git-branch>[ dirty-mark]) · <model> (<effort>) · <context%>
# Wired via statusLine in the payload claude-settings.json (the
# read-only payload mount agent-session.sh passes with --settings).
# Reads the status JSON on stdin; requires jq (installed by the tools
# recipe beside tmux — the carrier only runs where both exist).

input=$(cat)

# One jq pass for every payload field (line protocol, empty line = absent) —
# this renders on every statusline tick, so process count is the budget.
{
  IFS= read -r cwd
  IFS= read -r model
  IFS= read -r effort
  IFS= read -r used_pct
  IFS= read -r total_in
  IFS= read -r ctx_size
} < <(jq -r '
  .workspace.current_dir // "",
  .model.display_name // "",
  .effort.level // "",
  (.context_window.used_percentage // ""),
  (.context_window.total_input_tokens // ""),
  (.context_window.context_window_size // "")
' <<<"$input" 2>/dev/null)
[ -z "$cwd" ] && cwd="$PWD"

GREEN=$'\033[0;32m'
BLUE=$'\033[1;34m'
CYAN=$'\033[0;36m'
RED=$'\033[1;31m'
YELLOW=$'\033[1;33m'
MAGENTA=$'\033[0;35m'
GRAY=$'\033[2;37m'
RESET=$'\033[0m'

user="${GITHUB_USER:-$(whoami)}"

# Trim like PROMPT_DIRTRIM=4: show the full path if it's short, otherwise the
# last 4 path components.
dir=$(awk -F'/' -v full="$cwd" 'BEGIN {
  n = split(full, parts, "/")
  if (n <= 5) { print full; exit }
  out = "..."
  for (i = n - 3; i <= n; i++) out = out "/" parts[i]
  print out
}')

# HEAD resolution doubles as the are-we-in-a-repo probe; the branch guard
# keeps `diff --quiet` (and its dirty mark) inside real repos only.
branch=$(git -C "$cwd" --no-optional-locks symbolic-ref --short HEAD 2>/dev/null ||
  git -C "$cwd" --no-optional-locks rev-parse --short HEAD 2>/dev/null)
dirty=""
if [ -n "$branch" ] && ! git -C "$cwd" --no-optional-locks diff --quiet 2>/dev/null; then
  dirty=" ${YELLOW}✗"
fi

out="${GREEN}${user}${RESET} ${RESET}➜${RESET} ${BLUE}${dir}${RESET}"
if [ -n "$branch" ]; then
  out="${out} ${CYAN}(${RED}${branch}${dirty}${CYAN})${RESET}"
fi

# Model name (dim magenta) + effort level (dim gray). `.effort.level` is the LIVE
# per-turn value — present only on effort-capable models, so it degrades cleanly.
if [ -n "$model" ]; then
  out="${out} ${GRAY}·${RESET} ${MAGENTA}${model}${RESET}"
  [ -n "$effort" ] && out="${out} ${GRAY}(${effort})${RESET}"
fi

# Context usage — prefer the pre-calculated used_percentage; color-coded
# green/yellow/red by pressure. Fall back to a compact "85k/200k" token
# ratio if the percentage isn't available. Skipped entirely if neither is.
fmt_tokens() {
  awk -v n="$1" 'BEGIN {
    if (n >= 1000) { printf "%.0fk", n / 1000 } else { printf "%d", n }
  }'
}

if [ -n "$used_pct" ]; then
  pct=$(awk -v p="$used_pct" 'BEGIN { printf "%.0f", p }')
  if [ "$pct" -ge 80 ]; then
    ctx_color="$RED"
  elif [ "$pct" -ge 50 ]; then
    ctx_color="$YELLOW"
  else
    ctx_color="$GREEN"
  fi
  out="${out} ${GRAY}·${RESET} ${ctx_color}${pct}%${RESET}"
elif [ -n "$total_in" ] && [ -n "$ctx_size" ]; then
  out="${out} ${GRAY}·${RESET} ${GRAY}$(fmt_tokens "$total_in")/$(fmt_tokens "$ctx_size")${RESET}"
fi

printf "%s" "$out"
