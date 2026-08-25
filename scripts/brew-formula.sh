#!/bin/sh
# Author: Navjyot Nishant
# Created: 2026-08-19
# Last updated: 2026-08-19
# Description: Generate the Homebrew formula for a release, with checksums
# read from the built artifacts rather than typed.
#
# Usage:
#   scripts/brew-formula.sh <version> [dist-dir]
#
# Writes dun.rb to stdout. The publish job in release.yml commits it to the
# navjyotnishant/homebrew-tap repository; run it by hand only when that job
# could not (see the workflow's notes on the packaging token).
#
# This is the macOS/Linux half of what scoop-manifest.sh does for Windows,
# and it exists because the formula was previously hand-edited. That is why
# the tap sat on 0.3.0 while v0.3.1 shipped: `brew upgrade dun` correctly
# reported nothing to do, because 0.3.0 was the newest version brew knew
# about. Generating the formula from the artifacts removes the step that
# was being forgotten.
#
# The checksums are READ from checksums.txt rather than recomputed, so the
# formula cannot disagree with what the release actually published.
set -eu

VERSION="${1:?usage: $0 <version> [dist-dir]}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="${2:-$ROOT/dist}"

case "$VERSION" in
  v*) ;;
  *) echo "version must start with 'v' (e.g. v0.3.1), got: $VERSION" >&2; exit 1 ;;
esac

CHECKSUMS="$DIST/checksums.txt"
[ -f "$CHECKSUMS" ] || {
  echo "no checksums.txt in $DIST - run scripts/release.sh $VERSION first" >&2
  exit 1
}

# Homebrew's version field has no leading v; the URLs do.
BARE="${VERSION#v}"
BASE="https://github.com/navjyotnishant/whodunit/releases/download/$VERSION"

# Look up one archive's digest by name. Fails loudly rather than emitting an
# empty hash: a formula with a blank sha256 fails at install time with a
# mismatch, which is a confusing way to learn the lookup missed.
sha_for() {
  _name="$1"
  _sha="$(awk -v n="$_name" '$2 == n || $2 == "*" n { print $1 }' "$CHECKSUMS")"
  [ -n "$_sha" ] || { echo "no checksum for $_name in $CHECKSUMS" >&2; exit 1; }
  echo "$_sha"
}

DARWIN_ARM64="dun_${VERSION}_darwin_arm64.tar.gz"
DARWIN_AMD64="dun_${VERSION}_darwin_amd64.tar.gz"
LINUX_ARM64="dun_${VERSION}_linux_arm64.tar.gz"
LINUX_AMD64="dun_${VERSION}_linux_amd64.tar.gz"

cat <<EOF
class Dun < Formula
  desc "Local-only git trailer standard for AI-attribution provenance"
  homepage "https://github.com/navjyotnishant/whodunit"
  version "$BARE"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "$BASE/$DARWIN_ARM64"
      sha256 "$(sha_for "$DARWIN_ARM64")"
    else
      url "$BASE/$DARWIN_AMD64"
      sha256 "$(sha_for "$DARWIN_AMD64")"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "$BASE/$LINUX_ARM64"
      sha256 "$(sha_for "$LINUX_ARM64")"
    else
      url "$BASE/$LINUX_AMD64"
      sha256 "$(sha_for "$LINUX_AMD64")"
    end
  end

  def install
    # The archive contains a plain \`dun\` (NAV-101). Before v0.3.0 it held
    # the versioned filename, which is why this used to rename per
    # platform - four lines that all resolve to the same thing now, and
    # that would each fail with "no such file" against a current archive.
    bin.install "dun"
  end

  # Hooks carry the version that wrote them, so a change to the hook
  # script's own shape does not reach a repository just because the binary
  # was upgraded (NAV-76). dun repairs a repository on the next command run
  # there, but a repository nobody visits stays stale indefinitely - and
  # stale hooks attribute less while looking like they are working.
  #
  # An earlier version of this formula claimed Homebrew has no post-install
  # hook. It does: post_install runs on install and on upgrade.
  #
  # The failure is swallowed deliberately. A repository that has moved, or
  # whose hooks directory is not writable, must not fail the upgrade -
  # ending up with stale hooks is a far better outcome than ending up with
  # no new binary. \`dun repos update\` prints what it did either way.
  def post_install
    system bin/"dun", "repos", "update"
  rescue StandardError
    opoo "could not refresh git hooks; run \`dun repos update\` when convenient"
  end

  test do
    assert_match "dun", shell_output("#{bin}/dun --help")
  end
end
EOF
