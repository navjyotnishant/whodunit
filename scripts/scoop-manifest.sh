#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-15
# Last updated: 2026-08-15
# Description: Generate the Scoop manifest for a release, with checksums read
# from the built artifacts rather than typed.
#
# Usage:
#   scripts/scoop-manifest.sh <version> [dist-dir]
#
# Writes dun.json to stdout. Commit it to the scoop-bucket repository:
#
#   scripts/release.sh v0.3.0
#   scripts/scoop-manifest.sh v0.3.0 > ../scoop-bucket/bucket/dun.json
#
# This is the Windows half of what scripts/brew-formula.sh does for macOS and
# Linux (NAV-103). Both are run by the publish-packages job in release.yml;
# run this by hand only when that job could not (it skips when
# PACKAGING_TOKEN is unset).
#
# The checksums are READ from checksums.txt rather than recomputed here, so
# the manifest cannot disagree with what the release actually published. A
# manifest with a wrong hash fails at install time with a checksum mismatch,
# which is a confusing way to learn that a release script and a packaging
# script computed a digest differently.
set -eu

VERSION="${1:?usage: $0 <version> [dist-dir]}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="${2:-$ROOT/dist}"

case "$VERSION" in
  v*) ;;
  *) echo "version must start with 'v' (e.g. v0.3.0), got: $VERSION" >&2; exit 1 ;;
esac

CHECKSUMS="$DIST/checksums.txt"
[ -f "$CHECKSUMS" ] || {
  echo "no checksums.txt in $DIST — run scripts/release.sh $VERSION first" >&2
  exit 1
}

# Scoop's version field has no leading v; the URLs do.
BARE="${VERSION#v}"
RELEASES="https://github.com/navjyotnishant/whodunit/releases"
BASE="$RELEASES/download/$VERSION"

# The `architecture` URLs above pin this release; the `autoupdate` block
# below must NOT — it is a template Scoop fills with the version it just
# discovered, so a hardcoded v0.2.0 in the path would keep fetching the old
# release forever while reporting the new version number.

# Look up one archive's digest by name. Fails loudly rather than emitting an
# empty hash: Scoop treats "" as "skip verification", so a silent miss here
# would ship a manifest that installs anything at all.
sha_for() {
  _name="$1"
  _sha="$(awk -v n="$_name" '$2 == n || $2 == "*" n { print $1 }' "$CHECKSUMS")"
  [ -n "$_sha" ] || { echo "no checksum for $_name in $CHECKSUMS" >&2; exit 1; }
  echo "$_sha"
}

AMD64="dun_${VERSION}_windows_amd64.zip"
ARM64="dun_${VERSION}_windows_arm64.zip"

# What the archive actually contains, which is not the same across releases.
#
# v0.2.0 and earlier shipped the versioned filename — dun_v0.2.0_windows_amd64.exe
# — because the rename to a plain `dun.exe` (NAV-101) landed in release.sh
# after v0.2.0 was cut. Scoop's `bin` field names a file inside the archive,
# so guessing wrong fails at install with "couldn't find dun.exe", and the
# per-arch names differ, so the shim has to be declared per architecture too.
#
# Read from the artifact rather than assumed: this script is run against a
# built dist/ or a downloaded release, so the answer is always available and
# never has to be remembered.
bin_for() {
  _zip="$DIST/$1"
  if [ -f "$_zip" ] && command -v unzip >/dev/null 2>&1; then
    unzip -Z1 "$_zip" | grep -i '\.exe$' | head -1
  else
    # No archive to inspect (checksums-only run): assume the current
    # release.sh behaviour rather than a name that is now historical.
    echo "dun.exe"
  fi
}

BIN_AMD64="$(bin_for "$AMD64")"
BIN_ARM64="$(bin_for "$ARM64")"

cat <<EOF
{
    "version": "$BARE",
    "description": "Record AI-attribution provenance in git trailers, so productivity claims tie to evidence.",
    "homepage": "https://github.com/navjyotnishant/whodunit",
    "license": "Apache-2.0",
    "architecture": {
        "64bit": {
            "url": "$BASE/$AMD64",
            "hash": "$(sha_for "$AMD64")",
            "bin": [["$BIN_AMD64", "dun"]]
        },
        "arm64": {
            "url": "$BASE/$ARM64",
            "hash": "$(sha_for "$ARM64")",
            "bin": [["$BIN_ARM64", "dun"]]
        }
    },
    "checkver": {
        "github": "https://github.com/navjyotnishant/whodunit"
    },
    "autoupdate": {
        "architecture": {
            "64bit": {
                "url": "$RELEASES/download/v\$version/dun_v\$version_windows_amd64.zip"
            },
            "arm64": {
                "url": "$RELEASES/download/v\$version/dun_v\$version_windows_arm64.zip"
            }
        },
        "hash": {
            "url": "$RELEASES/download/v\$version/checksums.txt"
        }
    }
}
EOF
