job "example2" {
  datacenters = ["dc1"]
  type        = "service"

  group "example" {
    task "server" {
      driver = "nix2"

      config {
        packages = [
          "github:nixos/nixpkgs#python3",
          "github:nixos/nixpkgs#bash",
          "github:nixos/nixpkgs#coreutils",
          "github:nixos/nixpkgs#curl",
          "github:nixos/nixpkgs#nix",
          "github:nixos/nixpkgs#git",
          "github:nixos/nixpkgs#cacert",
          "github:nixos/nixpkgs#strace",
          "github:nixos/nixpkgs#gnugrep",
          "github:nixos/nixpkgs#mount",
        ]
        command = "python3"
        args = [ "-m", "http.server", "8080" ]
      }
      user = "lx"
    }
  }
}
