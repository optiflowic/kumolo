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
        # Temporary overlay: pin go_1_27 to 1.27.2 (security release; fixes the
        # net/http, net/textproto, crypto/tls and os advisories GO-2026-6603..6617).
        # Remove once nixpkgs-unstable ships 1.27.2 natively.
        goOverlay = final: prev: {
          go_1_27 = prev.go_1_27.overrideAttrs (_: {
            version = "1.27.2";
            src = prev.fetchurl {
              url = "https://go.dev/dl/go1.27.2.src.tar.gz";
              hash = "sha256-A0ldorpkiU1A9cSZLklFT6eLUGkGBP+Stq//UIG3bmI=";
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
          go_1_27
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
