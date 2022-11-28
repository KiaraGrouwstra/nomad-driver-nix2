job "example" {
  datacenters = ["dc1"]
  type        = "batch"

  group "example" {
    task "hello-world" {
      driver = "exec2"

      config {
        command =  "/nix/store/y41s1vcn0irn9ahn9wh62yx2cygs7qjj-coreutils-8.32/bin/cat"
        args = ["/host-etc/nscd.conf"]
        bind_read_only = {
          "/nix" = "/nix",
          "/etc" = "/host-etc",
        }
      }
    }
  }
}
