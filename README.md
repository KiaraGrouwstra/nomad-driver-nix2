Nomad Nix Driver Plugin
==========

A Nomad driver to run Nix jobs.
Uses the same isolation mechanism as the `exec` driver.
Partially based on [`nomad-driver-nix`](https://github.com/input-output-hk/nomad-driver-nix)

Requirements
-------------------

- [Go](https://golang.org/doc/install) v1.18 or later (to compile the plugin)
- [Nomad](https://www.nomadproject.io/downloads.html) v0.9+ (to run the plugin)

Building and using the Nix driver plugin
-------------------

To build the plugin and run a dev agent:

```sh
$ make build
$ nomad agent -dev -config=./example/agent.hcl -plugin-dir=$(pwd)

# in another shell
$ nomad run ./example/example-batch.hcl
$ nomad run ./example/example-service.hcl
$ nomad logs <ALLOCATION ID>
```

Writing Nix job specifications
-------------------

See documentation comments in example HCL files.
