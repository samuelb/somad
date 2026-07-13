{
  description = "A client for streaming SomaFM radio channels";

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
            pname = "somad";
            version = "0.14.1";
            src = self;

            vendorHash = null;

            nativeBuildInputs = [
              pkgs.installShellFiles
            ] ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
              pkgs.pkg-config
            ];
            buildInputs = pkgs.lib.optionals pkgs.stdenv.isLinux [
              pkgs.alsa-lib
            ];

            subPackages = [ "cmd/soma" ];
            ldflags = [
              "-s"
              "-w"
              "-X main.version=v0.14.1"
              "-X main.commit=${self.shortRev or "dirty"}"
              "-X main.date=unknown"
            ];

            postInstall = ''
              installShellCompletion --cmd soma \
                --bash cmd/soma/completions/soma.bash \
                --zsh cmd/soma/completions/soma.zsh
            '';

            meta = {
              description = "A client for streaming SomaFM radio channels";
              homepage = "https://github.com/samuelb/somad";
              license = pkgs.lib.licenses.mit;
              mainProgram = "soma";
              platforms = supportedSystems;
            };
          };
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/soma";
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
