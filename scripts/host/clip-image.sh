#!/usr/bin/env bash
#
# Save the HOST clipboard image into the dev container's /tmp so agents can read
# it — the workaround for image paste not working in the container.
#
# Why paste doesn't work: Claude Code's Ctrl-V image paste reads the OS clipboard
# from the process side (on plain WSL it shells out to powershell.exe via interop).
# Inside the container there is no WSL interop and no display server, so the OS
# clipboard is unreachable — no terminal or tmux setting can fix that (the
# terminal only ever sends TEXT down the pty).
#
# Invoked by `vibe clip [DIR]` on the host (WSL or macOS). By default the PNG is
# streamed into the running container's /tmp over `devcontainer exec` (nothing
# lands in the repo, gone on rebuild). With DIR — a workspace-relative directory
# — it is written straight into the bind-mounted repo instead (persists; no
# running container needed; gitignore the directory if it stays). Either way the
# container path is printed and — QoL — replaces the image on the host clipboard
# so the next paste in an agent prompt is the path itself.
set -euo pipefail

if [ "$#" -lt 3 ]; then
  echo "Usage: clip-image.sh REPO_ROOT DEST_DIR_OR_EMPTY DEVCONTAINER_CLI [CLI_ARG ...]" >&2
  echo "(normally invoked via: .devcontainer/vibe clip [DIR])" >&2
  exit 2
fi
repo_root="$1"
dest_dir="$2"
shift 2
cli=("$@")

case "$dest_dir" in
  /*)
    echo "Destination must be a relative path inside the workspace: $dest_dir" >&2
    exit 2
    ;;
  ..|../*|*/..|*/../*)
    echo "Destination must not escape the workspace: $dest_dir" >&2
    exit 2
    ;;
esac
dest_dir="${dest_dir%/}"

file_name="clip-$(date +%Y%m%d-%H%M%S).png"
if [ -n "$dest_dir" ]; then
  # Workspace mode: the repo is bind-mounted, so writing on the host is enough.
  mkdir -p "$repo_root/$dest_dir"
  # The `..` check above is lexical; a repo-controlled symlink (e.g.
  # .captures -> ../../.ssh) would still let mkdir/write escape. Resolve the
  # real directory with `pwd -P` (POSIX, portable) and confirm it stays under
  # the real repo root; then refuse an existing symlink at the target file so a
  # pre-planted link can't redirect the write.
  repo_root_real="$(cd "$repo_root" && pwd -P)"
  dest_real="$(cd "$repo_root/$dest_dir" && pwd -P)"
  case "$dest_real" in
    "$repo_root_real" | "$repo_root_real"/*) : ;;
    *)
      echo "Destination resolves outside the workspace (symlink?): $dest_dir" >&2
      exit 2
      ;;
  esac
  host_png="$dest_real/$file_name"
  if [ -L "$host_png" ]; then
    echo "Refusing to write through an existing symlink: $dest_dir/$file_name" >&2
    exit 2
  fi
  container_path="/workspaces/$(basename "$repo_root")/$dest_dir/$file_name"
else
  container_path="/tmp/$file_name"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "$tmp_dir"' EXIT
  host_png="$tmp_dir/clip.png"
fi

if command -v powershell.exe >/dev/null 2>&1; then
  # WSL: PowerShell needs a WINDOWS path; wslpath maps the WSL-side temp file.
  # Pass the destination through the environment, never interpolated into the
  # script text: a path containing a single quote would otherwise break out of
  # the PowerShell string and run as host-side code. WSL only shares variables
  # listed in WSLENV with Windows processes; a flagless entry passes the value
  # verbatim (it is already a Windows path, so no /p translation).
  CLIP_WIN_PATH="$(wslpath -w "$host_png")"
  export CLIP_WIN_PATH
  export WSLENV="${WSLENV:+$WSLENV:}CLIP_WIN_PATH"
  # shellcheck disable=SC2016  # single quotes are deliberate: $env:... is PowerShell, not bash
  result="$(powershell.exe -NoProfile -Command '
    $ErrorActionPreference = "Stop"
    try {
      Add-Type -AssemblyName System.Windows.Forms
      $img = [System.Windows.Forms.Clipboard]::GetImage()
      if ($img -eq $null) { Write-Output "NOIMAGE" }
      else { $img.Save($env:CLIP_WIN_PATH, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output "SAVED" }
    } catch { Write-Output "ERROR: $_" }
  ' | tr -d '\r')"
  if [ "$result" = "NOIMAGE" ]; then
    echo "No image on the Windows clipboard." >&2
    exit 1
  elif [ "$result" != "SAVED" ]; then
    echo "Failed to save the clipboard image: ${result:-no output from powershell.exe}" >&2
    exit 1
  elif [ ! -s "$host_png" ]; then
    echo "powershell.exe reported success but wrote no file: $host_png" >&2
    exit 1
  fi
elif command -v osascript >/dev/null 2>&1; then
  # macOS: stock AppleScript; errors out before opening the file when the
  # clipboard has no PNG-convertible image. The path is passed as a run-handler
  # argument (never interpolated into the script), so a path containing a double
  # quote can't break out of the AppleScript string into host code.
  if ! osascript - "$host_png" >/dev/null 2>&1 <<'APPLESCRIPT'; then
on run argv
  set outPath to item 1 of argv
  set png to the clipboard as «class PNGf»
  set f to open for access POSIX file outPath with write permission
  write png to f
  close access f
end run
APPLESCRIPT
    echo "No image on the macOS clipboard." >&2
    exit 1
  fi
else
  echo "Neither powershell.exe (WSL) nor osascript (macOS) is available —" >&2
  echo "run this on the host, not inside the container." >&2
  exit 1
fi

if [ -z "$dest_dir" ]; then
  # Stream into the container as base64 so the CLI's stdin handling can't mangle
  # binary bytes. /tmp is container-local: survives detach, gone on rebuild.
  base64 <"$host_png" | "${cli[@]}" exec --workspace-folder "$repo_root" \
    bash -c "base64 -d >'$container_path'"
else
  echo "Saved: $dest_dir/$file_name"
fi

echo "In the container: $container_path"

if command -v clip.exe >/dev/null 2>&1; then
  printf '%s' "$container_path" | clip.exe && echo "(path copied to clipboard)"
elif command -v pbcopy >/dev/null 2>&1; then
  printf '%s' "$container_path" | pbcopy && echo "(path copied to clipboard)"
fi
