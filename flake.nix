{
  description = "lentitud - a small WebSocket chat client and server";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      forEachSystem = f: nixpkgs.lib.genAttrs systems (system: f (import nixpkgs { inherit system; }));

      mklentitud =
        pkgs: name:
        pkgs.buildGoModule {
          pname = "lentitud-${name}";
          version = "0.1.0";
          src = ./.;
          subPackages = [ "cmd/${name}" ];
          vendorHash = "sha256-0Qxw+MUYVgzgWB8vi3HBYtVXSq/btfh4ZfV/m1chNrA=";
          doCheck = true;
          ldflags = [
            "-s"
            "-w"
          ];

          meta = {
            description = "lentitud WebSocket ${name}";
            mainProgram = name;
            platforms = nixpkgs.lib.platforms.unix;
          };
        };
    in
    {
      packages = forEachSystem (
        pkgs:
        let
          server = mklentitud pkgs "server";
          client = mklentitud pkgs "client";
        in
        {
          inherit server client;

          default = pkgs.symlinkJoin {
            name = "lentitud";
            paths = [
              server
              client
            ];
          };
        }
      );
    };
}
