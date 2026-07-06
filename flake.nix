{
  description = "Terminal UI client for streaming SomaFM radio channels";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      supportedSystems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        {
          default = pkgs.buildGoModule {
            pname = "somatui";
            version = "0.9.0";
            src = self;

            vendorHash = null;

            nativeBuildInputs = pkgs.lib.optionals pkgs.stdenv.isLinux [
              pkgs.pkg-config
            ];
            buildInputs = pkgs.lib.optionals pkgs.stdenv.isLinux [
              pkgs.alsa-lib
            ];

            subPackages = [ "cmd/somatui" ];
            ldflags = [
              "-s"
              "-w"
              "-X main.version=v0.9.0"
              "-X main.commit=${self.shortRev or "dirty"}"
              "-X main.date=unknown"
            ];

            meta = {
              description = "Terminal UI client for streaming SomaFM radio channels";
              homepage = "https://github.com/samuelb/somatui";
              license = pkgs.lib.licenses.mit;
              mainProgram = "somatui";
              platforms = supportedSystems;
            };
          };
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/somatui";
        };
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
            ] ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
              pkgs.alsa-lib
              pkgs.pkg-config
            ];
          };
        }
      );
    };
}
