#!/bin/sh
# Installs the hookshield CLI from the GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/hookshield-hq/hookshield/main/scripts/install.sh | sh
#
# Options (environment variables):
#   HOOKSHIELD_VERSION      version to install, e.g. 1.2.0 (default: the latest release)
#   HOOKSHIELD_INSTALL_DIR  where to put the binary (default: /usr/local/bin if writable, else ~/.local/bin)
#   HOOKSHIELD_RELEASES     base URL of the release files (default: GitHub; for mirrors and tests)
#
# Linux and macOS, amd64 and arm64. Windows: download the .zip from the releases page.
# The archive is checked against the release's checksums.txt (SHA-256) before installing.
set -eu

REPO="hookshield-hq/hookshield"
RELEASES=${HOOKSHIELD_RELEASES:-https://github.com/$REPO/releases}

say() { printf '%s\n' "$*"; }
die() { printf '✗ %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "this installer needs $1"; }

need uname
need tar
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  fetch_url() { curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  fetch_url() { wget -S --spider "$1" 2>&1 | sed -n 's/^ *[Ll]ocation: *\([^ ]*\).*/\1/p' | tail -1 | tr -d '\r'; }
else
  die "this installer needs curl or wget"
fi

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported system $(uname -s): download the CLI from $RELEASES" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m): download the CLI from $RELEASES" ;;
esac

version=${HOOKSHIELD_VERSION:-}
if [ -z "$version" ]; then
  # /releases/latest redirects to /releases/tag/vX.Y.Z
  latest=$(fetch_url "$RELEASES/latest") || die "cannot reach $RELEASES"
  version=${latest##*/}
fi
version=${version#v}
[ -n "$version" ] && [ "$version" != "latest" ] || die "no release found at $RELEASES"

archive="hookshield_${version}_${os}_${arch}.tar.gz"
base="$RELEASES/download/v$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "• downloading hookshield $version ($os/$arch)…"
fetch "$base/$archive" "$tmp/$archive" || die "download failed: $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: $base/checksums.txt"

expected=$(awk -v f="$archive" '$2 == f {print $1}' "$tmp/checksums.txt")
[ -n "$expected" ] || die "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
  die "this installer needs sha256sum or shasum to verify the download"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch for $archive (expected $expected, got $actual)"

tar -xzf "$tmp/$archive" -C "$tmp" hookshield || die "unexpected archive content"

dir=${HOOKSHIELD_INSTALL_DIR:-}
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/hookshield" "$dir/hookshield" 2>/dev/null || { cp "$tmp/hookshield" "$dir/hookshield" && chmod 0755 "$dir/hookshield"; }

say "✓ installed $("$dir/hookshield" version) to $dir/hookshield"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) say "  add it to your PATH: export PATH=\"$dir:\$PATH\"" ;;
esac
say "  next: hookshield login"
