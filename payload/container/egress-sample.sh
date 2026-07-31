#!/usr/bin/env bash
#
# Container-side feeder for the egress view (ROADMAP R6): a one-shot
# snapshot of live sockets attributed to processes, engine-invoked via
# `vibe _egress`. Pure /proc on purpose — `ss` is not guaranteed by
# arbitrary base images, unprivileged `ss -p` has exactly the same
# uid-limited attribution as the readlink pass below, and a second
# output grammar would double the engine's parse surface; do not
# "improve" this back to ss. Packet capture is off the table by design
# (cap_drop ALL removes NET_RAW).
#
# Output contract (parsed by internal/app parseEgressSample):
#
#   egress-sample\t1                          version line
#   <proto>\t<local>\t<remote>\t<pid>\t<comm> one row per socket
#
# proto is tcp/tcp6/udp/udp6 (tcp filtered to ESTABLISHED, udp to
# connected sockets); pid/comm are `-` when the socket belongs to
# another uid (their /proc/<pid>/fd is unreadable — accepted blind
# spot, rendered as unattributed, never guessed). comm is free bytes
# (PR_SET_NAME), so it is allowlist-stripped here AND re-sanitized
# engine-side — same defense in depth as agent-ps.sh. Rows are capped
# and emitted in fixed table order so output is deterministic for a
# given /proc state. Addresses are decoded assuming little-endian
# words, which holds on every release target (amd64, arm64).
#
# VIBE_EGRESS_PROC exists solely so the payload test can drive the
# parser against a fixture tree; the script runs as the container user
# over container-controlled data either way, so it is not a trust knob.
set -euo pipefail

proc="${VIBE_EGRESS_PROC:-/proc}"
max_rows=512

printf 'egress-sample\t1\n'

# Pass 1: socket inode -> owning pid/comm, first claimant wins. Other
# users' fd dirs make readlink fail -> those sockets stay unattributed.
declare -A inode2pid inode2comm
for fd in "$proc"/[0-9]*/fd/*; do
  link=$(readlink "$fd" 2>/dev/null) || continue
  case "$link" in 'socket:['*']') ;; *) continue ;; esac
  inode=${link#'socket:['}
  inode=${inode%']'}
  pid=${fd#"$proc"/}
  pid=${pid%%/*}
  if [ -z "${inode2pid[$inode]:-}" ]; then
    comm=$(head -c 32 "$proc/$pid/comm" 2>/dev/null || true)
    inode2pid[$inode]=$pid
    inode2comm[$inode]=${comm//[^a-zA-Z0-9:._-]/}
  fi
done

# AABBCCDD:PPPP -> a.b.c.d (address little-endian per word, port big).
ip4() {
  local h=$1
  printf '%d.%d.%d.%d' $((16#${h:6:2})) $((16#${h:4:2})) $((16#${h:2:2})) $((16#${h:0:2}))
}

# 32 hex chars as four little-endian 32-bit words -> bracketable v6
# text; v4-mapped addresses render as plain IPv4.
ip6() {
  local h=$1 net="" w
  for w in "${h:0:8}" "${h:8:8}" "${h:16:8}" "${h:24:8}"; do
    net+="${w:6:2}${w:4:2}${w:2:2}${w:0:2}"
  done
  net=${net,,}
  if [ "${net:0:24}" = "00000000000000000000ffff" ]; then
    printf '%d.%d.%d.%d' $((16#${net:24:2})) $((16#${net:26:2})) $((16#${net:28:2})) $((16#${net:30:2}))
    return
  fi
  printf '%s:%s:%s:%s:%s:%s:%s:%s' \
    "${net:0:4}" "${net:4:4}" "${net:8:4}" "${net:12:4}" \
    "${net:16:4}" "${net:20:4}" "${net:24:4}" "${net:28:4}"
}

addr() { # <proto> <HEX:PORT>
  local hex=${2%%:*} port=$((16#${2##*:}))
  case "$1" in
    tcp | udp) printf '%s:%d' "$(ip4 "$hex")" "$port" ;;
    *)
      local a
      a=$(ip6 "$hex")
      case "$a" in *:*) printf '[%s]:%d' "$a" "$port" ;; *) printf '%s:%d' "$a" "$port" ;; esac
      ;;
  esac
}

rows=0
for t in tcp tcp6 udp udp6; do
  [ -r "$proc/net/$t" ] || continue
  while read -ra f; do
    [ "${#f[@]}" -ge 10 ] || continue
    case "${f[0]}" in sl) continue ;; esac
    local_hex=${f[1]} rem_hex=${f[2]} st=${f[3]} inode=${f[9]}
    case "$t" in
      tcp | tcp6) [ "$st" = "01" ] || continue ;; # ESTABLISHED only
      *) case "$rem_hex" in *[1-9A-Fa-f]*) ;; *) continue ;; esac ;; # connected udp only
    esac
    pid=${inode2pid[$inode]:--}
    comm=${inode2comm[$inode]:--}
    [ -n "$comm" ] || comm=-
    printf '%s\t%s\t%s\t%s\t%s\n' "$t" "$(addr "$t" "$local_hex")" "$(addr "$t" "$rem_hex")" "$pid" "$comm"
    rows=$((rows + 1))
    [ "$rows" -lt "$max_rows" ] || break 2
  done <"$proc/net/$t"
done
