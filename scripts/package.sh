#!/usr/bin/env bash
# Build and package one release target.
#
#   scripts/package.sh <version> <os> <arch> <outdir>
#
# version  without the leading "v" (1.2.3 or 1.2.3-rc.1)
# os       linux | darwin | windows
# arch     amd64 | arm64
#
# Writes <outdir>/envmgr_<version>_<os>_<arch>.tar.gz (linux, darwin) or .zip
# (windows). The archive holds envmgr (envmgr.exe), LICENSE and README.md at its
# top level. Asset names are a contract with `envmgr update`: see
# .claude/docs/releases.md.
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "usage: $0 <version> <os> <arch> <outdir>" >&2
  exit 2
fi
version="$1" goos="$2" goarch="$3" outdir="$4"

case "$version" in v*) echo "version must not start with v: $version" >&2; exit 2 ;; esac
case "$goos" in linux | darwin | windows) ;; *) echo "unsupported os: $goos" >&2; exit 2 ;; esac
case "$goarch" in amd64 | arm64) ;; *) echo "unsupported arch: $goarch" >&2; exit 2 ;; esac

root="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$outdir"
outdir="$(cd "$outdir" && pwd)"

name="envmgr_${version}_${goos}_${goarch}"
bin="envmgr"
[ "$goos" = "windows" ] && bin="envmgr.exe"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/$name"

pkg="github.com/dev-vinicius-andrade/environment-manager/internal/buildinfo"
commit="$(git -c safe.directory='*' -C "$root" rev-parse HEAD 2>/dev/null || echo unknown)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ldflags="-s -w -X ${pkg}.Version=${version} -X ${pkg}.Commit=${commit} -X ${pkg}.Date=${date}"

echo "building $name"
(cd "$root" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
  go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$stage/$name/$bin" .)
cp "$root/LICENSE" "$root/README.md" "$stage/$name/"
chmod 0755 "$stage/$name/$bin"

# The archive's files sit at its top level, not inside a folder.
cd "$stage/$name"
if [ "$goos" = "windows" ]; then
  out="$outdir/$name.zip"
  rm -f "$out"
  if command -v zip >/dev/null 2>&1; then
    zip -q -X "$out" "$bin" LICENSE README.md
  elif command -v bsdtar >/dev/null 2>&1; then
    bsdtar -a -cf "$out" "$bin" LICENSE README.md
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$out" "$bin" LICENSE README.md <<'PY'
import sys, zipfile
out, *files = sys.argv[1:]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for f in files:
        z.write(f)
PY
  else
    echo "no zip tool found: install zip, bsdtar or python3" >&2
    exit 1
  fi
else
  out="$outdir/$name.tar.gz"
  tar -czf "$out" "$bin" LICENSE README.md
fi
echo "wrote $out"
