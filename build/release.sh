#!/usr/bin/env bash
# build/release.sh — build and package release archives in EXACTLY the
# layout internal/release verifies (source.go names the assets,
# acquire.go's mapEntry admits only `vibe` and `payload/**`,
# verify.go's findChecksum reads `checksums.txt`). One script serves
# the local snapshot and the release workflow so the two cannot drift;
# TestDefaultBaseURL pins the naming this script must produce.
#
#   build/release.sh [VERSION]
#
# VERSION is the v-prefixed tag. Absent, a snapshot version is minted
# (v0.0.0-snapshot.<sha12>) — stamped like a real release but named so
# it can never be mistaken for one. Output lands in dist/:
# three per-platform tar.gz archives plus checksums.txt.
#
# Archives are reproducible for a given commit: -trimpath on the
# build, sorted entries, zeroed ownership, commit-time mtimes, ustar
# format (pax could emit extended-header entry types mapEntry
# rejects), and gzip -n (no name/timestamp in the stream header).
set -euo pipefail
cd "$(dirname "$0")/.."

version="${1:-}"
commit="$(git rev-parse HEAD)"
if [ -z "$version" ]; then
  version="v0.0.0-snapshot.${commit:0:12}"
fi
case "$version" in
v*) ;;
*)
  echo "release.sh: version must be v-prefixed (got '$version')" >&2
  exit 2
  ;;
esac

mtime="$(git show -s --format=%cI HEAD)"
dist="dist"
rm -rf "$dist"
mkdir -p "$dist"

for platform in linux/amd64 linux/arm64 darwin/arm64; do
  os="${platform%/*}"
  arch="${platform#*/}"
  stage="$(mktemp -d)"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-X vibe/internal/version.release=$version -X vibe/internal/version.commit=$commit" \
    -o "$stage/vibe" ./cmd/vibe
  cp -R payload "$stage/payload"
  name="vibe_${version}_${os}_${arch}.tar.gz"
  tar --format=ustar --sort=name --owner=0 --group=0 --numeric-owner \
    --mtime="$mtime" -C "$stage" -cf - vibe payload |
    gzip -n -9 >"$dist/$name"
  rm -rf "$stage"
  echo "packed $name"
done

(cd "$dist" && sha256sum vibe_*.tar.gz >checksums.txt)
echo "dist/checksums.txt:"
cat "$dist/checksums.txt"
