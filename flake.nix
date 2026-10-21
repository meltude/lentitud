{
  description = "lentitud - a small WebSocket chat client and server";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      perSystem =
        { pkgs, lib, ... }:
        let
          mklentitud =
            name:
            pkgs.buildGoModule {
              pname = "lentitud-${name}";
              version = "0.0.1.1";
              src = ./.;
              subPackages = [ "cmd/lentitud-${name}" ];
              vendorHash = null;
              ldflags = [
                "-s"
                "-w"
              ];

              meta = {
                description = "lentitud WebSocket ${name}";
                mainProgram = "lentitud-${name}";
                platforms = lib.platforms.unix;
              };
            };

          server = mklentitud "server";
          client = mklentitud "client";
        in
        {
          packages = {
            inherit server client;

            default = pkgs.symlinkJoin {
              name = "lentitud";
              paths = [
                server
                client
              ];
              meta.mainProgram = "lentitud-client";
            };
          };

          apps = {
            server.program = lib.getExe server;
            client.program = lib.getExe client;
          };

          devShells.default = pkgs.mkShell {
            packages = with pkgs; [
              go
              gopls
              gotools
            ];
          };

          formatter = pkgs.nixfmt;
        };
    };
}
