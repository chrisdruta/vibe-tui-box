#!/usr/bin/env bash
#
# vibe host root-of-trust store (docs/security.md "host-only root of trust").
#
# The single rule everything here serves:
#
#   The host never executes, sources, evals, or feeds to the docker daemon
#   any byte a container could have written. Host code runs only from a
#   materialized, content-verified snapshot; host-consumed project inputs are
#   snapshotted and frozen before use; project identity and trust live outside
#   every workspace bind.
#
# This file is SOURCED by the trusted launcher (which itself runs only from a
# materialized versions/<sha>/ tree) and by the shim's first-contact path. It
# is pure library: sourcing produces no output and sets no shell options —
# callers own their tier. Host-side, so bash-3.2 (stock macOS) only: no
# associative arrays, no `mapfile`, no `${var^^}`.
#
# Store layout ($VIBE_HOME, default ~/.vibe, mode 0700):
#   bin/vibe                    the shim (host PATH entry point)
#   versions/<sha>/             immutable materialized harness trees (a-w)
#   versions/<sha>.manifest     sha256 <mode> <path> lines, one per file
#   repo.git                    host-owned bare mirror of the canonical remote
#   canonical-remote            one line: the trusted upstream URL
#   state/projects/<digest>     per-project trust records (strict k=v data)
#   state/snapshots/<digest>/   host-only compose input snapshots
#   state/lock/                 exclusive lockdirs for materialize/record writes

# ── environment sanitization ──────────────────────────────────────────────
# Called at the very top of any trusted operation. Strips the env vars that
# would otherwise let a caller's environment redirect code execution or git
# behavior. PATH is reset to a known-safe prefix; the launcher re-derives tool
# paths from there.
vibe_sanitize_env() {
  # Git config/hook/filter/credential redirection, alternate rc files, and the
  # tmux binary selector all become code-execution levers under a hostile env.
  unset BASH_ENV ENV SHELLOPTS GLOBIGNORE 2>/dev/null || true
  unset VIBE_TUI_TMUX 2>/dev/null || true
  local v
  for v in $(env 2>/dev/null | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do
    unset "$v" 2>/dev/null || true
  done
  # Deterministic, hostile-config-free git for every trusted git call.
  GIT_CONFIG_NOSYSTEM=1
  GIT_TERMINAL_PROMPT=0
  export GIT_CONFIG_NOSYSTEM GIT_TERMINAL_PROMPT
  case ":$PATH:" in
    *:/usr/bin:*) ;;
    *) PATH="/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin${PATH:+:$PATH}" ;;
  esac
  export PATH
}

# ── stat portability (GNU -c first for Linux; BSD -f for macOS) ────────────
# GNU `stat -f` is "filesystem status" and SUCCEEDS, so a BSD-first cascade
# never falls through on Linux — try GNU spelling first.
vibe_stat_owner() {
  stat -c '%u' "$1" 2>/dev/null || stat -f '%u' "$1" 2>/dev/null || echo -1
}
vibe_stat_mode() {
  stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1" 2>/dev/null || echo ''
}

# ── store resolution & validation ─────────────────────────────────────────
# Production is always $HOME/.vibe. A VIBE_HOME override is honored ONLY under
# VIBE_ALLOW_INSECURE_HOME=1 (tests), and even then must pass the same
# ownership/mode/symlink checks and sit outside any workspace root.
vibe_store_home() {
  if [ -n "${VIBE_HOME:-}" ] && [ "${VIBE_ALLOW_INSECURE_HOME:-}" = "1" ]; then
    printf '%s\n' "$VIBE_HOME"
    return 0
  fi
  if [ -z "${HOME:-}" ]; then
    printf 'vibe: HOME is unset — cannot locate the trust store\n' >&2
    return 1
  fi
  printf '%s/.vibe\n' "$HOME"
}

# A path is safe if no component is a symlink, and every EXISTING component is
# a directory owned by us or by root, and no non-owned component is writable by
# group/other (a world-writable ancestor a non-root attacker controls could
# swap a component). Root-owned system ancestors (/, /home) are expected and
# fine — a root that owns them can already do anything. Walks store root -> /.
vibe_path_is_secure() {
  local dir="$1" uid
  uid="$(id -u)"
  while [ -n "$dir" ] && [ "$dir" != "/" ]; do
    if [ -L "$dir" ]; then
      printf 'vibe: refusing symlinked path component: %s\n' "$dir" >&2
      return 1
    fi
    if [ -e "$dir" ]; then
      if [ ! -d "$dir" ]; then
        printf 'vibe: expected a directory: %s\n' "$dir" >&2
        return 1
      fi
      # BSD stat (macOS) vs GNU stat (Linux) — try both spellings.
      local owner perm
      owner="$(vibe_stat_owner "$dir")"
      if [ "$owner" != "$uid" ] && [ "$owner" != "0" ]; then
        printf 'vibe: %s is owned by uid %s (not you or root)\n' "$dir" "$owner" >&2
        return 1
      fi
      if [ "$owner" != "$uid" ]; then
        # A root-owned ancestor is fine unless it is group/other-writable
        # WITHOUT the sticky bit (a non-root attacker could swap the component).
        perm="$(vibe_stat_mode "$dir")"
        # Normalize to 4 digits: sticky/setuid, owner, group, other.
        while [ "${#perm}" -lt 4 ]; do perm="0$perm"; done
        local sticky group other
        sticky="${perm%${perm#?}}"      # first char
        group="$(printf '%s' "$perm" | cut -c3)"
        other="$(printf '%s' "$perm" | cut -c4)"
        case "$sticky" in 1) ;; *)  # sticky bit makes a writable dir safe enough
          case "$group" in 2|3|6|7) printf 'vibe: %s is group-writable and not sticky\n' "$dir" >&2; return 1 ;; esac
          case "$other" in 2|3|6|7) printf 'vibe: %s is world-writable and not sticky\n' "$dir" >&2; return 1 ;; esac
          ;;
        esac
      fi
    fi
    dir="$(dirname -- "$dir")"
  done
  return 0
}

# Ensure the store exists, is 0700, and every component is secure. Creates it
# on first use. Prints the store root on stdout.
vibe_store_init() {
  local home
  home="$(vibe_store_home)" || return 1
  if [ ! -e "$home" ]; then
    # Parent must itself be secure before we create under it.
    vibe_path_is_secure "$(dirname -- "$home")" || return 1
    ( umask 077 && mkdir -p "$home" ) || return 1
  fi
  vibe_path_is_secure "$home" || return 1
  local mode
  mode="$(vibe_stat_mode "$home")"
  case "$mode" in
    700) ;;
    *) chmod 700 "$home" 2>/dev/null || {
         printf 'vibe: could not set 0700 on %s\n' "$home" >&2; return 1; } ;;
  esac
  ( umask 077 && mkdir -p "$home/versions" "$home/state/projects" \
      "$home/state/snapshots" "$home/state/lock" "$home/bin" ) || return 1
  printf '%s\n' "$home"
}

# ── canonical checkout digest (the project key) ───────────────────────────
# Symlink-resolved path digest, first 16 hex. Stable per checkout location; a
# moved checkout gets a new key (and a stale-record check catches a replaced
# checkout at the same path). Mirrors repo-root.sh's suffix algorithm but is
# wider (collision key, not a human label).
vibe_checkout_digest() {
  local canonical
  canonical="$(cd -- "$1" 2>/dev/null && pwd -P)" || return 1
  vibe_sha256_string "$canonical" | cut -c1-16
}

# sha256 of stdin's first argument as a string. coreutils / macOS / POSIX
# fallbacks, same cascade as repo-root.sh.
vibe_sha256_string() {
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | tr -dc '0-9a-f'
  elif command -v shasum >/dev/null 2>&1; then
    printf '%s' "$1" | shasum -a 256 | tr -dc '0-9a-f'
  else
    # cksum is not cryptographic; only reached on a host lacking both sha
    # tools. Digest collisions here degrade project keying, not code trust
    # (code trust is the git object hash + manifest).
    printf '%s' "$1" | cksum | tr -dc '0-9a-f'
  fi
}

vibe_sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" 2>/dev/null | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" 2>/dev/null | cut -d' ' -f1
  else
    cksum "$1" 2>/dev/null | cut -d' ' -f1
  fi
}

# ── trust records (strict k=v data — NEVER sourced/eval'd) ─────────────────
# A record is lines of KEY=VALUE with a fixed key set. Read prints the value
# for a requested key; values are validated against a charset by the caller.
# Keys: sha, project_name, ws_base, compose_snapshot_hash, mode (normal|dev),
#       dev_version, created.
vibe_record_path() {
  local home; home="$(vibe_store_home)" || return 1
  printf '%s/state/projects/%s\n' "$home" "$1"
}

vibe_record_get() {
  # $1 = record file, $2 = key. Prints value or nothing. No eval — a plain
  # line read, first match wins, value taken verbatim after the first '='.
  local file="$1" key="$2" line
  [ -f "$file" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      "$key="*) printf '%s\n' "${line#*=}"; return 0 ;;
    esac
  done <"$file"
  return 1
}

# Atomically write a record from a set of key=value pairs passed as argv.
# Values are written verbatim; callers validate charset before calling. Uses
# a lockdir + mktemp (no predictable .tmp — closes the M-1 symlink class).
vibe_record_write() {
  local file="$1"; shift
  local home lock tmp
  home="$(vibe_store_home)" || return 1
  lock="$home/state/lock/record"
  vibe_with_lock "$lock" || return 1
  tmp="$(vibe_mktemp "$(dirname -- "$file")")" || { vibe_unlock "$lock"; return 1; }
  local pair
  {
    for pair in "$@"; do
      printf '%s\n' "$pair"
    done
  } >"$tmp"
  chmod 600 "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$file"
  vibe_unlock "$lock"
}

# ── locking & temp (same-filesystem, no predictable names) ────────────────
vibe_mktemp() {
  # A file under $1 (must be a secure dir). mktemp templates keep it on the
  # same filesystem so the later mv is atomic.
  local dir="${1:-.}"
  mktemp "$dir/.vibe.XXXXXXXX" 2>/dev/null || return 1
}

vibe_mktemp_dir() {
  local dir="${1:-.}"
  mktemp -d "$dir/.vibe.XXXXXXXX" 2>/dev/null || return 1
}

# mkdir-based mutex: mkdir is atomic on POSIX filesystems. Spins briefly then
# gives up (a crashed holder leaves a stale dir — the caller can force with
# VIBE_LOCK_STEAL=1 after inspecting).
vibe_with_lock() {
  local lock="$1" i=0
  while ! mkdir "$lock" 2>/dev/null; do
    i=$((i + 1))
    if [ "$i" -gt 100 ]; then
      if [ "${VIBE_LOCK_STEAL:-}" = "1" ]; then
        rm -rf "$lock" 2>/dev/null && continue
      fi
      printf 'vibe: could not acquire lock %s (another vibe running? VIBE_LOCK_STEAL=1 to force)\n' "$lock" >&2
      return 1
    fi
    sleep 1
  done
  return 0
}

vibe_unlock() {
  rmdir "$1" 2>/dev/null || rm -rf "$1" 2>/dev/null || true
}

# ── materialization ───────────────────────────────────────────────────────
# Produce an immutable versions/<sha>/ tree for a git SHA, from a trusted
# object source, rejecting anything that could redirect later host execution.
#
# $1 = sha (already publisher-authenticated by the caller — reachable from a
#      canonical release ref, or explicitly UNVERIFIED-confirmed)
# $2 = object source: a git dir/url to fetch the sha from
# Prints the version dir on success.
vibe_materialize() {
  local sha="$1" src="$2"
  case "$sha" in
    *[!0-9a-f]* | "") printf 'vibe: bad sha: %s\n' "$sha" >&2; return 1 ;;
  esac
  local home; home="$(vibe_store_init)" || return 1
  local dest="$home/versions/$sha"
  if [ -d "$dest" ] && vibe_verify_version "$sha" >/dev/null 2>&1; then
    printf '%s\n' "$dest"
    return 0
  fi
  local lock="$home/state/lock/materialize-$sha"
  vibe_with_lock "$lock" || return 1
  # Re-check under lock (another process may have just finished).
  if [ -d "$dest" ] && vibe_verify_version "$sha" >/dev/null 2>&1; then
    vibe_unlock "$lock"; printf '%s\n' "$dest"; return 0
  fi

  # 1. Fetch into an isolated bare repo with NO local hardlinks (never share
  #    inodes with, or corrupt, an object source) and fsck on transfer.
  local work; work="$(vibe_mktemp_dir "$home/versions")" || { vibe_unlock "$lock"; return 1; }
  local bare="$work/obj.git"
  if ! git init --bare -q "$bare" 2>/dev/null; then
    rm -rf "$work"; vibe_unlock "$lock"; return 1
  fi
  if ! git -C "$bare" -c fetch.fsckObjects=true -c transfer.fsckObjects=true \
        fetch --no-tags --no-recurse-submodules -q "file://$src" "$sha" 2>/dev/null; then
    # Non-file URL (https mirror) — retry without the file:// prefix.
    if ! git -C "$bare" -c fetch.fsckObjects=true -c transfer.fsckObjects=true \
          fetch --no-tags --no-recurse-submodules -q "$src" "$sha" 2>/dev/null; then
      printf 'vibe: could not fetch %s from %s\n' "$sha" "$src" >&2
      rm -rf "$work"; vibe_unlock "$lock"; return 1
    fi
  fi
  if ! git -C "$bare" cat-file -e "$sha^{commit}" 2>/dev/null; then
    printf 'vibe: %s is not a commit in the fetched objects\n' "$sha" >&2
    rm -rf "$work"; vibe_unlock "$lock"; return 1
  fi
  git -C "$bare" fsck --no-dangling >/dev/null 2>&1 || {
    printf 'vibe: fsck failed on fetched objects for %s\n' "$sha" >&2
    rm -rf "$work"; vibe_unlock "$lock"; return 1
  }

  # 2. Extract the tree via archive (drops .git, no checkout, no hooks).
  local tree="$work/tree"
  ( umask 077 && mkdir -p "$tree" ) || { rm -rf "$work"; vibe_unlock "$lock"; return 1; }
  if ! git -C "$bare" archive --format=tar "$sha" | ( cd "$tree" && tar -xf - ); then
    printf 'vibe: archive/extract failed for %s\n' "$sha" >&2
    rm -rf "$work"; vibe_unlock "$lock"; return 1
  fi

  # 3. Reject anything that could redirect host execution: symlinks, special
  #    files, or gitlinks anywhere in the extracted tree.
  if ! vibe_tree_is_clean "$tree"; then
    rm -rf "$work"; vibe_unlock "$lock"; return 1
  fi

  # 4. Publish, freeze, THEN manifest. Order matters two ways: renaming a
  #    directory that has subdirectories needs write on the directory itself
  #    (the kernel rewrites child ".." links) so a-w must come after the move;
  #    and the manifest records file modes, so it must be built from the FROZEN
  #    tree (444), or verify's rebuild-from-frozen would mismatch the recorded
  #    644.
  local manifest="$work/manifest"
  # Clear any prior (frozen) dir at this path before re-publishing.
  if [ -d "$dest" ]; then chmod -R u+w "$dest" 2>/dev/null || true; rm -rf "$dest"; fi
  if ! mv "$tree" "$dest"; then
    rm -rf "$work"; vibe_unlock "$lock"; return 1
  fi
  chmod -R a-w "$dest" 2>/dev/null || true
  if ! vibe_build_manifest "$dest" >"$manifest"; then
    chmod -R u+w "$dest" 2>/dev/null; rm -rf "$dest" "$work"; vibe_unlock "$lock"; return 1
  fi
  mv -f "$manifest" "$home/versions/$sha.manifest"
  rm -rf "$work" 2>/dev/null || true
  vibe_unlock "$lock"
  printf '%s\n' "$dest"
}

# Walk a tree; fail if any entry is a symlink, gitlink dir, or non-regular /
# non-dir special file. bash-3.2: no `find -print0 | while read -d` needed —
# newlines in harness paths don't occur, and we control the input tree.
vibe_tree_is_clean() {
  local tree="$1"
  # Symlinks (files or dirs) first — find -type l catches both.
  if find "$tree" -type l 2>/dev/null | grep -q .; then
    printf 'vibe: refusing materialized tree: contains a symlink\n' >&2
    find "$tree" -type l 2>/dev/null | sed 's/^/  symlink: /' >&2
    return 1
  fi
  # Anything that is neither a regular file nor a directory (fifo, socket,
  # device, gitlink placeholder).
  if find "$tree" ! -type f ! -type d 2>/dev/null | grep -q .; then
    printf 'vibe: refusing materialized tree: contains a special file\n' >&2
    find "$tree" ! -type f ! -type d 2>/dev/null | sed 's/^/  special: /' >&2
    return 1
  fi
  return 0
}

# sha256 <octal-mode> <relpath> for every regular file, sorted by path.
vibe_build_manifest() {
  local tree="$1" f rel mode hash
  ( cd "$tree" && find . -type f 2>/dev/null | LC_ALL=C sort ) | while IFS= read -r rel; do
    f="$tree/${rel#./}"
    mode="$(vibe_stat_mode "$f")"; [ -z "$mode" ] && mode="?"
    hash="$(vibe_sha256_file "$f")"
    printf '%s %s %s\n' "$hash" "$mode" "${rel#./}"
  done
}

# Verify a version dir against its recorded manifest. Prints the sha on match.
vibe_verify_version() {
  local sha="$1" home dest manifest
  home="$(vibe_store_home)" || return 1
  dest="$home/versions/$sha"
  manifest="$home/versions/$sha.manifest"
  [ -d "$dest" ] || { printf 'vibe: version %s not materialized\n' "$sha" >&2; return 1; }
  [ -f "$manifest" ] || { printf 'vibe: no manifest for %s\n' "$sha" >&2; return 1; }
  # Re-reject symlinks/specials (a post-publish tamper could add one).
  vibe_tree_is_clean "$dest" || return 1
  local cur
  cur="$(vibe_build_manifest "$dest")"
  if [ "$cur" != "$(cat "$manifest")" ]; then
    printf 'vibe: version %s does not match its manifest (tampered?)\n' "$sha" >&2
    return 1
  fi
  printf '%s\n' "$sha"
}

# Cheap pre-exec check: verify only the handful of scripts the launcher is
# about to run, not the whole tree (full check lives in doctor). $1 = version
# dir. Verifies vibe + the host scripts against the manifest.
vibe_verify_exec_paths() {
  local dest="$1" manifest sha rel f mode hash want
  sha="$(basename -- "$dest")"
  local home; home="$(vibe_store_home)" || return 1
  manifest="$home/versions/$sha.manifest"
  [ -f "$manifest" ] || { printf 'vibe: no manifest for %s\n' "$sha" >&2; return 1; }
  for rel in vibe src/scripts/host/tui.sh src/scripts/host/state-render.sh \
             src/scripts/host/sidebar.sh src/scripts/host/clip-to-pane.sh \
             src/scripts/repo-root.sh src/scripts/update.sh; do
    f="$dest/$rel"
    [ -f "$f" ] || continue
    [ -L "$f" ] && { printf 'vibe: %s is a symlink in the version tree\n' "$rel" >&2; return 1; }
    hash="$(vibe_sha256_file "$f")"
    want="$(grep -E " $rel\$" "$manifest" | head -1 | cut -d' ' -f1)"
    if [ -z "$want" ] || [ "$hash" != "$want" ]; then
      printf 'vibe: %s fails manifest verification\n' "$rel" >&2
      return 1
    fi
  done
  return 0
}

# ── canonical remote & host mirror (publisher authentication) ─────────────
# The mirror's remote is recorded host-side at install time and is the ONLY
# source of release authenticity — never re-derived from workspace
# .gitmodules/submodule config (which a container can rewrite).
vibe_canonical_remote() {
  local home; home="$(vibe_store_home)" || return 1
  [ -f "$home/canonical-remote" ] || return 1
  head -1 "$home/canonical-remote"
}

# Record the canonical remote (install-time, human-confirmed). Refuses to
# silently change an existing one.
vibe_set_canonical_remote() {
  local url="$1" home cur
  home="$(vibe_store_init)" || return 1
  cur="$(vibe_canonical_remote 2>/dev/null || true)"
  if [ -n "$cur" ] && [ "$cur" != "$url" ]; then
    printf 'vibe: canonical remote already set to %s (was asked to set %s).\n' "$cur" "$url" >&2
    printf '      Change it deliberately: rm %s/canonical-remote and re-run.\n' "$home" >&2
    return 1
  fi
  ( umask 077 && printf '%s\n' "$url" >"$home/canonical-remote" )
}

# Refresh the host mirror from the canonical remote. Idempotent; creates the
# bare mirror on first call.
vibe_mirror_refresh() {
  local home remote; home="$(vibe_store_init)" || return 1
  remote="$(vibe_canonical_remote)" || {
    printf 'vibe: no canonical remote recorded — run the installer self-step first\n' >&2
    return 1
  }
  local mirror="$home/repo.git"
  if [ ! -d "$mirror" ]; then
    git clone --mirror -q "$remote" "$mirror" 2>/dev/null || {
      printf 'vibe: mirror clone from %s failed (offline?)\n' "$remote" >&2
      return 1
    }
    return 0
  fi
  git -C "$mirror" -c fetch.fsckObjects=true remote update --prune >/dev/null 2>&1 || {
    printf 'vibe: mirror refresh failed (offline?) — using already-fetched objects\n' >&2
    return 0
  }
}

# Is a SHA reachable from a release ref (tag or the tracked branch) in the host
# mirror? This is the v1.0 publisher-authentication bar (canonical-remote
# reachability; signed-tag verification is a later hardening). Returns 0 and
# prints the nearest describing tag when reachable.
vibe_sha_is_release() {
  local sha="$1" home mirror ref
  home="$(vibe_store_home)" || return 1
  mirror="$home/repo.git"
  [ -d "$mirror" ] || return 1
  git -C "$mirror" cat-file -e "$sha^{commit}" 2>/dev/null || return 1
  # Reachable from any tag or any remote/local branch head in the mirror?
  for ref in $(git -C "$mirror" for-each-ref --format='%(refname)' refs/tags refs/heads 2>/dev/null); do
    if git -C "$mirror" merge-base --is-ancestor "$sha" "$ref" 2>/dev/null; then
      git -C "$mirror" describe --tags "$sha" 2>/dev/null || printf '%s\n' "$sha"
      return 0
    fi
  done
  return 1
}

# ── compose / daemon gate: snapshot → render → structural enforce ─────────
# Everything that reaches the docker daemon goes through here. It never hands
# the daemon a workspace path; it hands it a host-only snapshot, and only after
# the rendered effective model satisfies the non-negotiable invariants.
#
# The compose invariants (rendered `dev` service):
#   user=vscode, cap_drop includes ALL, no-new-privileges, workspace bind and
#   the exact RO harness overmount present, and NONE of: privileged, cap_add,
#   devices, host pid/ipc/uts/cgroup/network namespaces, docker socket bind at
#   any path, use_api_socket, ssh/host-secret/host-config binds, security_opt
#   weakening, userns_mode. Tripping any of these requires --unsafe.

# Copy the referenced-input closure into a host-only snapshot dir. $1=repo_root,
# $2=snapshot dir (emptied first). Rejects symlinked inputs. Records present/
# absent so a newly-added Dockerfile counts as drift.
vibe_snapshot_compose_inputs() {
  local root="$1" snap="$2"
  rm -rf "$snap" 2>/dev/null || true
  ( umask 077 && mkdir -p "$snap/.vibe" ) || return 1
  local rel src
  # The fixed project-owned control files. The referenced-input closure beyond
  # these (include/extends/env_file/build context) is captured by rendering
  # under --project-directory below and by copying the whole .vibe tree, minus
  # the harness submodule (which is trusted separately and RO-mounted).
  for rel in compose.yaml Dockerfile .dockerignore config.env; do
    src="$root/.vibe/$rel"
    if [ -L "$src" ]; then
      printf 'vibe: refusing symlinked compose input: .vibe/%s\n' "$rel" >&2
      return 1
    fi
    if [ -e "$src" ]; then
      cp -p "$src" "$snap/.vibe/$rel" 2>/dev/null || return 1
      printf 'present .vibe/%s\n' "$rel" >>"$snap/.inputs"
    else
      printf 'absent .vibe/%s\n' "$rel" >>"$snap/.inputs"
    fi
  done
  return 0
}

# Render the merged compose config under a SCRUBBED environment — only
# launcher-minted VIBE_* vars, and NO project .env (so ${...} interpolation
# can't shift meaning behind an unchanged file hash). Prints the rendered YAML.
# Args: base_yaml project_compose project_dir project_name ws_base harness_dir
vibe_render_compose() {
  local base="$1" proj="$2" pdir="$3" pname="$4" ws="$5" hdir="$6"
  local compose_bin
  if docker compose version >/dev/null 2>&1; then compose_bin="docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then compose_bin="docker-compose"
  else printf 'vibe: docker compose not found\n' >&2; return 1; fi
  # env -i: an empty environment; re-add only what compose legitimately needs
  # and what the base/render themselves reference. PATH is required to find
  # docker. No project .env, no inherited host vars.
  env -i \
    PATH="$PATH" HOME="$HOME" \
    VIBE_PROJECT_NAME="$pname" \
    VIBE_WORKSPACE_BASENAME="$ws" \
    VIBE_REPO_ROOT="$pdir" \
    VIBE_USER_UID="$(id -u)" \
    VIBE_HARNESS_DIR="$hdir" \
    VIBE_HARNESS_SRC="$hdir/src" \
    $compose_bin `# intentional word-split: docker compose is two words` \
      --project-name "$pname" \
      --project-directory "$pdir" \
      -f "$base" \
      -f "$proj" \
      config 2>/dev/null
}

# Structural enforcement on a rendered compose model (read on stdin). Returns 0
# if the dev service satisfies every invariant, 2 if an invariant is violated
# (caller decides: refuse, or proceed under --unsafe with a banner). We parse
# the rendered YAML with grep/awk — it is compose's own normalized output, and
# we FAIL CLOSED: an unrecognized shape that could hide a violation is treated
# as a violation.
vibe_enforce_compose() {
  local rendered="$1" ws_base="$2"
  local violations="" model
  model="$(cat "$rendered")"

  # Forbidden top-level/service keys anywhere in the rendered model. These are
  # never legitimate for the dev service under the boundary.
  local key
  for key in privileged cap_add devices userns_mode; do
    if printf '%s\n' "$model" | grep -Eq "^[[:space:]]*${key}:" ; then
      # cap_add: [] renders empty — only flag non-empty.
      case "$key" in
        cap_add)
          printf '%s\n' "$model" | awk '/^[[:space:]]*cap_add:/{f=1;next} f&&/^[[:space:]]*-/{print;e=1} f&&/^[[:space:]]*[a-z_]+:/{f=0} END{exit !e}' >/dev/null \
            && violations="$violations cap_add" ;;
        *) violations="$violations $key" ;;
      esac
    fi
  done

  # Host namespaces: pid/ipc/uts/cgroup/network: host (or "host" values).
  for key in pid ipc uts cgroup network_mode; do
    if printf '%s\n' "$model" | grep -Eq "^[[:space:]]*${key}:[[:space:]]*[\"']?host[\"']?[[:space:]]*$"; then
      violations="$violations ${key}=host"
    fi
  done

  # Docker socket at ANY path, and use_api_socket.
  if printf '%s\n' "$model" | grep -Eq 'docker\.sock'; then
    violations="$violations docker.sock-bind"
  fi
  if printf '%s\n' "$model" | grep -Eq '^[[:space:]]*use_api_socket:[[:space:]]*true'; then
    violations="$violations use_api_socket"
  fi
  # SSH agent / host secret forwarding surfaces.
  if printf '%s\n' "$model" | grep -Eq 'SSH_AUTH_SOCK|ssh-agent'; then
    violations="$violations ssh-agent"
  fi

  # security_opt must not drop no-new-privileges nor add anything permissive.
  if ! printf '%s\n' "$model" | grep -Eq 'no-new-privileges:true'; then
    violations="$violations missing-no-new-privileges"
  fi
  # Must run as non-root vscode.
  if ! printf '%s\n' "$model" | grep -Eq '^[[:space:]]*user:[[:space:]]*[\"'\'']?vscode'; then
    violations="$violations user-not-vscode"
  fi
  # cap_drop must include ALL.
  if ! printf '%s\n' "$model" | awk '/cap_drop:/{f=1} f&&/ALL/{print;e=1} END{exit !e}' >/dev/null; then
    violations="$violations cap_drop-not-ALL"
  fi

  # The exact RO harness overmount must be present with read_only true. We
  # check the rendered model names the harness target as read-only.
  local target="/workspaces/$ws_base/.vibe/harness"
  if ! printf '%s\n' "$model" | grep -Fq "$target"; then
    violations="$violations missing-harness-overmount"
  fi

  if [ -n "$violations" ]; then
    printf 'COMPOSE INVARIANT VIOLATIONS:%s\n' "$violations" >&2
    return 2
  fi
  return 0
}

# The full gate. Snapshots inputs, renders under scrubbed env, enforces
# invariants, and on drift-from-record shows a diff and re-records. Sets the
# global VIBE_SNAPSHOT_COMPOSE to the snapshot compose path the caller must
# hand the daemon. Args:
#   repo_root base_yaml project_name ws_base harness_dir record_file [--unsafe]
vibe_compose_gate() {
  local root="$1" base="$2" pname="$3" ws="$4" hdir="$5" record="$6" unsafe="${7:-}"
  local home; home="$(vibe_store_init)" || return 1
  local digest; digest="$(vibe_checkout_digest "$root")" || return 1
  local snap="$home/state/snapshots/$digest"
  vibe_snapshot_compose_inputs "$root" "$snap" || return 1

  local rendered="$snap/rendered.yaml"
  if ! vibe_render_compose "$base" "$snap/.vibe/compose.yaml" "$root" "$pname" "$ws" "$hdir" >"$rendered"; then
    printf 'vibe: could not render the merged compose config for review\n' >&2
    return 1
  fi

  # Structural enforcement.
  local enforce_rc=0
  vibe_enforce_compose "$rendered" "$ws" "$hdir" || enforce_rc=$?
  if [ "$enforce_rc" = "2" ]; then
    if [ "$unsafe" = "--unsafe" ]; then
      printf '\n*** --unsafe: the container boundary is DISABLED for this command. ***\n' >&2
      printf '*** The rendered compose config violates the hardening invariants above. ***\n\n' >&2
    else
      printf '\nRefusing to run: the project compose config would weaken the container\n' >&2
      printf 'boundary (see violations above). If this is deliberate, re-run with --unsafe\n' >&2
      printf '(which loudly disables the boundary for that one command).\n' >&2
      return 1
    fi
  fi

  # Drift-from-record: hash the rendered model + the inputs list.
  local cur_hash rec_hash
  cur_hash="$(vibe_sha256_file "$rendered")"
  rec_hash="$(vibe_record_get "$record" compose_snapshot_hash 2>/dev/null || true)"
  if [ -n "$rec_hash" ] && [ "$rec_hash" != "$cur_hash" ]; then
    printf '\nThe project compose config changed since it was last trusted.\n' >&2
    if [ -f "$snap/prev-rendered.yaml" ]; then
      diff -u "$snap/prev-rendered.yaml" "$rendered" >&2 || true
    fi
    if [ -t 0 ] && [ -t 1 ]; then
      printf 'Trust the new compose config for this project? [y/N]: ' >&2
      local ans; read -r ans
      case "$ans" in y|Y|yes|YES) ;; *) printf 'Aborted.\n' >&2; return 1 ;; esac
    else
      printf 'Non-interactive: refusing to accept compose drift. Use `vibe provision`.\n' >&2
      return 1
    fi
  fi
  cp -p "$rendered" "$snap/prev-rendered.yaml" 2>/dev/null || true
  VIBE_SNAPSHOT_COMPOSE="$snap/.vibe/compose.yaml"
  VIBE_COMPOSE_SNAPSHOT_HASH="$cur_hash"
  export VIBE_SNAPSHOT_COMPOSE VIBE_COMPOSE_SNAPSHOT_HASH
  return 0
}
