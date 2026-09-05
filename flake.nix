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
        # Temporary overlay: pin go_1_26 to 1.26.8 (latest point release; picks up
        # accumulated cgo/compiler/runtime/debug/elf/os fixes from 1.26.7 and 1.26.8).
        # Remove once nixpkgs-unstable ships 1.26.8 natively.
        goOverlay = final: prev: {
          go_1_26 = prev.go_1_26.overrideAttrs (_: {
            version = "1.26.8";
            src = prev.fetchurl {
              url = "https://go.dev/dl/go1.26.8.src.tar.gz";
              hash = "sha256-Tjm5jkL5RvoFrIvFtxh335fb23y7Gnd7VBZnrXEX/S4=";
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
          config.allowUnfreePredicate = pkg: builtins.elem (pkgs.lib.getName pkg) [ "terraform" ];
        };
        # Packages shared between both shells.
        commonPackages = with pkgs; [
          go_1_26
          gnumake
          govulncheck
          goreleaser
          golangci-lint
        ];
      in
      {
        devShells = {
          # Local development shell — includes Terraform and AWS CLI for e2e tests.
          default = pkgsWithUnfree.mkShell {
            packages = commonPackages ++ [ pkgsWithUnfree.terraform pkgs.awscli2 pkgs.jq ];
            shellHook = ''
              unset GOROOT
              echo "kumolo dev env: $(go version)"
            '';
          };
          # Core shell — no Terraform; used by CI and release workflows.
          core = pkgs.mkShell {
            packages = commonPackages;
            shellHook = ''
              unset GOROOT
            '';
          };
        };
      }
    );
}
