#log_level = "TRACE"

client {
}

plugin "nix2-driver" {
  config {
    default_nixpkgs = "github:nixos/nixpkgs/nixos-22.05"
  }
}
