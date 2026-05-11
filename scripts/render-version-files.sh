#!/usr/bin/env bash
# Stamp the release version into FyneApp.toml and dist/Info.plist
# from a single source (the git tag, surfaced as goreleaser's
# {{ .Version }}). Invoked from `before.hooks` in the goreleaser
# config. Mutations are not committed; CI regenerates them on each tag.
set -euo pipefail

if [ "$#" -ne 1 ] || [ -z "${1:-}" ]; then
  echo "usage: $0 <version>" >&2
  exit 2
fi

version="$1"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fyne="$root/FyneApp.toml"
plist="$root/dist/Info.plist"

# BSD/GNU sed compat: write to a temp and move into place.
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

awk -v v="$version" '
  /^[[:space:]]*Version[[:space:]]*=/ { sub(/=.*/, "= \"" v "\"") }
  { print }
' "$fyne" > "$tmp"
mv "$tmp" "$fyne"

# CFBundleShortVersionString sits one line below its <key>.
tmp="$(mktemp)"
awk -v v="$version" '
  prev ~ /<key>CFBundleShortVersionString<\/key>/ {
    sub(/<string>[^<]*<\/string>/, "<string>" v "</string>")
  }
  { print; prev = $0 }
' "$plist" > "$tmp"
mv "$tmp" "$plist"

echo "stamped version $version into FyneApp.toml and dist/Info.plist"
