#!/usr/bin/env bash
# Update flake.nix's `release = { ... }` pin after a goreleaser
# release. Reads the latest release tag from `gh release view`,
# computes nix sha256 hashes for the two user-consumable archives,
# and rewrites flake.nix in place.
#
# Usage: scripts/bump-flake-version.sh [tag]
#   tag defaults to the latest release on the upstream repo.
set -euo pipefail

if ! command -v gh >/dev/null 2>&1; then
  echo "error: gh CLI not on PATH" >&2; exit 1
fi
if ! command -v nix-prefetch-url >/dev/null 2>&1; then
  echo "error: nix-prefetch-url not on PATH (run inside nix develop)" >&2; exit 1
fi

repo="${REPO:-linuskendall/cosmonaut}"
owner="${repo%%/*}"
name="${repo##*/}"
tag="${1:-$(gh release view --repo "$repo" --json tagName -q .tagName)}"

# Asset names are the canonical user-facing artifact names produced
# by goreleaser (see .goreleaser.{linux,darwin}.yaml). Update here
# if those name templates change.
linux_asset="cosmonaut-amd64.tar.gz"
darwin_asset="cosmonaut-macos-arm64.tar.gz"

linux_url="https://github.com/$repo/releases/download/$tag/$linux_asset"
darwin_url="https://github.com/$repo/releases/download/$tag/$darwin_asset"

echo "==> bumping flake.nix release pin to $tag"
echo "    $linux_url"
echo "    $darwin_url"

linux_sha=$(nix-prefetch-url --type sha256 "$linux_url")
darwin_sha=$(nix-prefetch-url --type sha256 "$darwin_url")

flake="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/flake.nix"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

awk -v owner="$owner" -v repo="$name" -v tag="$tag" \
    -v lsha="$linux_sha" -v dsha="$darwin_sha" '
  $0 ~ /^      release[[:space:]]*=[[:space:]]*\{/ { in_block = 1 }
  in_block && /owner[[:space:]]*=/  { sub(/=.*/, "= \"" owner "\";") }
  in_block && /repo[[:space:]]*=/   { sub(/=.*/, "= \"" repo "\";") }
  in_block && /tag[[:space:]]*=/    { sub(/=.*/, "= \"" tag "\";") }
  in_block && /linuxSha[[:space:]]*=/  { sub(/=.*/, "= \"" lsha "\";") }
  in_block && /darwinSha[[:space:]]*=/ { sub(/=.*/, "= \"" dsha "\";") }
  in_block && /^      \};/ { in_block = 0 }
  { print }
' "$flake" > "$tmp"
mv "$tmp" "$flake"

echo "wrote tag=$tag"
echo "wrote linuxSha=$linux_sha"
echo "wrote darwinSha=$darwin_sha"
echo
echo "Verify with:"
echo "  nix flake check"
echo "  nix build .#cosmonaut-prebuilt"
