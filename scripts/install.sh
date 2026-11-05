#!/bin/sh
# Install envmgr from the latest GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/dev-vinicius-andrade/environment-manager/main/scripts/install.sh | sh
#
# Environment:
#   ENVMGR_VERSION      version to install, e.g. 1.2.0 (default: latest release)
#   ENVMGR_INSTALL_DIR  where to put the binary (default: $HOME/.local/bin)
#   GITHUB_TOKEN        optional, avoids GitHub's anonymous rate limit
#
# The archive's SHA-256 is checked against the release's checksums.txt before
# anything is installed. Nothing is run with sudo.
set -eu

REPO="dev-vinicius-andrade/environment-manager"
VERSION="${ENVMGR_VERSION:-}"
INSTALL_DIR="${ENVMGR_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf 'envmgr-install: %s\n' "$*" >&2; }
die() { say "error: $*"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

if have curl; then
  api_get() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
      curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$1"
    else
      curl -fsSL "$1"
    fi
  }
  fetch_to() { curl -fsSL -o "$2" "$1"; }
elif have wget; then
  api_get() { wget -qO- "$1"; }
  fetch_to() { wget -qO "$2" "$1"; }
else
  die "curl or wget is required"
fi

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS $(uname -s); on Windows use scripts/install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

if [ -z "$VERSION" ]; then
  say "looking up the latest release"
  tag=$(api_get "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1) || tag=""
  [ -n "$tag" ] || die "could not find a release (has one been published?)"
  VERSION="${tag#v}"
else
  VERSION="${VERSION#v}"
fi
case "$VERSION" in
  *[!0-9A-Za-z.+-]* | "") die "invalid version '$VERSION'" ;;
esac

name="envmgr_${VERSION}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/v${VERSION}"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t envmgr)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading $name"
fetch_to "$base/$name" "$tmp/$name" || die "download failed: $base/$name"
fetch_to "$base/checksums.txt" "$tmp/checksums.txt" || die "could not download checksums.txt"

want=$(awk -v f="$name" '{ n=$2; sub(/^\*/, "", n); if (n == f) print $1 }' "$tmp/checksums.txt" | head -n 1)
[ -n "$want" ] || die "no checksum for $name in checksums.txt; refusing to install"
if have sha256sum; then
  got=$(sha256sum "$tmp/$name" | awk '{print $1}')
elif have shasum; then
  got=$(shasum -a 256 "$tmp/$name" | awk '{print $1}')
else
  die "sha256sum or shasum is required to verify the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $name (expected $want, got $got)"
say "checksum ok"

mkdir -p "$tmp/x"
tar -xzf "$tmp/$name" -C "$tmp/x" envmgr || die "envmgr not found in the archive"
mkdir -p "$INSTALL_DIR" || die "cannot create $INSTALL_DIR"
[ -w "$INSTALL_DIR" ] || die "$INSTALL_DIR is not writable; set ENVMGR_INSTALL_DIR to a directory you own"
# Replace through a temp file in the same directory so a running envmgr is not corrupted.
cp "$tmp/x/envmgr" "$INSTALL_DIR/.envmgr.new" && chmod 755 "$INSTALL_DIR/.envmgr.new" && mv -f "$INSTALL_DIR/.envmgr.new" "$INSTALL_DIR/envmgr"

say "installed envmgr $VERSION to $INSTALL_DIR/envmgr"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    say "$INSTALL_DIR is not in your PATH; add this to your shell profile:"
    say "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac
say "next: see the README to install the shell hook; update later with: envmgr update"
