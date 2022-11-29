package main

import (
	"git.deuxfleurs.fr/lx/nomad-driver-nix2/nix2"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins"
)

func main() {
	// Serve the plugin
	plugins.Serve(factory)
}

// factory returns a new instance of a nomad driver plugin
func factory(log hclog.Logger) interface{} {
	return nix2.NewPlugin(log)
}
