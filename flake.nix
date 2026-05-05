{
  description = "darkly — auction deal-hunting tool";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in {
        devShells.default = pkgs.mkShell {
          name = "darkly";
          packages = with pkgs; [
            go
            gopls
            golangci-lint
            sqlc
            goose
          ] ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
            chromium
          ];

          shellHook = ''
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
