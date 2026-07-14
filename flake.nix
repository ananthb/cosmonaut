{
  description = "Cosmonaut Launcher – start/create GitHub Codespaces and open them in Zed";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    # nix-appimage produces a real, portable AppImage by bundling the full
    # closure into the squashfs and mounting it via user namespaces, so the
    # binary's /nix/store interpreter and RUNPATH resolve at runtime on any
    # Linux box. Replaces the previous hand-rolled AppRun that just exported
    # LD_LIBRARY_PATH to host /nix/store paths.
    nix-appimage = {
      url = "github:ralismark/nix-appimage";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs = { self, nixpkgs, flake-utils, nix-appimage }:
    let
      # Pinned release for the prebuilt-fetch package. Bumped by
      # scripts/bump-flake-version.sh after each goreleaser release.
      # Placeholder hashes make `nix build .#cosmonaut-prebuilt` fail
      # with a clear "hash mismatch" error until the script has run
      # against a real release.
      release = {
        owner = "linuskendall";
        repo = "cosmonaut";
        tag = "v0.0.0-placeholder";
        linuxSha = "0000000000000000000000000000000000000000000000000000";
        darwinSha = "0000000000000000000000000000000000000000000000000000";
      };
    in
    {
      homeManagerModules.default = import ./modules/home-manager.nix self;
      homeManagerModules.cosmonaut = import ./modules/home-manager.nix self;
      # Backwards compatibility alias.
      homeManagerModules.codespace-zed = import ./modules/home-manager.nix self;
    }
    //
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # Linux cgo deps for fyne (GL/X11/glfw) + systray (gtk3).
        cgoLinuxLibs = pkgs.lib.optionals pkgs.stdenv.isLinux [
          pkgs.gtk3
          pkgs.libappindicator-gtk3
          pkgs.libGL
          pkgs.xorg.libX11
          pkgs.xorg.libXcursor
          pkgs.xorg.libXi
          pkgs.xorg.libXinerama
          pkgs.xorg.libXrandr
          pkgs.xorg.libXxf86vm
          pkgs.xorg.libXext
          pkgs.xorg.libXfixes
          pkgs.xorg.libXrender
          # X protocol headers (X.h, Xfuncproto.h) — Xlib.h #include's them.
          pkgs.xorg.xorgproto
        ];

        # writeShellApplication doesn't run stdenv's cc-wrapper setup
        # hook, so the lint/test apps must inject these themselves:
        # PKG_CONFIG_PATH for go-gl/gl's `#cgo pkg-config: gl`,
        # CGO_CFLAGS for hotkey/glfw's hand-written `#include <X11/...>`.
        cgoLinuxPkgConfigPath =
          pkgs.lib.makeSearchPathOutput "dev" "lib/pkgconfig" cgoLinuxLibs;
        cgoLinuxCFLAGS = builtins.concatStringsSep " "
          (map (lib: "-isystem ${pkgs.lib.getDev lib}/include") cgoLinuxLibs);
        cgoLinuxLDFLAGS = builtins.concatStringsSep " "
          (map (lib: "-L${pkgs.lib.getLib lib}/lib") cgoLinuxLibs);

        # cosmonautFromSource is the hermetic build used as input to
        # the AppImage and as the home-manager default package binding.
        # The user-facing release tarball + DMG come from goreleaser
        # (see .goreleaser.{linux,darwin}.yaml). Once a goreleaser
        # release is pinned in `release` above, home-manager users can
        # opt into `packages.cosmonaut-prebuilt` instead.
        cosmonautFromSource = pkgs.buildGoModule {
          pname = "cosmonaut";
          # Not a user-facing version — this derivation only feeds the
          # AppImage and serves as the home-manager default until users
          # opt into cosmonautPrebuilt. The released version comes from
          # the git tag via goreleaser's `-X main.version` ldflag.
          version = "unstable";
          src = ./.;

          vendorHash = "sha256-Hc22uW6Eq1tY567WipjS8GCPWNJcT9Db5Wpovs/MAdU=";

          env.CGO_ENABLED = 1;
          tags = [ "netgo" ];

          nativeBuildInputs =
