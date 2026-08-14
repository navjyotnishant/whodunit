#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-11
# Description: Build and (optionally) publish a whodunit release without goreleaser.
#
# Usage:
#   scripts/release.sh <version>            build cross-platform binaries + checksums into dist/
#   scripts/release.sh <version> --publish  also create a git tag and a GitHub release (needs gh)
#
# --publish requires being on the PRD branch — releases are only cut from PRD.
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

  # The archive keeps the version and platform in its name — that is how
  # release assets are told apart. The binary inside is plain `dun`.
  #
  # Those used to be the same string, so unzipping on Windows produced
  # dun_v0.2.0_windows_amd64.exe, which does nothing until the user renames
  # it: the git hook resolves `dun` from PATH by name, so a differently-named
  # binary means every commit is silently stamped undetermined.
  #
  # Homebrew hid this on macOS and Linux by renaming during install
  # (`bin.install "dun_v0.2.0_darwin_arm64" => "dun"`). An archive has no
  # install step, so the archive has to be right on its own — and the plain
  # archive is a supported route for anyone whose policy forbids package
  # managers.
  binary="dun${ext}"
  out="$DIST/$binary"
  echo "  $os/$arch"
  ( cd "$ROOT" && \
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -ldflags "-s -w -X main.version=$VERSION" -o "$out" ./cmd/dun )

  archive_base="dun_${VERSION}_${os}_${arch}"
  if [ "$os" = "windows" ]; then
    # -X drops the extra-attribute records macOS zip adds. Without it the
    # archive carries a second entry (__MACOSX/._dun.exe) that means nothing
    # on the machine unpacking it.
    ( cd "$DIST" && zip -qX "${archive_base}.zip" "$binary" && rm "$binary" )
  else
    # COPYFILE_DISABLE stops bsdtar on macOS writing an AppleDouble "._dun"
    # beside the binary. Harmless on Linux, where the variable is ignored.
    ( cd "$DIST" && COPYFILE_DISABLE=1 tar czf "${archive_base}.tar.gz" "$binary" && rm "$binary" )
  fi
done

# Globbing whatever is in dist/ would checksum any stray file that happened
# to be there. dist/ is wiped on entry so it should hold only what was just
# built, but a checksum manifest is the wrong place to find out otherwise.
( cd "$DIST" && shasum -a 256 dun_* > checksums.txt )

echo "built:"
ls -1 "$DIST"

if [ "$PUBLISH" = "--publish" ]; then
  if ! command -v gh >/dev/null 2>&1; then
    echo "gh not found on PATH — cannot publish. Artifacts are still in dist/." >&2
    exit 1
  fi

  CURRENT_BRANCH="$(cd "$ROOT" && git rev-parse --abbrev-ref HEAD)"
  if [ "$CURRENT_BRANCH" != "PRD" ]; then
    echo "releases are only cut from PRD, currently on '$CURRENT_BRANCH'. Checkout PRD first." >&2
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
