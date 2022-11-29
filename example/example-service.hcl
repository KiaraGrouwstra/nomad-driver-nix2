job "nix2-example-service" {
  datacenters = ["dc1"]
  type        = "service"

  group "example" {
    # This task defines a server that runs a simple python file server on port 8080,
    # which allows to explore the contents of the filesystem namespace as visible
    # by processes that run inside the task.
    # A bunch of utilities are included as well, so that you can exec into the container
    # and explore what's inside by yourself.
    task "nix-python-serve-http" {
      driver = "nix2"

      config {
        packages = [
          "python3",
          "bash",
          "coreutils",
          "curl",
          "nix",
          "git",
          "cacert",
          "strace",
          "gnugrep",
          "findutils",
          "mount",
        ]
        command = "python3"
        args = [ "-m", "http.server", "8080" ]
      }
      env = {
        SSL_CERT_FILE = "/etc/ssl/certs/ca-bundle.crt"
      }
    }
  }
}
