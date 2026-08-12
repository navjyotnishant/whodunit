#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-11
# Description: Build and (optionally) publish a whodunit release without goreleaser.
#
# Usage:
#   scripts/release.sh <version>            build cross-platform binaries + checksums into dist/
#   scripts/release.sh <version> --publish  also create a git tag and a GitHub release (needs gh)
#
# Example: scripts/release.sh v0.1.0
#
# This exists so a release can be cut with only go, git, and (for --publish)
# gh on PATH — no goreleaser, no network calls beyond git/gh, nothing
# AI-assisted required to build or ship a binary.
set -eu

VERSION="${1:?usage: $0 <version> [--publish]}"
PUBLISH="${2:-}"

case "$VERSION" in
  v*) ;;
  *) echo "version must start with 'v' (e.g. v0.1.0), got: $VERSION" >&2; exit 1 ;;
esac

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
rm -rf "$DIST"
mkdir -p "$DIST"

TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

echo "building $VERSION for: $TARGETS"

for target in $TARGETS; do
  os="${target%/*}"
  arch="${target#*/}"
  ext=""
  [ "$os" = "windows" ] && ext=".exe"

  out="$DIST/dun_${VERSION}_${os}_${arch}${ext}"
  echo "  $os/$arch"
  ( cd "$ROOT" && \
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -ldflags "-s -w -X main.version=$VERSION" -o "$out" ./cmd/dun )

  archive_base="dun_${VERSION}_${os}_${arch}"
  if [ "$os" = "windows" ]; then
    ( cd "$DIST" && zip -q "${archive_base}.zip" "$(basename "$out")" && rm "$(basename "$out")" )
  else
    ( cd "$DIST" && tar czf "${archive_base}.tar.gz" "$(basename "$out")" && rm "$(basename "$out")" )
  fi
done

( cd "$DIST" && shasum -a 256 * > checksums.txt )

echo "built:"
ls -1 "$DIST"

if [ "$PUBLISH" = "--publish" ]; then
  if ! command -v gh >/dev/null 2>&1; then
    echo "gh not found on PATH — cannot publish. Artifacts are still in dist/." >&2
    exit 1
  fi
  echo "tagging and creating GitHub release $VERSION"
  ( cd "$ROOT" && git tag "$VERSION" && git push origin "$VERSION" )
  ( cd "$ROOT" && gh release create "$VERSION" "$DIST"/dun_* "$DIST"/checksums.txt \
      --title "$VERSION" --generate-notes )
  echo "published $VERSION"
else
  echo "not published (pass --publish to tag + create a GitHub release)"
fi
