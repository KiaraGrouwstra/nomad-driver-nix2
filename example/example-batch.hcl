job "nix2-example-batch" {
  datacenters = ["dc1"]
  type        = "batch"

  group "example" {
    # Simple example: how to run a binary from a Nixpkgs package
    # By default, this will use nixpkgs from github:nixos/nixpkgs/nixos-22.05
    # as a base system, as defined in the agent config file.
    # This could be overridden by setting nixpkgs = "another flake"
    # inside the config {} block
    task "nix-hello" {
      driver = "nix2"

      config {
        # Packages contains a list of Nix flakes to include in the environement.
        # Entries that start with # will be relative to nixpkgs.
        # Otherwise, they are flake names that are passed directly to Nix build
        packages = [
          "#hello"   # equivalent to "github:nixos/nixpkgs/nixos-22.05#hello"
        ]
        command = "hello"
      }
    }

    # This example show how to setup root CA certificates so that jobs
    # can do TLS connections 
    # Here, a Nix profile is built using packages curl and cacert from nixpkgs.
    # Because the cacert package is included, the ca-bundle.crt file is added to
    # /etc in that profile. Then, the nix2 driver binds all files from that
    # profile in the root directory, making ca-bundle.crt available directly under /etc.
    # Reference: see https://gist.github.com/CMCDragonkai/1ae4f4b5edeb021ca7bb1d271caca999
    task "nix-curl-ssl" {
      driver = "nix2"

      config {
        packages = [
          "#curl", "#cacert"
        ]
        command = "curl"
        args = [
          "https://nixos.org"
        ]
      }
      env = {
        SSL_CERT_FILE = "/etc/ssl/certs/ca-bundle.crt"
      }
    }

    # This example show how to use a flake defined from a file
    task "nix-hello-flake" {
      driver = "nix2"

      config {
        # Packages contains a list of Nix flakes to include in the environement.
        # Entries that start with # will be relative to nixpkgs.
        # Otherwise, they are flake names that are passed directly to Nix build
        packages = [
          ".#hello"
        ]
        command = "hello"
      }

      template {
        data = file("flake.nix")
        destination = "flake.nix"
      }
    }
  }
}
