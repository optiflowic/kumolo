{
  description = "kumolo – local AWS emulator";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        # Overlay to patch go_1_26 to 1.26.3 (fixes GO-2026-4971: NUL byte panic in net on Windows)
        goOverlay = final: prev: {
          go_1_26 = prev.go_1_26.overrideAttrs (_: {
            version = "1.26.3";
            src = prev.fetchurl {
              url = "https://go.dev/dl/go1.26.3.src.tar.gz";
              hash = "sha256-HGRoddCqh5kTMYTtV895/yS97+jIggRwYCqdPW2Rkrg=";
            };
          });
        };
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ goOverlay ];
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go_1_26
            gnumake
            govulncheck
            goreleaser
            awscli2
          ];

          shellHook = ''
            unset GOROOT
            echo "kumolo dev env: $(go version)"
          '';
        };
      }
    );
}
