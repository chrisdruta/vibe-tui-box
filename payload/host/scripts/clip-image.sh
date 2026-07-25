#!/usr/bin/env bash
#
# Host clipboard glue for `vibe clip` — platform clipboard operations
# and NOTHING else. The engine (internal/app/clip.go) owns everything
# that used to live here: workspace containment (os.Root, O_EXCL),
# container streaming (typed docker exec), path minting, and the
# human/machine output. This script never touches the workspace, never
# runs docker, and only ever writes the engine-owned temp path it is
# handed.
#
# Modes:
#   save PATH   save the host clipboard image as PNG at PATH
#               (exit 1 with a one-line stderr diagnostic when the
#               clipboard has no image or no platform tool exists)
#   copy TEXT   put TEXT on the host clipboard (the QoL copy-back so
#               the next paste in an agent prompt is the path itself);
#               exit 1 when no clipboard tool exists
#
# Why this exists at all: Claude Code's Ctrl-V image paste reads the OS
# clipboard from the process side (on plain WSL it shells out to
# powershell.exe via interop). Inside the container there is no WSL
# interop and no display server, so the OS clipboard is unreachable —
# no terminal or tmux setting can fix that (the terminal only ever
# sends TEXT down the pty).
set -euo pipefail

mode="${1:-}"
arg="${2:-}"

case "$mode" in
save)
  if [ -z "$arg" ]; then
    echo "Usage: clip-image.sh save PATH" >&2
    exit 2
  fi
  if command -v powershell.exe >/dev/null 2>&1; then
    # WSL: PowerShell needs a WINDOWS path; wslpath maps the WSL-side
    # temp file. Pass the destination through the environment, never
    # interpolated into the script text: a path containing a single
    # quote would otherwise break out of the PowerShell string and run
    # as host-side code. WSL only shares variables listed in WSLENV
    # with Windows processes; a flagless entry passes the value
    # verbatim (it is already a Windows path, so no /p translation).
    CLIP_WIN_PATH="$(wslpath -w "$arg")"
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
    elif [ ! -s "$arg" ]; then
      echo "powershell.exe reported success but wrote no file: $arg" >&2
      exit 1
    fi
  elif command -v osascript >/dev/null 2>&1; then
    # macOS: stock AppleScript; errors out before opening the file when
    # the clipboard has no PNG-convertible image. The path is passed as
    # a run-handler argument (never interpolated into the script), so a
    # path containing a double quote can't break out of the AppleScript
    # string into host code.
    if ! osascript - "$arg" >/dev/null 2>&1 <<'APPLESCRIPT'; then
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
  ;;
copy)
  if command -v clip.exe >/dev/null 2>&1; then
    printf '%s' "$arg" | clip.exe
  elif command -v pbcopy >/dev/null 2>&1; then
    printf '%s' "$arg" | pbcopy
  else
    exit 1
  fi
  ;;
*)
  echo "Usage: clip-image.sh save PATH | copy TEXT" >&2
  exit 2
  ;;
esac
