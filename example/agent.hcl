#log_level = "TRACE"

client {
}

plugin "exec2-driver" {
  config {
    bind_read_only = {
      "/etc" = "/etc",
    }
  }
}
