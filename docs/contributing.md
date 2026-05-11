# Contributing

## Development setup

The project uses [Nix](https://nixos.org/) for builds and a dev shell.

```bash
# Enter dev shell with Go, gh, and other tools
nix develop

# Build the binary
nix build

# Run directly
go run .
```

## Testing

```bash
xvfb-run -a go test ./...
```

The daemon imports the global hotkey package, which initializes X11 on Linux.
Use `xvfb-run` in headless environments such as Codespaces.

## Local macOS test builds

On a Mac, build a downloadable test binary and app bundle zip with:

```bash
scripts/build-macos-local.sh arm64
```

This writes `dist/cosmonaut-darwin-arm64` and
`dist/cosmonaut-macos-arm64.zip`.

Linux-to-macOS builds require Apple's macOS SDK and a Darwin-capable linker
because the app uses Fyne/CGO. Plain `GOOS=darwin` builds are not sufficient.

## Releasing a new version

The git tag is the single source of truth for the release version. The CI workflow renders it into `FyneApp.toml` and `dist/Info.plist` on the runner via [`scripts/render-version-files.sh`](https://github.com/linuskendall/cosmonaut/blob/main/scripts/render-version-files.sh) — the committed values of those files are cosmetic and may drift behind the latest tag.

```bash
git tag -a v0.9.0 -m "v0.9.0"
git push origin v0.9.0
```

The [release workflow](https://github.com/linuskendall/cosmonaut/blob/main/.github/workflows/release.yml) runs automatically on tag push. It:

1. Builds the hermetic Linux AppImage via [nix-appimage](https://github.com/ralismark/nix-appimage) (squashfs + user namespaces — runs on any Linux box).
2. Builds the Linux tarball and the macOS DMG + `.app`-bearing tar.gz via [goreleaser](https://goreleaser.com/) (see `.goreleaser.linux.yaml` / `.goreleaser.darwin.yaml`). The macOS post-build hook in [`scripts/post-build-darwin.sh`](https://github.com/linuskendall/cosmonaut/blob/main/scripts/post-build-darwin.sh) assembles `Cosmonaut.app`, ad-hoc codesigns, and asserts the binary references only system dylibs (no `/nix/store` or homebrew leaks).
3. Computes `SHA256SUMS`, [cosign](https://docs.sigstore.dev/)-signs every artifact (keyless, GitHub OIDC), and uploads the GitHub release.

Notarization is intentionally out of scope. The DMG is ad-hoc codesigned, so users still run `xattr -d com.apple.quarantine` after copying `Cosmonaut.app` to `/Applications`.

### Bump the prebuilt-fetch nix package

After the release workflow has published the artifacts, point the flake's `cosmonaut-prebuilt` package at the new tag:

```bash
nix develop --command scripts/bump-flake-version.sh
git add flake.nix
git commit -m 'bump cosmonaut-prebuilt to v0.9.0'
git push origin main
```

The script reads the latest GitHub release, computes `nix-prefetch-url` sha256s for `cosmonaut-amd64.tar.gz` and `cosmonaut-macos-arm64.tar.gz`, and rewrites the `release = { ... }` block at the top of `flake.nix`. Until you run it, `nix build .#cosmonaut-prebuilt` fails with a clear hash-mismatch error — placeholder hashes ship in the flake by default so an unbumped flake never produces a working-but-stale binary.

### Home-manager module drift

The Nix Home Manager module in `modules/home-manager.nix` duplicates some daemon config defaults (like the hotkey) for documentation purposes. If you change a default in the Go code, update the Nix module default to match. This is a manual check — there's no CI job that asserts it.

## Updating documentation

Docs live in `docs/` and are built with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/). The site is configured in `mkdocs.yml`.

To preview locally:

```bash
mkdocs serve
```

API reference docs under `docs/api/` are generated from godoc comments using [gomarkdoc](https://github.com/princjef/gomarkdoc). The [docs workflow](https://github.com/linuskendall/cosmonaut/blob/main/.github/workflows/docs.yml) regenerates and deploys them on push to `main`.

To add a new page, create a markdown file in `docs/` and add it to the `nav` section in `mkdocs.yml`.
