#!/usr/bin/env bash
# Goreleaser post-build hook for darwin: assemble Cosmonaut.app, sign
# ad-hoc, and create the user-facing DMG plus a .app-bearing tar.gz
# (consumed by the in-repo nix prebuilt-fetch derivation). Mirrors
# scripts/build-macos-local.sh and the DMG block previously in
# .github/workflows/release.yml.
#
# Args:
#   $1 absolute path to the goreleaser-built binary
#   $2 release version (without the leading 'v')
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <binary-path> <version>" >&2
  exit 2
fi

binary="$1"
version="$2"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Committed packaging files (Info.plist, example config, systemd unit)
# live in dist/ — that path predates the goreleaser migration.
# Goreleaser's working directory is build/ (configured via `dist:` in
# .goreleaser.darwin.yaml) so its --clean step doesn't touch dist/.
src_dist="$root/dist"
out="$root/build"

if [ ! -x "$binary" ]; then
  echo "error: $binary is not an executable file" >&2
  exit 1
fi

# Sanity-check: the binary should reference only system dylibs and
# @rpath entries. /nix/store, /opt/homebrew, /usr/local etc. are
# runner-specific and would fail dyld on end-user Macs. The previous
# pipeline had to rewrite a libresolv path because the build closure
# included nixpkgs's libresolv; building outside /nix/store sidesteps
# that, but assert it so a future config drift is caught early.
suspicious=$(
  otool -L "$binary" \
    | tail -n +2 \
    | awk '{print $1}' \
    | grep -vE '^(/usr/lib/|/System/|@rpath/|@executable_path/)' \
    || true
)
if [ -n "$suspicious" ]; then
  echo "error: binary references non-system dylibs:" >&2
  echo "$suspicious" >&2
  exit 1
fi
if otool -l "$binary" | awk '/LC_RPATH/{getline; getline; print}' \
    | grep -vE '^[[:space:]]*path (/usr/|/System/|@executable_path)' \
    | grep -q .; then
  echo "error: binary has unexpected LC_RPATH entries:" >&2
  otool -l "$binary" | grep -A2 LC_RPATH >&2
  exit 1
fi

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

app="$stage/Cosmonaut.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

cp "$binary" "$app/Contents/MacOS/cosmonaut"
chmod +w "$app/Contents/MacOS/cosmonaut"

cp "$src_dist/Info.plist" "$app/Contents/Info.plist"
cp "$root/assets/logo.icns" "$app/Contents/Resources/icon.icns"

# Ad-hoc codesign keeps the bundle Gatekeeper-checkable; users still
# need `xattr -d com.apple.quarantine` after install. Apple Developer
# ID / notarization is not part of this migration.
if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$app" >/dev/null
fi

mkdir -p "$out"

dmg="$out/cosmonaut-macos-arm64.dmg"
tarball="$out/cosmonaut-macos-arm64.tar.gz"

# Stage the example config alongside the .app for the DMG (matches the
# old release.yml layout and the user-facing experience: open the DMG,
# see Cosmonaut.app + the example config).
dmg_stage="$(mktemp -d)"
trap 'rm -rf "$stage" "$dmg_stage"' EXIT
cp -R "$app" "$dmg_stage/Cosmonaut.app"
cp "$src_dist/cosmonaut.config.example.json" "$dmg_stage/"

rm -f "$dmg" "$tarball"
hdiutil create \
  -volname "cosmonaut" \
  -srcfolder "$dmg_stage" \
  -ov \
  -format UDZO \
  "$dmg" >/dev/null

# tar.gz containing just the .app bundle. Consumed by the prebuilt-fetch
# nix derivation in flake.nix (DMG mounting from inside a nix
# derivation needs hdiutil and is fiddly; a .tar.gz round-trips cleanly).
if command -v gtar >/dev/null 2>&1; then
  tar_cmd=gtar
else
  tar_cmd=tar
fi
$tar_cmd -czf "$tarball" -C "$stage" Cosmonaut.app

echo "wrote $dmg"
echo "wrote $tarball"
