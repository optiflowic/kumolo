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
        # Temporary overlay: pin go_1_26 to 1.26.4 (fixes GO-2026-5039/GO-2026-5037).
        # Remove once nixpkgs-unstable ships 1.26.4 natively.
        goOverlay = final: prev: {
          go_1_26 = prev.go_1_26.overrideAttrs (_: {
            version = "1.26.4";
            src = prev.fetchurl {
              url = "https://go.dev/dl/go1.26.4.src.tar.gz";
              hash = "sha256-T2aKMvv8ETLmqIH7lowvHa2mMUkqM5IRc1+7JVpCYC0=";
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
        ];
      in
      {
        devShells = {
          # Local development shell — includes Terraform and AWS CLI for e2e tests.
          default = pkgsWithUnfree.mkShell {
            packages = commonPackages ++ [ pkgsWithUnfree.terraform pkgs.awscli2 ];
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
