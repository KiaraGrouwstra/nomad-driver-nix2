job "example" {
  datacenters = ["dc1"]
  type        = "batch"

  group "example" {
    task "test-host-bin" {
      driver = "exec2"

      config {
        command =  "/bin/sh"
        args = ["-c", "echo hello world"]
        bind_read_only = {
          "/bin" = "/bin",
          "/lib" = "/lib",
          "/lib64" = "/lib64",
          "/usr" = "/usr",
          "/nix" = "/nix",
        }
      }
      user = "lx"
    }

    task "test-nix-hello" {
      driver = "exec2"

      config {
        command = "/sw/bin/nix"
        args = [
          "--extra-experimental-features", "flakes",
          "--extra-experimental-features", "nix-command",
          "run",
          "github:NixOS/nixpkgs#hello"
          ]
        bind = {
          "/nix" = "/nix",
        }
        bind_read_only = {
          "/home/lx/.nix-profile" = "/sw",
        }
      }
      user = "lx"
    }

    task "test-nix-store" {
      driver = "exec2"

      config {
        command =  "/nix/store/30j23057fqnnc1p4jqmq73p0gxgn0frq-bash-5.1-p16/bin/sh"
        args = ["-c", "/nix/store/y41s1vcn0irn9ahn9wh62yx2cygs7qjj-coreutils-8.32/bin/ls /*; /nix/store/y41s1vcn0irn9ahn9wh62yx2cygs7qjj-coreutils-8.32/bin/id"]
        bind_read_only = {
          "/nix" = "/nix",
        }
      }
      user = "lx"
    }
  }
}
