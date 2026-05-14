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
        # Separate pkgs instance that permits Terraform's BSL 1.1 license.
        # Used only in the default (local dev) shell so CI is unaffected.
        pkgsWithUnfree = import nixpkgs {
          inherit system;
          overlays = [ goOverlay ];
          config.allowUnfreePredicate = pkg: builtins.elem (pkgs.lib.getName pkg) [ "terraform" ];
        };
        # Packages shared between both shells.
        commonPackages = with pkgs; [
          go_1_26
          gnumake
          govulncheck
          goreleaser
          awscli2
        ];
      in
      {
        devShells = {
          # Local development shell — includes Terraform for make e2e-terraform.
          default = pkgsWithUnfree.mkShell {
            packages = commonPackages ++ [ pkgsWithUnfree.terraform ];
            shellHook = ''
              unset GOROOT
              echo "kumolo dev env: $(go version)"
            '';
          };
          # CI shell — no Terraform; keeps CI fast and avoids the unfree license issue.
          ci = pkgs.mkShell {
            packages = commonPackages;
            shellHook = ''
              unset GOROOT
            '';
          };
        };
      }
    );
}
