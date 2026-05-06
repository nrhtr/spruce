{
  description = "spruce — auction deal-hunting tool";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    git-hooks = {
      url = "github:cachix/git-hooks.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, flake-utils, git-hooks }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        pre-commit-check = git-hooks.lib.${system}.run {
          src = ./.;
          package = pkgs.prek;
          hooks = {
            govet.enable = true;
            gofmt.enable = true;
            markdownlint.enable = true;
            gitleaks = {
              enable = true;
              name = "gitleaks";
              entry = "${pkgs.gitleaks}/bin/gitleaks protect --staged -v";
              pass_filenames = false;
            };
          };
        };
      in {
        checks.pre-commit-check = pre-commit-check;
        devShells.default = pkgs.mkShell {
          name = "spruce";
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            sqlc
            goose
            gitleaks
          ] ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
            chromium
          ];

          shellHook = pre-commit-check.shellHook + ''
            export CGO_ENABLED=0
            if command -v chromium &>/dev/null; then
              export CHROMIUM_PATH="$(command -v chromium)"
            elif command -v google-chrome-stable &>/dev/null; then
              export CHROMIUM_PATH="$(command -v google-chrome-stable)"
            elif command -v google-chrome &>/dev/null; then
              export CHROMIUM_PATH="$(command -v google-chrome)"
            fi
          '';
        };
      });
}
