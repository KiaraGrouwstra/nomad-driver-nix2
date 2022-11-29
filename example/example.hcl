job "example" {
  datacenters = ["dc1"]
  type        = "batch"

  group "example" {
    task "test-nix-hello" {
      driver = "nix2"

      config {
        command = "sh"
        args = [
          "-c",
          "pwd; ls -l *; mount; hello"
        ]
        packages = [
          "github:NixOS/nixpkgs#coreutils",
          "github:NixOS/nixpkgs#bash",
          "github:NixOS/nixpkgs#hello"
          ]
      }
      user = "lx"
    }
  }
}
